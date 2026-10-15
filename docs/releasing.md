# Releasing

There is no release automation yet: a release is built locally and published by hand.

## Build

```bash
git checkout main && git pull
make check
make release VERSION=v0.1.0          # refuses a dirty tracked tree; ALLOW_DIRTY=1 overrides (testing only)
(cd dist && sha256sum -c SHA256SUMS) # every line must say OK
```

`make release` (`tools/release.sh`) produces, for `amd64` and `arm64`:

- `dist/ownci_v0.1.0_linux_<arch>.tar.gz` — the `ownci` binary, `README.md` and `LICENSE` if one exists,
  built with `CGO_ENABLED=0 -trimpath` and no `dev` build tag;
- `dist/SHA256SUMS`.

The script fails if a binary contains any `OWNCI_DEV` string (the dev overrides of D8 must never
ship; `cmd/ownci/release_test.go` checks the same). Check a binary by hand:

```bash
tar -xzf dist/ownci_v0.1.0_linux_amd64.tar.gz -C /tmp
/tmp/ownci version            # ownci v0.1.0 (commit …, built …) — and no "build: dev" line
grep -ac OWNCI_DEV /tmp/ownci # 0
```

The CLI version and the runner version are independent: `ownci version` shows both once a runner is
set up, and the runner pin lives in `internal/runner/versions.go` (changing it is a deliberate,
separately tested change).

## Publish

```bash
git tag -a v0.1.0 -m "OwnCI v0.1.0" && git push origin v0.1.0
gh release create v0.1.0 dist/* --title "OwnCI v0.1.0" --notes-file <notes.md>
```

Before announcing a release, run the real-GitHub runbook ([`e2e.md`](e2e.md)) with the release
binary and record it in [`acceptance.md`](acceptance.md). A platform is not "supported" until its
acceptance criteria pass.

## Not done yet

CI for this repository, package managers, signing/attestation of the archives, and auto-update.
