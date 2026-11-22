package workflows

import (
	"os"
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
					Include []struct{ Arch, Digest string }
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
	var scanRef, platform, report, uploaded, category string
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "aquasecurity/trivy-action@") {
			scanRef, platform, report = step.With["scan-ref"], step.Env["TRIVY_PLATFORM"], step.With["output"]
		}
		if strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@") {
			uploaded, category = step.With["sarif_file"], step.With["category"]
		}
	}
	seen := map[string]bool{}
	for _, row := range rows {
		build := map[string]string{"amd64": "build-amd64", "arm64": "build-arm64"}[row.Arch]
		if build == "" || row.Digest != "${{ needs."+build+".outputs.digest }}" || seen[row.Arch] {
			t.Fatalf("unexpected scan row=%+v", row)
		}
		if wf.Jobs[build].Outputs["digest"] != "${{ steps.push.outputs.digest }}" {
			t.Fatalf("%s does not export its actual build digest", build)
		}
		seen[row.Arch] = true
		digest := "sha256:" + strings.Repeat(map[string]string{"amd64": "a", "arm64": "b"}[row.Arch], 64)
		expand := strings.NewReplacer("${{ matrix.arch }}", row.Arch, "${{ matrix.digest }}", digest).Replace
		if expand(platform) != "linux/"+row.Arch || expand(scanRef) != "ghcr.io/${{ inputs.ghcr-image }}@"+digest {
			t.Fatalf("%s scan resolves wrong image/platform: %s %s", row.Arch, expand(scanRef), expand(platform))
		}
		if expand(report) != "trivy-results-"+row.Arch+".sarif" || expand(uploaded) != expand(report) || expand(category) != "trivy-"+row.Arch {
			t.Fatalf("%s report upload is missing or overwrites another architecture", row.Arch)
		}
	}
	if strings.Join(job.Needs, ",") != "multi-arch-manifest,build-amd64,build-arm64" {
		t.Fatalf("scan cannot access the build digests: %v", job.Needs)
	}
	dependencies := strings.Join(wf.Jobs["github-release"].Needs, ",")
	if dependencies != "multi-arch-manifest,scan-images" {
		t.Fatalf("release is not gated on all scans: %s", dependencies)
	}
}
