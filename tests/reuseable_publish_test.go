package workflows

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestReusablePublishReleaseAssetAuthentication(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatalf("read reusable publish workflow: %v", err)
	}

	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("parse reusable publish workflow: %v", err)
	}

	for _, step := range workflow.Jobs["build-binaries"].Steps {
		if step.Name != "Upload release asset" {
			continue
		}
		if got, want := step.Env["GH_TOKEN"], "${{ github.token }}"; got != want {
			t.Fatalf("Upload release asset GH_TOKEN = %q, want %q", got, want)
		}
		curlCommand := continuedShellCommand(step.Run, "curl")
		if curlCommand == "" {
			t.Fatal("Upload release asset must invoke curl")
		}
		if !strings.Contains(curlCommand, `-H "Authorization: Bearer $GH_TOKEN"`) {
			t.Fatal("Upload release asset must authenticate with GH_TOKEN")
		}
		return
	}

	t.Fatal("build-binaries job has no Upload release asset step")
}

func TestReusablePublishPassesPrereleaseToScriptThroughEnvironment(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatalf("read reusable publish workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID   string            `yaml:"id"`
				Env  map[string]string `yaml:"env"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("parse reusable publish workflow: %v", err)
	}
	for _, step := range workflow.Jobs["create-draft-release"].Steps {
		if step.ID != "create-release" {
			continue
		}
		if step.Env["PRERELEASE"] != "${{ inputs.prerelease }}" {
			t.Errorf("PRERELEASE env = %q, want workflow input", step.Env["PRERELEASE"])
		}
		script := step.With["script"]
		if !strings.Contains(script, "process.env.PRERELEASE === 'true'") ||
			strings.Contains(script, "${{ inputs.prerelease }}") {
			t.Errorf("release script does not read prerelease from its environment:\n%s", script)
		}
		return
	}
	t.Fatal("create-draft-release job has no create-release script")
}

func TestReusablePublishFinalizationPassesReleaseIDThroughEnvironment(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatalf("read reusable publish workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Env  map[string]string `yaml:"env"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("parse reusable publish workflow: %v", err)
	}
	for _, step := range workflow.Jobs["finalize-release"].Steps {
		if step.With["github-token"] == "" {
			continue
		}
		if got, want := step.Env["RELEASE_ID"], "${{ needs.create-draft-release.outputs.RELEASE_ID }}"; got != want {
			t.Errorf("finalize-release RELEASE_ID env = %q, want %q", got, want)
		}
		script := step.With["script"]
		for _, required := range []string{
			"process.env.RELEASE_ID",
			"Number.isSafeInteger(releaseIDNumber)",
			"release_id: releaseIDNumber",
		} {
			if !strings.Contains(script, required) {
				t.Errorf("finalize-release script is missing %q:\n%s", required, script)
			}
		}
		if strings.Contains(script, "${{") {
			t.Errorf("finalize-release script contains a workflow expression:\n%s", script)
		}
		return
	}
	t.Fatal("finalize-release job has no release update script")
}

func TestReusablePublishCustomBuildUsesMatrixTarget(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatalf("read reusable publish workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("parse reusable publish workflow: %v", err)
	}
	for _, step := range workflow.Jobs["build-binaries"].Steps {
		if step.Name != "Build custom binary" {
			continue
		}
		for name, want := range map[string]string{
			"GOOS":   "${{ matrix.os }}",
			"GOARCH": "${{ matrix.arch }}",
		} {
			if got := step.Env[name]; got != want {
				t.Errorf("custom build %s = %q, want %q", name, got, want)
			}
		}
		return
	}
	t.Fatal("build-binaries job has no custom build step")
}

