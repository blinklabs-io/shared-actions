package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsMSIProducer(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("PowerShell is required")
	}
	wf := loadDesktopWorkflow(t)
	var script string
	for _, step := range wf.Jobs["build-binaries"].Steps {
		if step.Name == "Build MSI (Windows)" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("MSI build step missing")
	}
	for _, tc := range []struct {
		name        string
		missing     bool
		exitCode    string
		wantSuccess bool
	}{
		{name: "successful packaging", exitCode: "0", wantSuccess: true},
		{name: "missing payload", missing: true, exitCode: "0"},
		{name: "packaging failure", exitCode: "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			packaging := filepath.Join(dir, "packaging", "windows")
			if err := os.MkdirAll(packaging, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(packaging, "build-msi.ps1"), []byte("exit "+tc.exitCode+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "adder.exe")
			tray := filepath.Join(dir, "adder-tray.exe")
			if err := os.WriteFile(exe, []byte("payload"), 0600); err != nil {
				t.Fatal(err)
			}
			if !tc.missing {
				if err := os.WriteFile(tray, []byte("payload"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			envFile := filepath.Join(dir, "env")
			if err := os.WriteFile(envFile, nil, 0600); err != nil {
				t.Fatal(err)
			}
			scriptPath := filepath.Join(dir, "step.ps1")
			if err := os.WriteFile(scriptPath, []byte("$ErrorActionPreference = 'Stop'\n"+script), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "pwsh", "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "ADDER_EXE="+exe, "ADDER_TRAY_EXE="+tray, "GITHUB_ENV="+envFile, "VERSION=v1.2.3", "APPLICATION_NAME=adder", "ARCH=amd64")
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("unexpected script result: %v\n%s", err, output)
			}
			if tc.missing && !strings.Contains(string(output), "Missing signed MSI payload") {
				t.Fatalf("missing payload diagnostic absent: %s", output)
			}
			if tc.exitCode == "7" {
				if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 7 {
					t.Fatalf("expected packaging exit 7, got %v\n%s", err, output)
				}
			}
			data, err := os.ReadFile(envFile)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantSuccess && strings.TrimSpace(string(data)) != "MSI_PATH=dist/adder-1.2.3-windows-amd64.msi" {
				t.Fatalf("unexpected environment output %q", data)
			}
			if !tc.wantSuccess && len(data) != 0 {
				t.Fatalf("failed packaging wrote environment output %q", data)
			}
		})
	}
}
