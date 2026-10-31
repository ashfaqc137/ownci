//go:build !dev

package runner

// devOverride is a no-op in release builds: the override code is not
// even compiled in.
func devOverride(string) (Asset, bool, error) { return Asset{}, false, nil }
