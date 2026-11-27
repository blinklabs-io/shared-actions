package workflows

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseDraftWorkflowWiring(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-release-draft.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		On struct {
			Call struct {
				Outputs map[string]struct {
					Value string `yaml:"value"`
				} `yaml:"outputs"`
			} `yaml:"workflow_call"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Permissions map[string]string               `yaml:"permissions"`
			Outputs     map[string]string               `yaml:"outputs"`
			Steps       []struct{ Name, ID, If string } `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	job := wf.Jobs["prepare"]
	if len(job.Permissions) != 1 || job.Permissions["contents"] != "write" || job.Outputs["release-id"] != "${{ steps.create.outputs.result }}" || wf.On.Call.Outputs["release-id"].Value != "${{ jobs.prepare.outputs.release-id }}" {
		t.Fatal("draft permissions or output propagation changed")
	}
	if len(job.Steps) != 3 || job.Steps[0].Name != "Validate manual dispatch" || job.Steps[0].If != "github.event_name == 'workflow_dispatch'" || job.Steps[1].Name != "Validate release tag" || job.Steps[1].If != "startsWith(github.ref, 'refs/tags/')" || job.Steps[2].ID != "create" || job.Steps[2].If != "startsWith(github.ref, 'refs/tags/')" {
		t.Fatal("draft guards must run before tag-only creation")
	}
}

func TestReleaseDraftGuards(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("Bash required")
	}
	wf := readReusableWorkflow(t, "../.github/workflows/reuseable-release-draft.yml")
	var manual, tag string
	for _, step := range wf.Jobs["prepare"].Steps {
		switch step.Name {
		case "Validate manual dispatch":
			manual = step.Run
		case "Validate release tag":
			tag = step.Run
		}
	}
	if manual == "" || tag == "" {
		t.Fatal("missing release guards")
	}
	for _, tc := range []struct {
		name, script, ref, branch string
		valid                     bool
	}{
		{"manual default", manual, "refs/heads/main", "main", true},
		{"manual branch", manual, "refs/heads/other", "main", false},
		{"manual tag", manual, "refs/tags/v1.2.3", "main", false},
		{"missing default", manual, "refs/heads/main", "", false},
		{"stable", tag, "refs/tags/v1.2.3", "", true},
		{"prerelease", tag, "refs/tags/v1.2.3-rc.1", "", true},
		{"invalid semver", tag, "refs/tags/v01.2.3", "", false},
		{"invalid prerelease", tag, "refs/tags/v1.2.3-rc.01", "", false},
		{"shell input", tag, "refs/tags/v1.2.3;id", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envfile := filepath.Join(t.TempDir(), "env")
			cmd := exec.Command("bash", "-e", "-c", tc.script)
			cmd.Env = append(os.Environ(), "GITHUB_REF="+tc.ref, "DEFAULT_BRANCH="+tc.branch, "GITHUB_ENV="+envfile)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.valid {
				t.Fatalf("result %v: %s", err, out)
			}
			if tc.valid && tc.script == tag {
				data, err := os.ReadFile(envfile)
				if err != nil {
					t.Fatal(err)
				}
				want := "RELEASE_PRERELEASE=false"
				if strings.Contains(tc.ref, "-rc.") {
					want = "RELEASE_PRERELEASE=true"
				}
				if !strings.Contains(string(data), want) {
					t.Fatalf("wrong channel: %s", data)
				}
			}
		})
	}
}

func TestReleaseDraftOwnershipAndRetry(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	wf := readReusableWorkflow(t, "../.github/workflows/reuseable-release-draft.yml")
	var script string
	for _, step := range wf.Jobs["prepare"].Steps {
		if step.ID == "create" {
			script = step.With["script"]
		}
	}
	if script == "" {
		t.Fatal("missing draft creation")
	}
	fixture := `
const assert = require('node:assert/strict');
const create = async (github, context) => {
` + script + `
};
(async () => {
 Object.assign(process.env,{GITHUB_RUN_ID:'42', RELEASE_TAG:'v1.2.3-rc.1', RELEASE_PRERELEASE:'true'});
 const context={repo:{owner:'example',repo:'app'}}; let releases=[]; let count=0;
 const repos={listReleases(){}, async createRelease(request){count++;assert.equal(request.draft,true);assert.equal(request.prerelease,true);assert.equal(request.tag_name,'v1.2.3-rc.1');const result={...request,id:12,author:{login:'github-actions[bot]'}};releases.push(result);return {data:result}}};
 const github={rest:{repos}, async paginate(method,params){assert.equal(method,repos.listReleases);assert.equal(params.owner,'example');return releases}};
 assert.equal(await create(github,context),12);assert.equal(await create(github,context),12);assert.equal(count,1);
 for(const altered of [{draft:false},{author:{login:'other'}},{body:'unrelated'}]){const original=releases;releases=[{...original[0],...altered}];await assert.rejects(()=>create(github,context),/already exists/);releases=original}
 process.env.GITHUB_RUN_ID='43';await assert.rejects(()=>create(github,context),/already exists/);
 process.env.GITHUB_RUN_ID='invalid';await assert.rejects(()=>create(github,context),/positive integer/);
})().catch(error=>{console.error(error);process.exitCode=1});
`
	cmd := exec.CommandContext(t.Context(), node, "-e", fixture)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("draft script: %v\n%s", err, out)
	}
}
