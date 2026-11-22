package workflows

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGoLibraryProxyPullHandlesSkippedExamples(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-go-library-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If    string   `yaml:"if"`
			Needs []string `yaml:"needs"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["pull-go-module"]
	// A status function overrides GitHub's implicit success() check, which
	// otherwise propagates the skipped optional example job to proxy indexing.
	if !strings.Contains(job.If, "!cancelled()") {
		t.Fatal("proxy pull condition must handle skipped example builds without running on cancellation")
	}
	if !strings.Contains(job.If, "needs.publish-release.result == 'success'") {
		t.Fatal("proxy pull must require successful publication")
	}
	if len(job.Needs) != 1 || job.Needs[0] != "publish-release" {
		t.Fatalf("proxy pull dependencies = %v, want publish-release", job.Needs)
	}
}
