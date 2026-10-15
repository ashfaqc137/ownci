# t04 — Runner artifact: pinned version, download, verify, safe extract (+ fake runner fixture)

> Read `tasks/00-context.md` first (D5, D8, §4, §7 "Runner release artifacts" / "config.sh" / "run.sh"), then the Implementation notes of t01–t03.

## Goal
`ownci setup --download-only` downloads the pinned official runner for this machine's arch, verifies its SHA256 **before** extracting, extracts it safely into `runners/<name>/`, and confirms the installed version. Also build the **fake runner** fixture that later tasks (t05–t09) use for tests and manual dev flows.

## Depends on / current state
- t02: `config.Paths` (`CacheDir`, `RunnerDir(name)`), `platform.Info.RunnerArch`, logging.
- t03: `execx.Runner` / `execx.Fake`, doctor.
- `setup` is still a stub (t01). This task adds only the `--download-only` path and its flags.

## What later tasks build on
- `runner.Pinned`, `runner.AssetFor(arch)`, `runner.Fetch`, `runner.Extract`, `runner.InstalledVersion`, `runner.Install(...)`. t06 calls `Install` as part of full setup.
- `internal/testutil` fake runner: `testutil.FakeRunnerTarball(opts) (data []byte, sha256hex string)` and the scripts in `internal/testutil/fakerunner/`. t05's fakegh serves it. t06–t09 drive it.
- The `--name` flag and default name logic (`ownci-<short hostname>`, lowercased, `[a-z0-9-]` only, max 64 chars), shared with t06.

## Scope

### 1. Pinned version (`internal/runner/versions.go`)
- Find the current latest runner release and its SHA256 values:
  ```bash
  V=$(gh api repos/actions/runner/releases/latest --jq .tag_name | sed 's/^v//')
  for a in linux-x64 linux-arm64; do
    echo "$a $(gh api repos/actions/runner/releases/tags/v$V --jq .body | grep -o "BEGIN SHA $a -->[0-9a-f]*" | sed 's/.*-->//')"
  done
  ```
  If `gh` is not authenticated, read the release page in a browser or use `curl -s https://api.github.com/repos/actions/runner/releases/latest`.
- Encode it as:
  ```go
  type Asset struct { Arch, URL, SHA256 string }
  type Release struct { Version string; Assets map[string]Asset }
  var Pinned = Release{...}          // URL built from the pattern in 00-context §7
  func AssetFor(arch string) (Asset, error)   // dev override first (see 4), then Pinned
  ```
- Record the chosen version and the date it was pinned in a comment, and in the Implementation notes.

### 2. Download (`internal/runner/download.go`)
```go
type FetchOptions struct { Asset Asset; CacheDir string; HTTP *http.Client; Progress io.Writer /* may be nil */; Logger *slog.Logger }
func Fetch(ctx context.Context, o FetchOptions) (archivePath string, err error)
```
- Cache file: `<CacheDir>/<basename of URL>`. If it exists and its SHA256 matches → reuse (log "cache hit"). If it exists but **mismatches** → log a warning, delete it, re-download.
- Download to `<file>.partial` (0600), hashing while streaming. Non-200 → error with status and URL. On SHA mismatch → delete partial and return `ErrChecksumMismatch` (wrapped, includes expected/actual; hint: "the download was corrupted or tampered with; retry, and report if it persists"). On success → rename into place.
- Progress: if `Progress != nil`, print `Downloading <file> (<MB> MB)…` then `done` (or a simple percentage every 10%). Keep it minimal.
- The HTTP client default timeout must allow large downloads (use context + a generous transport timeout, not a 30s `Client.Timeout`).

