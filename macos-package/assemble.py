#!/usr/bin/env python3
"""Assemble a macOS application and installer from prebuilt product binaries."""

import argparse
import json
import os
import plistlib
import shutil
import stat
import subprocess
import sys
import tempfile
from pathlib import Path

from render import render


def workspace_path(root: Path, relative: str, *, exists: bool) -> Path:
    if not isinstance(relative, str) or "\n" in relative or "\r" in relative:
        raise ValueError("path must be a single-line string")
    path = Path(relative)
    if path.is_absolute() or not path.parts or ".." in path.parts:
        raise ValueError("paths must be relative to the workspace")
    resolved = (root / path).resolve(strict=exists)
    if not resolved.is_relative_to(root):
        raise ValueError("path escapes the workspace")
    if "\n" in str(resolved) or "\r" in str(resolved):
        raise ValueError("resolved path must be a single-line string")
    return resolved


def run(*args, **kwargs):
    subprocess.run(args, check=True, **kwargs)


def publish_atomic(source_path, output):
    descriptor, staged_name = tempfile.mkstemp(prefix=".macos-package-", dir=output.parent)
    staged = Path(staged_name)
    try:
        with os.fdopen(descriptor, "wb") as target:
            os.fchmod(target.fileno(), 0o644)
            with source_path.open("rb") as source:
                shutil.copyfileobj(source, target)
        os.link(staged, output)
    finally:
        staged.unlink(missing_ok=True)


def assemble(config, workspace, output_path, mode, app_identity, installer_identity):
    if not isinstance(config, dict) or set(config) != {"metadata", "binaries", "icon"}:
        raise ValueError("expected metadata, binaries and icon settings")
    metadata = config["metadata"]
    binaries = config["binaries"]
    if not isinstance(metadata, dict) or not isinstance(binaries, dict):
        raise ValueError("metadata and binaries must be objects")
    if mode not in ("signed", "unsigned", "adhoc"):
        raise ValueError("unsupported package mode")
    if mode == "signed" and (not app_identity or not installer_identity):
        raise ValueError("signed packages require application and installer identities")
    expected = metadata.get("binary_names")
    if (
        not isinstance(expected, list)
        or not all(isinstance(name, str) for name in expected)
        or set(binaries) != set(expected)
    ):
        raise ValueError("binary sources must match the configured bundle contents")
    sources = {}
    for name, path in binaries.items():
        if not isinstance(path, str):
            raise ValueError("binary source paths must be strings")
        source = workspace_path(workspace, path, exists=True)
        if not source.is_file() or not stat.S_ISREG(source.stat().st_mode):
            raise ValueError("binary source must be a regular file")
        sources[name] = source
    icon_path = config["icon"]
    if not isinstance(icon_path, str):
        raise ValueError("icon path must be a string")
    icon_candidate = workspace_path(workspace, icon_path, exists=False)
    icon: Path | None = icon_candidate
    if not icon_candidate.is_file():
        if mode == "signed":
            raise ValueError("signed packages require an icon")
        print("warning: icon is absent from the development package", file=sys.stderr)
        icon = None
    original_output = workspace / output_path
    if original_output.exists() or original_output.is_symlink():
        raise ValueError("output already exists")
    output = workspace_path(workspace, output_path, exists=False)
    if output.suffix != ".pkg":
        raise ValueError("output must have a .pkg extension")
    if output.exists() or output.is_symlink():
        raise ValueError("output already exists")

    with tempfile.TemporaryDirectory(prefix="macos-package-") as temporary:
        work = Path(temporary)
        generated = work / "metadata"
        render(metadata, generated)
        app = work / "root" / "Applications" / (metadata["application_name"] + ".app")
        contents = app / "Contents"
        macos = contents / "MacOS"
        resources = contents / "Resources"
        macos.mkdir(parents=True)
        resources.mkdir()
        for name in metadata["binary_names"]:
            destination = macos / name
            shutil.copyfile(sources[name], destination)
            destination.chmod(0o755)
        if icon is not None:
            shutil.copyfile(icon, resources / (metadata["icon_name"] + ".icns"))
        shutil.copyfile(generated / "Info.plist", contents / "Info.plist")
        subprocess.run(["xattr", "-cr", str(app)], check=False)
        if mode in ("signed", "adhoc"):
            signing_options = (
                ["--force", "--timestamp", "--options", "runtime", "--sign", app_identity]
                if mode == "signed"
                else ["--force", "--sign", "-"]
            )
            for name in metadata["binary_names"]:
                run("codesign", *signing_options, str(macos / name))
            run("codesign", *signing_options, str(app))
            run("codesign", "--verify", "--deep", "--strict", "--verbose=2", str(app))
        component_plist = work / "component.plist"
        run("pkgbuild", "--analyze", "--root", str(work / "root"), str(component_plist))
        with component_plist.open("rb") as component_file:
            components = plistlib.load(component_file)
        if (
            not isinstance(components, list)
            or len(components) != 1
            or not isinstance(components[0], dict)
        ):
            raise ValueError("pkgbuild returned an unexpected component list")
        components[0]["BundleIsRelocatable"] = False
        with component_plist.open("wb") as target:
            plistlib.dump(components, target)
        component = work / "component.pkg"
        unsigned = work / "unsigned.pkg"
        run(
            "pkgbuild",
            "--root",
            str(work / "root"),
            "--component-plist",
            str(component_plist),
            "--identifier",
            metadata["bundle_id"],
            "--version",
            metadata["version"],
            "--install-location",
            "/",
            "--scripts",
            str(generated / "scripts"),
            str(component),
        )
        run(
            "productbuild",
            "--distribution",
            str(generated / "distribution.xml"),
            "--package-path",
            str(work),
            str(unsigned),
        )
        package = unsigned
        if mode == "signed":
            package = work / "signed.pkg"
            run("productsign", "--sign", installer_identity, str(unsigned), str(package))
            run("pkgutil", "--check-signature", str(package))
        output.parent.mkdir(parents=True, exist_ok=True)
        publish_atomic(package, output)
    return output


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("config", type=Path)
    parser.add_argument("output")
    parser.add_argument("--mode", choices=("signed", "unsigned", "adhoc"), default="unsigned")
    args = parser.parse_args()
    try:
        workspace = Path(os.environ["GITHUB_WORKSPACE"]).resolve(strict=True)
        with args.config.open(encoding="utf-8") as source:
            config = json.load(source)
        result = assemble(
            config,
            workspace,
            args.output,
            args.mode,
            os.environ.get("MACOS_APPLICATION_IDENTITY", ""),
            os.environ.get("MACOS_INSTALLER_IDENTITY", ""),
        )
        output_file = os.environ.get("GITHUB_OUTPUT")
        if output_file:
            with open(output_file, "a", encoding="utf-8") as output:
                output.write(f"package={result}\n")
        print(result)
    except (KeyError, ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"macOS package assembly: {error}\n")


if __name__ == "__main__":
    main()
