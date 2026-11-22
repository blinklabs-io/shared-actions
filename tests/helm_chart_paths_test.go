package workflows

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmPublishRejectsEscapingChartMembers(t *testing.T) {
	script := helmPublishStep(t, "Verify chart paths")
	for _, test := range []struct {
		name, wantError string
	}{
		{name: "regular chart"},
		{name: "internal shared directory"},
		{name: "external file", wantError: "must be inside the repository"},
		{name: "external directory", wantError: "must be inside the repository"},
		{name: "nested external link", wantError: "must be inside the repository"},
		{name: "directory cycle", wantError: "symlink cycle"},
		{name: "dangling link", wantError: "cannot be resolved"},
		{name: "outside chart", wantError: "must be inside the repository"},
		{name: "Git metadata", wantError: "must not include Git metadata"},
		{name: "untracked workspace file", wantError: "must come from checked-out source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			chart := filepath.Join(root, "charts", "alpha")
			shared := filepath.Join(root, "shared")
			outside := t.TempDir()
			for _, dir := range []string{chart, shared} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []string{filepath.Join(chart, "Chart.yaml"), filepath.Join(shared, "values.yaml"), filepath.Join(outside, "values.yaml")} {
				if err := os.WriteFile(file, []byte("fixture: true\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			symlink := func(target, name string) {
				t.Helper()
				if err := os.Symlink(target, name); err != nil {
					t.Fatal(err)
				}
			}
			selected := chart
			switch test.name {
			case "internal shared directory":
				symlink(shared, filepath.Join(chart, "shared"))
			case "external file":
				symlink(filepath.Join(outside, "values.yaml"), filepath.Join(chart, "values.yaml"))
			case "external directory":
				symlink(outside, filepath.Join(chart, "shared"))
			case "nested external link":
				symlink(outside, filepath.Join(shared, "external"))
				symlink(shared, filepath.Join(chart, "shared"))
			case "directory cycle":
				symlink(chart, filepath.Join(chart, "cycle"))
			case "dangling link":
				symlink(filepath.Join(root, "missing"), filepath.Join(chart, "values.yaml"))
			case "outside chart":
				selected = outside
			case "Git metadata":
				symlink(filepath.Join(root, ".git", "config"), filepath.Join(chart, "metadata"))
			case "untracked workspace file":
				untracked := filepath.Join(root, "private-values.yaml")
				if err := os.WriteFile(untracked, []byte("dummy: private\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				symlink(untracked, filepath.Join(chart, "private-values.yaml"))
			}
			for _, args := range [][]string{{"init", "-b", "main"}, {"add", "charts", "shared"}} {
				cmd := exec.CommandContext(t.Context(), "git", args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("prepare source index: %v\n%s", err, out)
				}
			}
			encoded, err := json.Marshal([]string{selected})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CHARTS="+string(encoded))
			out, err := cmd.CombinedOutput()
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("valid chart rejected: %v\n%s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), test.wantError) {
				t.Fatalf("unsafe chart accepted or failed elsewhere: %v\n%s", err, out)
			}
		})
	}
}
