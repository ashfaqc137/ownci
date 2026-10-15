package hooks

// CleanupImage is the throwaway container the hook deletes root-owned files
// with. busybox 1.37.0 is pinned by its multi-arch index digest (looked up
// with `docker buildx imagetools inspect busybox:1.37.0`, 2026-10-15), so
// amd64 and arm64 both resolve to reviewed content.
const CleanupImage = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"
