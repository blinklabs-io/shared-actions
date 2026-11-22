package workflows

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMultiarchScanCoversBothPublishedArchitectures(t *testing.T) {
	wf := readReusableWorkflow(t, "../.github/workflows/reuseable-publish-docker-multiarch.yml")
	job := wf.Jobs["scan-images"]
	data, err := os.ReadFile("../.github/workflows/reuseable-publish-docker-multiarch.yml")
	if err != nil {
		t.Fatal(err)
	}
	var matrixWorkflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []struct{ Arch, Tag string }
				}
			}
		}
	}
	if err := yaml.Unmarshal(data, &matrixWorkflow); err != nil {
		t.Fatal(err)
	}
	rows := matrixWorkflow.Jobs["scan-images"].Strategy.Matrix.Include
	if len(rows) != 2 {
		t.Fatalf("scan matrix=%v, want two published architectures", rows)
	}
	var scanRef, platform, report, uploaded, category, tags string
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "docker/metadata-action@") {
			tags = step.With["tags"]
		}
		if strings.HasPrefix(step.Uses, "aquasecurity/trivy-action@") {
			scanRef, platform, report = step.With["scan-ref"], step.Env["TRIVY_PLATFORM"], step.With["output"]
		}
		if strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@") {
			uploaded, category = step.With["sarif_file"], step.With["category"]
		}
	}
	customTag := false
	for _, line := range strings.Split(tags, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if fields[0] != "type=match" {
			continue
		}
		for _, field := range fields {
			if strings.HasPrefix(field, "pattern=") {
				match := regexp.MustCompile(strings.TrimPrefix(field, "pattern=")).FindStringSubmatch("vnightly")
				customTag = customTag || (len(match) > 1 && match[1] == "nightly")
			}
		}
	}
	if !customTag {
		t.Fatal("scan metadata cannot select the published vnightly image")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		expand := strings.NewReplacer("${{ matrix.arch }}", row.Arch, "${{ matrix.tag }}", row.Tag).Replace
		wantTag := map[string]string{"amd64": "amd64", "arm64": "arm64v8"}[row.Arch]
		if wantTag == "" || row.Tag != wantTag || seen[row.Arch] {
			t.Fatalf("unexpected scan row=%+v", row)
		}
		seen[row.Arch] = true
		if expand(platform) != "linux/"+row.Arch || !strings.HasSuffix(expand(scanRef), ":${{ steps.meta.outputs.version }}-"+wantTag) {
			t.Fatalf("%s scan resolves wrong image/platform: %s %s", row.Arch, expand(scanRef), expand(platform))
		}
		if expand(report) != "trivy-results-"+row.Arch+".sarif" || expand(uploaded) != expand(report) || expand(category) != "trivy-"+row.Arch {
			t.Fatalf("%s report upload is missing or overwrites another architecture", row.Arch)
		}
	}
	dependencies := strings.Join(wf.Jobs["github-release"].Needs, ",")
	if dependencies != "multi-arch-manifest,scan-images" {
		t.Fatalf("release is not gated on all scans: %s", dependencies)
	}
}
