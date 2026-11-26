#!/bin/bash
set -euo pipefail

for name in MACOS_APP_CERT MACOS_APP_PASSWORD MACOS_KEYCHAIN_PASSWORD MACOS_APPLE_ID MACOS_APPLE_PASSWORD MACOS_TEAM_ID MACOS_ORGANIZATION GITHUB_OUTPUT GITHUB_ACTION_PATH; do
    if [ -z "${!name:-}" ]; then
        echo "Required signing input is missing: $name" >&2
        exit 1
    fi
done
case "${MACOS_REQUIRE_INSTALLER:-true}" in
    true|false) ;;
    *) echo 'require-installer must be true or false' >&2; exit 1 ;;
esac
if [ "${MACOS_REQUIRE_INSTALLER:-true}" = true ] && { [ -z "${MACOS_INSTALL_CERT:-}" ] || [ -z "${MACOS_INSTALL_PASSWORD:-}" ]; }; then
    echo 'Installer certificate and password are required' >&2
    exit 1
fi
if { [ -n "${MACOS_INSTALL_CERT:-}" ] && [ -z "${MACOS_INSTALL_PASSWORD:-}" ]; } || { [ -z "${MACOS_INSTALL_CERT:-}" ] && [ -n "${MACOS_INSTALL_PASSWORD:-}" ]; }; then
    echo 'Installer certificate and password must be supplied together' >&2
    exit 1
fi
case "$MACOS_TEAM_ID$MACOS_ORGANIZATION" in
    *$'\n'*|*$'\r'*) echo 'Identity values must be single-line' >&2; exit 1 ;;
esac

app_cert=$MACOS_APP_CERT
app_password=$MACOS_APP_PASSWORD
installer_cert=${MACOS_INSTALL_CERT:-}
installer_password=${MACOS_INSTALL_PASSWORD:-}
keychain_password=$MACOS_KEYCHAIN_PASSWORD
apple_id=$MACOS_APPLE_ID
apple_password=$MACOS_APPLE_PASSWORD
unset MACOS_APP_CERT MACOS_APP_PASSWORD MACOS_INSTALL_CERT MACOS_INSTALL_PASSWORD \
    MACOS_KEYCHAIN_PASSWORD MACOS_APPLE_ID MACOS_APPLE_PASSWORD

umask 077
original_keychain=$(security default-keychain -d user | sed -e 's/^[[:space:]]*"//' -e 's/"[[:space:]]*$//')
if [ -z "$original_keychain" ]; then
    echo 'Cannot determine original default keychain' >&2
    exit 1
fi
workdir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/macos-signing.XXXXXXXX")
keychain="$workdir/signing.keychain-db"
profile=release-notary
complete=false
cleanup() {
    rm -f "$workdir/application.p12" "$workdir/installer.p12"
    if [ "$complete" != true ]; then
        security default-keychain -d user -s "$original_keychain" >/dev/null 2>&1 || true
        security delete-keychain "$keychain" >/dev/null 2>&1 || true
        rmdir "$workdir" 2>/dev/null || true
    fi
}
trap cleanup EXIT
printf '%s' "$app_cert" | base64 --decode -o "$workdir/application.p12"
unset app_cert
SECRET_INPUT="$keychain_password" SECRET_ACTION=create SECRET_KEYCHAIN="$keychain" /usr/bin/expect "$GITHUB_ACTION_PATH/secret.exp"
security default-keychain -d user -s "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
SECRET_INPUT="$keychain_password" SECRET_ACTION=unlock SECRET_KEYCHAIN="$keychain" /usr/bin/expect "$GITHUB_ACTION_PATH/secret.exp"
P12_PASSWORD="$app_password" P12_PATH="$workdir/application.p12" TRUSTED_APPLICATION=/usr/bin/codesign xcrun swift "$GITHUB_ACTION_PATH/import.swift"
unset app_password
rm -f "$workdir/application.p12"
installer_identity=''
if [ -n "${installer_cert:-}" ]; then
    printf '%s' "$installer_cert" | base64 --decode -o "$workdir/installer.p12"
    unset installer_cert
    P12_PASSWORD="$installer_password" P12_PATH="$workdir/installer.p12" TRUSTED_APPLICATION=/usr/bin/productsign xcrun swift "$GITHUB_ACTION_PATH/import.swift"
    unset installer_password
    rm -f "$workdir/installer.p12"
    installer_identity="Developer ID Installer: $MACOS_ORGANIZATION ($MACOS_TEAM_ID)"
fi
SECRET_INPUT="$keychain_password" SECRET_ACTION=partition SECRET_KEYCHAIN="$keychain" /usr/bin/expect "$GITHUB_ACTION_PATH/secret.exp"
unset keychain_password
SECRET_INPUT="$apple_password" SECRET_ACTION=notary SECRET_KEYCHAIN="$keychain" SECRET_PROFILE="$profile" NOTARY_APPLE_ID="$apple_id" NOTARY_TEAM_ID="$MACOS_TEAM_ID" /usr/bin/expect "$GITHUB_ACTION_PATH/secret.exp"
unset apple_password
{
    printf 'keychain=%s\n' "$keychain"
    printf 'original-keychain=%s\n' "$original_keychain"
    printf 'application-identity=Developer ID Application: %s (%s)\n' "$MACOS_ORGANIZATION" "$MACOS_TEAM_ID"
    printf 'installer-identity=%s\n' "$installer_identity"
    printf 'notary-profile=%s\n' "$profile"
} >> "$GITHUB_OUTPUT"
complete=true
