//go:build !dev

package runner

import "testing"

// D8: a release build must ignore the dev override entirely.
func TestReleaseBuildIgnoresDevOverride(t *testing.T) {
	t.Setenv("OWNCI_DEV_RUNNER_URL", "http://evil.example/r.tar.gz")
	t.Setenv("OWNCI_DEV_RUNNER_SHA256", "abc")
	a, err := AssetFor("linux-x64")
	if err != nil || a.URL == "http://evil.example/r.tar.gz" || a.Version != Pinned.Version {
		t.Fatalf("got %+v, %v", a, err)
	}
}