### 3. Safe extraction (`internal/runner/extract.go`)
```go
func Extract(archivePath, dest string) error
```
- `dest` must not exist. Extract into a sibling temp dir `dest + ".tmp-<random>"` and `os.Rename` to `dest` on success. Remove the temp dir on any failure.
- **Use `os.Root`** (`os.OpenRoot` on the temp dir) for every create/mkdir/symlink/link so writes cannot escape the root, even through symlinked parents. Also validate explicitly:
  - reject absolute names, names containing `..` path elements, and empty names;
  - symlinks: reject absolute targets and targets that resolve outside the root (the official tarball contains relative symlinks inside `externals/node*/bin`, which must work);
  - hardlinks: the target must be a previously extracted path inside the root;
  - reject char/block devices, FIFOs; ignore PAX/global headers properly;
  - permissions: keep `0o777` bits from the header, strip setuid/setgid/sticky, ensure owner `rw` (files) / `rwx` (dirs);
  - limits: total uncompressed size ≤ 2 GiB, entries ≤ 100,000. Exceeding either → error.
- Errors name the offending entry.

### 4. Dev override (D8)
- `internal/runner/override_dev.go` (`//go:build dev`): if `OWNCI_DEV_RUNNER_URL` is set, `AssetFor` returns `Asset{URL: that, SHA256: OWNCI_DEV_RUNNER_SHA256, Version: OWNCI_DEV_RUNNER_VERSION or "0.0.0-dev"}`. If the SHA env is missing → error (no unverified downloads, even in dev).
- `internal/runner/override_release.go` (`//go:build !dev`): no-op.
- Put the stderr warning `DEV BUILD: overrides active (OWNCI_DEV_…)` in `internal/cli` (printed once per invocation when `buildinfo.DevBuild` is true and any `OWNCI_DEV_*` var is set). t05 reuses it.
- Note: the version must be part of `Asset` (add a `Version` field), so the dev path can report what it installed.

### 5. Install orchestration (`internal/runner/install.go`)
```go
type InstallOptions struct { Paths config.Paths; Name string; Asset Asset; Exec execx.Runner; HTTP *http.Client; Force bool; Progress io.Writer; Logger *slog.Logger }
type InstallResult struct { Dir, ArchivePath, ExpectedVersion, InstalledVersion string; VersionErr error }
func Install(ctx context.Context, o InstallOptions) (InstallResult, error)
```
- If `RunnerDir(name)` exists:
  - contains `.runner` (registered) → **refuse even with Force**: `UserError{"runner '<name>' is registered", Hint: "run 'ownci uninstall --unregister' first (t09) or choose another --name"}`;
  - otherwise → refuse unless `Force` (then remove the dir and continue).
- `Fetch` → `Extract` → `InstalledVersion(ctx, exec, dir)`, which runs `<dir>/bin/Runner.Listener --version` and trims the output. If that command fails (commonly missing ICU), **do not fail the install**: set `VersionErr` with the hint `sudo <dir>/bin/installdependencies.sh`.

### 6. `ownci setup --download-only`
- Flags added to `setup`: `--download-only`, `--name`, `--force`. Without `--download-only`, setup still returns not-implemented (t06).
- Output:
  ```
  Runner 2.xxx.x (linux-x64) verified and installed at <dir>
  ```
  If `VersionErr` is set, print a warning + hint instead of the version line.

### 7. Fake runner fixture (`internal/testutil`)
Scripts live as real files in `internal/testutil/fakerunner/` (embedded via `embed.FS`) so they are reviewable and shellcheck-able. `FakeRunnerTarball(FakeRunnerOptions{Version string})` builds a `.tar.gz` (file modes 0755 for scripts) and returns bytes + hex SHA256. Also add `WriteFakeRunner(t, dir)` for tests that want an already-extracted tree.

