#!/bin/bash
set -euo pipefail

artifact=${MACOS_NOTARIZE_ARTIFACT:?artifact is required}
profile=${MACOS_NOTARIZE_PROFILE:?notary profile is required}
keychain=${MACOS_NOTARIZE_KEYCHAIN:?keychain is required}
format=${MACOS_NOTARIZE_FORMAT:?artifact format is required}

case "$format" in
    pkg|zip) ;;
    *) echo "Unsupported notarization format: $format" >&2; exit 1 ;;
esac
case "$artifact" in
    *".$format") ;;
    *) echo "Artifact extension does not match $format format" >&2; exit 1 ;;
esac
if [ ! -f "$artifact" ]; then
    echo "Notarization artifact does not exist: $artifact" >&2
    exit 1
fi

result=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization.XXXXXXXX")
trap 'rm -f "$result"' EXIT
xcrun notarytool submit "$artifact" --keychain-profile "$profile" --keychain "$keychain" --wait --output-format json > "$result"
status=$(plutil -extract status raw -o - "$result")
if [ "$status" != Accepted ]; then
    echo "Notarization did not succeed: $status" >&2
    exit 1
fi

if [ "$format" = pkg ]; then
    for operation in staple validate; do
        succeeded=false
        for attempt in 1 2 3 4 5; do
            if xcrun stapler "$operation" "$artifact"; then
                succeeded=true
                break
            fi
            if [ "$attempt" -lt 5 ]; then
                sleep "$((attempt * 15))"
            fi
        done
        if [ "$succeeded" != true ]; then
            echo "Failed to $operation notarization ticket" >&2
            exit 1
        fi
    done
fi
