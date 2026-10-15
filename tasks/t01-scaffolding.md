# t01 — Project scaffolding, CLI skeleton, build tooling

> Read `tasks/00-context.md` first. It defines the decisions, layout and conventions referenced here.

## Goal
Create the Go module, the `ownci` CLI skeleton with every MVP command present as a stub, version reporting, and the build/test tooling (`Makefile`) that every later task's definition of done relies on.

## Depends on / current state
- Nothing. The repo contains only `plan.md` and `tasks/`.
- Tooling on the machine: Go 1.27.x, git, bash, make (install `make` via `sudo apt-get install -y make` if `make --version` fails, and note it).

## What later tasks build on
- `internal/cli` root command and its persistent flags (`--data-dir`, `--verbose`). t02 wires them into config/logging.
- `cli.UserError` and the exit-code mapping (00-context §5).
- Stub commands, replaced one by one: `doctor` (t03), `setup` (t04/t06), `start`/`stop`/`status` (t07), `logs`/`uninstall` (t09).
- `make build` / `make build-dev` / `make check`.
- The `dev` build tag mechanism (D8). t04/t05 add the actual override code.

## Scope
1. `git init` (if not already a repo) and a `.gitignore` covering `bin/`, `dist/`, `*.test`, `coverage.out`, `.ownci-dev/`.
2. `go mod init github.com/ashfaqc137/ownci`; add `github.com/spf13/cobra`.
3. `cmd/ownci/main.go`: calls `os.Exit(cli.Execute())`. No other logic.
4. `internal/buildinfo`: `var Version = "dev"`, `Commit = "none"`, `Date = "unknown"`, set through `-ldflags -X`. Also `const DevBuild bool`, defined in two files: `devbuild_dev.go` (`//go:build dev`, `true`) and `devbuild_release.go` (`//go:build !dev`, `false`).
5. `internal/cli`:
   - `root.go`: `NewRootCmd() *cobra.Command` and `Execute() int`. Root `Use: "ownci"`, `Short: "Your PR. Your Compute. — run GitHub Actions jobs on your own machine"`. Persistent flags: `--data-dir string` (help: "OwnCI data directory (default: $OWNCI_DATA_DIR or $XDG_DATA_HOME/ownci)") and `--verbose/-v bool`. `SilenceUsage: true`, `SilenceErrors: true`. Errors are rendered by `Execute`.
   - `errors.go`: `type UserError struct { Msg, Hint string; Code int }` implementing `error`, plus `ErrNotImplemented` (a `UserError` with Code 2 and Hint "This command is not implemented yet."). `Execute` renders any error as `Error: <msg>` (and `Hint: <hint>` when set) on stderr, and returns `UserError.Code`, `2` for cobra usage errors (unknown flag/command), or `1` otherwise.
   - `version.go`: `ownci version` prints
     ```
     ownci <Version> (commit <Commit>, built <Date>)
     go: <runtime.Version()> <GOOS>/<GOARCH>
     ```
     plus a third line `build: dev` when `buildinfo.DevBuild` is true. `--json` prints the same fields as a JSON object (`version`, `commit`, `date`, `go`, `os`, `arch`, `devBuild`). The runner version is added in t07; leave a `// t07: runner version` comment.
   - Stub commands, each in its own file, each `RunE` returning `ErrNotImplemented`: `doctor`, `setup`, `start`, `stop`, `status`, `logs`, `uninstall`. Give each a correct `Short` description that matches the PRD (e.g. `setup`: "Download, verify and register the official GitHub Actions runner").
6. `Makefile` targets:
   - `build` → `go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ownci ./cmd/ownci`
   - `build-dev` → same with `-tags dev`, output `bin/ownci-dev`
   - `test` → `go test -race ./...`
   - `vet` → `go vet ./...` and `go vet -tags dev ./...`
   - `fmt` → `gofmt -w .`; `fmtcheck` → fails if `gofmt -l .` outputs anything
   - `check` → `fmtcheck vet test build build-dev`
   - `clean` → remove `bin/ dist/`
   - `LDFLAGS` sets `buildinfo.Version` from `git describe --tags --always --dirty 2>/dev/null || echo dev`, `Commit` from `git rev-parse --short HEAD 2>/dev/null || echo none`, and `Date` as UTC RFC3339.

