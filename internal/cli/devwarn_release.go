//go:build !dev

package cli

import "io"

// printDevWarning does nothing in release builds: they honor no overrides.
func printDevWarning(io.Writer) {}
