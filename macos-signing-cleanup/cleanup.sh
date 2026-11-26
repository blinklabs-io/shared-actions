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
snapshot="$physical_workdir/search-list.json"
original_snapshot="$physical_workdir/original-keychain"
if [ ! -f "$snapshot" ] || [ -L "$snapshot" ] || [ ! -f "$original_snapshot" ] || [ -L "$original_snapshot" ] || [ "$(cat "$original_snapshot")" != "$original" ]; then
    echo 'Invalid keychain recovery data' >&2
    exit 1
fi
restored=true
python3 "$GITHUB_ACTION_PATH/../macos-signing-setup/search-list.py" restore "$snapshot" || restored=false
security default-keychain -d user -s "$original" || restored=false
if [ -e "$physical_workdir/signing.keychain-db" ]; then
    security delete-keychain "$physical_workdir/signing.keychain-db" || restored=false
fi
if [ "$restored" != true ]; then
    echo "Keychain cleanup failed; recovery data remains in $physical_workdir" >&2
    exit 1
fi
rm -f "$snapshot" "$original_snapshot"
rmdir "$physical_workdir"
