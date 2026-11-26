#!/usr/bin/env python3
"""Verify a notarized macOS installer and optionally test its installation."""
import argparse
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from assemble import workspace_path
from render import render

APPLICATIONS = Path("/Applications")
LOCAL_BIN = Path("/usr/local/bin")


def command(*args, check=True):
    return subprocess.run(args, check=check, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)


def retry_stapler(package):
    for attempt in range(5):
        result = command("xcrun", "stapler", "validate", str(package), check=False)
        if result.returncode == 0:
            return
        if attempt < 4:
            time.sleep((attempt + 1) * 15)
    raise ValueError("stapled ticket validation failed")


def assess(*args):
    result = command("spctl", "-a", "-vv", *args, check=False)
    if result.returncode != 0 or "accepted" not in result.stdout or "Notarized Developer ID" not in result.stdout:
        raise ValueError("Gatekeeper did not accept a notarized artifact")


def verify(package, metadata, install):
    if not package.is_file() or package.suffix != ".pkg":
        raise ValueError("package must be an existing .pkg file")
    with tempfile.TemporaryDirectory(prefix="macos-package-metadata-") as temporary:
        render(metadata, Path(temporary))
    signature = command("pkgutil", "--check-signature", str(package), check=False)
    if signature.returncode != 0 or "Developer ID Installer" not in signature.stdout:
        raise ValueError("package lacks a valid Developer ID Installer signature")
    assess("--type", "install", str(package))
    retry_stapler(package)
    if not install:
        return

    app = APPLICATIONS / (metadata["application_name"] + ".app")
    receipt = metadata["bundle_id"]
    if app.exists() or app.is_symlink():
        raise ValueError("application already exists; refusing to replace it")
    receipts = command("pkgutil", "--pkgs").stdout.splitlines()
    if receipt in receipts:
        raise ValueError("package receipt already exists; refusing to replace it")
    cli_name = metadata.get("cli_symlink", "")
    cli_link = LOCAL_BIN / cli_name if cli_name else None
    cli_preexisting = cli_link is not None and (cli_link.exists() or cli_link.is_symlink())
    primary_error = None
    try:
        command("sudo", "installer", "-pkg", str(package), "-target", "/", "-verbose")
        if not app.is_dir():
            raise ValueError("installer did not create the application")
        command("pkgutil", "--pkg-info", receipt)
        command("codesign", "--verify", "--deep", "--strict", "--verbose=2", str(app))
        assess("-t", "exec", str(app))
        arch = {"arm64": "arm64", "amd64": "x86_64"}[metadata["architecture"]]
        for name in metadata["binary_names"]:
            binary = app / "Contents" / "MacOS" / name
            if not binary.is_file() or not os.access(binary, os.X_OK):
                raise ValueError(f"installed binary is missing or not executable: {name}")
            if arch not in command("lipo", "-archs", str(binary)).stdout.split():
                raise ValueError(f"installed binary has the wrong architecture: {name}")
    except BaseException as error:
        primary_error = error

    cleanup_errors = []
    try:
        if cli_link is not None and not cli_preexisting and cli_link.is_symlink():
            target = os.readlink(cli_link)
            if target == str(app / "Contents" / "MacOS" / cli_name):
                command("sudo", "rm", "-f", str(cli_link))
    except (OSError, subprocess.CalledProcessError) as error:
        cleanup_errors.append(f"CLI link cleanup failed: {error}")
    try:
        command("sudo", "rm", "-rf", str(app))
    except (OSError, subprocess.CalledProcessError) as error:
        cleanup_errors.append(f"application cleanup failed: {error}")
    try:
        if receipt in command("pkgutil", "--pkgs").stdout.splitlines():
            command("sudo", "pkgutil", "--forget", receipt)
    except (OSError, subprocess.CalledProcessError) as error:
        cleanup_errors.append(f"receipt cleanup failed: {error}")
    if primary_error is not None:
        if cleanup_errors:
            raise ValueError(f"{primary_error}; {'; '.join(cleanup_errors)}") from primary_error
        raise primary_error
    if cleanup_errors:
        raise ValueError("; ".join(cleanup_errors))



def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("metadata", type=Path)
    parser.add_argument("package")
    parser.add_argument("--install", action="store_true")
    args = parser.parse_args()
    try:
        workspace = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
        package_input = Path(args.package)
        if package_input.is_absolute():
            try:
                package_input = package_input.relative_to(workspace)
            except ValueError as error:
                raise ValueError("package path escapes the workspace") from error
        package = workspace_path(workspace, str(package_input), exists=True)
        with args.metadata.open(encoding="utf-8") as source:
            metadata = json.load(source)
        verify(package, metadata, args.install)
    except (KeyError, ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"macOS package verification: {error}\n")


if __name__ == "__main__":
    main()
