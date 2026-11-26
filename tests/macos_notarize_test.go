package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSNotarization(t *testing.T) {
	script, err := filepath.Abs("../macos-notarize/notarize.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, format, status, failure, extension string
		wantSuccess                              bool
		wantStapler                              bool
	}{
		{name: "accepted package", format: "pkg", status: "Accepted", wantSuccess: true, wantStapler: true},
		{name: "accepted zip", format: "zip", status: "Accepted", wantSuccess: true},
		{name: "transient stapler failure", format: "pkg", status: "Accepted", failure: "transient", wantSuccess: true, wantStapler: true},
		{name: "rejected package", format: "pkg", status: "Invalid"},
		{name: "submission fails", format: "pkg", status: "Accepted", failure: "submit"},
		{name: "stapling fails", format: "pkg", status: "Accepted", failure: "staple", wantStapler: true},
		{name: "validation fails", format: "pkg", status: "Accepted", failure: "validate", wantStapler: true},
		{name: "unknown format", format: "dmg", status: "Accepted"},
		{name: "mismatched format", format: "zip", extension: "pkg", status: "Accepted"},
		{name: "missing status", format: "pkg", status: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write("xcrun", `#!/bin/bash
printf '%s\n' "$*" >> "$TEST_LOG"
if [ "$1" = notarytool ]; then
  if [ "$TEST_FAILURE" = submit ]; then exit 7; fi
  printf '{"status":"%s"}\n' "$TEST_STATUS"
  exit 0
fi
if [ "$2" = "$TEST_FAILURE" ]; then exit 8; fi
if [ "$TEST_FAILURE" = transient ] && [ "$2" = staple ] && [ ! -e "$TEST_LOG.retried" ]; then
  touch "$TEST_LOG.retried"
  exit 8
fi
`)
			write("plutil", `#!/usr/bin/env python3
import json, sys
if sys.argv[1:5] != ["-extract", "status", "raw", "-o"] or sys.argv[5] != "-":
    sys.exit(2)
with open(sys.argv[6]) as f:
    result = json.load(f)
if not result.get("status"):
    sys.exit(1)
print(result["status"], end="")
`)
			write("sleep", "#!/bin/bash\nexit 0\n")
			extension := tc.extension
			if extension == "" {
				extension = tc.format
			}
			artifact := filepath.Join(dir, "artifact with space."+extension)
			if err := os.WriteFile(artifact, []byte("artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			cmd := exec.CommandContext(t.Context(), "bash", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "MACOS_NOTARIZE_ARTIFACT="+artifact, "MACOS_NOTARIZE_PROFILE=test-profile", "MACOS_NOTARIZE_KEYCHAIN=test-keychain", "MACOS_NOTARIZE_FORMAT="+tc.format, "TEST_LOG="+log, "TEST_STATUS="+tc.status, "TEST_FAILURE="+tc.failure)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("unexpected result %v: %s", err, output)
			}
			data, readErr := os.ReadFile(log)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			calls := string(data)
			resultFiles, err := filepath.Glob(filepath.Join(dir, "notarization.*"))
			if err != nil || len(resultFiles) != 0 {
				t.Fatalf("temporary result not cleaned: %v %v", resultFiles, err)
			}
			if tc.failure == "transient" && strings.Count(calls, "stapler staple ") != 2 {
				t.Fatalf("transient failure did not retry once: %s", calls)
			}
			if tc.failure == "staple" && (strings.Count(calls, "stapler staple ") != 5 || strings.Contains(calls, "stapler validate ")) {
				t.Fatalf("incorrect staple retries: %s", calls)
			}
			if tc.failure == "validate" && strings.Count(calls, "stapler validate ") != 5 {
				t.Fatalf("incorrect validate retries: %s", calls)
			}
			if strings.Contains(calls, "stapler") != tc.wantStapler {
				t.Fatalf("unexpected stapler calls: %s", calls)
			}
			if tc.wantSuccess && !strings.Contains(calls, "notarytool submit "+artifact+" --keychain-profile test-profile --keychain test-keychain --wait --output-format json") {
				t.Fatalf("incorrect submission: %s", calls)
			}
			if tc.wantSuccess && tc.format == "pkg" && (!strings.Contains(calls, "stapler staple "+artifact) || !strings.Contains(calls, "stapler validate "+artifact)) {
				t.Fatalf("missing package validation: %s", calls)
			}
		})
	}
}
