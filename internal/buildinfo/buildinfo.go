// Package buildinfo holds build-time metadata injected through -ldflags -X.
package buildinfo

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
