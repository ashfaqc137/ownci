//go:build dev

package cli

import "testing"

func TestDevWarning(t *testing.T) {
	env := []string{"PATH=/bin", "OWNCI_DEV_RUNNER_URL=http://x", "OWNCI_DEV_GITHUB_API_URL=http://y", "OWNCI_DEV_EMPTY="}
	if got := devWarning(env); got != "DEV BUILD: overrides active (OWNCI_DEV_GITHUB_API_URL, OWNCI_DEV_RUNNER_URL)" {
		t.Errorf("got %q", got)
	}
	if got := devWarning([]string{"PATH=/bin", "OWNCI_DEV_EMPTY="}); got != "" {
		t.Errorf("no overrides but warned: %q", got)
	}
}
