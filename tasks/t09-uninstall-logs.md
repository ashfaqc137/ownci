# t09 — `ownci uninstall` and `ownci logs`

> Read `tasks/00-context.md` first (D4, D11, §4 data dir + marker, §7 "config.sh" remove), then the Implementation notes of t01–t08.

## Goal
Let the user cleanly stop contributing compute permanently: stop the runner, optionally unregister it from GitHub, optionally delete **only** OwnCI-managed files, and say exactly what remains. Add `ownci logs` for diagnosis.

## Depends on / current state
- t02 Paths + marker, t05 GitHub client (`RemoveToken`, `FindRunner`, `DeleteRunner`, `WebBaseURL`), t06 config + `ReadRunnerFile`, t07 `supervisor.IsRunning` / `supervisor.Stop`, t08 the cleanup image + container pattern (for root-owned files).

## Scope

### `ownci uninstall`
Flags: `--unregister`, `--purge`, `--local-only` (mutually exclusive with `--unregister`; explicitly skip GitHub), `--yes` (skip confirmation), `--name` (defaults to the configured name; allows cleaning up a dir whose config.json is missing or broken).

Flow:
1. Resolve the target: from config.json, or `--name` + an existing `runners/<name>/`. Nothing found → `Nothing to uninstall in <data-dir>`, exit 0 — except `--purge` on a marked data dir with no runner dirs at all (installed, never set up): it still purges the shared files, the per-runner files of runners whose directory is gone (unless that runner holds its lock), and the data dir. A missing data dir → `Nothing to uninstall: <data-dir> does not exist`.
2. **Confirmation** (unless `--yes` or a non-TTY stdin, which requires `--yes` → otherwise a usage error): print the plan (stop / unregister / purge) and ask `Proceed? [y/N]`.
3. **Stop:** `supervisor.Stop` if running (timeout 45 s, then force). Report it.
4. **Unregister** (`--unregister`):
   - Resolve the token (t05). If there is none → fail with a hint (or suggest `--local-only` plus the manual removal URL).
   - `RemoveToken` → `./config.sh remove --token ***` (via execx, Dir = runnerDir). Success → `✓ Runner unregistered from <repo>`.
   - If config.sh remove fails (e.g. local `.runner` is corrupt or already removed): fall back to `FindRunner(name)` → `DeleteRunner(id)`. Runner already absent on GitHub → `✓ Runner was not registered on GitHub (nothing to remove)`.
   - Unregister failure without `--purge` → stop here with an error (exit 1), keeping all files so the user can retry.
5. **Purge** (`--purge`):
   - **Safety:** require the marker `<data-dir>/.ownci-data-dir`. If it is missing → refuse ("not an OwnCI data dir"). Refuse if the data dir resolves to `/`, `$HOME`, or a path with fewer than 2 components.
   - Delete **only** known layout paths: `runners/<name>`, `run/<name>.*`, `logs/runner-<name>.log`, `logs/supervisor-<name>.log`, then `config/config.json`. If no other runners remain, also delete `cache/`, `logs/`, `run/`, `runners/`, `config/` and the marker, and finally the data dir itself **only if it is now empty**. Never `RemoveAll(dataDir)`.
   - Root-owned files under `runners/<name>/_work`: first try a normal remove. On `EACCES`, run the t08 cleanup container to empty `_work` (`docker run --rm --pull never --network none -v <work>:/w <image> sh -c 'rm -rf /w/* /w/.[!.]*'`), then retry. If Docker is unavailable → report the leftover path and the `sudo rm -rf <path>` hint.
   - Without `--unregister` and with a still-registered runner (`.runner` present, not `--local-only`): warn loudly that the registration will remain on GitHub (offline). Print the settings URL `<WebBaseURL>/<owner>/<repo>/settings/actions/runners` and require confirmation.
6. **Summary** (always printed):
   ```
   Uninstall summary
     Runner process   stopped
     GitHub           unregistered | still registered (remove at <url>) | skipped (--local-only)
     Local files      removed | kept at <data-dir> (use --purge to remove)
     Not touched      Docker images (busybox cleanup image, job images) — remove with 'docker image prune' if desired
   ```
7. Plain `ownci uninstall` (no flags) = stop only + summary with guidance. Nothing is deleted or unregistered.

