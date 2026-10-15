//go:build dev

package github

import "os"

// Dev builds honor OWNCI_DEV_GITHUB_API_URL / _WEB_URL (D8) so the CLI can be
// driven against tools/fakegh.
func devAPIURL() string { return os.Getenv("OWNCI_DEV_GITHUB_API_URL") }
func devWebURL() string { return os.Getenv("OWNCI_DEV_GITHUB_WEB_URL") }
