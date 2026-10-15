//go:build !dev

package buildinfo

import "testing"

func TestDevBuildFalseInReleaseBuilds(t *testing.T) {
	if DevBuild {
		t.Fatal("DevBuild must be false without -tags dev")
	}
}
