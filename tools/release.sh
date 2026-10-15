#!/usr/bin/env bash
# Build the release archives: make release VERSION=v0.1.0 [ALLOW_DIRTY=1]
#
# dist/ownci_<version>_linux_{amd64,arm64}.tar.gz (the ownci binary, README.md,
# LICENSE if present) and dist/SHA256SUMS. The binaries are built without the
# `dev` tag, and the build fails if any dev-override string slipped in (D8).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

version="${VERSION:-}"
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: make release VERSION=v0.1.0   (got: '${version}')" >&2
  exit 1
fi
if [ -z "${ALLOW_DIRTY:-}" ] && [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "refusing to release from a dirty git tree (commit first, or ALLOW_DIRTY=1)" >&2
  exit 1
fi

commit="$(git rev-parse --short HEAD)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pkg=github.com/ashfaqc137/ownci/internal/buildinfo
ldflags="-X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.Date=$date"

rm -rf dist
mkdir -p dist
for arch in amd64 arm64; do
  stage="$(mktemp -d)"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags "$ldflags" -o "$stage/ownci" ./cmd/ownci

  if grep -aq 'OWNCI_DEV' "$stage/ownci"; then
    echo "FATAL: $arch binary contains OWNCI_DEV strings; dev overrides must not ship" >&2
    exit 1
  fi

  cp README.md "$stage/"
  files=(ownci README.md)
  if [ -f LICENSE ]; then
    cp LICENSE "$stage/"
    files+=(LICENSE)
  fi
  archive="dist/ownci_${version}_linux_${arch}.tar.gz"
  tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$(git log -1 --format=%ct)" \
    -cf - -C "$stage" "${files[@]}" | gzip -n > "$archive"
  rm -rf "$stage"
  echo "built $archive"
done

(cd dist && sha256sum ./ownci_*.tar.gz | sed 's# \./# #' > SHA256SUMS)
echo "wrote dist/SHA256SUMS"
