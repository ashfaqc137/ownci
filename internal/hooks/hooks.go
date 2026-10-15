// Package hooks holds the scripts OwnCI installs into the runner directory
// and the pinned image they use.
package hooks

import (
	_ "embed"
	"path/filepath"
)

// JobCompletedScript is the runner's job-completed hook: it wipes the job
// workspace after every job (see job-completed.sh).
//
//go:embed job-completed.sh
var JobCompletedScript []byte

// Directory inside the runner dir for OwnCI-managed files.
const dirName = "ownci"

// Dir is <runnerDir>/ownci.
func Dir(runnerDir string) string { return filepath.Join(runnerDir, dirName) }

// JobCompletedPath is where the hook is installed.
func JobCompletedPath(runnerDir string) string {
	return filepath.Join(Dir(runnerDir), "job-completed.sh")
}

// LastCleanupPath is the record the hook writes after each cleanup.
func LastCleanupPath(runnerDir string) string {
	return filepath.Join(Dir(runnerDir), "last-cleanup.json")
}
