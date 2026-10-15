//go:build dev

package github

import "testing"

func TestDevOverridesHonored(t *testing.T) {
	t.Setenv("OWNCI_DEV_GITHUB_API_URL", "http://127.0.0.1:1/")
	t.Setenv("OWNCI_DEV_GITHUB_WEB_URL", "http://127.0.0.1:1/web")
	if got := APIBaseURL(); got != "http://127.0.0.1:1" {
		t.Errorf("APIBaseURL = %q", got)
	}
	if got := WebBaseURL(); got != "http://127.0.0.1:1/web" {
		t.Errorf("WebBaseURL = %q", got)
	}
}

func TestDevOverridesUnset(t *testing.T) {
	t.Setenv("OWNCI_DEV_GITHUB_API_URL", "")
	t.Setenv("OWNCI_DEV_GITHUB_WEB_URL", "")
	if APIBaseURL() != "https://api.github.com" || WebBaseURL() != "https://github.com" {
		t.Errorf("defaults wrong: %q %q", APIBaseURL(), WebBaseURL())
	}
}
