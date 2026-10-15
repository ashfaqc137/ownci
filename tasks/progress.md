# OwnCI — Progress

> Status and cross-task pointers only. Details belong in each task's `## Implementation notes`.
> The **LLM** updates "Implemented", "Open items" and "Decision changes". The **user** owns "Verified" and "Commit".

## Status

| Task | Title | Implemented (LLM) | Verified (user) | Commit | Notes |
|------|-------|-------------------|-----------------|--------|-------|
| t01 | Scaffolding, CLI skeleton, build tooling | ✅ | – | – | |
| t02 | Platform, data dir & config, logging | ✅ | – | – | |
| t03 | Exec seam, Docker checks, `doctor` | ✅ | – | – | |
| t04 | Runner download, verify, extract, fake runner | ✅ | – | – | |
| t05 | GitHub client, `tools/fakegh` | ✅ | – | – | |
| t06 | `setup` | ✅ | – | – | |
| t07 | `start` / `stop` / `status` | ✅ | – | – | |
| t08 | Isolation enforcement + cleanup hook | ✅ | – | – | |
| t09 | `uninstall` / `logs` | ✅ | – | – | |
| t10 | Real E2E, docs, release | ⚠️ | – | – | Offline parts done (assets, docs, README, `make release`). **Real-GitHub runbook not executed**; `docs/acceptance.md` is all unchecked. |

Legend: – not started · 🚧 in progress · ✅ done · ⚠️ done with caveats (see Notes)

Post-release ideas are collected in [`future.md`](future.md).

## Open items (cross-task)

<!-- Anything a later task must pick up. Format: - [ ] (tNN → tMM) description. Tick when resolved and name the resolving task. -->
- [x] (t03 → t06) Enable Docker Desktop WSL integration for this distro before t06 (setup/start require Docker) — resolved: the real Docker check passed during t06 verification (2026-10-15)
- [x] (t06 → t07) `setup --force` must refuse while the runner is running — resolved in t07 (`detectExisting` checks `supervisor.IsRunning`)
- [x] (t06 → t08) Implement `installIsolation` in `internal/setup/setup.go` — resolved in t08 (`isolation.Install`)
- [x] (t07 → t08) Fill `beforeLaunch` — resolved in t08 (Repair → Verify → RunCleanup; status Isolation line added). Original item: fill `beforeLaunch` in `internal/cli/supervise.go` (re-assert `.env`, run cleanup) and add an isolation section to `statusReport`/`renderStatus` in `internal/cli/status.go`
- [x] (t07 → t09) Resolved in t09. Use `supervisor.IsRunning` / `supervisor.Stop` (`ErrNotRunning`) in uninstall; `logs` can tail `logs/runner-<name>.log` and `logs/supervisor-<name>.log`
- [ ] (t08 → t10) Document the remaining isolation gaps in the README: the six-item list in `tasks/t08-isolation-cleanup.md` Implementation notes (network, sibling containers, shared daemon, root in the job container, host-run runner/hook, hook timing/resource limits)
- [x] (t08 → t09) Resolved in t09 (`internal/uninstall/purge.go`). `--purge` must delete root-owned files in `_work` with a cleanup container (`hooks.CleanupImage`, same flags as the hook but removing everything); the hook script keeps `_temp`/`_PipelineMapping`, so it is not enough on its own
- [ ] (t08 → t10) Verify on real GitHub: the job-completed hook timing relative to the job-container stop
- [ ] (t08 → t10) Verify on real GitHub: wiping `_work` (except `_temp`, `_PipelineMapping`) doesn't break the runner
- [ ] (t09 → future) `setupApp` creates the layout dirs even in a foreign non-empty `--data-dir` (also for `uninstall`); consider not creating them for read-only/uninstall commands (see t09 notes)
- [ ] (t10 → user) Execute `docs/e2e.md` against a private sandbox repo (needs `OWNCI_E2E_REPO`, an admin token, Docker) and record the evidence in `docs/acceptance.md`; fill the Findings table
- [ ] (t10) Decide on a license (README placeholder until then)

## Decision changes

<!-- Any change to a decision (D#) in 00-context.md or to a task spec made during review. Also edit the source doc. -->
| Date | Change | Affects |
|------|--------|---------|

## Environment notes

<!-- Machine facts that matter to later tasks. -->
- Dev machine: WSL2 (kernel 6.18, `microsoft-standard-WSL2`), Go 1.27.2, `gh` installed.
- Docker: Docker Desktop on Windows; WSL integration **on** as of 2026-10-15 (the real `docker` preflight passed).
- Pinned runner version: 2.338.0 (pinned 2026-10-15)
- Cleanup image: `busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e` (set in t08, `internal/hooks/image.go`)
