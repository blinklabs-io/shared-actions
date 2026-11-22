package workflows

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// reusablePublishDesktopPath is the reusable workflow that drives adder's
// multi-platform release pipeline (macOS .pkg, Windows .msi, tarballs, images).
const reusablePublishDesktopPath = "../.github/workflows/reuseable-publish-desktop.yml"

// loadPublishDesktopWorkflow parses the reusable-publish-desktop workflow's
// workflow_call inputs/secrets and job names. The `on` key is decoded via a
// struct field tag so yaml.v3 does not fold the bare `on:` key to a boolean.
func loadPublishDesktopWorkflow(t *testing.T) (inputs, secrets, jobs map[string]struct{}) {
	t.Helper()
	data, err := os.ReadFile(reusablePublishDesktopPath)
	if err != nil {
		t.Fatalf("read reusable publish-desktop workflow: %v", err)
	}
	var wf struct {
		On struct {
			WorkflowCall struct {
				Inputs  map[string]yaml.Node `yaml:"inputs"`
				Secrets map[string]yaml.Node `yaml:"secrets"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse reusable publish-desktop workflow: %v", err)
	}
	toSet := func(m map[string]yaml.Node) map[string]struct{} {
		out := make(map[string]struct{}, len(m))
		for k := range m {
			out[k] = struct{}{}
		}
		return out
	}
	inputs = toSet(wf.On.WorkflowCall.Inputs)
	secrets = toSet(wf.On.WorkflowCall.Secrets)
	jobs = toSet(wf.Jobs)
	if len(inputs) == 0 {
		t.Fatal("reusable publish-desktop workflow declares no workflow_call inputs")
	}
	if len(jobs) == 0 {
		t.Fatal("reusable publish-desktop workflow declares no jobs")
	}
	return inputs, secrets, jobs
}

// TestReusablePublishDesktopJobs verifies the reusable workflow keeps the full
// release pipeline: draft release, the multi-platform binary/installer matrix,
// native image builds, the manifest merge, and the finalize step.
func TestReusablePublishDesktopJobs(t *testing.T) {
	_, _, jobs := loadPublishDesktopWorkflow(t)
	for _, want := range []string{
		"create-draft-release",
		"build-binaries",
		"build-images",
		"build-image-manifest",
		"finalize-release",
	} {
		if _, ok := jobs[want]; !ok {
			t.Errorf("reusable publish-desktop workflow missing job %q", want)
		}
	}
}

// TestReusablePublishDesktopRequiredInputs verifies the caller-facing contract:
// application-name (required) plus the identifiers adder configures.
func TestReusablePublishDesktopRequiredInputs(t *testing.T) {
	inputs, _, _ := loadPublishDesktopWorkflow(t)
	for _, want := range []string{
		"application-name",
		"go-version",
		"docker-image",
		"description",
	} {
		if _, ok := inputs[want]; !ok {
			t.Errorf("reusable publish-desktop workflow missing input %q", want)
		}
	}
}

func TestDesktopBinaryTargetMatrixDefault(t *testing.T) {
	wf := loadDesktopWorkflow(t)
	job, ok := wf.Jobs["build-binaries"]
	if !ok {
		t.Fatal("build-binaries job missing")
	}
	if got, want := job.Strategy.Matrix.Include.Value, "${{ fromJSON(inputs.binary-target-matrix) }}"; got != want {
		t.Fatalf("build-binaries matrix include = %q, want %q", got, want)
	}

	input, ok := wf.On.WorkflowCall.Inputs["binary-target-matrix"]
	if !ok {
		t.Fatal("missing binary-target-matrix input")
	}
	var spec struct {
		Required bool   `yaml:"required"`
		Type     string `yaml:"type"`
		Default  string `yaml:"default"`
	}
	if err := input.Decode(&spec); err != nil {
		t.Fatalf("decode binary-target-matrix input: %v", err)
	}
	if spec.Required || spec.Type != "string" {
		t.Errorf("binary-target-matrix must be an optional string input, got required=%v type=%q", spec.Required, spec.Type)
	}
	var rows []desktopTargetRow
	dec := json.NewDecoder(strings.NewReader(spec.Default))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rows); err != nil {
		t.Fatalf("binary-target-matrix default is not a JSON row array: %v", err)
	}
	want := []desktopTargetRow{
		{"macos-15", "darwin", "arm64", true},
		{"ubuntu-26.04", "freebsd", "amd64", false},
		{"ubuntu-26.04", "freebsd", "arm64", false},
		{"ubuntu-26.04", "linux", "amd64", true},
		{"ubuntu-26.04-arm", "linux", "arm64", true},
		{"windows-latest", "windows", "amd64", true},
		{"windows-11-arm", "windows", "arm64", true},
		{"macos-15-intel", "darwin", "amd64", true},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("binary-target-matrix default rows =\n%+v\nwant\n%+v", rows, want)
	}
}

// TestDesktopBuildBinariesMatrixKeys asserts build-binaries reads no matrix key
// beyond the four every binary-target-matrix row carries. A step that grew a
// dependency on a new key would otherwise see an empty value on every row a
// caller supplies without it.
func TestDesktopBuildBinariesMatrixKeys(t *testing.T) {
	data, err := os.ReadFile(reusablePublishDesktopPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "\n  build-binaries:\n")
	end := strings.Index(text, "\n  build-images:\n")
	if start < 0 || end < start {
		t.Fatal("cannot locate the build-binaries job")
	}
	allowed := map[string]bool{"runner": true, "os": true, "arch": true, "tray": true}
	for _, m := range regexp.MustCompile(`matrix\.([A-Za-z0-9_-]+)`).FindAllStringSubmatch(text[start:end], -1) {
		if !allowed[m[1]] {
			t.Errorf("build-binaries reads matrix.%s, which binary-target-matrix rows do not define", m[1])
		}
	}
}

type desktopTargetRow struct {
	Runner string `json:"runner"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Tray   bool   `json:"tray"`
}

// TestDesktopBinaryTargetMatrixDefault pins the default build-binaries rows to
// the eight targets the matrix built before it became an input, in the same
// order, so a caller that does not pass binary-target-matrix builds exactly
// what it built before. The order matters: the two darwin rows sit at opposite
// ends so the default max-parallel of 2 never holds two macOS slots at once.