Fake runner contents and **behavior contract** (later tasks depend on this exact behavior; implement it fully now):
- `bin/Runner.Listener`: `--version` prints the version, exit 0. Anything else → exit 1.
- `bin/installdependencies.sh`: prints `fake: dependencies ok`, exit 0.
- `config.sh`:
  - refuses root unless `RUNNER_ALLOW_RUNASROOT=1` (message like the real one: `Must not run with sudo`), exit 1;
  - `config.sh --version` → prints the version;
  - **configure** (`--unattended --url U --token T --name N [--labels L] [--no-default-labels] [--work W] [--replace]`): missing url/token/name → exit 1 with a message. If `.runner` exists and there is no `--replace` → `Cannot configure the runner because it is already configured.` exit 1. If env `OWNCI_DEV_GITHUB_API_URL` is set, `curl -fsS -X POST $API/_fake/register` with JSON `{token,name,labels,url}`; non-2xx → print `Http response code: Unauthorized` and exit 1. Write `.runner` **with a UTF-8 BOM** (`{"agentId":<n>,"agentName":N,"poolId":1,"serverUrl":"https://fake","gitHubUrl":U,"workFolder":W}`), `.credentials` (0600, dummy), and `.fake-config-args` (all args with the token value replaced by `***`). Print `√ Runner successfully added` and `√ Settings Saved.`, exit 0;
  - **remove** (`remove --token T`): no `.runner` → exit 1 `Runner is not configured`. If the API env is set, `curl -X DELETE $API/_fake/register/<name>?token=T`, non-2xx → exit 1. Delete `.runner`, `.credentials`, print `√ Runner removed successfully`, exit 0.
- `run.sh` (bash, must behave like the real runner for t07/t08):
  - no `.runner` → print `Runner is not configured.`, exit 1;
  - load `<dir>/.env` like the Listener does (split on first `=`, export);
  - trap `INT TERM` → print `Exiting...`, exit 0 (use `sleep 0.2 & wait $!` loops so traps fire promptly);
  - print `√ Connected to GitHub`, `Current runner version: '<ver>'`, then `<UTC ts>: Listening for Jobs` (ts format `2006-01-02 15:04:05Z`);
  - if `FAKE_RUNNER_EXIT_CODE=N` (env or .env) → after printing the connect lines, exit N (simulates crash=1, deprecated=7);
  - also append the same lines to `_diag/Runner_<ts>.log`;
  - **simulated jobs:** every 0.2 s, check for `<dir>/.fake-job-request`. Its content is the job name, optionally followed by ` nocontainer`. Consume (delete) the file, print `<ts>: Running job: <name>`, then:
    - if `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true` and `nocontainer` → write `Jobs without a job container are forbidden on this runner, please add a 'container:' to your job or contact your self-hosted runner administrator.` to `_diag/fake-job-<n>.log`, result `Failed`;
    - otherwise create marker files `_work/_tool/fake-marker`, `_work/_actions/fake-marker`, `_work/fakerepo/fakerepo/fake-marker`, `_work/_temp/fake-marker`; then, if `ACTIONS_RUNNER_HOOK_JOB_COMPLETED` points to an existing file, run it with `bash` (env `RUNNER_ROOT=<dir>`, `GITHUB_WORKSPACE=<dir>/_work/fakerepo/fakerepo`), appending output to `_diag/fake-job-<n>.log`. Hook exit ≠ 0 → result `Failed`, else `Succeeded`;
    - print `<ts>: Job <name> completed with result: <Result>`.
- Must pass `shellcheck` if it is installed. Don't make shellcheck a hard dependency.

## Out of scope
GitHub API/registration (t05/t06). Supervisor (t07). `.env` writing (t08). The fake scripts' API-calling paths are exercised in t05/t06. Here they only need to be skipped cleanly when the env var is unset.

## Files
`internal/runner/{versions.go,download.go,extract.go,install.go,override_dev.go,override_release.go,*_test.go}`, `internal/testutil/{fakerunner.go,fakerunner_test.go}`, `internal/testutil/fakerunner/{config.sh,run.sh,bin/Runner.Listener,bin/installdependencies.sh}`, `internal/cli/{setup.go,devwarn.go}`.

