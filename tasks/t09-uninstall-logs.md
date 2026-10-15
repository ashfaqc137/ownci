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
1. Resolve the target: from config.json, or `--name` + an existing `runners/<name>/`. Nothing found → `Nothing to uninstall in <data-dir>`, exit 0.
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
