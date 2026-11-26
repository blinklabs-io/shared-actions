#!/usr/bin/env python3
"""Sign a prebuilt CLI executable and archive the exact signed bytes."""
import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from assemble import publish_atomic, workspace_path
from render import IDENTIFIER, NAME, checked


def sign_zip(workspace, binary_path, archive_path, identity, identifier):
    checked(identifier, IDENTIFIER, "code signing identifier")
    if not identity or "\n" in identity or "\r" in identity:
        raise ValueError("a single-line application identity is required")
    binary = workspace_path(workspace, binary_path, exists=True)
    binary_name = checked(Path(binary_path).name, NAME, "binary name")
    if not binary.is_file():
        raise ValueError("binary must be a regular file")
    original_output = workspace / archive_path
    if original_output.exists() or original_output.is_symlink():
        raise ValueError("archive already exists")
    output = workspace_path(workspace, archive_path, exists=False)
    if output.suffix != ".zip":
        raise ValueError("archive must have a .zip extension")
    with tempfile.TemporaryDirectory(prefix="macos-cli-") as directory:
        temporary = Path(directory)
        payload_dir = temporary / "payload"
        payload_dir.mkdir()
        signed = payload_dir / binary_name
        shutil.copyfile(binary, signed)
        signed.chmod(0o755)
        subprocess.run(["codesign", "--force", "--sign", identity, "--timestamp", "--options", "runtime", "--identifier", identifier, str(signed)], check=True)
        subprocess.run(["codesign", "--verify", "--strict", "--verbose=2", str(signed)], check=True)
        archive = temporary / "artifact.zip"
        subprocess.run(["zip", "-q", str(archive), signed.name], cwd=payload_dir, check=True)
        output.parent.mkdir(parents=True, exist_ok=True)
        publish_atomic(archive, output)
    return output


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary")
    parser.add_argument("archive")
    parser.add_argument("identifier")
    args = parser.parse_args()
    try:
        workspace = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
        result = sign_zip(workspace, args.binary, args.archive, os.environ.get("MACOS_APPLICATION_IDENTITY", ""), args.identifier)
        output_file = os.environ.get("GITHUB_OUTPUT")
        if output_file:
            with open(output_file, "a", encoding="utf-8") as output:
                output.write(f"archive={result}\n")
        print(result)
    except (KeyError, ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"macOS CLI signing: {error}\n")


if __name__ == "__main__":
    main()
