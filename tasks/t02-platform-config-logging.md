# t02 — Platform detection, data directory & config, logging with redaction

> Read `tasks/00-context.md` first (especially §4 Data directory and §5 Conventions), then the Implementation notes of t01.

## Goal
Give every later command a resolved, correctly-permissioned data directory, a typed config file, platform facts (OS, arch, runner arch, WSL), and a logger that can never leak tokens.

## Depends on / current state
- t01: `internal/cli` root with persistent `--data-dir` and `--verbose` flags, `UserError`, `runCLI` test helper, Makefile.

## What later tasks build on
- `platform.Detect()`. t03 uses it in doctor, t04 for runner arch selection.
- `config.ResolveDataDir`, `config.Paths`, `config.Load/Save`, `config.ErrNotConfigured`. Used by all commands.
- `logging.Setup` / `logging.Redact` / `logging.RegisterSecret`. t05/t06 register tokens as they obtain them.
- A shared per-invocation `cli.App` struct (built in the root's `PersistentPreRunE`) that holds `Paths`, `Logger`, `Platform`, stdout/stderr writers. Later commands take what they need from it instead of re-resolving.

## Scope

### `internal/platform`
```go
type Info struct {
    OS, Arch     string // runtime.GOOS / GOARCH
    RunnerArch   string // "linux-x64" | "linux-arm64" | "" when unsupported
    IsWSL        bool
    WSLDistro    string // from $WSL_DISTRO_NAME, may be empty
    KernelRelease string
}
func Detect() Info                          // uses real /proc + env
func DetectWith(fs fs.FS / readFile func, getenv func(string) string, goos, goarch string) Info // test seam
func (i Info) Supported() error             // UserError-friendly error for non-linux or unsupported arch (D9)
func IsWindowsMount(path string) bool       // true for /mnt/<letter>/... (WSL drvfs)
```
WSL = `/proc/sys/kernel/osrelease` contains `microsoft` (case-insensitive) **or** `WSL_DISTRO_NAME` is set.

### `internal/config`
- `ResolveDataDir(flagVal string, getenv func(string) string, home string) (string, error)`: precedence flag → `OWNCI_DATA_DIR` → `$XDG_DATA_HOME/ownci` → `<home>/.local/share/ownci`. Returns an absolute, cleaned path. Refuses `/` and the home dir itself (a guard for the later `--purge`).
- `type Paths struct { Root, Marker, ConfigDir, ConfigFile, LogsDir, OwnciLog, CacheDir, RunDir, RunnersDir string }`, plus methods `RunnerDir(name)`, `RunnerLog(name)`, `LockFile(name)`, `PidFile(name)`, `StateFile(name)`.
- `NewPaths(root string) Paths` (pure), and `(Paths) Ensure() error`: creates all dirs with `0700` and writes the `.ownci-data-dir` marker (content: `ownci data directory — managed by ownci; safe to delete with 'ownci uninstall --purge'\n`). If the dir already exists with looser permissions, tighten them to `0700` and log at debug. Idempotent.
  - **Marker adoption rule (safety for t09 `--purge`):** write the marker only when the root dir is newly created, is empty, or already has the marker. If the root exists, is non-empty and has no marker, use it anyway but **don't** write the marker. Log a warning and expose `Paths.Foreign() bool`, so that `--purge` later refuses to delete a directory OwnCI didn't create.
- Config types exactly as in 00-context §4 (`Config{SchemaVersion int; Runner *RunnerConfig}`), with JSON tags.
- `Load(p Paths) (*Config, error)` returns `ErrNotConfigured` when the file is missing or `Runner == nil`. It returns a descriptive error on unknown `schemaVersion` or malformed JSON (include the path and a hint to re-run `ownci setup --force`).
- `Save(p Paths, c *Config) error`: atomic (temp file in the same dir + `fsync` + rename), mode `0600`, `schemaVersion` forced to 1.

### `internal/logging`
- `Setup(opts Options) (*slog.Logger, io.Closer, error)` with `Options{LogFile string; Verbose bool; Stderr io.Writer}`. Text handler to the file at Debug level, always on (append, `0600`). Stderr handler only when Verbose (Debug level). Without Verbose, nothing is logged to stderr: the CLI layer prints user-facing messages itself.
- **Redaction:** a `slog.Handler` wrapper that redacts every string attribute value and the message. `Redact(s string) string` replaces:
  - GitHub token patterns: `gh[pousr]_[A-Za-z0-9]{20,}`, `github_pat_[A-Za-z0-9_]{20,}`.
  - Every value registered via `RegisterSecret(s string)` (process-global, concurrency-safe; ignore strings shorter than 8 chars).
  - `--token <x>` / `--token=<x>` in command lines.
  - Replacement text: `***`.
- Also export `RedactWriter(w io.Writer) io.Writer` for later use when piping subprocess output into logs. It must handle secrets split across writes reasonably: line-buffered redaction is acceptable. Document this.

### `internal/cli` wiring
- `PersistentPreRunE` on root builds `App{Paths, Logger, Platform, Out, Err}`: resolve data dir, `Ensure()`, set up logging to `Paths.OwnciLog`, detect platform, then log a debug line `ownci start args=… version=… datadir=…` (redacted).
- `version` must **not** require a writable data dir. Mark it with an annotation (e.g. `cmd.Annotations["skipApp"]="true"`) so the pre-run skips data-dir setup. Exception: if `--verbose` is passed together with `--data-dir`, run setup anyway. That keeps the manual check below possible without inventing a new command, and is documented in help.
- Close the log file in `PersistentPostRunE` (and on error paths in `Execute`).

## Out of scope
Doctor checks (t03), any command behavior, GitHub/runner logic.

## Files
`internal/platform/{platform.go,platform_test.go}`, `internal/config/{datadir.go,paths.go,config.go,*_test.go}`, `internal/logging/{logging.go,redact.go,*_test.go}`, `internal/cli/{app.go,root.go}` (modified).

## Tests (required)
- platform: table tests via `DetectWith` covering WSL osrelease (`…microsoft-standard-WSL2`), plain Linux, env-only WSL, amd64→linux-x64, arm64→linux-arm64, 386/darwin unsupported. `IsWindowsMount`: `/mnt/c/x` true, `/mnt/wsl/x` false, `/home/x` false.
- config: precedence table for `ResolveDataDir`, refusal of `/` and home; `Ensure` creates dirs with mode 0700 and the marker file, and tightens an existing 0755 dir; an existing non-empty dir without the marker → no marker written and `Foreign() == true`; an empty existing dir → marker written; Save/Load round trip; saved file mode is 0600; missing file → `ErrNotConfigured`; bad JSON → error mentioning the path; unknown schemaVersion → error.
- logging: `Redact` table (ghp_, gho_, ghs_, github_pat_, registered secret, `--token abc…`, `--token=abc…`, short registered secret ignored, unrelated text untouched); a logger writing to a `bytes.Buffer` file target with a token in both the message and an attr → output contains no token; `RedactWriter` across two writes in one line.
- cli: `runCLI(t, "--data-dir", tmp, "--verbose", "version")` creates the layout; `runCLI(t, "--data-dir", "/", "status")` fails with a clear error.

## Manual verification
```bash
make check
rm -rf /tmp/ownci-t02
./bin/ownci --data-dir /tmp/ownci-t02 --verbose version
find /tmp/ownci-t02 -printf '%M %p\n' | sort -k2
#   expect: drwx------ for every dir; -rw------- for .ownci-data-dir and logs/ownci.log
cat /tmp/ownci-t02/logs/ownci.log            # expect: a debug line with args and datadir
rm -rf /tmp/ownci-t02b
OWNCI_DATA_DIR=/tmp/ownci-t02b ./bin/ownci status; ls -a /tmp/ownci-t02b
#   expect: status still says "not implemented" (exit 2), but the env var was honored: the layout + marker exist in /tmp/ownci-t02b
go test ./internal/logging -run Redact -v    # expect: PASS, cases listed
```

## Definition of done
- [ ] Global DoD.
- [ ] No code path writes a token pattern to `logs/ownci.log` (redaction tests prove this).
- [ ] Implementation notes list the final exported API of `platform`, `config`, `logging`, and the `cli.App` fields.