## Tests (required)
- download (httptest server): fresh download + verify; cache hit (server counts requests → 0 on the second call); corrupted cache → re-download; server returns wrong bytes → `ErrChecksumMismatch` and no file left behind; 404 → error with status.
- extract (tarballs built in-test with `archive/tar`): the happy path keeps modes and in-root relative symlinks; each rejection case: absolute path, `../x`, symlink to `/etc`, symlink `a -> ..` then `a/x`, hardlink outside, FIFO, size limit (use a small limit via an unexported var), dest already exists. After a failure, neither `dest` nor any `.tmp-*` exists.
- install: uses the fake runner tarball + httptest + `execx.Fake`; registered dir refused even with Force; unregistered dir refused without Force and replaced with Force; version mismatch is reported; `--version` failure → `VersionErr` set, install succeeds.
- testutil: the tarball contains the expected paths with mode 0755; a bash-driven test extracts the fake runner and runs `config.sh` (configure → `.runner` starts with BOM, `.fake-config-args` has `***`, second configure without `--replace` fails), `run.sh` (start, observe "Listening for Jobs" within 3s, send SIGINT → exits 0 with "Exiting..."), a simulated job without a hook → `Succeeded`, and `FAKE_RUNNER_EXIT_CODE=7` → exit 7.
- dev override: test file with `//go:build dev`: env set → `AssetFor` returns the override; URL without SHA → error. Run it via `go test -tags dev ./internal/runner/` (add to `make test`).

## Manual verification (needs internet to download the public runner; no GitHub account needed)
```bash
make check
rm -rf /tmp/ownci-t04
./bin/ownci --data-dir /tmp/ownci-t04 setup --download-only --name t04
#   expect: "Runner <pinned> (linux-x64) verified and installed at /tmp/ownci-t04/runners/t04"
/tmp/ownci-t04/runners/t04/bin/Runner.Listener --version          # expect: <pinned version>
ls -l /tmp/ownci-t04/cache/                                         # expect: actions-runner-linux-x64-<v>.tar.gz
./bin/ownci --data-dir /tmp/ownci-t04 setup --download-only --name t04; echo "exit=$?"   # expect: refuses (exists), hint mentions --force
# tamper with the cache, force reinstall → re-download (see ownci.log "checksum mismatch… re-downloading")
printf 'x' >> /tmp/ownci-t04/cache/actions-runner-linux-x64-*.tar.gz
./bin/ownci --data-dir /tmp/ownci-t04 setup --download-only --name t04 --force && grep -i mismatch /tmp/ownci-t04/logs/ownci.log
# dev override with a WRONG checksum must fail and extract nothing:
make build-dev
OWNCI_DEV_RUNNER_URL=$(grep -o 'https://github.com/actions/runner/releases/download/[^"]*linux-x64[^"]*' internal/runner/versions.go) \
OWNCI_DEV_RUNNER_SHA256=0000000000000000000000000000000000000000000000000000000000000000 \
  ./bin/ownci-dev --data-dir /tmp/ownci-t04 setup --download-only --name t04dev; echo "exit=$?"
#   expect: "DEV BUILD: overrides active", checksum mismatch error, exit=1, no runners/t04dev dir
```

## Definition of done
- [ ] Global DoD.
- [ ] The archive is never extracted before its checksum is verified.
- [ ] The fake runner implements the full behavior contract above (later tasks rely on it).
- [ ] Implementation notes: pinned version/date, exported API signatures, the fake runner controls (`.fake-job-request`, `FAKE_RUNNER_EXIT_CODE`).

## Implementation notes

**Pinned runner**: `2.338.0`, pinned **2026-10-15** (release published 2026-10-06). SHA256 from the release notes: `linux-x64 af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32`, `linux-arm64 628b4a7258487b80c1d3c221095a7ded349f5ae0175fd3dfab708d07428f041b`. The x64 value was confirmed by a real download (see verification).

**Deviations**
- `runner.Error{Msg, Hint, Err}` (with `UserHint()`, `Unwrap()`) replaces `UserError` inside `internal/runner`, which cannot import `cli`. `cli.renderError` now prints `Hint:` for any error implementing `UserHint() string` (a `hinted` interface; `docker.Error` got `UserHint()` too). Exit code 1.
- `Install` downloads **before** it removes an existing dir (with `--force`), so a failed download never destroys a working install. A registered dir (`.runner` present) is refused even with `--force`.
- Added `runner.ValidateName` (`^[a-z0-9][a-z0-9-]{0,63}$`) and `runner.DefaultName(hostname)`. The name becomes a directory/file name, so this doubles as a path-traversal guard; `Install` validates it.
- `Fetch` refuses an `Asset` with an empty SHA256. Progress prints `Downloading <file> (<MB> MB)… done` (or `failed`).
- `Extract`: implicit parent dirs are created `0755`; entries whose name already exists are removed first (duplicates and "symlink then file" never write through a link); `TypeRegA` is accepted; PAX global headers are skipped; the `./` root entry is skipped.
- A symlink is rejected if its target resolves **lexically** outside the root; writes are additionally confined by `os.Root`.
- Release builds contain no override code (`override_release.go` is a no-op; a test asserts the env vars are ignored). `make test` now also runs `go test -tags dev ./internal/buildinfo/ ./internal/runner/`.
- The dev-warning code is in `internal/cli/devwarn.go` (`devWarning(devBuild, environ)` is pure and unit-tested; `printDevWarning(w)` is called from the root pre-run). t05 reuses it.
- `shellcheck` is not installed on this machine, so the fake scripts were only checked with `bash -n`.
- `FAKE_RUNNER_EXIT_CODE` exits right after the `Connected`/`Current runner version` lines, **before** `Listening for Jobs`.

