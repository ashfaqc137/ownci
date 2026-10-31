#!/bin/sh
# Install the OwnCI CLI from a GitHub release:
#
#   curl -fsSL https://sparshlabs.com/ownci/install.sh | sh
#
# Linux (including WSL2) on amd64 or arm64 only. The archive is checked against
# the release's SHA256SUMS before anything is installed.
#
# Environment:
#   OWNCI_VERSION        release tag to install, e.g. v0.1.0 (default or "latest": the latest release)
#   OWNCI_INSTALL_DIR    where to put the binary (default: $HOME/.local/bin)
#   OWNCI_DOWNLOAD_BASE  base URL of the release downloads (default: GitHub; for testing)
#
# The checksum proves the download is intact. To also check where it was built, verify the
# release's build-provenance attestation with the GitHub CLI:
#   gh attestation verify ownci_<version>_linux_<arch>.tar.gz --repo ashfaqc137/ownci
#
# Everything runs from main, called on the last line, so a truncated download runs nothing.
set -eu

OWNCI_REPO_URL="https://github.com/ashfaqc137/ownci"

say() { printf 'ownci-install: %s\n' "$*"; }
fail() {
	printf 'ownci-install: error: %s\n' "$*" >&2
	exit 1
}

detect_arch() {
	case "$(uname -s)" in
	Linux) ;;
	Darwin) fail "macOS is not supported yet; OwnCI runs on Linux and WSL2 only" ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT) fail "native Windows is not supported; install OwnCI inside WSL2 (https://learn.microsoft.com/windows/wsl/install)" ;;
	*) fail "unsupported operating system $(uname -s); OwnCI runs on Linux and WSL2 only" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) fail "unsupported architecture $(uname -m); OwnCI supports amd64 and arm64" ;;
	esac
}

# latest_version prints the tag the /releases/latest redirect points to.
latest_version() {
	latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$OWNCI_REPO_URL/releases/latest")" ||
		fail "could not reach $OWNCI_REPO_URL/releases/latest"
	case "$latest_url" in
	*/releases/tag/v*) echo "${latest_url##*/}" ;;
	*) fail "no OwnCI release found at $OWNCI_REPO_URL/releases" ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		fail "need sha256sum or shasum to verify the download"
	fi
}

main() {
	command -v curl >/dev/null 2>&1 || fail "curl is required"
	command -v tar >/dev/null 2>&1 || fail "tar is required"

	arch="$(detect_arch)"
	version="${OWNCI_VERSION:-}"
	if [ -z "$version" ] || [ "$version" = latest ]; then
		version="$(latest_version)"
	fi
	case "$version" in
	v*) ;;
	*) version="v$version" ;;
	esac
	base="${OWNCI_DOWNLOAD_BASE:-$OWNCI_REPO_URL/releases/download}/$version"
	archive="ownci_${version}_linux_${arch}.tar.gz"
	install_dir="${OWNCI_INSTALL_DIR:-$HOME/.local/bin}"

	if [ "$(id -u)" = 0 ]; then
		say "warning: running as root; the GitHub runner refuses to run as root, so use OwnCI as a normal user"
	fi

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM

	say "downloading OwnCI $version (linux/$arch)"
	curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download failed: $base/$archive"
	curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "download failed: $base/SHA256SUMS"

	expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/SHA256SUMS")"
	[ -n "$expected" ] || fail "$archive is not listed in SHA256SUMS"
	actual="$(sha256_of "$tmp/$archive")"
	[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive (expected $expected, got $actual); nothing was installed"
	say "checksum OK"

	tar -xzf "$tmp/$archive" -C "$tmp" ownci || fail "could not extract $archive"
	mkdir -p "$install_dir"
	install -m 0755 "$tmp/ownci" "$install_dir/ownci"
	say "installed $install_dir/ownci"

	ownci="ownci"
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*)
		ownci="$install_dir/ownci"
		say "note: $install_dir is not on your PATH. To add it for new shells, run:"
		say "  echo 'export PATH=\"$install_dir:\$PATH\"' >> ~/.profile   # or your shell's rc file, e.g. ~/.bashrc or ~/.zshrc"
		say "  then open a new terminal. Until then, use the full path: $ownci"
		;;
	esac

	"$install_dir/ownci" version
	say "next: run '$ownci doctor', then '$ownci setup --repo owner/name'"
}

main "$@"
