//go:build darwin

package workflows

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSSigningSetup(t *testing.T) {
	setup, err := filepath.Abs("../macos-signing-setup")
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := filepath.Abs("../macos-signing-cleanup/cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fail       string
		requireInstaller bool
		success          bool
	}{
		{name: "package", requireInstaller: true, success: true},
		{name: "cli", success: true},
		{name: "missing certificate", fail: "missing", requireInstaller: true},
		{name: "invalid certificate", fail: "base64", requireInstaller: true},
		{name: "keychain lookup fails", fail: "lookup", requireInstaller: true},
		{name: "import fails", fail: "import", requireInstaller: true},
		{name: "notary fails", fail: "notary", requireInstaller: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write("security", `#!/bin/bash
set -eu
printf 'security %s\n' "$*" >> "$TEST_LOG"
for var in MACOS_APP_CERT MACOS_APP_PASSWORD MACOS_INSTALL_CERT MACOS_INSTALL_PASSWORD MACOS_KEYCHAIN_PASSWORD MACOS_APPLE_ID MACOS_APPLE_PASSWORD; do
  if printenv "$var" >/dev/null; then echo "leaked $var" >&2; exit 9; fi
done
case "$1" in
  default-keychain) if [ "$#" = 3 ]; then [ "$TEST_FAIL" != lookup ] || exit 7; printf '    "/tmp/original keychain"\n'; fi ;;
  create-keychain|unlock-keychain|set-key-partition-list)
    printf 'Password:'; IFS= read -r secret
    if [ "$1" = create-keychain ]; then touch "$2"; fi ;;
  delete-keychain) rm -f "$2" ;;
esac
`)
			write("xcrun", `#!/bin/bash
set -eu
printf 'xcrun %s\n' "$*" >> "$TEST_LOG"
for var in MACOS_APP_CERT MACOS_APP_PASSWORD MACOS_INSTALL_CERT MACOS_INSTALL_PASSWORD MACOS_KEYCHAIN_PASSWORD MACOS_APPLE_ID MACOS_APPLE_PASSWORD; do
  if printenv "$var" >/dev/null; then echo "leaked $var" >&2; exit 9; fi
done
if [ "$1" = swift ]; then
  [ "$(stat -f %Lp "$P12_PATH")" = 600 ] || exit 8
  [ "$TEST_FAIL" != import ] || exit 7
else
  printf 'Password:'; IFS= read -r secret
  [ "$TEST_FAIL" != notary ] || exit 7
fi
`)
			outputFile := filepath.Join(dir, "output")
			if err := os.WriteFile(outputFile, nil, 0600); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			encoded := base64.StdEncoding.EncodeToString([]byte("certificate"))
			appCert := encoded
			installerCert, installerPassword := "", ""
			if tc.requireInstaller {
				installerCert = encoded
				installerPassword = "installer-password"
			}
			if tc.fail == "missing" {
				installerCert = ""
			}
			if tc.fail == "base64" {
				appCert = "!!!"
			}
			env := append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "GITHUB_OUTPUT="+outputFile, "GITHUB_ACTION_PATH="+setup, "TEST_LOG="+log, "TEST_FAIL="+tc.fail, "MACOS_APP_CERT="+appCert, "MACOS_APP_PASSWORD=app-password", "MACOS_INSTALL_CERT="+installerCert, "MACOS_INSTALL_PASSWORD="+installerPassword, "MACOS_REQUIRE_INSTALLER="+map[bool]string{true: "true", false: "false"}[tc.requireInstaller], "MACOS_KEYCHAIN_PASSWORD=key-password", "MACOS_APPLE_ID=apple@example.test", "MACOS_APPLE_PASSWORD=notary-password", "MACOS_TEAM_ID=TEAMID", "MACOS_ORGANIZATION=Test Org")
			cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(setup, "setup.sh"))
			cmd.Env = env
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("unexpected result %v: %s", err, output)
			}
			data, err := os.ReadFile(outputFile)
			if err != nil {
				t.Fatal(err)
			}
			calls, _ := os.ReadFile(log)
			for _, secret := range []string{encoded, "app-password", "installer-password", "key-password", "notary-password"} {
				if strings.Contains(string(output)+string(calls), secret) {
					t.Fatalf("secret appeared in output or command arguments")
				}
			}
			dirs, err := filepath.Glob(filepath.Join(dir, "macos-signing.*"))
			if err != nil {
				t.Fatal(err)
			}
			if !tc.success {
				if len(dirs) != 0 {
					t.Fatalf("failed setup left files: %v", dirs)
				}
				if len(data) != 0 {
					t.Fatalf("failed setup wrote outputs: %s", data)
				}
				return
			}
			outputs := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				key, value, ok := strings.Cut(line, "=")
				if !ok {
					t.Fatalf("bad output %q", line)
				}
				outputs[key] = value
			}
			if outputs["original-keychain"] != "/tmp/original keychain" || outputs["notary-profile"] != "release-notary" {
				t.Fatalf("unexpected outputs: %s", data)
			}
			cmd = exec.CommandContext(t.Context(), "bash", cleanup)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "TEST_LOG="+log, "MACOS_SIGNING_KEYCHAIN="+outputs["keychain"], "MACOS_ORIGINAL_KEYCHAIN="+outputs["original-keychain"])
			output, err = cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("cleanup failed: %v %s", err, output)
			}
			dirs, err = filepath.Glob(filepath.Join(dir, "macos-signing.*"))
			if err != nil || len(dirs) != 0 {
				t.Fatalf("cleanup left files: %v %v", dirs, err)
			}
		})
	}
}

func TestMacOSSigningCleanupRejectsUnsafePaths(t *testing.T) {
	cleanup, err := filepath.Abs("../macos-signing-cleanup/cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "macos-signing.link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "macos-signing.nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	security := filepath.Join(root, "security")
	log := filepath.Join(root, "security-called")
	if err := os.WriteFile(security, []byte("#!/bin/sh\ntouch \"$TEST_LOG\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(link, "signing.keychain-db"),
		nested + "/../../other/signing.keychain-db",
		filepath.Join(outside, "signing.keychain-db"),
	} {
		t.Run(path, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "bash", cleanup)
			cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+root, "MACOS_SIGNING_KEYCHAIN="+path, "MACOS_ORIGINAL_KEYCHAIN=original", "TEST_LOG="+log)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("unsafe path accepted: %s", output)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatalf("security was called for unsafe path: %v", err)
			}
		})
	}
}