func TestReusablePublishAttestsEveryUploadedBinary(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatalf("read reusable publish workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Run  string            `yaml:"run"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("parse reusable publish workflow: %v", err)
	}

	var uploadScript, subjectPath string
	for _, step := range workflow.Jobs["build-binaries"].Steps {
		switch step.Name {
		case "Prepare release assets":
			uploadScript = step.Run
		case "Attest binary":
			subjectPath = step.With["subject-path"]
		}
	}
	if uploadScript == "" {
		t.Fatal("build-binaries job has no Prepare release assets step")
	}
	if !strings.Contains(subjectPath, "env.RELEASE_ASSET_FILENAMES") {
		t.Fatalf("attestation subject-path does not use every uploaded filename: %q", subjectPath)
	}

	tmp := t.TempDir()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required to execute the workflow script")
	}
	for _, name := range []string{"primary", "extra-one", "extra-two"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	githubEnv := filepath.Join(tmp, "github-env")
	cmd := exec.Command("bash", "-e", "-c", uploadScript)
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(),
		"GITHUB_ENV="+githubEnv,
		"GITHUB_RUN_ID=42",
		"GITHUB_RUN_ATTEMPT=1",
		"BINARY_NAME=primary",
		"ADDITIONAL_BINARY_NAMES=[\"extra-one\",\"extra-two\"]",
		"MATRIX_OS=linux",
		"MATRIX_ARCH=amd64",
		"BINARY_COMPRESS=false",
		"RELEASE_TAG=component/v1.2.3",
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run release asset preparation step: %v\n%s", err, output)
	}
	got, err := os.ReadFile(githubEnv)
	if err != nil {
		t.Fatalf("read GitHub environment file: %v", err)
	}
	want := "RELEASE_ASSET_FILENAMES<<RELEASE_ASSET_FILENAMES_EOF_42_1\n" +
		"primary-component_v1.2.3-linux-amd64\nextra-one-component_v1.2.3-linux-amd64\nextra-two-component_v1.2.3-linux-amd64\n" +
		"RELEASE_ASSET_FILENAMES_EOF_42_1\n"
	if string(got) != want {
		t.Errorf("release asset filenames = %q, want %q", got, want)
	}

	for _, input := range []string{`["extra-one",`, `{"unexpected":"extra-one"}`, `[false]`, `[""]`, `["extra-one\n"]`, `["primary"]`} {
		t.Run(input, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), "github-env")
			invalid := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", uploadScript)
			invalid.Dir = tmp
			invalid.Env = append(cmd.Environ(), "GITHUB_ENV="+outputPath, "ADDITIONAL_BINARY_NAMES="+input)
			if out, err := invalid.CombinedOutput(); err == nil {
				t.Fatalf("invalid additional binary names accepted: %s", out)
			}
			if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
				t.Fatalf("invalid additional binary names exported filenames: %v", err)
			}
		})
	}

	t.Run("compression is stable across build timestamps", func(t *testing.T) {
		version, err := exec.CommandContext(t.Context(), "tar", "--version").Output()
		if err != nil || !bytes.Contains(version, []byte("GNU tar")) {
			t.Skip("GNU tar required to reproduce Ubuntu runner compression")
		}
		fixture := filepath.Join(tmp, "primary")
		archive := filepath.Join(tmp, "primary-component_v1.2.3-linux-amd64.tar.gz")
		packageBinary := func(timestamp time.Time) []byte {
			t.Helper()
			if err := os.Chtimes(fixture, timestamp, timestamp); err != nil {
				t.Fatal(err)
			}
			compress := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", uploadScript)
			compress.Dir = tmp
			compress.Env = append(cmd.Environ(), "BINARY_COMPRESS=true", "ADDITIONAL_BINARY_NAMES=[]")
			if out, err := compress.CombinedOutput(); err != nil {
				t.Fatalf("compress asset: %v\n%s", err, out)
			}
			contents, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			return contents
		}
		if first, second := packageBinary(time.Unix(1, 0)), packageBinary(time.Unix(86401, 0)); !bytes.Equal(first, second) {
			t.Fatal("archive changed despite identical binary contents and permissions")
		}
	})
}

func TestReusablePublishFinalizationGatesOptionalArtifacts(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatalf("read reusable publish workflow: %v", err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatalf("parse reusable publish workflow: %v", err)
	}
	condition := workflow.Jobs["finalize-release"].If
	for _, required := range []string{
		"!inputs.publish-container-image",
		"needs.image-manifest.result == 'success'",
		"needs.checksum-assets.result == 'success'",
		"needs.checksum-assets.result == 'skipped'",
	} {
		if !strings.Contains(condition, required) {
			t.Errorf("finalize-release condition %q is missing %q", condition, required)
		}
	}

	checksumCondition := workflow.Jobs["checksum-assets"].If
	for _, required := range []string{
		"inputs.create-checksums",
		"startsWith(github.ref, 'refs/tags/')",
	} {
		if !strings.Contains(checksumCondition, required) {
			t.Errorf("checksum-assets condition %q is missing %q", checksumCondition, required)
		}
	}
}

func continuedShellCommand(script, command string) string {
	var commandLines []string
	capturing := false
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if !capturing {
			if !strings.HasPrefix(line, command+" ") {
				continue
			}
			capturing = true
		}
		commandLines = append(commandLines, strings.TrimSuffix(line, `\`))
		if !strings.HasSuffix(line, `\`) {
			break
		}
	}
	return strings.Join(commandLines, " ")
}
