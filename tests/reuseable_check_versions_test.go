package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageVersionUpdatePreservesRemoteBranch(t *testing.T) {
	wf := readReusableWorkflow(t, "../.github/workflows/reuseable-check-versions.yml")
	var script string
	for _, step := range wf.Jobs["check-versions"].Steps {
		if strings.Contains(step.Run, "gh pr create") {
			script = step.Run
			break
		}
	}
	if script == "" {
		t.Fatal("version update step missing")
	}
	for _, test := range []struct {
		name, failure string
	}{
		{name: "new branch"},
		{name: "open PR"},
		{name: "existing branch", failure: "manual review required"},
		{name: "blocked then eligible", failure: "manual review required"},
		{name: "PR lookup failure", failure: "PR lookup unavailable"},
		{name: "remote lookup failure", failure: "does not appear to be a git repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			remote := filepath.Join(t.TempDir(), "origin.git")
			env := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), "git", args...)
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

			const branch = "chore/service-1.2.4"
			version := "SERVICE_VERSION=${SERVICE_VERSION:-1.2.3}\n"
			git("init", "--bare", remote)
			git("init", "-b", "release")
			git("config", "user.name", "Updater")
			git("config", "user.email", "updater@example.com")
			git("config", "commit.gpgsign", "false")
			git("remote", "add", "origin", remote)
			upstream := `{"service":{"compose_var":"SERVICE_VERSION","ghcr_image":"example/service"}}`
			if test.name == "blocked then eligible" {
				upstream = `{"service":{"compose_var":"SERVICE_VERSION","ghcr_image":"example/service"},"worker":{"compose_var":"WORKER_VERSION","ghcr_image":"example/worker"}}`
				version += "WORKER_VERSION=${WORKER_VERSION:-1.2.3}\n"
			}
			write("upstream.json", upstream, 0o644)
			write("versions.env", version, 0o644)
			git("add", "upstream.json", "versions.env")
			git("commit", "-s", "-m", "test: initial version")
			git("push", "origin", "release")
			base := git("rev-parse", "HEAD")
			var remoteBranch string
			if test.name == "existing branch" || test.name == "blocked then eligible" {
				git("checkout", "-b", branch)
				write("versions.env", version+"# keep this manual change\n", 0o644)
				git("commit", "-sam", "test: manual update")
				git("push", "origin", branch)
				remoteBranch = git("rev-parse", "HEAD")
				git("checkout", "release")
				git("branch", "-D", branch)
			}
			if test.name == "remote lookup failure" {
				git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
			}
			write("bin/gh", `#!/bin/sh
case "$1 $2" in
  "auth setup-git") exit 0;;
  "api /orgs/example/packages/container/service/versions"|"api /orgs/example/packages/container/worker/versions") echo 1.2.4;;
  "pr list")
    case "$SCENARIO" in
      "PR lookup failure") echo "PR lookup unavailable" >&2; exit 1;;
      "open PR") echo 17;;
    esac;;
  "pr create") printf '%s\n' "$@" > pr-created;;
  *) exit 1;;
esac
`, 0o755)
			write("bin/docker", `#!/bin/sh
case "$*" in
  "manifest inspect ghcr.io/example/service:1.2.4"|"manifest inspect ghcr.io/example/worker:1.2.4") exit 0;;
  *) exit 1;;
esac
`, 0o755)
			write("add-version.sh", `#!/bin/sh
case "$1" in
  service) variable=SERVICE_VERSION;;
  worker) variable=WORKER_VERSION;;
  *) exit 1;;
esac
echo "$1" >> generated
awk -v "variable=$variable" -v "version=$2" '
  index($0, variable "=") == 1 {
    printf "%s=${%s:-%s}\n", variable, variable, version
    next
  }
  { print }
' "$COMPOSE_FILE" > "$COMPOSE_FILE.new"
mv "$COMPOSE_FILE.new" "$COMPOSE_FILE"
`, 0o755)
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Dir = root
			cmd.Env = append(env, "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"SCENARIO="+test.name, "UPSTREAM_CONFIG=upstream.json", "COMPOSE_FILE=versions.env",
				"ADD_VERSION_SCRIPT=./add-version.sh", "BASE_BRANCH=release", "GIT_USER_NAME=Updater",
				"GIT_USER_EMAIL=updater@example.com")
			out, err := cmd.CombinedOutput()
			if test.name == "new branch" {
				if err != nil {
					t.Fatalf("new update failed: %v\n%s", err, out)
				}
				pr, err := os.ReadFile(filepath.Join(root, "pr-created"))
				if err != nil || !strings.Contains(string(pr), "\n--base\nrelease\n--head\n"+branch+"\n") {
					t.Fatalf("PR not requested with the expected base and head: %v\n%s", err, pr)
				}
				if parent := git("rev-parse", "origin/"+branch+"^"); parent != base {
					t.Fatalf("update parent=%s want %s", parent, base)
				}
				if contents := git("show", "origin/"+branch+":versions.env"); contents != "SERVICE_VERSION=${SERVICE_VERSION:-1.2.4}" {
					t.Fatalf("updated version=%q", contents)
				}
				if body := git("show", "-s", "--format=%B", "origin/"+branch); !strings.Contains(body, "Signed-off-by: Updater <updater@example.com>") {
					t.Fatal("update commit lacks signoff")
				}
				return
			}
			if test.name == "open PR" && !strings.Contains(string(out), "PR #17 already exists") {
				t.Fatalf("did not recognize the open update PR:\n%s", out)
			}
			if remoteBranch != "" && git("ls-remote", "--heads", remote, "refs/heads/"+branch) != remoteBranch+"\trefs/heads/"+branch {
				t.Fatal("existing remote branch was replaced")
			}
			if test.name == "blocked then eligible" {
				if err == nil || !strings.Contains(string(out), test.failure) {
					t.Fatalf("blocked package must fail the run after checking later packages: %v\n%s", err, out)
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
				if contents := git("show", "origin/chore/worker-1.2.4:versions.env"); contents != "SERVICE_VERSION=${SERVICE_VERSION:-1.2.3}\nWORKER_VERSION=${WORKER_VERSION:-1.2.4}" {
					t.Fatalf("later version update changed the wrong inputs: %q", contents)
				}
				return
			}
			for _, path := range []string{"generated", "pr-created"} {
				if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Errorf("existing or unobservable branch reached %s: %v\n%s", path, err, out)
				}
			}
			if contents, err := os.ReadFile(filepath.Join(root, "versions.env")); err != nil || string(contents) != version {
				t.Errorf("base version changed: %q, error=%v", contents, err)
			}
			if (err != nil) != (test.failure != "") || !strings.Contains(string(out), test.failure) {
				t.Fatalf("update error = %v, expected diagnostic %q\n%s", err, test.failure, out)
			}
		})
	}
}
