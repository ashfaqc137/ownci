//go:build !dev

package github

import "testing"

// D8: a release build must ignore the dev overrides entirely.
func TestReleaseBuildIgnoresDevOverrides(t *testing.T) {
	t.Setenv("OWNCI_DEV_GITHUB_API_URL", "http://evil.example")
	t.Setenv("OWNCI_DEV_GITHUB_WEB_URL", "http://evil.example/web")
	if APIBaseURL() != "https://api.github.com" || WebBaseURL() != "https://github.com" {
		t.Fatalf("release build honored override: %q %q", APIBaseURL(), WebBaseURL())
	}
}