## Out of scope
Config, data dir creation, logging (t02). Any real command behavior. CI workflows for the OwnCI repo itself. Release packaging (t10).

## Files
`go.mod`, `go.sum`, `.gitignore`, `Makefile`, `cmd/ownci/main.go`, `internal/buildinfo/{buildinfo.go,devbuild_dev.go,devbuild_release.go}`, `internal/cli/{root.go,errors.go,version.go,doctor.go,setup.go,start.go,stop.go,status.go,logs.go,uninstall.go}`, `internal/cli/*_test.go`.

## Tests (required)
- `internal/cli/root_test.go`: run the root command in-process with captured stdout/stderr (helper `runCLI(t, args...) (stdout, stderr string, code int)` built on `NewRootCmd()` + the same error rendering as `Execute`; export the rendering as an internal function so tests reuse it):
  - `version` → exit 0, stdout matches `^ownci \S+ \(commit \S+, built \S+\)`.
  - `version --json` → valid JSON with all keys.
  - Each stub command → exit 2, stderr contains `not implemented`.
  - Unknown command `frobnicate` → exit 2.
  - `--help` lists all 8 commands.
- `internal/buildinfo`: a test that `DevBuild == false` in normal builds (file `devbuild_test.go` with `//go:build !dev`), and a matching `//go:build dev` test asserting `true`. `make vet` covers both tags. Also run `go test -tags dev ./internal/buildinfo/` inside `make test` (add it to the target).

## Manual verification
```bash
cd ~/work/ownci
make check                       # expect: all steps pass, exit 0
./bin/ownci --help               # expect: lists doctor, logs, setup, start, status, stop, uninstall, version
./bin/ownci version              # expect: "ownci <git-describe> (commit <sha|none>, built 2026-…Z)" + go line, NO "build: dev"
./bin/ownci-dev version          # expect: same plus "build: dev"
./bin/ownci version --json | python3 -m json.tool   # expect: valid JSON
./bin/ownci setup; echo "exit=$?"   # expect: "Error: … not implemented …", "Hint: …", exit=2
./bin/ownci nope;  echo "exit=$?"   # expect: error about unknown command, exit=2
```

## Definition of done
- [ ] Global DoD (00-context §6).
- [ ] Every command in 00-context §3 exists, with accurate help text.
- [ ] `bin/ownci` and `bin/ownci-dev` both build. Only the dev one reports `build: dev`.
- [ ] Implementation notes appended (record the `runCLI` test helper signature for later tasks).

## Implementation notes

**Deviations**
- Repo was already `git init`'d (default branch `master`, no commits); left as is. 00-context mentions `main` as the main branch, so rename before the first push if desired.
- `make` was missing; the user installed it (OpenSUSE WSL, GNU Make 4.4.1).
- Cobra's auto `completion` command is disabled (`CompletionOptions.DisableDefaultCmd`) so help lists only the 8 spec commands.
- Stub commands use `cobra.ArbitraryArgs` so extra args still yield "not implemented" rather than a usage error.

**APIs for later tasks**
- `cli.NewRootCmd() *cobra.Command`, `cli.Execute() int`.
- `cli.UserError{Msg, Hint string; Code int}`; `cli.ErrNotImplemented` (Code 2). Code 0 in a `UserError` maps to exit 1.
- `renderError(w io.Writer, err error) int` (internal): prints `Error:`/`Hint:` and returns the exit code. Cobra usage errors (unknown command/flag, arg-count) → 2 via string match; flag errors are also wrapped as `UserError{Code: 2}` by `SetFlagErrorFunc`.
- Exit code constants in `errors.go`: `exitOK`, `exitFailure`, `exitUsage`, `exitDoctorBad` (3, for t03).
- Test helper: `runCLI(t *testing.T, args ...string) (stdout, stderr string, code int)` in `internal/cli/root_test.go`.
- Persistent flag values live in an unexported `rootFlags` struct (`dataDir`, `verbose`) created in `NewRootCmd`; t02 should wire them from there.
- `buildinfo.Version/Commit/Date` (ldflags), `buildinfo.DevBuild` (const, `-tags dev`).

**Verification run**: `make check` (all steps pass); `./bin/ownci --help`, `version`, `version --json`, `setup` (exit 2 + hint), `nope` (exit 2); `./bin/ownci-dev version` shows `build: dev`, the release binary does not. Version shows `dev`/`none` because the repo has no commits or tags yet.
