# t07 — Process supervision: `ownci start`, `ownci stop`, `ownci status`

> Read `tasks/00-context.md` first (D6, §4 data dir `run/` + `logs/`, §7 "run.sh / process behavior" and "Runner stdout markers"), then the Implementation notes of t01–t06 (especially the fake runner controls from t04).

## Goal
Run the official runner under an OwnCI supervisor, in the foreground or detached, with reliable stop and **truthful** status. "Running" is derived from a held lock plus the runner's own output, never from files merely existing (PRD §7 `status`, §11).

## Depends on / current state
- t02 Paths (`LockFile`, `PidFile`, `StateFile`, `RunnerLog`), t03 `docker.Check`, t04 fake runner (`run.sh` behavior, `.fake-job-request`, `FAKE_RUNNER_EXIT_CODE`), t05 GitHub client (for `--remote`), t06 config + `ReadRunnerFile`, plus the setup TODO "runner must not be running".

## What later tasks build on
- `supervisor.IsRunning(paths, name) (bool, PidInfo, error)`. t06 (fix the TODO now), t08 and t09 use it.
- `supervisor.Stop(ctx, paths, name, StopOptions) error`. t09 uninstall uses it.
- `state.json` schema and `status` output. t08 adds an isolation section.
- A pre-launch hook point `beforeLaunch(...)` in `start`. t08 re-asserts `.env` and runs cleanup there.

## Scope

### Supervisor (`internal/supervisor`, `//go:build linux`)
- **Single instance:** acquire an exclusive non-blocking `flock` on `run/<name>.lock` for the supervisor's lifetime. If it is already held → `ErrAlreadyRunning`. The lock is the source of truth for "running". The pidfile is informational: `run/<name>.pid` JSON `{pid, mode: "foreground"|"detached", startedAt, ownciVersion}`, removed on clean exit.
- **Launch:** `exec.Cmd` for `<runnerDir>/run.sh`, `Dir = runnerDir`, env = `os.Environ()` + `RUNNER_MANUALLY_TRAP_SIG=1` + `ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1`, `SysProcAttr{Setpgid: true}`. Stdout and stderr are merged through a pipe.
- **Output handling:** a line scanner (allow long lines, 1 MiB) → (a) appended to `logs/runner-<name>.log` (0600) through `logging.RedactWriter`; (b) in foreground mode, echoed to the terminal; (c) fed to the **state machine**.
- **State machine** (`internal/supervisor/state.go`), persisted atomically to `run/<name>.state.json` on every transition:
  ```go
  type State struct {
    Name string; Phase string // starting|connected|listening|busy|reconnecting|stopping|stopped|crashed|registration_lost|version_deprecated|duplicate_session
    Since time.Time; RunnerVersion string; RunnerPID int
    CurrentJob string; LastJob *JobInfo /*{Name, Result, FinishedAt}*/
    LastError string; ExitCode *int; Restarts int; SupervisorPID int
  }
  ```
  Transitions come from the stdout markers in 00-context §7 (substring matches; table-driven parser `ParseLine(line) Event`).
- **Exit handling / restart policy:**
  - exit after a requested stop → `stopped`;
  - exit code `7` → `version_deprecated`, no restart (hint: "the runner could not self-update; run `ownci setup --force` after upgrading ownci");
  - a registration-lost marker was seen → `registration_lost`, no restart (hint: `ownci setup --force`);
  - `duplicate_session` marker → no restart (hint: another runner process with this registration is running elsewhere);
  - otherwise (crash or unexpected exit) → restart with backoff `5s, 15s, 45s` (injectable for tests), at most 3 restarts within 10 minutes, then `crashed` with `LastError` = the last 20 output lines (redacted).
- **Signals:** on the first SIGINT/SIGTERM to the supervisor → phase `stopping`, send `SIGINT` to the runner's **process group** (`syscall.Kill(-pgid, SIGINT)`), wait up to the grace period (default 30 s). A second signal or grace expiry → `SIGKILL` to the process group. Always reap the child, release the lock, and remove the pidfile.
- **Hidden command** `ownci __supervise --name <n>` (inherits `--data-dir`) runs the supervisor in detached mode. Used only by `start --detach`.

### `ownci start`
- Preconditions (each a `UserError` with hint): config present (`ErrNotConfigured` → "run `ownci setup --repo owner/name`"), runner dir with a `.runner` file, `docker.Check` passes (container jobs need Docker), not already running ("already running (pid N, since T) — see `ownci status`", exit 1).
- `beforeLaunch(ctx, app, cfg)`: a no-op hook for t08 (leave a comment).
- **Foreground (default):** run the supervisor in-process and print runner output live. Ctrl-C → graceful stop. Exit 0 after a requested stop, 1 on a terminal failure phase.
- **`--detach`:** re-exec `os.Executable()` as `__supervise` with `Setsid: true`, stdin `/dev/null`, stdout/stderr → `logs/supervisor-<name>.log`. The parent then polls `state.json` (and lock liveness) for up to `--wait` (default 60 s):
  - `listening`/`busy` → `✓ Runner '<name>' is listening for jobs on <repo> (pid N). Stop with: ownci stop`, exit 0;
  - terminal failure phase or supervisor died → print the phase, hint and last 20 runner-log lines, exit 1;
  - timeout → `still starting — check 'ownci status'`, exit 0.
- Print the WSL note once when `Platform.IsWSL`: "Note: the runner stops if WSL shuts down or the machine sleeps."