**API for later tasks**
- `runner.Asset{Arch, URL, SHA256, Version}`, `runner.Release`, `runner.Pinned`, `runner.AssetFor(arch) (Asset, error)`.
- `runner.Fetch(ctx, FetchOptions{Asset, CacheDir, HTTP, Progress, Logger}) (archivePath string, err error)`, `runner.ErrChecksumMismatch`.
- `runner.Extract(archivePath, dest string) error`.
- `runner.Install(ctx, InstallOptions{Paths, Name, Asset, Exec, HTTP, Force, Progress, Logger}) (InstallResult{Dir, ArchivePath, ExpectedVersion, InstalledVersion, VersionErr}, error)`, `runner.InstalledVersion(ctx, exec, dir) (string, error)`.
- `runner.ValidateName(name) error`, `runner.DefaultName(hostname) string`.
- cli test seams: `assetFor`, `newHTTPClient` (alongside `newExecRunner`, `geteuid`); `setup_test.go` has `useFakeRunnerServer`.
- `testutil.FakeRunnerTarball(FakeRunnerOptions{Version}) (data []byte, sha256hex string)` (deterministic, entries prefixed `./`), `testutil.WriteFakeRunner(t, dir, opts...)`. Default version `2.999.0-fake`.
- **Fake runner controls**: `.fake-job-request` in the runner dir containing `<job> [nocontainer]` simulates one job (`Running job` → markers in `_work/{_tool,_actions,fakerepo/fakerepo,_temp}` → runs `ACTIONS_RUNNER_HOOK_JOB_COMPLETED` with `RUNNER_ROOT` and `GITHUB_WORKSPACE` → `Job <n> completed with result: Succeeded|Failed`; job output in `_diag/fake-job-<n>.log`). `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true` + `nocontainer` → `Failed` with the real error text. `FAKE_RUNNER_EXIT_CODE=N` (env or `.env`) → exit N. `.env` is loaded like the Listener (split on first `=`, CRLF tolerated). `config.sh` records its args (token masked) in `.fake-config-args`; with `OWNCI_DEV_GITHUB_API_URL` set it POSTs `{token,name,labels[],url}` to `<api>/_fake/register` and DELETEs `<api>/_fake/register/<name>?token=T` on remove (exercised in t05/t06).

**Known limitations**
- The registration/remove API paths in the fake scripts are not exercised until t05/t06.
- The `0755` mode of extracted implicit dirs is subject to the process umask.

**Verification run**: `make check` passes. With the real runner: `./bin/ownci --data-dir /tmp/ownci-t04 setup --download-only --name t04` → `Runner 2.338.0 (linux-x64) verified and installed at /tmp/ownci-t04/runners/t04`; `Runner.Listener --version` → `2.338.0`; cache holds the 217 MB archive (`0600`); a second run → `already exists` + `--force` hint (exit 1); after appending a byte to the cached archive, `--force` re-downloads and `ownci.log` has `checksum mismatch in cached runner archive, re-downloading`. `bin/ownci-dev` with the real URL and an all-zero SHA → `DEV BUILD: overrides active`, checksum-mismatch error with hint, nothing extracted into `runners/t04dev` (the stale cache file with the same name was deleted); URL without SHA → refused.
