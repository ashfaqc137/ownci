//go:build dev

package buildinfo

import "testing"

func TestDevBuildTrueInDevBuilds(t *testing.T) {
	if !DevBuild {
		t.Fatal("DevBuild must be true with -tags dev")
	}
}
