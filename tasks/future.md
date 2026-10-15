# OwnCI — Future work (post initial release)

> Ideas deliberately kept out of the MVP (t01–t10). Pick them up as separate tasks after the first release. Add new entries at the bottom; move an entry into its own `tNN-*.md` spec when it is scheduled.

## F1 — User-supplied runner (`--runner-archive` / `--runner-url`)

**Status:** not started · **Raised:** 2026-10-15 (during t04 review)

**Problem.** `ownci setup` only installs the runner version pinned in `internal/runner/versions.go` (D5). Some users need a different source: air-gapped machines or internal mirrors, GitHub Enterprise Server (which may want the runner version it ships with), or staying on an older runner for reproducibility.

**Proposed design**
- Explicit **flags** on `setup` (not environment variables, so it is a deliberate action): `--runner-archive <file>` or `--runner-url <url>`.
- Either flag **requires `--runner-sha256 <hex>`**. No unverified installs, the same rule the dev override follows (D8).
- Reuse `runner.Fetch` / `runner.Extract` / `runner.Install`. Only the `Asset` passed in changes (extend `AssetFor` or build the asset in `cli/setup.go`). A local archive skips the download but still verifies the checksum before extraction.
- Print a one-line warning that a non-default runner is in use, and record the source (`runnerSource`: `pinned` | `url` | `archive`, plus URL/path) in `config.json` so `status` can show it. That is a config schema addition, so bump or extend `schemaVersion` handling accordingly.
- Keep the D8 rule intact: release builds must still not honor `OWNCI_DEV_RUNNER_*`.

**Open questions**
- Should `--runner-version <v>` (a known GitHub release, with its checksum fetched from the release notes) exist as well? That trusts the same server as the download, so it needs a clear warning or a documented trade-off.
- GHES: does the runner download URL differ, and does registration need extra flags? Needs its own investigation.

**Touches:** `internal/runner/versions.go`, `internal/cli/setup.go`, `internal/config/config.go` (+ `status` in t07), README (trust model section).
