package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func flutterSteps(t *testing.T) []struct{ Name, Run string } {
	t.Helper()
	data, err := os.ReadFile("../.github/workflows/reuseable-flutter.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct{ Steps []struct{ Name, Run string } }
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	return wf.Jobs["flutter"].Steps
}

func TestFlutterJavaSelectionBeforeAPKBuild(t *testing.T) {
	dir := t.TempDir()
	jdk := filepath.Join(dir, "Java 17")
	if err := os.MkdirAll(filepath.Join(jdk, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jdk, "bin", "java"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
set -eu
case "$1" in
config) test "$2" = --jdk-dir; test "$3" = "$EXPECTED_JDK"; printf '%s' "$3" > "$JDK_STATE" ;;
build) test -f "$JDK_STATE" || { echo 'Gradle rejected the default runner JDK' >&2; exit 1; }; test "$(cat "$JDK_STATE")" = "$EXPECTED_JDK" ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "flutter"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	built := false
	for _, step := range flutterSteps(t) {
		if step.Name != "Select Java for APK build" && step.Name != "Build APK" {
			continue
		}
		cmd := exec.Command("bash", "-e", "-c", step.Run)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "JAVA_VERSION=17", "JAVA_HOME_17_X64="+jdk, "EXPECTED_JDK="+jdk, "JDK_STATE="+filepath.Join(dir, "jdk"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", step.Name, err, out)
		}
		if step.Name == "Build APK" {
			built = true
		}
	}
	if !built {
		t.Fatal("APK build did not run")
	}
}

func TestFlutterJavaSelectionIsOptionalForAPKBuilds(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-flutter.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On struct {
			Call struct {
				Inputs map[string]struct {
					Type, Default string
					Required      bool
				}
			} `yaml:"workflow_call"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Steps []struct {
				Name, If string
				Env      map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	input, ok := wf.On.Call.Inputs["java-version"]
	if !ok || input.Type != "string" || input.Default != "" || input.Required {
		t.Fatal("Java selection must be an optional, unset-by-default input")
	}
	found := false
	for _, step := range wf.Jobs["flutter"].Steps {
		if step.Name != "Select Java for APK build" {
			continue
		}
		found = true
		if step.If != "${{ inputs.build-apk && inputs.java-version != '' }}" || step.Env["JAVA_VERSION"] != "${{ inputs.java-version }}" {
			t.Fatal("Java selection must be conditional on an explicit APK input passed through the environment")
		}
	}
	if !found {
		t.Fatal("Java selection step missing")
	}
}

func TestFlutterJavaSelectionRejectsInvalidOrMissingRuntime(t *testing.T) {
	var script string
	for _, step := range flutterSteps(t) {
		if step.Name == "Select Java for APK build" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("Java selection step missing")
	}
	for _, tc := range []struct{ version, diagnostic string }{
		{"0", "positive major"},
		{"17.0", "positive major"},
		{"17; touch injected", "positive major"},
		{"$(touch injected)", "positive major"},
		{"99999", "not installed"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "JAVA_VERSION="+tc.version, "JAVA_HOME_99999_X64=")
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.diagnostic) {
				t.Fatalf("invalid runtime accepted: %v: %s", err, out)
			}
			if _, err := os.Stat(filepath.Join(cmd.Dir, "injected")); !os.IsNotExist(err) {
				t.Fatal("input executed")
			}
		})
	}
}
