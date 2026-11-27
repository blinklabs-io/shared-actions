package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type uiCIWorkflow struct {
	Jobs map[string]struct {
		Needs []string `yaml:"needs"`
		If    string   `yaml:"if"`
		Steps []struct {
			Name, Run, If string
			Env, With     map[string]string
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadUIChecks(t *testing.T) uiCIWorkflow {
	t.Helper()
	data, err := os.ReadFile("../.github/workflows/reuseable-go-web-desktop-ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf uiCIWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	return wf
}
func uiStep(t *testing.T, job, name string) string {
	t.Helper()
	for _, step := range loadUIChecks(t).Jobs[job].Steps {
		if step.Name == name {
			return step.Run
		}
	}
	t.Fatalf("missing %s/%s", job, name)
	return ""
}
func TestUIProjectSettingsValidation(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash required")
	}
	script := uiStep(t, "validate", "Validate project settings")
	for _, tc := range []struct {
		name, web, dist, ext, extName, goDir, build string
		valid                                       bool
	}{
		{"desktop with extension", "ui/web", "ui/web/dist", "ui/extension", "connector", "ui", "./cmd/wallet", true},
		{"without extension", "web", "web/dist", "", "", ".", "./cmd/app", true},
		{"outside workspace", "../web", "web/dist", "", "", "ui", "./cmd/app", false},
		{"absolute path", "/tmp/web", "web/dist", "", "", "ui", "./cmd/app", false},
		{"missing extension name", "web", "web/dist", "extension", "", "ui", "./cmd/app", false},
		{"escaping Go build", "web", "web/dist", "", "", "ui", "./../other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Env = append(os.Environ(), "WEB_DIRECTORY="+tc.web, "WEB_DIST_DIRECTORY="+tc.dist, "EXTENSION_DIRECTORY="+tc.ext, "EXTENSION_NAME="+tc.extName, "GO_DIRECTORY="+tc.goDir, "GO_BUILD_PACKAGE="+tc.build, "WINDOWS_TEST_PACKAGE=", "WEBVIEW_TARGET=")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.valid {
				t.Fatalf("result %v, valid=%t: %s", err, tc.valid, out)
			}
		})
	}
	for _, tc := range []struct {
		name, windowsPackage, webviewTarget string
		valid                               bool
	}{
		{"optional checks", "./internal/vault/", "wallet-binary-webview", true},
		{"Windows option", "-args", "", false},
		{"Windows escape", "./../outside", "", false},
		{"make option", "", "--file=external", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Env = append(os.Environ(), "WEB_DIRECTORY=web", "WEB_DIST_DIRECTORY=web/dist", "EXTENSION_DIRECTORY=", "EXTENSION_NAME=", "GO_DIRECTORY=ui", "GO_BUILD_PACKAGE=./cmd/app", "WINDOWS_TEST_PACKAGE="+tc.windowsPackage, "WEBVIEW_TARGET="+tc.webviewTarget)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.valid {
				t.Fatalf("result %v, valid=%t: %s", err, tc.valid, out)
			}
		})
	}
}
func TestUIGoPackageArguments(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash required")
	}
	script := uiStep(t, "ui-go", "Vet and test Go UI")
	for _, tc := range []struct {
		name, packages string
		valid          bool
	}{{"scoped", "./cmd/... ./internal/...", true}, {"default", "./...", true}, {"flag injection", "./cmd/... -v", false}, {"path escape", "./../outside", false}, {"multiple lines", "./cmd/...\n./internal/...", false}, {"empty", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stub := filepath.Join(dir, "go")
			if err := os.WriteFile(stub, []byte("#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$TEST_LOG\"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_LOG="+log, "GO_PACKAGES="+tc.packages)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.valid {
				t.Fatalf("result %v: %s", err, out)
			}
			if tc.valid {
				data, err := os.ReadFile(log)
				if err != nil || string(data) != "vet "+tc.packages+"\ntest "+tc.packages+"\n" {
					t.Fatalf("wrong commands %q %v", data, err)
				}
			} else if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("invalid arguments reached Go")
			}
		})
	}
}
func TestUIWorkflowDependencies(t *testing.T) {
	wf := loadUIChecks(t)
	for job, want := range map[string]string{"web": "validate", "extension": "validate", "ui-go": "web", "ui-windows": "validate", "ui-webview": "ui-go", "ui-lint": "ui-go"} {
		needs := wf.Jobs[job].Needs
		if len(needs) != 1 || needs[0] != want {
			t.Errorf("%s needs %v, want %s", job, needs, want)
		}
	}
	if wf.Jobs["extension"].If != "inputs.extension-directory != ''" || wf.Jobs["ui-windows"].If != "inputs.windows-test-package != ''" || wf.Jobs["ui-webview"].If != "inputs.webview-target != ''" {
		t.Fatal("optional capabilities must be selected through inputs")
	}
	for _, job := range []string{"web", "ui-go", "ui-webview", "ui-lint"} {
		var artifact bool
		for _, step := range wf.Jobs[job].Steps {
			if step.With["name"] == "${{ inputs.web-artifact-name }}" {
				artifact = true
			}
		}
		if !artifact {
			t.Errorf("%s does not use the shared web artifact", job)
		}
	}
}
func TestUIWebCommandsStopOnFailure(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash required")
	}
	for job, step := range map[string]string{"web": "Install and check web project", "extension": "Install and check extension"} {
		t.Run(job, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			stub := filepath.Join(dir, "npm")
			if err := os.WriteFile(stub, []byte("#!/bin/bash\nprintf '%s\\n' \"$*\" >> \"$TEST_LOG\"\n[[ \"$*\" != 'run lint' ]]\n"), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-e", "-c", uiStep(t, job, step))
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_LOG="+log)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("lint failure did not fail workflow: %s", out)
			}
			data, err := os.ReadFile(log)
			if err != nil || strings.TrimSpace(string(data)) != "ci\nrun lint" {
				t.Fatalf("commands continued after failure: %q %v", data, err)
			}
		})
	}
}
