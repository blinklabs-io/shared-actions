package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestImageMetadataPreservesPrereleaseSuffix(t *testing.T) {
	for _, file := range []string{"reuseable-publish.yml", "reuseable-publish-desktop.yml", "reuseable-publish-docker-multiarch.yml"} {
		workflow := readReusableWorkflow(t, "../.github/workflows/"+file)
		for jobName, job := range workflow.Jobs {
			for index, step := range job.Steps {
				var rules []*regexp.Regexp
				for _, line := range strings.Split(step.With["tags"], "\n") {
					fields := strings.Split(strings.TrimSpace(line), ",")
					if fields[0] != "type=match" {
						continue
					}
					for _, field := range fields {
						if strings.HasPrefix(field, "pattern=") {
							rules = append(rules, regexp.MustCompile(strings.TrimPrefix(field, "pattern=")))
						}
					}
				}
				if len(rules) == 0 {
					continue
				}
				tests := []struct {
					tag, stable string
					wantAlias   bool
				}{
					{tag: "v1.2.3-rc1", stable: "1.2.3"},
					{tag: "v1.2.3-beta.2", stable: "1.2.3"},
					{tag: "v1.2.3-5-rc1", stable: "1.2.3"},
					{tag: "v1.2.3-5", stable: "1.2.3", wantAlias: true},
				}
				if file == "reuseable-publish-docker-multiarch.yml" {
					tests = append(tests, []struct {
						tag, stable string
						wantAlias   bool
					}{
						{tag: "v13.5.0.1", stable: "13.5.0.1", wantAlias: true},
						{tag: "v13.5.0.1-1", stable: "13.5.0.1", wantAlias: true},
						{tag: "v13.5.0.1-rc1", stable: "13.5.0.1"},
						{tag: "v13.5.0.1-1-rc1", stable: "13.5.0.1"},
						{tag: "v20261123-1", stable: "20261123", wantAlias: true},
						{tag: "v20261123-12", stable: "20261123", wantAlias: true},
						{tag: "v20261123-123", stable: "20261123", wantAlias: true},
						{tag: "v20261123-rc1", stable: "20261123"},
						{tag: "v20261123-1-rc1", stable: "20261123"},
						{tag: "v2026112-1", stable: "2026112"},
						{tag: "v202611234-1", stable: "202611234"},
					}...)
				}
				for _, test := range tests {
					tag := test.tag
					alias := false
					for _, re := range rules {
						match := re.FindStringSubmatch(tag)
						if len(match) >= 2 && match[1] == test.stable {
							alias = true
						}
					}
					if want := test.wantAlias; alias != want {
						t.Errorf("%s/%s step %d stable alias for %s=%v want=%v", file, jobName, index, tag, alias, want)
					}
				}
			}
		}
	}
}

