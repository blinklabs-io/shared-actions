//go:build darwin

package workflows

import (
	"encoding/base64"
	"encoding/json"
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
		{name: "search list lookup fails", fail: "search", requireInstaller: true},
		{name: "search list update fails", fail: "extend", requireInstaller: true},
		{name: "import fails", fail: "import", requireInstaller: true},
		{name: "import and cleanup fail", fail: "import-delete", requireInstaller: true},
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
  list-keychains)
    if [ "$#" = 3 ]; then
      [ "$TEST_FAIL" != search ] || exit 7
      printf '    "/tmp/first keychain"\n    "/tmp/second.keychain"\n'
    elif [ "$#" = 7 ] && [ "$TEST_FAIL" = extend ]; then
      exit 7
    fi ;;
  create-keychain|unlock-keychain|set-key-partition-list)
    printf 'Password:'; IFS= read -r secret
    if [ "$1" = create-keychain ]; then touch "$2"; fi ;;
  delete-keychain) [ "${TEST_FAIL:-}" != import-delete ] || exit 7; rm -f "$2" ;;
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
  [ "$TEST_FAIL" != import ] && [ "$TEST_FAIL" != import-delete ] || exit 7
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
				if tc.fail == "import-delete" {
					if len(dirs) != 1 || !strings.Contains(string(output), "recovery data remains") {
						t.Fatalf("failed deletion not reported: %v %s", dirs, output)
					}
					for _, name := range []string{"search-list.json", "original-keychain", "signing.keychain-db"} {
						if _, err := os.Stat(filepath.Join(dirs[0], name)); err != nil {
							t.Fatalf("missing recovery file %s: %v", name, err)
						}
					}
					if _, err := os.Stat(filepath.Join(dirs[0], "application.p12")); !os.IsNotExist(err) {
						t.Fatalf("certificate file remains: %v", err)
					}
					return
				}
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
			if !strings.Contains(string(calls), "--keychain "+outputs["keychain"]) {
				t.Fatalf("notary credentials must use the temporary keychain: %s", calls)
			}
			snapshot, err := os.ReadFile(filepath.Join(filepath.Dir(outputs["keychain"]), "search-list.json"))
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			if err := json.Unmarshal(snapshot, &paths); err != nil || len(paths) != 2 || paths[0] != "/tmp/first keychain" || paths[1] != "/tmp/second.keychain" {
				t.Fatalf("unexpected search list: %s %v", snapshot, err)
			}
			cmd = exec.CommandContext(t.Context(), "bash", cleanup)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "TEST_LOG="+log, "MACOS_SIGNING_KEYCHAIN="+outputs["keychain"], "MACOS_ORIGINAL_KEYCHAIN="+outputs["original-keychain"], "GITHUB_ACTION_PATH="+filepath.Dir(cleanup))
			output, err = cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("cleanup failed: %v %s", err, output)
			}
			calls, err = os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(calls), "security list-keychains -d user -s "+outputs["keychain"]+" /tmp/first keychain /tmp/second.keychain") || !strings.Contains(string(calls), "security list-keychains -d user -s /tmp/first keychain /tmp/second.keychain") {
				t.Fatalf("search list not extended and restored in order: %s", calls)
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

func TestMacOSSigningCleanupContinuesAfterRestoreFailure(t *testing.T) {
	cleanup, err := filepath.Abs("../macos-signing-cleanup/cleanup.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "macos-signing.fixture")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"signing.keychain-db": "keychain", "search-list.json": "[\"/tmp/original\"]", "original-keychain": "/tmp/original"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	security := filepath.Join(root, "security")
	stub := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_LOG"
if [ "$1" = list-keychains ]; then exit 7; fi
if [ "$1" = delete-keychain ]; then rm -f "$2"; fi
`
	if err := os.WriteFile(security, []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "calls")
	cmd := exec.CommandContext(t.Context(), "bash", cleanup)
	cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+root, "GITHUB_ACTION_PATH="+filepath.Dir(cleanup), "MACOS_SIGNING_KEYCHAIN="+filepath.Join(dir, "signing.keychain-db"), "MACOS_ORIGINAL_KEYCHAIN=/tmp/original", "TEST_LOG="+log)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "recovery data remains") {
		t.Fatalf("restore failure not reported: %v %s", err, output)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "default-keychain -d user -s /tmp/original") || !strings.Contains(string(calls), "delete-keychain ") {
		t.Fatalf("later cleanup was skipped: %s", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "signing.keychain-db")); !os.IsNotExist(err) {
		t.Fatalf("temporary keychain remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "search-list.json")); err != nil {
		t.Fatalf("recovery data missing: %v", err)
	}
	if err := os.WriteFile(security, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	retry := exec.CommandContext(t.Context(), "bash", cleanup)
	retry.Env = cmd.Env
	if output, err := retry.CombinedOutput(); err != nil {
		t.Fatalf("cleanup retry failed: %v %s", err, output)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup retry left recovery directory: %v", err)
	}
}
