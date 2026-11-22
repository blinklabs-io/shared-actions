package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopManifestUsesBuildDigests(t *testing.T) {
	wf := loadDesktopWorkflow(t)
	var record, manifest string
	var upload, download map[string]string
	for _, step := range wf.Jobs["build-images"].Steps {
		switch step.Name {
		case "Record image digest":
			record = step.Run
			if step.Env["IMAGE_DIGEST"] != "${{ steps.push.outputs.digest }}" {
				t.Fatal("digest recorder must use the digest returned by Buildx")
			}
		case "Upload image digest":
			upload = step.With
		}
	}
	for _, step := range wf.Jobs["build-image-manifest"].Steps {
		switch step.Name {
		case "Download image digests":
			download = step.With
		case "manifest-create":
			manifest = step.Run
		}
	}
	if manifest == "" {
		t.Fatal("manifest-create step missing")
	}

	amd64Digest := "sha256:" + strings.Repeat("a", 64)
	arm64Digest := "sha256:" + strings.Repeat("b", 64)
	for _, test := range []struct {
		name, arm64, wantErr string
	}{
		{name: "both registries", arm64: arm64Digest},
		{name: "missing amd64", arm64: arm64Digest, wantErr: "Missing image digest for amd64"},
		{name: "missing arm64", wantErr: "Missing image digest for arm64"},
		{name: "invalid arm64", arm64: "nightly-arm64", wantErr: "Invalid image digest for arm64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tmp := t.TempDir()
			digestDir := filepath.Join(tmp, "desktop-image-digests")
			if err := os.Mkdir(digestDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if test.name != "missing amd64" {
				writeDesktopDigest(t, digestDir, "amd64", amd64Digest)
			}
			if test.arm64 != "" {
				writeDesktopDigest(t, digestDir, "arm64", test.arm64)
			}
			docker := "#!/bin/sh\nprintf '%s\\n' \"$*\"\n"
			if err := os.WriteFile(filepath.Join(tmp, "docker"), []byte(docker), 0o755); err != nil {
				t.Fatal(err)
			}
			targets := "ghcr.io/example/desktop:1.2.3\nexample/desktop:1.2.3"
			cmd := exec.CommandContext(t.Context(), "bash", "-c", manifest)
			cmd.Env = append(os.Environ(), "PATH="+tmp+string(os.PathListSeparator)+os.Getenv("PATH"),
				"RUNNER_TEMP="+tmp, "TAGS="+targets)
			out, err := cmd.CombinedOutput()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(string(out), test.wantErr) ||
					strings.Contains(string(out), "imagetools create") {
					t.Fatalf("invalid handoff must fail before publishing: %v\n%s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("manifest failed: %v\n%s", err, out)
			}
			want := "buildx imagetools create -t ghcr.io/example/desktop:1.2.3 ghcr.io/example/desktop@" +
				amd64Digest + " ghcr.io/example/desktop@" + arm64Digest + "\n" +
				"buildx imagetools create -t example/desktop:1.2.3 example/desktop@" +
				amd64Digest + " example/desktop@" + arm64Digest + "\n"
			if string(out) != want {
				t.Fatalf("manifest sources = %q, want sources from current build digests %q", out, want)
			}
		})
	}

	if record == "" || upload["name"] != "desktop-image-digest-${{ inputs.application-name }}-${{ matrix.arch }}" ||
		upload["path"] != "${{ runner.temp }}/${{ matrix.arch }}.digest" ||
		download["pattern"] != "desktop-image-digest-${{ inputs.application-name }}-*" ||
		download["path"] != "${{ runner.temp }}/desktop-image-digests" ||
		download["merge-multiple"] != "true" || download["github-token"] != "" || download["run-id"] != "" {
		t.Fatal("build and manifest jobs must hand off per-architecture digests from the current run")
	}
	outDir := t.TempDir()
	for _, digest := range []string{amd64Digest, "nightly-amd64"} {
		cmd := exec.CommandContext(t.Context(), "bash", "-c", record)
		cmd.Env = append(os.Environ(), "RUNNER_TEMP="+outDir, "IMAGE_ARCH=amd64", "IMAGE_DIGEST="+digest)
		out, err := cmd.CombinedOutput()
		if digest == amd64Digest {
			if err != nil {
				t.Fatalf("record digest failed: %v\n%s", err, out)
			}
		} else if err == nil || !strings.Contains(string(out), "Missing or invalid image digest for amd64") {
			t.Fatalf("record accepted a mutable tag: %v\n%s", err, out)
		}
	}
	got, err := os.ReadFile(filepath.Join(outDir, "amd64.digest"))
	if err != nil || string(got) != amd64Digest+"\n" {
		t.Fatalf("recorded digest = %q, err = %v", got, err)
	}
}

func writeDesktopDigest(t *testing.T, dir, arch, digest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, arch+".digest"), []byte(digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopScanReadsItsBuildDigest(t *testing.T) {
	wf := loadDesktopWorkflow(t)
	var upload, download map[string]string
	var read string
	for _, step := range wf.Jobs["build-images"].Steps {
		if step.Name == "Upload image digest" {
			upload = step.With
		}
	}
	for _, step := range wf.Jobs["scan-images"].Steps {
		if !strings.Contains(step.If, "inputs.enable-trivy-scan") {
			t.Errorf("%s must do no work while scanning is disabled", step.Name)
		}
		switch step.Name {
		case "Download image digest":
			download = step.With
		case "Read image digest":
			if step.Env["IMAGE_ARCH"] != "${{ matrix.arch }}" {
				t.Fatal("scan digest reader does not receive its architecture")
			}
			read = step.Run
		}
	}
	if read == "" {
		t.Fatal("scan must read its build's immutable digest")
	}
	if download["name"] == "" || download["name"] != upload["name"] ||
		download["pattern"] != "" || download["github-token"] != "" ||
		download["run-id"] != "" || download["path"] != "${{ runner.temp }}/desktop-image-digests" {
		t.Fatal("each scan row must download only its build's artifact from the current run")
	}

	amd64Digest := "sha256:" + strings.Repeat("a", 64)
	arm64Digest := "sha256:" + strings.Repeat("b", 64)
	for _, test := range []struct {
		name, arch, digest, wantErr string
	}{
		{name: "amd64 digest", arch: "amd64", digest: amd64Digest},
		{name: "arm64 digest", arch: "arm64", digest: arm64Digest},
		{name: "missing arm64", arch: "arm64", wantErr: "Missing image digest for arm64"},
		{name: "mutable amd64", arch: "amd64", digest: "nightly-amd64", wantErr: "Invalid image digest for amd64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tmp := t.TempDir()
			dir := filepath.Join(tmp, "desktop-image-digests")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if test.arch == "amd64" {
				writeDesktopDigest(t, dir, "arm64", arm64Digest)
			} else {
				writeDesktopDigest(t, dir, "amd64", amd64Digest)
			}
			if test.digest != "" {
				writeDesktopDigest(t, dir, test.arch, test.digest)
			}
			outputPath := filepath.Join(tmp, "output")
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-c", read)
			cmd.Env = append(os.Environ(), "RUNNER_TEMP="+tmp, "IMAGE_ARCH="+test.arch, "GITHUB_OUTPUT="+outputPath)
			out, err := cmd.CombinedOutput()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(string(out), test.wantErr) {
					t.Fatalf("invalid handoff accepted: %v\n%s", err, out)
				}
				if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
					t.Fatalf("invalid handoff must leave no digest output, stat error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("read image digest failed: %v\n%s", err, out)
			}
			got, err := os.ReadFile(outputPath)
			if err != nil || string(got) != "digest="+test.digest+"\n" {
				t.Fatalf("output = %q, err = %v, want digest for %s", got, err, test.arch)
			}
		})
	}
}