### `ownci stop`
- Not running → `Runner '<name>' is not running`, exit 0.
- Otherwise send SIGTERM to the supervisor pid (from the pidfile; verify via `/proc/<pid>/cmdline` that it is an ownci process. If not, treat the pidfile as stale) and wait for the lock to be released, up to `--timeout` (default 45 s).
- `--force`: after the timeout, SIGKILL the supervisor and the runner process group (`RunnerPID` from state; `kill(-pgid)`), then clean up the pidfile and set state `stopped`.
- Output: `✓ Runner '<name>' stopped (registration kept; start again with 'ownci start')`.

### `ownci status`
Human output (and `--json` with the same data):
```
OwnCI        v0.x (commit …)
Runner       ownci-host  →  owner/name   labels: ownci
Installed    yes  (runner 2.xxx.x at /…/runners/ownci-host)
Configured   yes  (.runner agentName matches)
Process      running  (supervisor pid 1234, detached, since 10:02:11, restarts 0)
State        listening  (since 10:02:15)           | busy: running job "build"
Last job     build → Succeeded at 10:05:40
GitHub       online, idle                          (only with --remote)
Docker       ok (27.x, linux)
Last error   —
```
Rules:
- Not configured → print what exists (data dir, whether a runner dir exists) + hint, exit 0.
- `Process running` **only** if the lock is held. If the lock is free but `state.json` claims an active phase → report `stopped (unexpectedly; last state: listening at T)`.
- `Installed` version: prefer `RunnerVersion` from state (the actual running version after self-update), else `bin/Runner.Listener --version` (via execx, 5 s timeout).
- `--remote`: resolve a token (t05). Without one → `GitHub  skipped (no token)`. Otherwise `FindRunner` → online/offline + busy. Flag inconsistencies, e.g. local `listening` but remote `offline` → warn "GitHub hasn't seen the runner yet or connectivity issue".
- Exit 0 unless status itself fails internally.
- `ownci version` additionally prints `runner: <version>` when configured (remove the t01 TODO).

## Out of scope
`.env` / isolation (t08), uninstall and the `logs` command (t09), systemd, auto-start on boot.

## Files
`internal/supervisor/{lock.go,pidfile.go,state.go,parse.go,supervisor.go,stop.go,*_test.go}`, `internal/cli/{start.go,stop.go,status.go,supervise.go,version.go}`, `internal/setup/setup.go` (resolve the t06 TODO).

## Tests (required)
- `ParseLine` table over every marker in 00-context §7 (including timestamps and the `√` prefix).
- State machine transitions, including job start → completed → idle with `LastJob`.
- Supervisor integration tests with the **fake runner** (real bash processes; use short backoffs/grace via options):
  - start → `listening` within 5 s; the state file is correct; the runner log contains the lines;
  - simulated job (`echo build > <dir>/.fake-job-request`) → `busy` then `listening` with `LastJob.Result == "Succeeded"`;
  - stop → `stopped`, the lock is released, the pidfile is gone, and **no process remains in the runner's process group** (check `/proc/*/stat` pgid or `syscall.Kill(-pgid, 0)` → ESRCH);
  - `FAKE_RUNNER_EXIT_CODE=1` (via env) → restarts up to the cap → `crashed` with `LastError` populated;
  - `FAKE_RUNNER_EXIT_CODE=7` → `version_deprecated`, 0 restarts;
  - second supervisor on the same name → `ErrAlreadyRunning`;
  - stale pidfile pointing to a dead pid with a free lock → `IsRunning == false`, and start works.
- Detached mode: use the test-binary re-exec pattern (`TestMain` checks an env var like `OWNCI_TEST_SUPERVISE=1` and runs the supervise entrypoint) so `--detach` can be tested without building `ownci`.
- status: table over (config? lock? state phase?) → expected lines, including the "stopped unexpectedly" case; `--json` schema; `--remote` with fakegh online/offline.

## Manual verification (dev build + fakegh; Docker WSL integration must be ON for the docker precondition)
```bash
make check && make build-dev
rm -rf /tmp/ownci-t07 /tmp/fakegh-t07.json
go run ./tools/fakegh --state /tmp/fakegh-t07.json & sleep 2; eval "$(go run ./tools/fakegh --print-env)"
O="./bin/ownci-dev --data-dir /tmp/ownci-t07"; N=t07
$O setup --repo me/demo --name $N
$O status                                   # Process: not running; State: — ; no "running" claims
$O start --detach                           # ✓ listening …
$O status                                   # Process running (detached), State listening, runner 9.9.9-fake
$O status --remote                          # GitHub online, idle
$O start; echo "exit=$?"                    # already running, exit=1
echo build > /tmp/ownci-t07/runners/$N/.fake-job-request; sleep 1
$O status                                   # Last job build → Succeeded
$O stop                                     # ✓ stopped (registration kept)
$O status                                   # not running
pgrep -af 'runners/t07' || echo "no runner processes"   # expect: no runner processes
# crash path:
echo FAKE_RUNNER_EXIT_CODE=1 >> /tmp/ownci-t07/runners/$N/.env
$O start --detach --wait 120; $O status     # after retries: crashed + last error lines (takes ~65s with default backoff)
sed -i '/FAKE_RUNNER_EXIT_CODE/d' /tmp/ownci-t07/runners/$N/.env
# foreground: start, then Ctrl-C after "Listening for Jobs" → graceful exit 0
$O start
kill %1
```

## Definition of done
- [ ] Global DoD.
- [ ] Status never says running/listening without a held lock **and** a matching runner marker.
- [ ] Stop leaves no orphan processes (tested).
- [ ] The t06 "must not be running" TODO is resolved.
- [ ] Implementation notes: the state schema, `IsRunning`/`Stop` signatures, the `beforeLaunch` hook location.