### `ownci logs`
- `ownci logs [--source runner|ownci|supervisor|diag] [-n 100] [-f]`, default source `runner`.
  - `runner` → `logs/runner-<name>.log`; `ownci` → `logs/ownci.log`; `supervisor` → `logs/supervisor-<name>.log`; `diag` → the newest `runners/<name>/_diag/Runner_*.log`.
  - `-n N` → last N lines (efficient tail: read backwards in chunks, don't load huge files fully).
  - `-f` → follow (poll every 500 ms, handle truncation/rotation by reopening). Exit on Ctrl-C.
  - Output passes through the redactor (defense in depth).
  - A missing file → a friendly message naming the path and how it gets created.

## Out of scope
Multi-runner management, log rotation (note it as a future item), deleting Docker images.

## Files
`internal/uninstall/{uninstall.go,purge.go,*_test.go}`, `internal/cli/{uninstall.go,logs.go}`, `internal/logtail/{tail.go,tail_test.go}`.

## Tests (required)
- uninstall with fakegh + fake runner + fake docker:
  - no flags: running supervisor (from the t07 harness) stopped, files kept, GitHub still lists the runner;
  - `--unregister`: fakegh no longer lists the runner; the remove token was used through the fake `config.sh remove`; files kept;
  - `--unregister` with a corrupted `.runner` → falls back to the API delete;
  - `--unregister --purge --yes`: the data dir is gone; **a sibling file next to the data dir and an unrelated file created inside the data dir parent survive**; a file the user put **inside** the data dir root (unknown name) → the data dir is not removed (not empty) and the summary lists the leftover;
  - purge without the marker → refused, nothing deleted;
  - purge with a root-owned file emulated by a read-only dir (chmod 0500) → the fake docker cleanup path is invoked;
  - non-TTY without `--yes` → usage error, nothing done;
  - `--local-only --purge` → no GitHub calls (fakegh request count is 0);
  - no token for `--unregister` → error suggesting `--local-only`.
- logs/tail: last N of small and large (>1 MiB) files, a file without a trailing newline, follow picks up appended lines and survives truncation, redaction is applied.

## Manual verification (dev build + fakegh)
```bash
make check && make build-dev
rm -rf /tmp/ownci-t09 /tmp/fakegh-t09.json; mkdir -p /tmp/ownci-t09-parent && touch /tmp/ownci-t09-parent/keep-me
go run ./tools/fakegh --state /tmp/fakegh-t09.json & sleep 2; eval "$(go run ./tools/fakegh --print-env)"
O="./bin/ownci-dev --data-dir /tmp/ownci-t09-parent/data"; N=t09
$O setup --repo me/demo --name $N && $O start --detach
$O logs -n 5                                 # last runner lines incl. "Listening for Jobs"
$O logs --source ownci -n 5                  # ownci diagnostics, no tokens
$O uninstall --yes                           # stops; summary says still registered + files kept
curl -s $OWNCI_DEV_GITHUB_API_URL/_fake/state | grep $N    # still registered
$O uninstall --unregister --purge --yes      # unregistered + removed
curl -s $OWNCI_DEV_GITHUB_API_URL/_fake/state | grep -c $N # 0
ls -A /tmp/ownci-t09-parent                  # only keep-me remains
mkdir -p /tmp/not-ownci/runners/x && touch /tmp/not-ownci/important
./bin/ownci-dev --data-dir /tmp/not-ownci uninstall --purge --yes --name x; echo "exit=$?"
#   expect: refuses ("not an OwnCI data dir": no marker, because t02 never adopts a non-empty foreign dir), exit=1
ls -A /tmp/not-ownci                          # important + runners still there
kill %1
```

## Definition of done
- [ ] Global DoD.
- [ ] Purge can never delete outside the data dir or a data dir without the marker (tested).
- [ ] The summary always states the GitHub registration state truthfully.
- [ ] Implementation notes: final flag semantics, and the leftover/manual-cleanup cases.

## Implementation notes

**Final flag semantics** (`ownci uninstall`)
- No flags: stop only (if running), keep every file, print the summary. `--unregister`: also remove the registration (`config.sh remove` first, REST fallback). `--purge`: also delete OwnCI's files. `--local-only`: never contact GitHub and say so in the summary; it contradicts `--unregister` (usage error, exit 2). `--yes/-y` skips the prompt; **without `--yes` a non-terminal stdin is a usage error (exit 2)** — the terminal check asks the tty driver (`TCGETS`), because a character-device check would also accept `/dev/null`. `--name` selects the runner when `config.json` is missing/unreadable or names another one; with a broken config and exactly one `runners/<dir>`, that one is used (with a warning).
- Order: resolve → **purge safety check (before anything irreversible)** → plan + confirmation → stop (45 s, then `Force`) → unregister → purge → summary. `--purge` without `--unregister` and `--local-only` on a registered runner prints a loud WARNING with the settings URL (`<WebBaseURL>/<owner>/<repo>/settings/actions/runners`) on stderr; `--yes` still skips the prompt but not the warning.
- Unregister failure: without `--purge` → exit 1 with all files kept; **with `--purge` the purge continues** (literal reading of the spec), the summary says `still registered (unregistering failed; remove at <url>)` and the command still exits 1. "Runner already absent on GitHub" → `✓ Runner was not registered on GitHub (nothing to remove)`.
- Summary GitHub line is one of: `unregistered`, `not registered (nothing to remove)`, `still registered (remove at <url>)`, `skipped (--local-only)`, `not registered locally (…)` (no `.runner` file and nothing was attempted).

**Purge rules** (`internal/uninstall/purge.go`)
- Requires the marker as a regular file; refuses a data dir that resolves to `/`, has fewer than 2 path components, equals `$HOME`, or has a symlinked layout directory (`config/ logs/ cache/ run/ runners/`). Never `RemoveAll(dataDir)`.
- Deletes: `runners/<name>` (RemoveAll, symlinks inside are unlinked, never followed), `run/<name>.*`, `logs/runner-<name>.log`, `logs/supervisor-<name>.log`, `config/config.json` (unless it names a different runner). If no other `runners/*` directory remains: `cache/` (RemoveAll), `logs/ownci.log`, then `logs/ run/ runners/ config/` **only if empty** (non-recursive `Remove`), then the marker and the data dir **only if nothing else is in the root**. Unknown files (in the root or inside layout dirs) are kept and listed as `left: <path>`; the data dir and its marker then stay.
- Root-owned files: `RemoveAll` first; on a permission error, `docker run --rm --pull never --network none --label ownci.managed=true -v <runner>/_work:/w <hooks.CleanupImage> sh -c 'rm -rf /w/* /w/.[!.]*'` and retry. If Docker is missing or the container fails, the runner dir stays, the rest is purged, and the summary prints `sudo rm -rf <runner dir>`.
- Leftover/manual-cleanup cases: root-owned `_work` files without Docker; foreign files inside the data dir or its layout dirs; Docker images (never touched; the summary says so); a GitHub registration that could not be removed.

**APIs**: `uninstall.Run(ctx, Deps, Options) error` (`Deps{Paths, Exec, HTTP, Logger, In, Out, Err, Getenv, Home, IsTerminal, APIBaseURL, WebBaseURL, StopTimeout}`; `*uninstall.Error{Msg, Hint, Err, Usage}` mapped by the cli like setup's); `runner.Remove(ctx, ex, dir, token)` wraps `config.sh remove --token` (token registered with the redactor, output tail redacted); `logtail.Tail(path, n) (lines, size, err)`, `logtail.Print`, `logtail.Follow(ctx, w, path, offset, interval)`.

**`ownci logs`**: `--source runner|ownci|supervisor|diag` (default `runner`), `-n/--lines` (default 100, negative → exit 2), `-f/--follow`. `tail` reads backwards in 64 KiB chunks. Follow polls every 500 ms, restarts from the top after truncation (size < offset) or replacement (inode change), waits for a removed file to reappear, prints complete lines only, and redacts every line. Limitation (as with `tail -f`): a file truncated and refilled past the old offset between two polls is not noticed. A missing file → `Error: there is no <source> log yet: <path> does not exist` + a hint saying how it gets created (exit 1). `diag` picks the newest `_diag/Runner_*.log` by mtime.

**Deviations / notes**
- `setupApp` (t02) still creates the layout directories (`config/ logs/ cache/ run/ runners/`, no marker) inside a non-empty foreign `--data-dir` before any command runs, so `uninstall --purge` on such a directory is refused but leaves those empty directories behind. Pre-existing t02 behavior, not changed here (candidate for `future.md`: don't create the layout for `uninstall`/read-only commands).
- Not implemented (out of scope as specified): log rotation (a future item), removing Docker images, multi-runner management.
- The uninstall tests emulate "root-owned" files with a `chmod 0500` directory and a `docker run` emulation that restores permissions before running the `rm` script; real root-owned deletion was not exercised by hand (it uses the same busybox/`-v` pattern verified in t08).

**Verification run (2026-10-15)**: `make check` green (new: `internal/logtail` tests incl. >1 MiB files, no trailing newline, follow/truncate/rotate/redaction; `internal/uninstall` tests for every case in the spec plus prompt, `--name`, bad names, symlinks, other runners remaining, no-docker leftovers; `cli` tests for stopping a running detached runner, non-TTY refusal, flag conflicts, `--local-only --purge`, foreign dir refusal, `logs` sources and errors). Manual flow with `bin/ownci-dev` + `tools/fakegh` exactly as in the spec: `logs -n 5` showed `Listening for Jobs`; `logs --source ownci` showed diagnostics without tokens; `uninstall --yes` stopped the runner and reported "still registered" + files kept (fakegh still listed it); `uninstall --unregister --purge --yes` → fakegh count 0, data dir gone, only `keep-me` left in the parent; `uninstall --purge --yes --name x` on `/tmp/not-ownci` → "not an OwnCI data directory", exit 1, `important` and `runners/x` intact.
