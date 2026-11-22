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
				for _, tag := range []string{"v1.2.3-rc1", "v1.2.3-beta.2", "v1.2.3-5-rc1", "v1.2.3-5"} {
					alias := false
					for _, re := range rules {
						match := re.FindStringSubmatch(tag)
						if len(match) >= 2 && match[1] == "1.2.3" {
							alias = true
						}
					}
					if want := tag == "v1.2.3-5"; alias != want {
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
				}{
					{tag: "v1.2.3", wantLatest: true},
					{tag: "v1.2.3-5", wantLatest: true},
					{tag: "v1.2.3-rc1"},
					{tag: "v1.2.3-beta.2"},
					{tag: "v1.2.3-5-rc1"},
					{tag: "v1.2.3", prerelease: true},
					{tag: "v1.2.3-5", prerelease: true},
					{tag: "nightly"},
				} {
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
						cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
						cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
							"DOCKER_CALLS_FILE="+callsFile, "AMD64_DIGEST=sha256:"+strings.Repeat("a", 64), "ARM64_DIGEST=sha256:"+strings.Repeat("b", 64), "RELEASE_TAG="+test.tag, "PRERELEASE="+flag, "IS_PRERELEASE="+flag,
							"GHCR_IMAGE=ghcr.io/example/repo", "DOCKER_IMAGE=example/repo",
							"GHCR_TAG_TAGS=ghcr.io/example/repo:"+strings.TrimPrefix(test.tag, "v"),
							"DH_TAG_TAGS=example/repo:"+strings.TrimPrefix(test.tag, "v"))
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
