package workflows

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHelmDeployLintUsesDeploymentValues(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-helm-deploy.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct{ Name, Run string }
		}
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range wf.Jobs["prepare-chart"].Steps {
		if step.Name == "Prepare and verify chart" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("prepare chart step missing")
	}
	for _, test := range []struct {
		name, input, wantError string
		values                 []string
		local                  bool
	}{
		{name: "defaults"},
		{name: "reproducible local chart", local: true},
		{name: "ordered overlays", values: []string{"values.yaml", "path with spaces/production.yaml"}},
		{name: "missing overlay", input: "missing.yaml", wantError: "values file not found"},
		{name: "outside overlay", input: "outside.yaml", wantError: "values file must be inside"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var expected []string
			for _, file := range test.values {
				path := filepath.Join(root, file)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("configured: true\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				expected = append(expected, path)
			}
			if test.name == "outside overlay" {
				outside := filepath.Join(t.TempDir(), "outside.yaml")
				if err := os.WriteFile(outside, []byte("outside: true\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "outside.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			if expected == nil {
				expected = []string{}
			}
			encoded, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			fakeHelm := `#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
args = sys.argv[1:]
with open(os.environ['HELM_LOG'], 'a') as log:
    log.write(args[0] + '\n')
if args[0] == 'pull':
    Path(args[args.index('--destination') + 1], 'fixture-1.0.0.tgz').write_bytes(b'chart archive fixture')
elif args[0] == 'show':
    print('name: fixture\nversion: 1.0.0')
elif args[0] == 'package':
    if os.environ.get('SOURCE_DATE_EPOCH') != '0':
        raise SystemExit('local chart package must use SOURCE_DATE_EPOCH=0 for reproducible archive bytes')
    Path(args[args.index('--destination') + 1], 'fixture-1.0.0.tgz').write_bytes(b'chart archive fixture')
elif args[0] in ('lint', 'template'):
    values = [args[i+1] for i, arg in enumerate(args) if arg == '--values']
    expected = json.loads(os.environ['EXPECTED_VALUES'])
    if values != expected:
        raise SystemExit(f'{args[0]} values {values} do not match deployment values {expected}')
else:
    raise SystemExit(f'unexpected Helm operation: {args}')
`
			if err := os.WriteFile(filepath.Join(root, "helm"), []byte(fakeHelm), 0o755); err != nil {
				t.Fatal(err)
			}
			values := strings.Join(test.values, "\n\n")
			if test.input != "" {
				values = test.input
			}
			digest := sha256.Sum256([]byte("chart archive fixture"))
			log := filepath.Join(root, "helm.log")
			chart, version, wantOperations := "oci://example/charts/fixture", "1.0.0", "pull\nlint\ntemplate\n"
			if test.local {
				chart, version, wantOperations = root, "", "show\npackage\nlint\ntemplate\n"
				if err := os.WriteFile(filepath.Join(root, "Chart.yaml"), []byte("apiVersion: v2\nname: fixture\nversion: 1.0.0\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CHART="+chart, "CHART_VERSION="+version, "CHART_SHA256="+hex.EncodeToString(digest[:]),
				"RELEASE=fixture", "NAMESPACE=test", "VALUES_FILES="+values, "RUNNER_TEMP="+root,
				"GITHUB_WORKSPACE="+root, "GITHUB_OUTPUT="+filepath.Join(root, "output"), "HELM_LOG="+log,
				"EXPECTED_VALUES="+string(encoded), "SOURCE_DATE_EPOCH=1700000000")
			out, err := cmd.CombinedOutput()
			operations, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.wantError != "" {
				if err == nil || !strings.Contains(string(out), test.wantError) || string(operations) != "pull\n" {
					t.Fatalf("invalid values reached Helm lint/render: %v calls=%s\n%s", err, operations, out)
				}
				return
			}
			if err != nil || string(operations) != wantOperations {
				t.Fatalf("chart validation failed: %v calls=%s\n%s", err, operations, out)
			}
		})
	}
}

func TestHelmDeployVerifiesPreparedArchive(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/reuseable-helm-deploy.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ Name, Run string }
		}
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["deploy"].Steps {
		if step.Name == "Verify prepared chart and values" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("prepared chart verification step missing")
	}
	for _, test := range []struct {
		name, packageName, wantError string
		tamper                       bool
	}{
		{name: "release", packageName: "app-1.2.3.tgz"},
		{name: "build metadata", packageName: "app-1.2.3+build.1.tgz"},
		{name: "prerelease metadata", packageName: "app-1.2.3-rc.1+build.1.tgz"},
		{name: "nested path", packageName: "nested/app-1.2.3+build.1.tgz", wantError: "invalid prepared chart metadata"},
		{name: "parent path", packageName: "../app-1.2.3+build.1.tgz", wantError: "invalid prepared chart metadata"},
		{name: "backslash", packageName: `nested\app-1.2.3+build.1.tgz`, wantError: "invalid prepared chart metadata"},
		{name: "newline", packageName: "app-1.2.3+build.1\n.tgz", wantError: "invalid prepared chart metadata"},
		{name: "modified metadata archive", packageName: "app-1.2.3+build.1.tgz", tamper: true, wantError: "FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			archiveDir := filepath.Join(root, "prepared-chart")
			if err := os.Mkdir(archiveDir, 0o755); err != nil {
				t.Fatal(err)
			}
			archive := []byte("verified chart archive")
			digest := sha256.Sum256(archive)
			if test.tamper {
				archive = []byte("modified chart archive")
			}
			if test.wantError == "" || test.tamper {
				if err := os.WriteFile(filepath.Join(archiveDir, test.packageName), archive, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PACKAGE_NAME="+test.packageName,
				"CHART_SHA256="+hex.EncodeToString(digest[:]), "RELEASE=app", "NAMESPACE=test",
				"VALUES_FILES=", "RUNNER_TEMP="+root, "GITHUB_WORKSPACE="+root)
			out, err := cmd.CombinedOutput()
			if test.wantError != "" {
				if err == nil || !strings.Contains(string(out), test.wantError) {
					t.Fatalf("archive %q was not rejected by the expected check: %v\n%s", test.packageName, err, out)
				}
				return
			}
			if err != nil || !strings.Contains(string(out), test.packageName+": OK") {
				t.Fatalf("valid chart archive %q failed verification: %v\n%s", test.packageName, err, out)
			}
		})
	}
}
