// Package e2e holds the real-GitHub end-to-end assets (workflows and the
// helper script). This test only checks their shape; the workflows themselves
// are exercised by docs/e2e.md against a real repository.
package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWorkflowsTargetTheOwnciRunner(t *testing.T) {
	tests := []struct {
		file         string
		wantContains []string
		wantMissing  []string
		jobs         int
	}{
		{"ownci-pass.yml", []string{"pull_request:", "workflow_dispatch:", "container: ubuntu:24.04", "actions/checkout@v4", "RUNNER_NAME"}, nil, 1},
		{"ownci-nocontainer.yml", []string{"workflow_dispatch:"}, []string{"pull_request"}, 1},
		{"ownci-poison.yml", []string{"workflow_dispatch:", "needs: first", "RUNNER_TOOL_CACHE", "_actions"}, []string{"pull_request"}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			s := read(t, tc.file)
			if n := strings.Count(s, "runs-on: [ownci]"); n != tc.jobs {
				t.Errorf("runs-on: [ownci] appears %d times, want %d (one per job)", n, tc.jobs)
			}
			if strings.Contains(s, "self-hosted") || strings.Contains(s, "ubuntu-latest") {
				t.Error("must target only the opt-in label")
			}
			hasContainer := regexp.MustCompile(`(?m)^\s*container:`).MatchString(s)
			if wantContainer := tc.file != "ownci-nocontainer.yml"; hasContainer != wantContainer {
				t.Errorf("declares a job container = %v, want %v", hasContainer, wantContainer)
			}
			if strings.Contains(s, "\t") {
				t.Error("YAML must not contain tabs")
			}
			for _, w := range tc.wantContains {
				if !strings.Contains(s, w) {
					t.Errorf("missing %q", w)
				}
			}
			for _, w := range tc.wantMissing {
				if strings.Contains(s, w) {
					t.Errorf("must not contain %q", w)
				}
			}
			if !strings.Contains(s, "\njobs:\n") || !strings.Contains(s, "\non:\n") {
				t.Error("missing on:/jobs: top-level keys")
			}
		})
	}
}

func TestRunScriptIsExecutableAndGuardsAgainstPublicRepos(t *testing.T) {
	fi, err := os.Stat("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Error("run.sh is not executable")
	}
	b, _ := os.ReadFile("run.sh")
	for _, want := range []string{"set -euo pipefail", "OWNCI_E2E_REPO", `"PUBLIC"`, "Jobs without a job container are forbidden"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("run.sh lacks %q", want)
		}
	}
}
