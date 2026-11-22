package workflows

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReusablePublishBuildDateStableAcrossRetries(t *testing.T) {
	metadata := publishWorkflowStep(t, "build-binaries", "build-metadata")
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Build Fixture", "GIT_AUTHOR_EMAIL=build@example.com",
		"GIT_COMMITTER_NAME=Build Fixture", "GIT_COMMITTER_EMAIL=build@example.com",
		"GIT_AUTHOR_DATE=2023-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2024-02-03T01:02:03+05:30",
	)
	for _, args := range [][]string{
		{"init", "--template="},
		{"commit", "--allow-empty", "--no-gpg-sign", "-s", "-m", "test: build date fixture"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir, cmd.Env = dir, env
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	bin := t.TempDir()
	date := "#!/bin/sh\nprintf '%s\\n' \"$FIXTURE_WALL_CLOCK\"\n"
	if err := os.WriteFile(filepath.Join(bin, "date"), []byte(date), 0o700); err != nil {
		t.Fatal(err)
	}
	const want = "date=2024-02-02T19:32:03Z\n"
	for _, attempt := range []struct {
		zone, wallClock string
	}{
		{"EST5", "2025-01-01T00:00:00Z"},
		{"JST-9", "2025-01-02T00:00:00Z"},
	} {
		t.Run(attempt.wallClock, func(t *testing.T) {
			outputPath := filepath.Join(t.TempDir(), "github-output")
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", metadata.Run)
			cmd.Dir = dir
			cmd.Env = append(env,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_OUTPUT="+outputPath, "TZ="+attempt.zone, "FIXTURE_WALL_CLOCK="+attempt.wallClock,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run build metadata step: %v\n%s", err, output)
			}
			output, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(output) != want {
				t.Errorf("build metadata output = %q, want %q", output, want)
			}
		})
	}
}

func TestReusablePublishDraftRetry(t *testing.T) {
	step := publishWorkflowStep(t, "create-draft-release", "create-release")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required to execute the workflow script")
	}
	fixture := `
const assert = require("node:assert/strict");
const createDraft = async (github, context, core) => {
` + step.With["script"] + `
};

(async () => {
  Object.assign(process.env, {
    GITHUB_RUN_ID: "42", GITHUB_RUN_ATTEMPT: "1",
    BINARY_NAME: "primary", RELEASE_TAG: "component/v1.2.3", PRERELEASE: "true",
  });
  const repo = {owner: "example", repo: "tools"};
  const core = {setFailed: message => {throw new Error(message);}};
  let releases = [];
  let createCalls = 0;
  const repos = {
    listReleases() {},
    async deleteRelease() {
      assert.fail("retry attempted to delete the existing draft and its assets");
    },
    async createRelease(request) {
      createCalls++;
      assert.equal(request.draft, true);
      assert.equal(request.generate_release_notes, true);
      assert.equal(request.prerelease, true);
      assert.equal(request.tag_name, process.env.RELEASE_TAG);
      const draft = {
        ...request, id: 17, author: {login: "github-actions[bot]"},
        body: [request.body, "Generated and manually edited notes"].join("\n\n"),
        assets: [{id: 56, name: "uploaded-binary"}],
      };
      releases.push(draft);
      return {data: draft};
    },
  };
  const github = {rest: {repos}, async paginate(method, params) {
    assert.equal(method, repos.listReleases);
    assert.deepEqual(params, {...repo, per_page: 100});
    return releases;
  }};
  const run = () => createDraft(github, {repo}, core);

  assert.equal(await run(), 17);
  const [owned] = releases;
  const original = structuredClone(owned);
  process.env.GITHUB_RUN_ATTEMPT = "2";
  assert.equal(await run(), 17);
  assert.deepEqual(owned, original);

  for (const change of [
    {body: "Unrelated automated release notes"},
    {author: {login: "maintainer"}},
    {draft: false},
  ]) {
    releases = [{...owned, ...change}];
    await assert.rejects(run, /expected one draft from this workflow run/);
  }

  releases = [owned];
  process.env.GITHUB_RUN_ID = "43";
  await assert.rejects(run, /expected one draft from this workflow run/);

  process.env.GITHUB_RUN_ID = "42";
  process.env.BINARY_NAME = "other";
  await assert.rejects(run, /expected one draft from this workflow run/);

  process.env.BINARY_NAME = "primary";
  releases = [owned, {...owned, id: 18}];
  await assert.rejects(run, /expected one draft from this workflow run/);
  assert.equal(createCalls, 1);
})().catch(error => {console.error(error); process.exitCode = 1;});
`
	if output, err := exec.CommandContext(t.Context(), node, "-e", fixture).CombinedOutput(); err != nil {
		t.Fatalf("run draft creation workflow script: %v\n%s", err, output)
	}
}

