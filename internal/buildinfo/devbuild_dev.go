//go:build dev

package buildinfo

// DevBuild is true only for binaries built with -tags dev (make build-dev).
const DevBuild bool = true
