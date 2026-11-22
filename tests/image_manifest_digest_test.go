package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageManifestsUseCurrentBuildDigests(t *testing.T) {
	for _, file := range []string{"reuseable-publish.yml", "reuseable-publish-docker-multiarch.yml"} {
		wf := readReusableWorkflow(t, "../.github/workflows/"+file)
		builds, manifest := []string{"build-amd64", "build-arm64"}, "multi-arch-manifest"
		if file == "reuseable-publish.yml" {
			builds, manifest = []string{"build-image-amd64", "build-image-arm64"}, "image-manifest"
		}
		bindings := []string{}
		for index, build := range builds {
			job := wf.Jobs[build]
			if job.Outputs["digest"] != "${{ steps.push.outputs.digest }}" {
				t.Errorf("%s/%s must export the digest returned by the image build", file, build)
			}
			pushFound := false
			for _, step := range job.Steps {
				pushFound = pushFound || (step.ID == "push" && strings.HasPrefix(step.Uses, "docker/build-push-action@"))
			}
			if !pushFound {
				t.Errorf("%s/%s output references a missing image build", file, build)
			}
			bindings = append(bindings, "${{ needs."+build+".outputs.digest }}", "sha256:"+strings.Repeat([]string{"a", "b"}[index], 64))
			if file == "reuseable-publish.yml" {
				if job.Outputs["extra-digest"] != "${{ steps.push-extra.outputs.digest }}" {
					t.Errorf("%s extra image digest output missing", build)
				}
				bindings = append(bindings, "${{ needs."+build+".outputs.extra-digest }}", "sha256:"+strings.Repeat([]string{"c", "d"}[index], 64))
			}
		}
		for _, step := range wf.Jobs[manifest].Steps {
			if !strings.Contains(step.Run, "docker buildx imagetools create") {
				continue
			}
			for _, invalid := range []string{"", "missing amd64", "invalid arm64"} {
				t.Run(file+"/"+step.Name+"/"+invalid, func(t *testing.T) {
					dir := t.TempDir()
					callsFile := filepath.Join(dir, "calls")
					if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\necho \"$*\" >> \"$DOCKER_CALLS_FILE\"\n"), 0o755); err != nil {
						t.Fatal(err)
					}
					env := append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
						"DOCKER_CALLS_FILE="+callsFile, "RELEASE_TAG=v1.2.3", "PRERELEASE=false", "IS_PRERELEASE=false",
						"EXTRA_SUFFIX=variant", "GHCR_IMAGE=ghcr.io/example/app", "DOCKER_IMAGE=example/app",
						"GHCR_TAGS=ghcr.io/example/app:1.2.3", "GHCR_TAG_TAGS=ghcr.io/example/app:1.2.3",
						"DH_TAGS=example/app:1.2.3", "DH_TAG_TAGS=example/app:1.2.3")
					for name, expression := range wf.Jobs[manifest].Env {
						value := strings.NewReplacer(bindings...).Replace(expression)
						if strings.Contains(value, "${{") {
							t.Fatalf("unbound build digest %s=%s", name, value)
						}
						if invalid == "missing amd64" && strings.Contains(name, "AMD64") {
							value = ""
						}
						if invalid == "invalid arm64" && strings.Contains(name, "ARM64") {
							value = "1.2.3-arm64v8"
						}
						env = append(env, name+"="+value)
					}
					cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", step.Run)
					cmd.Env = env
					out, err := cmd.CombinedOutput()
					calls, readErr := os.ReadFile(callsFile)
					if invalid != "" {
						if err == nil || !os.IsNotExist(readErr) {
							t.Fatalf("invalid digest reached publication: %v calls=%s\n%s", err, calls, out)
						}
						return
					}
					if err != nil || readErr != nil {
						t.Fatalf("manifest failed: %v %v\n%s", err, readErr, out)
					}
					image := "ghcr.io/example/app"
					if strings.Contains(step.Name, "dockerhub") {
						image = "example/app"
					}
					letters := []string{"a", "b"}
					if strings.HasSuffix(step.Name, "-extra") {
						letters = []string{"c", "d"}
					}
					fields := strings.Fields(string(calls))
					if len(fields) < 2 || fields[len(fields)-2] != image+"@sha256:"+strings.Repeat(letters[0], 64) || fields[len(fields)-1] != image+"@sha256:"+strings.Repeat(letters[1], 64) {
						t.Fatalf("manifest used sources outside the current build: %s", calls)
					}
				})
			}
		}
	}
}
