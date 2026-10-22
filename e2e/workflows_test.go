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
		file          string
		wantContains  []string
		wantMissing   []string
		jobs          int
		wantContainer bool
	}{
		{"ownci-pass.yml", []string{"pull_request:", "workflow_dispatch:", "container: ubuntu:24.04", "actions/checkout@v4", "RUNNER_NAME"}, nil, 1, true},
		{"ownci-nocontainer.yml", []string{"workflow_dispatch:"}, []string{"pull_request"}, 1, false},
		{"ownci-poison.yml", []string{"workflow_dispatch:", "needs: first", "RUNNER_TOOL_CACHE", "_actions"}, []string{"pull_request"}, 2, true},
		{"ownci-default-image.yml", []string{"workflow_dispatch:", "needs: first", "sudo -n true", "/var/run/docker.sock", "ownci-poison", ":/mnt/"}, []string{"pull_request"}, 2, false},
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
			if hasContainer != tc.wantContainer {
				t.Errorf("declares a job container = %v, want %v", hasContainer, tc.wantContainer)
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
	for _, want := range []string{"set -euo pipefail", "OWNCI_E2E_REPO", `"PUBLIC"`, "Jobs without a job container are forbidden", "OWNCI_E2E_MODE", "ownci-default-image.yml"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("run.sh lacks %q", want)
		}
	}
}

// The example image for container-runner mode must keep everything the probe
// in internal/runnercontainer requires.
func TestUbuntu22ImageHasTheRunnerDependencies(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("images", "ubuntu22.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"FROM ubuntu:22.04", "libicu70", "liblttng-ust1", "libssl3", "git", "sudo", "docker/docker"} {
		if !strings.Contains(s, want) {
			t.Errorf("ubuntu22.Dockerfile lacks %q", want)
		}
	}
}
