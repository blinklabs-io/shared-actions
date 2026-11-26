package workflows

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMacOSPilotIsManualAndDefaultBranchOnly(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-test-macos-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf desktopWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	guard := wf.Jobs["validate-dispatch"].Steps
	if len(guard) != 1 {
		t.Fatal("expected a dispatch guard")
	}
	for name, expression := range map[string]string{
		"EVENT_NAME": "${{ github.event_name }}", "REF": "${{ github.ref }}",
		"DEFAULT_BRANCH": "${{ github.event.repository.default_branch }}",
	} {
		if guard[0].Env[name] != expression {
			t.Fatalf("dispatch guard %s is not mapped to %s", name, expression)
		}
	}
	for _, tc := range []struct {
		name, event, ref, branch string
		allow                    bool
	}{
		{"manual main", "workflow_dispatch", "refs/heads/main", "main", true},
		{"manual branch", "workflow_dispatch", "refs/heads/feature", "main", false},
		{"manual tag", "workflow_dispatch", "refs/tags/v1.0.0", "main", false},
		{"push", "push", "refs/heads/main", "main", false},
		{"missing default", "workflow_dispatch", "refs/heads/main", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-e", "-c", guard[0].Run)
			cmd.Env = append(os.Environ(), "EVENT_NAME="+tc.event, "REF="+tc.ref, "DEFAULT_BRANCH="+tc.branch)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.allow {
				t.Fatalf("allowed=%t want %t: %s", err == nil, tc.allow, out)
			}
		})
	}
	signed := wf.Jobs["signed-artifact"]
	if signed.RunsOn != "${{ inputs.architecture == 'arm64' && 'macos-15' || 'macos-15-intel' }}" {
		t.Fatal("pilot runner must match the target architecture")
	}
	if !strings.Contains(signed.Needs.Value, "validate-dispatch") {
		t.Fatal("signed job must depend on dispatch guard")
	}
	var sign, upload bool
	for _, step := range signed.Steps {
		if step.Uses == "$/macos-release" {
			sign = true
			if step.With["mode"] != "signed" {
				t.Fatal("pilot must require signed mode")
			}
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			upload = true
			if !sign || step.With["path"] != "${{ steps.signed.outputs.artifact }}" || step.With["if-no-files-found"] != "error" {
				t.Fatal("upload must use the verified artifact")
			}
		}
	}
	if !sign || !upload {
		t.Fatal("missing signing or artifact upload")
	}
}
