package supervisor

import (
	"slices"
	"testing"
)

func TestWithoutTokens(t *testing.T) {
	got := WithoutTokens([]string{"PATH=/bin", "GITHUB_TOKEN=ghp_x", "GH_TOKEN=gho_y", "GITHUB_TOKENS=keep", "HOME=/h"})
	want := []string{"PATH=/bin", "GITHUB_TOKENS=keep", "HOME=/h"}
	if !slices.Equal(got, want) {
		t.Errorf("WithoutTokens = %q, want %q", got, want)
	}
}
