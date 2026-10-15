//go:build dev

package runner

import "testing"

func TestDevOverride(t *testing.T) {
	t.Setenv("OWNCI_DEV_RUNNER_URL", "http://localhost:1/r.tar.gz")
	t.Setenv("OWNCI_DEV_RUNNER_SHA256", "abc")
	t.Setenv("OWNCI_DEV_RUNNER_VERSION", "")
	a, err := AssetFor("linux-x64")
	if err != nil || a.URL != "http://localhost:1/r.tar.gz" || a.SHA256 != "abc" || a.Version != "0.0.0-dev" {
		t.Fatalf("got %+v, %v", a, err)
	}
	t.Setenv("OWNCI_DEV_RUNNER_VERSION", "1.2.3")
	if a, _ := AssetFor("linux-x64"); a.Version != "1.2.3" {
		t.Errorf("version = %q", a.Version)
	}
}

func TestDevOverrideRequiresChecksum(t *testing.T) {
	t.Setenv("OWNCI_DEV_RUNNER_URL", "http://localhost:1/r.tar.gz")
	t.Setenv("OWNCI_DEV_RUNNER_SHA256", "")
	if _, err := AssetFor("linux-x64"); err == nil {
		t.Fatal("want error without SHA256")
	}
}

func TestNoOverrideFallsBackToPinned(t *testing.T) {
	t.Setenv("OWNCI_DEV_RUNNER_URL", "")
	a, err := AssetFor("linux-x64")
	if err != nil || a.Version != Pinned.Version {
		t.Fatalf("got %+v, %v", a, err)
	}
}
