package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildOwnci(t *testing.T, tags string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ownci")
	args := []string{"build", "-trimpath", "-o", out}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, ".")
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Release builds must not contain the dev override code. The dev build is
// built too, as a control: if it did not contain the strings the check would be
// vacuous.
func TestReleaseBuildHasNoDevOverrides(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary twice")
	}
	marker := []byte("OWNCI_DEV_")
	if bytes.Contains(buildOwnci(t, ""), marker) {
		t.Error("the release binary contains OWNCI_DEV_ strings")
	}
	if !bytes.Contains(buildOwnci(t, "dev"), marker) {
		t.Error("control failed: the dev binary does not contain OWNCI_DEV_ strings")
	}
}
