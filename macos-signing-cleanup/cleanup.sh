#!/bin/bash
set -euo pipefail
keychain=${MACOS_SIGNING_KEYCHAIN:?temporary keychain is required}
original=${MACOS_ORIGINAL_KEYCHAIN:?original keychain is required}
temp_root=${RUNNER_TEMP:?runner temporary directory is required}

workdir=$(dirname "$keychain")
name=$(basename "$workdir")
case "$name" in
    macos-signing.*) ;;
    *) echo 'Invalid temporary signing directory' >&2; exit 1 ;;
esac
if [ "$(basename "$keychain")" != signing.keychain-db ] || [ -L "$workdir" ] || [ -L "$keychain" ]; then
    echo 'Invalid temporary signing keychain' >&2
    exit 1
fi
root=$(cd -P "$temp_root" && pwd -P)
physical_workdir=$(cd -P "$workdir" && pwd -P)
if [ "$physical_workdir" != "$root/$name" ]; then
    echo 'Temporary signing keychain is outside the runner directory' >&2
    exit 1
fi
security default-keychain -d user -s "$original"
security delete-keychain "$physical_workdir/signing.keychain-db"
rmdir "$physical_workdir"
