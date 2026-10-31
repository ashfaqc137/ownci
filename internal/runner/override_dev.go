//go:build dev

package runner

import (
	"errors"
	"os"
)

// devOverride lets a dev build download a runner from OWNCI_DEV_RUNNER_URL.
// A checksum is mandatory: even dev builds never install unverified
// downloads.
func devOverride(arch string) (Asset, bool, error) {
	url := os.Getenv("OWNCI_DEV_RUNNER_URL")
	if url == "" {
		return Asset{}, false, nil
	}
	sha := os.Getenv("OWNCI_DEV_RUNNER_SHA256")
	if sha == "" {
		return Asset{}, false, errors.New("OWNCI_DEV_RUNNER_URL is set but OWNCI_DEV_RUNNER_SHA256 is not; refusing an unverified download")
	}
	version := os.Getenv("OWNCI_DEV_RUNNER_VERSION")
	if version == "" {
		version = "0.0.0-dev"
	}
	return Asset{Arch: arch, URL: url, SHA256: sha, Version: version}, true, nil
}
