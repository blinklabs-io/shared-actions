#!/usr/bin/env python3
"""Save, extend and restore the macOS user keychain search list."""

import json
import os
import shlex
import subprocess
import sys
from pathlib import Path


def main():
    if sys.argv[1:] == ["default"]:
        result = subprocess.run(
            ["security", "default-keychain", "-d", "user"],
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        )
        paths = shlex.split(result.stdout)
        if len(paths) != 1 or not paths[0].startswith("/") or "\n" in paths[0] or "\r" in paths[0]:
            raise ValueError("invalid default keychain")
        print(paths[0])
        return
    if len(sys.argv) not in (3, 4):
        raise ValueError("expected snapshot, extend or restore and a snapshot path")
    operation = sys.argv[1]
    snapshot = Path(sys.argv[2])
    if operation == "snapshot" and len(sys.argv) == 3:
        result = subprocess.run(
            ["security", "list-keychains", "-d", "user"],
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        )
        paths = shlex.split(result.stdout)
        if any(not path.startswith("/") or "\n" in path or "\r" in path for path in paths):
            raise ValueError("invalid keychain path in search list")
        descriptor = os.open(snapshot, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(paths, output)
    elif operation in ("extend", "restore"):
        with snapshot.open(encoding="utf-8") as source:
            paths = json.load(source)
        if not isinstance(paths, list) or any(
            not isinstance(path, str) or not path.startswith("/") or "\n" in path or "\r" in path
            for path in paths
        ):
            raise ValueError("invalid keychain search-list snapshot")
        if operation == "extend" and len(sys.argv) == 4:
            keychain = sys.argv[3]
            if not keychain.startswith("/") or "\n" in keychain or "\r" in keychain:
                raise ValueError("invalid temporary keychain path")
            paths = [keychain] + [path for path in paths if path != keychain]
        elif operation != "restore" or len(sys.argv) != 3:
            raise ValueError("invalid search-list operation")
        subprocess.run(["security", "list-keychains", "-d", "user", "-s", *paths], check=True)
    else:
        raise ValueError("invalid search-list operation")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, json.JSONDecodeError, subprocess.CalledProcessError) as error:
        print(f"keychain search list: {error}", file=sys.stderr)
        sys.exit(1)