func TestReusablePublishAssetRetry(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required to execute the workflow script")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum required to compare release asset digests")
	}
	const existing = "primary-v1.2.3-linux-amd64"
	const missing = "extra-v1.2.3-linux-amd64"
	const checksums = "dist/checksums.txt"
	const body = "binary contents\n"
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
	page := func(name, state, digest string, size int) string {
		return fmt.Sprintf(`[[],[{"name":%q,"state":%q,"digest":%q,"size":%d}]]`, name, state, digest, size)
	}

	binaryUpload := publishWorkflowStep(t, "build-binaries", "Upload release asset")
	checksumUpload := publishWorkflowStep(t, "checksum-assets", "Upload checksum manifest")
	for _, test := range []struct {
		name       string
		script     string
		files      string
		list       string
		wantUpload string
		wantError  bool
	}{
		{"partial upload uploads only missing file", binaryUpload.Run, existing + "\n" + missing, page(existing, "uploaded", digest, len(body)), missing, false},
		{"checksum retry skips identical asset", checksumUpload.Run, checksums, page("checksums.txt", "uploaded", digest, len(body)), "", false},
		{"checksum first attempt uses basename", checksumUpload.Run, checksums, "[[]]", checksums, false},
		{"different content", binaryUpload.Run, existing, page(existing, "uploaded", "sha256:different", len(body)), "", true},
		{"different size", binaryUpload.Run, existing, page(existing, "uploaded", digest, len(body)+1), "", true},
		{"missing digest", binaryUpload.Run, existing, page(existing, "uploaded", "", len(body)), "", true},
		{"incomplete upload", binaryUpload.Run, existing, page(existing, "starter", digest, len(body)), "", true},
		{"list API failure", binaryUpload.Run, existing, "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "dist"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"primary", "extra", existing, missing, checksums} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range map[string]string{
				"gh": `#!/bin/sh
set -eu
[ "$#" = 4 ]
[ "$1 $2 $3" = "api --paginate --slurp" ]
[ "$4" = "repos/example/tools/releases/17/assets?per_page=100" ]
[ -n "$ASSETS_RESPONSE" ]
printf '%s\n' "$ASSETS_RESPONSE"
`,
				"curl": `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    -H) [ "$2" != "Authorization: Bearer example-token" ] || authenticated=true; shift ;;
    --data-binary) file="${2#@}"; shift ;;
    --url-query) query="$2"; shift ;;
    https://uploads.github.com/repos/example/tools/releases/17/assets) endpoint="$1" ;;
    https://uploads.github.com/repos/example/tools/releases/17/assets\?*) endpoint="${1%%\?*}"; query="${1#*\?}" ;;
  esac
  shift
done
printf '%s\n' "$file" >> uploaded
[ "$authenticated" = true ]
[ "$query" = "name=${file##*/}" ]
[ -n "$endpoint" ]
[ -s "$file" ]
if [ -n "$ASSETS_RESPONSE" ] && jq -e --arg name "${file##*/}" 'any(.[][]; .name == $name)' >/dev/null <<EOF
$ASSETS_RESPONSE
EOF
then
  echo '422 duplicate asset' >&2
  exit 22
fi
`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", test.script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_REPOSITORY=example/tools", "RELEASE_ID=17", "GH_TOKEN=example-token",
				"GITHUB_ENV="+filepath.Join(dir, "env"), "GITHUB_RUN_ID=42", "GITHUB_RUN_ATTEMPT=2",
				"BINARY_NAME=primary", `ADDITIONAL_BINARY_NAMES=["extra"]`, "RELEASE_TAG=v1.2.3",
				"MATRIX_OS=linux", "MATRIX_ARCH=amd64", "BINARY_COMPRESS=false",
				"ASSET_FILES="+test.files, "ASSETS_RESPONSE="+test.list,
			)
			output, err := cmd.CombinedOutput()
			if (err != nil) != test.wantError {
				t.Fatalf("upload returned %v, want failure %v\n%s", err, test.wantError, output)
			}
			uploads, err := os.ReadFile(filepath.Join(dir, "uploaded"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(uploads)); got != test.wantUpload {
				t.Errorf("uploaded %q, want %q", got, test.wantUpload)
			}
		})
	}
}

type publishStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

func publishWorkflowStep(t *testing.T, job, name string) publishStep {
	t.Helper()
	data, err := os.ReadFile("../.github/workflows/reuseable-publish.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []publishStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, step := range workflow.Jobs[job].Steps {
		if step.Name == name || step.ID == name {
			return step
		}
	}
	t.Fatalf("step %q missing from job %q", name, job)
	return publishStep{}
}
