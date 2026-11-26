#!/usr/bin/env python3
"""Validate product settings and compose shared macOS release action inputs."""

import argparse
import json
import os
import sys
import tempfile
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "macos-package"))
from render import IDENTIFIER, NAME, VERSION, checked, render


def product_config(raw, architecture, mode):
    config = json.loads(raw)
    if not isinstance(config, dict):
        raise ValueError("release configuration must be an object")
    artifact_type = config.get("artifact_type")
    if artifact_type == "pkg":
        expected = {"artifact_type", "artifact_basename", "build", "metadata", "icon"}
    elif artifact_type == "zip":
        expected = {
            "artifact_type",
            "artifact_basename",
            "build",
            "cli_binary",
            "signing_identifier",
        }
        if mode != "signed":
            raise ValueError("CLI ZIP releases require signed mode")
    else:
        raise ValueError("artifact_type must be pkg or zip")
    if set(config) != expected:
        raise ValueError("release configuration has missing or unknown settings")
    if mode not in ("signed", "unsigned", "adhoc"):
        raise ValueError("invalid release mode")
    if architecture not in ("amd64", "arm64"):
        raise ValueError("invalid architecture")
    checked(config["artifact_basename"], NAME, "artifact basename")
    build = config["build"]
    if (
        not isinstance(build, dict)
        or not isinstance(build.get("binaries"), list)
        or not build["binaries"]
    ):
        raise ValueError("build settings must contain binary definitions")
    names = []
    for binary in build["binaries"]:
        if not isinstance(binary, dict):
            raise ValueError("binary definition must be an object")
        names.append(checked(binary.get("name"), NAME, "binary name"))
    if len({name.lower() for name in names}) != len(names):
        raise ValueError("binary names must be unique")
    if artifact_type == "pkg":
        metadata = config["metadata"]
        if not isinstance(metadata, dict) or {"version", "architecture", "binary_names"} & set(
            metadata
        ):
            raise ValueError(
                "package metadata must omit generated version, architecture and binary names"
            )
        if not isinstance(config["icon"], str) or not config["icon"]:
            raise ValueError("package icon path is required")
        sample = metadata | {
            "version": "0.0.0",
            "architecture": architecture,
            "binary_names": names,
        }
        with tempfile.TemporaryDirectory(prefix="macos-release-metadata-") as directory:
            render(sample, Path(directory))
    else:
        if checked(config["cli_binary"], NAME, "CLI binary") not in names:
            raise ValueError("CLI binary must be in build settings")
        checked(config["signing_identifier"], IDENTIFIER, "signing identifier")
    return config, names


def emit(values):
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        for key, value in values.items():
            if isinstance(value, (dict, list)):
                value = json.dumps(value, separators=(",", ":"))
            if not isinstance(value, str) or "\n" in value or "\r" in value:
                raise ValueError("release output must be single-line")
            output.write(f"{key}={value}\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("prepare", "package"))
    args = parser.parse_args()
    try:
        mode = os.environ["MACOS_RELEASE_MODE"]
        architecture = os.environ["MACOS_RELEASE_ARCH"]
        if os.environ.get("GITHUB_REF", "").startswith("refs/tags/") and mode != "signed":
            raise ValueError("tag releases require signed mode")
        config, names = product_config(os.environ["MACOS_RELEASE_CONFIG"], architecture, mode)
        if args.phase == "prepare":
            emit(
                {
                    "artifact-type": config["artifact_type"],
                    "build-config": config["build"],
                    "build-directory": f".macos-release/{uuid.uuid4().hex}",
                }
            )
            return
        directory = os.environ["MACOS_BUILD_DIRECTORY"]
        version = os.environ["MACOS_BUILD_VERSION"]
        if (
            not directory
            or Path(directory).is_absolute()
            or ".." in Path(directory).parts
            or "\n" in directory
            or "\r" in directory
        ):
            raise ValueError("invalid build directory")
        checked(version, VERSION, "version")
        artifact = f"dist/{config['artifact_basename']}-{version}-darwin-{architecture}.{config['artifact_type']}"
        if config["artifact_type"] == "pkg":
            metadata = config["metadata"] | {
                "version": version,
                "architecture": architecture,
                "binary_names": names,
            }
            emit(
                {
                    "metadata": metadata,
                    "package-config": {
                        "metadata": metadata,
                        "binaries": {name: f"{directory}/{name}" for name in names},
                        "icon": config["icon"],
                    },
                    "artifact-path": artifact,
                }
            )
        else:
            emit(
                {
                    "binary-path": f"{directory}/{config['cli_binary']}",
                    "identifier": config["signing_identifier"],
                    "artifact-path": artifact,
                }
            )
    except (KeyError, ValueError, TypeError, OSError, json.JSONDecodeError) as error:
        parser.exit(1, f"macOS release settings: {error}\n")


if __name__ == "__main__":
    main()
