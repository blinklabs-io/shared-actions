package workflows

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSharedLintDefaultsUseReviewedRelease(t *testing.T) {
	var versions []string
	for _, tc := range []struct{ file, input string }{
		{"reuseable-golangci-lint.yml", "version"},
		{"reuseable-go-test.yml", "golangci-version"},
	} {
		data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", tc.file))
		if err != nil {
			t.Fatal(err)
		}
		var wf struct {
			On struct {
				WorkflowCall struct {
					Inputs map[string]struct {
						Default string `yaml:"default"`
					} `yaml:"inputs"`
				} `yaml:"workflow_call"`
			} `yaml:"on"`
		}
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		version := wf.On.WorkflowCall.Inputs[tc.input].Default
		if !regexp.MustCompile(`^v[0-9]+[.][0-9]+[.][0-9]+$`).MatchString(version) {
			t.Errorf("%s: default %q is not a complete release version", tc.file, version)
		}
		versions = append(versions, version)
	}
	if versions[0] != versions[1] {
		t.Errorf("shared lint defaults differ: %s and %s", versions[0], versions[1])
	}
}
