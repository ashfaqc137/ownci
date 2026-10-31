# Releasing

Pushing a `v*` tag publishes a GitHub release through
[`.github/workflows/release.yml`](../.github/workflows/release.yml). Linux `amd64` and `arm64`
only: Windows users run the Linux binary in WSL2, and macOS is not supported yet.

## Release

```bash
git checkout main && git pull
make check
$EDITOR docs/release-notes/v0.1.0.md          # required: the workflow uses it as the release body
git add docs/release-notes/v0.1.0.md && git commit -m "release notes for v0.1.0" && git push
git tag -a v0.1.0 -m "OwnCI v0.1.0" && git push origin v0.1.0
gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
```

The workflow:

1. refuses a tag that is not on `main`, or that has no `docs/release-notes/<tag>.md`;
2. runs `make check`;
3. runs `make release VERSION=<tag>` and `sha256sum -c`;
4. attests the archives (build provenance);
5. creates the release with the archives, `SHA256SUMS` and `install.sh`. A tag with a
   `-` (e.g. `v0.2.0-rc.1`) becomes a pre-release, which `releases/latest` (and so
   `install.sh` without `OWNCI_VERSION`) ignores.

If the workflow fails after the tag is pushed, fix the cause on `main`, then delete and recreate
the tag (`git push --delete origin v0.1.0 && git tag -d v0.1.0`, retag, push). Delete a
half-created release first (`gh release delete v0.1.0`).

## After publishing

```bash
curl -fsSL https://sparshlabs.com/ownci/install.sh | sh   # or the copy attached to the release
ownci version                 # ownci v0.1.0 (commit …, built …), and no "build: dev" line
ownci doctor
gh release download v0.1.0 -p '*linux_amd64.tar.gz' -D /tmp/ownci-rel
gh attestation verify /tmp/ownci-rel/ownci_v0.1.0_linux_amd64.tar.gz --repo ashfaqc137/ownci
```

Before announcing a release with code changes, run the real-GitHub runbook ([`e2e.md`](e2e.md))
with the release binary and record it in [`acceptance.md`](acceptance.md). A platform is not
"supported" until its acceptance criteria pass.

## The site's install.sh

`https://sparshlabs.com/ownci/install.sh` is served by the website (`website/`), which copies the repo's
`install.sh` into its build (`npm run sync-installer`; the copy is gitignored). The site builds from `main`,
while the binaries come from the latest GitHub release, so `install.sh` must keep working with released
archive names (`ownci_<tag>_linux_<arch>.tar.gz`) and `SHA256SUMS`. Publish the release first, then deploy the site.

## Local build (dry run)

```bash
make release VERSION=v0.1.0          # refuses a dirty tracked tree; ALLOW_DIRTY=1 overrides (testing only)
(cd dist && sha256sum -c SHA256SUMS) # every line must say OK
```

`make release` (`tools/release.sh`) produces, for `amd64` and `arm64`:

- `dist/ownci_v0.1.0_linux_<arch>.tar.gz`: the `ownci` binary, `README.md` and `LICENSE`,
  built with `CGO_ENABLED=0 -trimpath` and no `dev` build tag;
- `dist/SHA256SUMS`.

The script fails if a binary contains any `OWNCI_DEV` string (the dev overrides must never
ship; `cmd/ownci/release_test.go` checks the same). Check a binary by hand:

```bash
tar -xzf dist/ownci_v0.1.0_linux_amd64.tar.gz -C /tmp
/tmp/ownci version            # ownci v0.1.0 (commit …, built …), and no "build: dev" line
grep -ac OWNCI_DEV /tmp/ownci # 0
```

Test `install.sh` against a local build. The base URL must contain a `<tag>/` directory:

```bash
ALLOW_DIRTY=1 make release VERSION=v0.1.0-rc.0
mkdir -p /tmp/rel/v0.1.0-rc.0 && cp dist/* /tmp/rel/v0.1.0-rc.0/
OWNCI_DOWNLOAD_BASE=file:///tmp/rel OWNCI_VERSION=v0.1.0-rc.0 OWNCI_INSTALL_DIR=/tmp/inst sh install.sh
```

The CLI version and the runner version are independent: `ownci version` shows both once a runner is
set up, and the runner pin lives in `internal/runner/versions.go` (changing it is a deliberate,
separately tested change).

## Not done yet

Package managers, signing the archives (beyond the GitHub attestations), auto-update, and
macOS/native Windows builds.