func TestImageLatestAliasesRespectReleaseChannel(t *testing.T) {
	for _, file := range []string{"reuseable-publish.yml", "reuseable-publish-docker-multiarch.yml"} {
		workflow := readReusableWorkflow(t, "../.github/workflows/"+file)
		for _, job := range workflow.Jobs {
			for _, step := range job.Steps {
				if !strings.Contains(step.Run, ":latest") {
					continue
				}
				for _, test := range []struct {
					tag        string
					prerelease bool
					wantLatest bool
					dockerOnly bool
				}{
					{tag: "v1.2.3", wantLatest: true},
					{tag: "v1.2.3-5", wantLatest: true},
					{tag: "v1.2.3-rc1"},
					{tag: "v1.2.3-beta.2"},
					{tag: "v1.2.3-5-rc1"},
					{tag: "v1.2.3", prerelease: true},
					{tag: "v1.2.3-5", prerelease: true},
					{tag: "nightly"},
					{tag: "v13.5.0.1", wantLatest: true, dockerOnly: true},
					{tag: "v13.5.0.1", prerelease: true, dockerOnly: true},
					{tag: "v13.5.0.1-1", wantLatest: true, dockerOnly: true},
					{tag: "v13.5.0.1-1", prerelease: true, dockerOnly: true},
					{tag: "v13.5.0.1-rc1", dockerOnly: true},
					{tag: "v13.5.0.1-1-rc1", dockerOnly: true},
					{tag: "v20261123-1", wantLatest: true, dockerOnly: true},
					{tag: "v20261123-12", wantLatest: true, dockerOnly: true},
					{tag: "v20261123-123", wantLatest: true, dockerOnly: true},
					{tag: "v20261123-1", prerelease: true, dockerOnly: true},
					{tag: "v20261123-rc1", dockerOnly: true},
					{tag: "v20261123-1-rc1", dockerOnly: true},
					{tag: "v2026112-1", dockerOnly: true},
					{tag: "v202611234-1", dockerOnly: true},
				} {
					if test.dockerOnly && file != "reuseable-publish-docker-multiarch.yml" {
						continue
					}
					t.Run(file+"/"+step.Name+"/"+test.tag+"/"+map[bool]string{true: "prerelease", false: "default"}[test.prerelease], func(t *testing.T) {
						dir := t.TempDir()
						callsFile := filepath.Join(dir, "calls")
						if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\necho \"$*\" >> \"$DOCKER_CALLS_FILE\"\n"), 0o755); err != nil {
							t.Fatal(err)
						}
						flag := "false"
						if test.prerelease {
							flag = "true"
						}
						ghcrTags := "ghcr.io/example/repo:" + strings.TrimPrefix(test.tag, "v")
						dockerTags := "example/repo:" + strings.TrimPrefix(test.tag, "v")
						// These date and four-component formats are not SemVer; only match rules can emit their tags.
						if test.dockerOnly {
							metadataTags := func(id, image string) string {
								for _, metadata := range job.Steps {
									if metadata.ID != id {
										continue
									}
									var tags []string
									for _, line := range strings.Split(metadata.With["tags"], "\n") {
										fields := strings.Split(strings.TrimSpace(line), ",")
										if fields[0] != "type=match" || test.prerelease {
											continue
										}
										for _, field := range fields {
											if strings.HasPrefix(field, "pattern=") {
												match := regexp.MustCompile(strings.TrimPrefix(field, "pattern=")).FindStringSubmatch(test.tag)
												if len(match) >= 2 {
													tags = append(tags, image+":"+match[1])
												}
											}
										}
									}
									return strings.Join(tags, "\n")
								}
								t.Fatalf("missing tag metadata step %s", id)
								return ""
							}
							ghcrTags = metadataTags("meta-ghcr-tag", "ghcr.io/example/repo")
							dockerTags = metadataTags("meta-dockerhub-tag", "example/repo")
						}
						cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
						cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
							"DOCKER_CALLS_FILE="+callsFile, "AMD64_DIGEST=sha256:"+strings.Repeat("a", 64), "ARM64_DIGEST=sha256:"+strings.Repeat("b", 64), "RELEASE_TAG="+test.tag, "PRERELEASE="+flag, "IS_PRERELEASE="+flag,
							"GHCR_IMAGE=ghcr.io/example/repo", "DOCKER_IMAGE=example/repo",
							"GHCR_TAG_TAGS="+ghcrTags,
							"DH_TAG_TAGS="+dockerTags)
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("manifest step failed: %v\n%s", err, out)
						}
						calls, err := os.ReadFile(callsFile)
						if err != nil && !os.IsNotExist(err) {
							t.Fatal(err)
						}
						if got := strings.Contains(string(calls), ":latest"); got != test.wantLatest {
							t.Fatalf("latest updated=%v want=%v; calls=%s", got, test.wantLatest, calls)
						}
					})
				}
			}
		}
	}
}

func TestVersionUpdatesNeverDowngrade(t *testing.T) {
	workflow := readReusableWorkflow(t, "../.github/workflows/reuseable-check-versions.yml")
	var script string
	for _, step := range workflow.Jobs["check-versions"].Steps {
		if step.Name == "Check and create PRs for new versions" {
			start := strings.Index(step.Run, `echo "  Local version:`)
			end := strings.Index(step.Run, `echo "  New version available:`)
			if start < 0 || end < start {
				t.Fatal("version decision block missing")
			}
			script = "for decision in once; do\n" + step.Run[start:end] + "\necho update > \"$VERSION_DECISION_FILE\"\ndone"
		}
	}
	if script == "" {
		t.Fatal("version updater missing")
	}
	for _, test := range []struct {
		local, upstream string
		update          bool
	}{{"2.0.0", "1.9.9", false}, {"1.2.3", "1.2.3", false}, {"1.2.3", "1.2.4", true}, {"1.9.0", "1.10.0", true}} {
		t.Run(test.local+"/"+test.upstream, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "decision")
			cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Env = append(os.Environ(), "LOCAL_VERSION="+test.local, "UPSTREAM_VERSION="+test.upstream, "VERSION_DECISION_FILE="+file)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("version decision failed: %v\n%s", err, out)
			}
			_, err := os.Stat(file)
			if (err == nil) != test.update {
				t.Fatalf("update=%v want=%v, stat error=%v", err == nil, test.update, err)
			}
		})
	}
}
