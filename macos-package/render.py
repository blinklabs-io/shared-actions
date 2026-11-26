#!/usr/bin/env python3
"""Render common macOS bundle metadata and installer hooks from product settings."""
import argparse
import json
import plistlib
import re
import shlex
from pathlib import Path
from xml.etree import ElementTree as ET


NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*\Z")
IDENTIFIER = re.compile(r"[A-Za-z0-9][A-Za-z0-9.-]*\Z")
VERSION = re.compile(r"[A-Za-z0-9][A-Za-z0-9.+-]*\Z")


def checked(value, pattern, label):
    if not isinstance(value, str) or not pattern.fullmatch(value):
        raise ValueError(f"invalid {label}")
    return value


def render(config, output):
    if not isinstance(config, dict):
        raise ValueError("configuration must be an object")
    allowed = {"application_name", "bundle_id", "executable", "icon_name", "version", "architecture", "binary_names", "cli_symlink", "minimum_macos", "description", "organization", "notification_alert_style"}
    if set(config) - allowed:
        raise ValueError("unknown package metadata setting")
    name = checked(config.get("application_name"), NAME, "application name")
    identifier = checked(config.get("bundle_id"), IDENTIFIER, "bundle identifier")
    executable = checked(config.get("executable"), NAME, "bundle executable")
    icon = checked(config.get("icon_name"), NAME, "icon name")
    version = checked(config.get("version"), VERSION, "version")
    architecture = config.get("architecture")
    architectures = {"arm64": "arm64", "amd64": "x86_64"}
    if not isinstance(architecture, str) or architecture not in architectures:
        raise ValueError("unsupported architecture")
    binary_names = config.get("binary_names")
    if not isinstance(binary_names, list) or not binary_names:
        raise ValueError("binary_names must be a nonempty list")
    for binary in binary_names:
        checked(binary, NAME, "binary name")
    if len(set(binary_names)) != len(binary_names) or executable not in binary_names:
        raise ValueError("binary names must be unique and include the bundle executable")
    cli = config.get("cli_symlink", "")
    if not isinstance(cli, str):
        raise ValueError("CLI symlink must be a string")
    if cli:
        checked(cli, NAME, "CLI symlink")
        if cli not in binary_names:
            raise ValueError("CLI symlink must name a bundled binary")
    minimum = config.get("minimum_macos", "11.0")
    if not isinstance(minimum, str) or not re.fullmatch(r"[0-9]+(?:\.[0-9]+){0,2}", minimum):
        raise ValueError("invalid minimum macOS version")
    description = config.get("description", name)
    organization = config.get("organization", identifier.rpartition(".")[0])
    if not isinstance(description, str) or not description or not isinstance(organization, str) or not IDENTIFIER.fullmatch(organization):
        raise ValueError("invalid installer description or organization")
    notification = config.get("notification_alert_style", "")
    if notification not in ("", "alert", "banner", "none"):
        raise ValueError("invalid notification alert style")

    output.mkdir(parents=True, exist_ok=True)
    plist = {
        "CFBundleExecutable": executable,
        "CFBundleIconFile": icon,
        "CFBundleIdentifier": identifier,
        "CFBundleName": name,
        "CFBundleDisplayName": name,
        "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": version,
        "CFBundleVersion": version,
        "LSMinimumSystemVersion": minimum,
        "LSUIElement": False,
        "NSHighResolutionCapable": True,
    }
    if notification:
        plist["NSUserNotificationAlertStyle"] = notification
    (output / "Info.plist").write_bytes(plistlib.dumps(plist, sort_keys=False))

    root = ET.Element("installer-gui-script", {"minSpecVersion": "2"})
    ET.SubElement(root, "title").text = name
    ET.SubElement(root, "organization").text = organization
    ET.SubElement(root, "options", {"customize": "never", "require-scripts": "false", "rootVolumeOnly": "true", "hostArchitectures": architectures[architecture]})
    volume = ET.SubElement(root, "volume-check")
    allowed = ET.SubElement(volume, "allowed-os-versions")
    ET.SubElement(allowed, "os-version", {"min": minimum})
    outline = ET.SubElement(root, "choices-outline")
    line = ET.SubElement(outline, "line", {"choice": "default"})
    ET.SubElement(line, "line", {"choice": identifier})
    ET.SubElement(root, "choice", {"id": "default"})
    choice = ET.SubElement(root, "choice", {"id": identifier, "visible": "false", "title": name, "description": description})
    ET.SubElement(choice, "pkg-ref", {"id": identifier})
    ET.SubElement(root, "pkg-ref", {"id": identifier, "version": version, "onConclusion": "none"}).text = "component.pkg"
    ET.ElementTree(root).write(output / "distribution.xml", encoding="utf-8", xml_declaration=True)

    scripts = output / "scripts"
    scripts.mkdir(exist_ok=True)
    app_path = f"/Applications/{name}.app"
    preinstall = f'''#!/bin/bash
set -u
APP_PATH={shlex.quote(app_path)}
if [ -d "$APP_PATH" ]; then
    /usr/bin/osascript -e {shlex.quote(f'tell application "{name}" to quit')} >/dev/null 2>&1 || true
    _binary="$APP_PATH/Contents/MacOS/{executable}"
    /usr/bin/pkill -f "${{_binary//./\\.}}" >/dev/null 2>&1 || true
fi
exit 0
'''
    postinstall = f'''#!/bin/bash
set -u
APP_PATH={shlex.quote(app_path)}
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister
if [ -x "$LSREGISTER" ] && [ -d "$APP_PATH" ]; then
    "$LSREGISTER" -f "$APP_PATH" >/dev/null 2>&1 || true
fi
'''
    if cli:
        postinstall += f'''CLI_SRC="$APP_PATH/Contents/MacOS/{cli}"
CLI_LINK=/usr/local/bin/{cli}
if [ -x "$CLI_SRC" ]; then
    /bin/mkdir -p /usr/local/bin 2>/dev/null || true
    if [ ! -e "$CLI_LINK" ] && [ ! -L "$CLI_LINK" ] || [ "$(readlink "$CLI_LINK" 2>/dev/null)" = "$CLI_SRC" ]; then
        /bin/ln -sf "$CLI_SRC" "$CLI_LINK" 2>/dev/null || echo "postinstall: could not create $CLI_LINK; CLI is at $CLI_SRC" >&2
    else
        echo "postinstall: $CLI_LINK exists and is not ours; leaving it untouched" >&2
    fi
else
    echo "postinstall: CLI binary not found at $CLI_SRC; skipping symlink" >&2
fi
'''
    postinstall += "exit 0\n"
    for filename, content in (("preinstall", preinstall), ("postinstall", postinstall)):
        path = scripts / filename
        path.write_text(content, encoding="utf-8")
        path.chmod(0o755)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("config", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    try:
        with args.config.open(encoding="utf-8") as source:
            render(json.load(source), args.output)
    except (ValueError, OSError, json.JSONDecodeError) as error:
        parser.exit(1, f"macOS package metadata: {error}\n")


if __name__ == "__main__":
    main()
