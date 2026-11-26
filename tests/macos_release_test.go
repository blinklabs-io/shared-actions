package workflows

import (
	"os"
	"os/exec"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMacOSReleaseSettings(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "macos_release_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("macOS release settings tests failed: %v\n%s", err, output)
	}
}

func TestMacOSReleaseSequence(t *testing.T) {
	data, err := os.ReadFile("../macos-release/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Outputs map[string]struct {
			Value string `yaml:"value"`
		} `yaml:"outputs"`
		Runs struct {
			Steps []struct {
				ID   string `yaml:"id"`
				Name string `yaml:"name"`
				Uses string `yaml:"uses"`
				If   string `yaml:"if"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	if got := action.Outputs["artifact"].Value; got != "${{ steps.complete.outputs.artifact }}" {
		t.Fatalf("artifact output bypasses completion: %s", got)
	}
	names := []string{"prepare", "build", "inputs", "signing", "package", "zip", "Notarize artifact", "Verify notarized installer", "Clean up signing keychain", "complete"}
	if len(action.Runs.Steps) != len(names) {
		t.Fatalf("unexpected release step count: %d", len(action.Runs.Steps))
	}
	for i, step := range action.Runs.Steps {
		name := step.ID
		if name == "" {
			name = step.Name
		}
		if name != names[i] {
			t.Fatalf("release step %d = %q, want %q", i, name, names[i])
		}
	}
	cleanup := action.Runs.Steps[8]
	if cleanup.If != "always() && steps.signing.outcome == 'success'" || cleanup.Uses != "$/macos-signing-cleanup" {
		t.Fatalf("keychain cleanup must run after later failures: %+v", cleanup)
	}
	if action.Runs.Steps[6].If != "inputs.mode == 'signed'" || action.Runs.Steps[7].If != "inputs.mode == 'signed' && steps.prepare.outputs.artifact-type == 'pkg'" {
		t.Fatal("signed artifacts must be notarized and packages verified")
	}
}
