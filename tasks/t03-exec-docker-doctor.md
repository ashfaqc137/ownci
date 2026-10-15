# t03 — Command execution seam, Docker checks, `ownci doctor`

> Read `tasks/00-context.md` first (§5 Conventions, §7 "Docker Desktop + WSL" and "config.sh"), then the Implementation notes of t01–t02.

## Goal
Implement `ownci doctor`: a fast, read-only health check that tells the user exactly what is missing to run OwnCI and how to fix it. Build the `execx` seam that makes every external command fakeable in tests.

## Depends on / current state
- t01: CLI skeleton, `UserError`, exit codes, `runCLI` test helper.
- t02: `cli.App{Paths, Logger, Platform, Out, Err}`, `platform.Info`, `platform.IsWindowsMount`, `logging.Redact`.

## What later tasks build on
- `execx.Runner` and `execx.Fake`. Used by t04 (`Runner.Listener --version`), t06 (`config.sh`), t08 (docker cleanup), t09 (`config.sh remove`).
- `docker.Check(ctx, runner) Status` and `docker.Client`. t06 gates setup on it, t07 gates start, t08 adds `RunCleanup`.
- `doctor.Run(ctx, checks) Report`. t06/t07 reuse a subset of checks as preflight. t08 adds isolation checks.

## Scope

### `internal/execx`
```go
type Cmd struct { Name string; Args []string; Dir string; Env []string /* appended to os.Environ() */; Stdin io.Reader }
type Result struct { Stdout, Stderr []byte; ExitCode int }
type Runner interface {
    Run(ctx context.Context, c Cmd) (Result, error) // error only for "could not start" / ctx cancel; non-zero exit → ExitCode, nil error
    LookPath(name string) (string, error)
}
func NewOS(logger *slog.Logger) Runner  // logs the redacted command line + exit code at debug
type Fake struct { /* scripted responses keyed by name+args prefix; records calls */ }
```
`Fake` lets tests register `On("docker", "version", ...)` → `Result`/error, plus a `LookPath` table, and asserts `Calls()`. Keep it simple and deterministic.

### `internal/docker`
```go
type Status struct {
    CLIPath        string
    CLIFound       bool
    DesktopShim    bool   // the WSL-integration-off shim was detected
    DaemonReachable bool
    ServerVersion  string
    ServerOS       string // must be "linux"
    Err            error  // first failure, with hint (UserError-compatible)
}
func Check(ctx context.Context, r execx.Runner) Status
```
- Run `docker version --format '{{json .}}'` with a 10s timeout.
- Detect the shim: combined output contains `could not be found in this WSL 2 distro` → `DesktopShim=true`, hint *"Enable WSL integration: Docker Desktop → Settings → Resources → WSL integration → enable '<distro>' → Apply & restart."*
- Daemon unreachable (`Cannot connect to the Docker daemon` / `permission denied` on the socket) → distinct hints: start Docker / `sudo usermod -aG docker $USER` then re-login.
- Parse JSON `Server.Version`, `Server.Os`. Server OS ≠ `linux` → fail ("Docker is in Windows-containers mode; switch to Linux containers").

### `internal/doctor`
```go
type Level int // Pass, Warn, Fail
type Result struct { Name string; Level Level; Detail, Hint string }
type Check struct { Name string; Run func(ctx context.Context) Result }
type Report struct { Results []Result }
func (r Report) Failed() bool
func Run(ctx context.Context, checks []Check) Report // sequential, each check panic-safe
```
Checks implemented in this task (constructors take their dependencies explicitly):
| Name | Pass | Warn | Fail |
|---|---|---|---|
| `platform` | linux + supported arch (shows RunnerArch) | — | `Info.Supported()` error |
| `wsl` | not WSL, or WSL with distro name shown | — | — (informational: Pass with detail "WSL2 (<distro>)") |
| `user` | euid ≠ 0 | — | running as root: "the runner refuses root; run as a normal user" |
| `data-dir` | writable, not a Windows mount | on `/mnt/<x>`: "slow & permission issues; use a Linux path" | not writable |
| `runner-deps` | ICU found: `ldconfig -p` output contains `libicuuc` (via execx) | `ldconfig` unavailable → can't verify | ICU missing → hint `sudo apt-get install -y libicu-dev` or run `bin/installdependencies.sh` after download |
| `docker` | `docker.Check` ok, shows version | — | any docker failure with its hint |
| `git` | `git` on PATH | missing: "git not found on host (not required for container jobs; useful for troubleshooting)" | — |

Note: inside a container job, `actions/checkout` uses git from the **job container** (or falls back to the REST API download if the image has no git), not the host. `git` on the host is therefore only a Warn.

### `ownci doctor` command
- Output, one line per check, aligned: `[PASS] platform     linux/amd64 → runner linux-x64`. Hints are printed indented on the next line for WARN/FAIL. Then a summary: `3 passed, 1 warning, 1 failed`.
- `--json` outputs the report as JSON.
- Exit code `3` if any FAIL, otherwise 0. No color unless stdout is a TTY. Keep color optional and simple, or omit it entirely.
- Doctor is **read-only** (apart from the data-dir `Ensure` that t02's pre-run already does).

## Out of scope
GitHub token checks (t05/t06), runner-installed checks (t07 status), isolation checks (t08).

## Files
`internal/execx/{execx.go,fake.go,*_test.go}`, `internal/docker/{check.go,check_test.go}`, `internal/doctor/{doctor.go,checks.go,*_test.go}`, `internal/cli/doctor.go`.

## Tests (required)
- execx: `NewOS` runs `bash -c 'echo out; echo err >&2; exit 3'` → stdout/stderr/exit 3 with nil error; missing binary → error; ctx timeout → error; debug log line is redacted when args contain `--token ghp_…`.
- docker (using `execx.Fake`): healthy JSON → pass; shim text with exit 1 → `DesktopShim` + WSL hint; "Cannot connect to the Docker daemon" → daemon hint; "permission denied … docker.sock" → group hint; Server.Os windows → fail; `LookPath` miss → CLIFound false; malformed JSON → fail with raw output excerpt (truncated to 300 chars).
- doctor: `Run` order + panic recovery; each check's pass/warn/fail paths with fakes; root detection via an injected `geteuid` func.
- cli: `runCLI(... "doctor")` using fakes injected through an unexported package-level factory (e.g. `var newExecRunner = execx.NewOS` overridable in tests) → exit 3 when docker fails, 0 when all pass; `--json` valid.

## Manual verification
```bash
make check
./bin/ownci doctor; echo "exit=$?"
#   On the author's machine right now (Docker Desktop WSL integration OFF), expect:
#   [FAIL] docker  … could not be found in this WSL 2 distro
#          Hint: Enable WSL integration: Docker Desktop → Settings → Resources → WSL integration …
#   exit=3
# Then enable WSL integration in Docker Desktop, open a new shell:
./bin/ownci doctor; echo "exit=$?"        # expect: [PASS] docker  <version> (linux), exit=0 (warnings allowed)
./bin/ownci doctor --json | python3 -m json.tool
./bin/ownci --data-dir /mnt/c/Users/Public/ownci-test doctor   # expect: [WARN] data-dir … Windows mount
sudo ./bin/ownci doctor                   # expect: [FAIL] user … root
```

## Definition of done
- [ ] Global DoD.
- [ ] Every FAIL/WARN has an actionable hint.
- [ ] No test depends on a real Docker daemon or on network access.
- [ ] Implementation notes document the `execx.Fake` usage pattern for later tasks.
