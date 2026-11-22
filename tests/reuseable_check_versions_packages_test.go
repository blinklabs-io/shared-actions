package workflows

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageUpdaterDownloadRejectsErrors(t *testing.T) {
	workflow := readReusableWorkflow(t, "../.github/workflows/reuseable-check-versions-packages.yml")
	var script string
	for _, step := range workflow.Jobs["check-versions"].Steps {
		if step.Name == "Download cardano-up" {
			start := strings.LastIndex(step.Run, "\ncurl ")
			if start < 0 {
				t.Fatal("asset download missing")
			}
			script = step.Run[start:]
		}
	}
	if script == "" {
		t.Fatal("download step missing")
	}
	for _, test := range []struct {
		name string
		code int
		body string
		fail bool
	}{
		{"executable", http.StatusOK, "#!/bin/sh\n[ \"$1\" = --help ]\n", false},
		{"HTTP error", http.StatusInternalServerError, "unavailable", true},
		{"invalid executable", http.StatusOK, "invalid-binary-content", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "DOWNLOAD_URL="+server.URL)
			if out, err := cmd.CombinedOutput(); (err != nil) != test.fail {
				t.Fatalf("download error=%v want failure=%v\n%s", err, test.fail, out)
			}
		})
	}
}

func TestPackageUpdatePreservesRemoteBranch(t *testing.T) {
	workflow := readReusableWorkflow(t, "../.github/workflows/reuseable-check-versions-packages.yml")
	var script string
	for _, step := range workflow.Jobs["check-versions"].Steps {
		if step.Name == "Check and create PRs for new versions" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("package updater step missing")
	}
	for _, scenario := range []string{"new branch", "existing branch", "blocked then eligible", "unavailable remote"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			remote := filepath.Join(t.TempDir(), "origin.git")
			env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir, cmd.Env = root, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			write := func(name, body string, mode os.FileMode) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			git("init", "--bare", remote)
			git("init", "-b", "main")
			git("config", "user.name", "Updater")
			git("config", "user.email", "updater@example.com")
			git("config", "commit.gpgsign", "false")
			git("remote", "add", "origin", remote)
			upstream := `{"service":{"repo":"example/service","tag_pattern":"v"}}`
			if scenario == "blocked then eligible" {
				upstream = `{"service":{"repo":"example/service","tag_pattern":"v"},"worker":{"repo":"example/worker","tag_pattern":"v"}}`
				write("packages/worker/worker-1.2.3.yaml", "version: 1.2.3\n", 0o644)
			}
			write("upstream.json", upstream, 0o644)
			write("packages/service/service-1.2.3.yaml", "version: 1.2.3\n", 0o644)
			git("add", "upstream.json", "packages")
			git("commit", "-s", "-m", "test: initial package")
			git("push", "origin", "main")
			branch := "chore/service-1.2.4"
			var before string
			if scenario == "existing branch" || scenario == "blocked then eligible" {
				git("checkout", "-b", branch)
				write("packages/service/service-1.2.4.yaml", "version: 1.2.4\nmanual: keep\n", 0o644)
				git("add", "packages")
				git("commit", "-s", "-m", "test: preserve manual update")
				git("push", "origin", branch)
				before = git("rev-parse", "HEAD")
				git("checkout", "main")
				git("branch", "-D", branch)
			}
			if scenario == "unavailable remote" {
				git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
			}
			write("bin/gh", `#!/bin/sh
case "$1 $2" in
  "auth setup-git") exit 0;;
  "api repos/example/service/releases/latest"|"api repos/example/worker/releases/latest") echo v1.2.4;;
  "pr list") exit 0;;
  "pr create") printf '%s\n' "$@" > "$FAKE_PR_CREATED_FILE";;
  *) exit 1;;
esac
`, 0o755)
			write("add-version.sh", "#!/bin/sh\necho \"$1\" >> generated\nprintf 'version: %s\\n' \"$2\" > \"packages/$1/$1-$2.yaml\"\n", 0o755)
			write("cardano-up", "#!/bin/sh\nexit 0\n", 0o755)
			cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Dir = root
			cmd.Env = append(env, "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"UPSTREAM_CONFIG=upstream.json", "PACKAGES_DIR=packages", "ADD_VERSION_SCRIPT=./add-version.sh",
				"BASE_BRANCH=main", "GIT_USER_NAME=Updater", "GIT_USER_EMAIL=updater@example.com", "VALIDATE_ARGS=-D",
				"FAKE_PR_CREATED_FILE="+filepath.Join(root, "pr-created"))
			out, err := cmd.CombinedOutput()
			if scenario == "new branch" {
				if err != nil {
					t.Fatalf("new update failed: %v\n%s", err, out)
				}
				if _, err := os.Stat(filepath.Join(root, "pr-created")); err != nil {
					t.Fatalf("new branch did not create PR: %v\n%s", err, out)
				}
				if body := git("show", "-s", "--format=%B", "origin/"+branch); !strings.Contains(body, "Signed-off-by: Updater <updater@example.com>") {
					t.Fatal("update commit lacks signoff")
				}
				return
			}
			if before != "" && git("ls-remote", "--heads", "origin", "refs/heads/"+branch) != before+"\trefs/heads/"+branch {
				t.Fatal("existing remote branch was replaced")
			}
			if err == nil {
				t.Fatalf("unsafe remote state succeeded:\n%s", out)
			}
			if scenario == "blocked then eligible" {
				if !strings.Contains(string(out), "Remote branch "+branch+" exists without an open PR") {
					t.Fatalf("blocked package did not report its existing branch:\n%s", out)
				}
				generated, err := os.ReadFile(filepath.Join(root, "generated"))
				if err != nil || string(generated) != "worker\n" {
					t.Fatalf("only the later package should be generated: %v, %q", err, generated)
				}
				pr, err := os.ReadFile(filepath.Join(root, "pr-created"))
				if err != nil || !strings.Contains(string(pr), "--head\nchore/worker-1.2.4\n") {
					t.Fatalf("later package PR missing: %v, %q", err, pr)
				}
				if body := git("show", "-s", "--format=%B", "origin/chore/worker-1.2.4"); !strings.Contains(body, "Signed-off-by: Updater <updater@example.com>") {
					t.Fatal("later package update commit lacks signoff")
				}
				if contents := git("show", "origin/chore/worker-1.2.4:packages/worker/worker-1.2.4.yaml"); contents != "version: 1.2.4" {
					t.Fatalf("later package version = %q", contents)
				}
				return
			}
			for _, path := range []string{"generated", "pr-created"} {
				if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Fatalf("unsafe remote state reached %s: %v\n%s", path, err, out)
				}
			}
		})
	}
}
