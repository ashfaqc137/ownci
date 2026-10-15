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
- [x] (t08 → t10) Resolved in t10 (README "Security and trust model" lists all six gaps). Original item: document the remaining isolation gaps in the README: the six-item list in `tasks/t08-isolation-cleanup.md` Implementation notes (network, sibling containers, shared daemon, root in the job container, host-run runner/hook, hook timing/resource limits)
- [x] (t08 → t09) Resolved in t09 (`internal/uninstall/purge.go`). `--purge` must delete root-owned files in `_work` with a cleanup container (`hooks.CleanupImage`, same flags as the hook but removing everything); the hook script keeps `_temp`/`_PipelineMapping`, so it is not enough on its own
- [x] (t08 → t10) Verified on real GitHub (2026-10-15, `ashfaqc137/blog` PR #1, `ownci-poison`): the hook runs after the job container is stopped and removed ("Stop containers" at 16:12:58.0–.3Z, then `ownci: workspace cleaned` at 16:12:58.6Z, in both jobs). Original item: verify on real GitHub the job-completed hook timing relative to the job-container stop
- [x] (t08 → t10) Verified on real GitHub (2026-10-15, `ashfaqc137/blog` PR #1, one `pull_request` job): the hook wiped `_work` (only `_PipelineMapping` and `_temp` remain), the job reported Succeeded and the runner went back to listening. Confirmed with a second job: in `ownci-poison` the `second` job started fine after the wipe and found all three planted markers absent. Original item: verify on real GitHub: wiping `_work` (except `_temp`, `_PipelineMapping`) doesn't break the runner
- [x] (t09 → future) Resolved (2026-10-15): `uninstall` is marked `noLayout`; on a non-empty directory without the OwnCI marker `setupApp` no longer creates the layout or `logs/ownci.log` (`internal/cli/app.go`, test `TestUninstallLeavesAForeignDirectoryUntouched`). Other commands still adopt the directory as before. Original item: `setupApp` creates the layout dirs even in a foreign non-empty `--data-dir` (also for `uninstall`); consider not creating them for read-only/uninstall commands (see t09 notes)
- [ ] (t10 → user) Execute `docs/e2e.md` against a private sandbox repo (needs `OWNCI_E2E_REPO`, an admin token, Docker) and record the evidence in `docs/acceptance.md`; fill the Findings table. Status 2026-10-15: steps 3 and 5-12 were exercised ad hoc on private `ashfaqc137/blog` (PR #1, workflows triggered by `pull_request` rather than `workflow_dispatch`) and passed: setup, start, container job, no-container rejection, poison/wipe, stop/restart keeps runner id 21 and the PR check passes, kill test, no secrets in logs, `uninstall --unregister --purge --yes`. Not run: step 4 (public repo refused, needs a public repo you administer) and step 2/6 via `e2e/run.sh`. Evidence is not yet copied into `docs/acceptance.md`.
- [x] (t10 → future) Resolved (2026-10-15): before every launch the supervisor (which holds the runner lock) kills processes still running from the runner directory and logs a notice (`internal/supervisor/orphans.go`, test `TestRunKillsLeftoverRunnerProcesses`). Not yet re-verified against real GitHub. Original item: After `kill -9` of the supervisor the `Runner.Listener` survives and holds the GitHub session; `ownci start` then spawns a second listener that loops on "A session for this runner already exists … Conflict" and `start` only says "still starting". Recovery worked after killing the orphan by hand. Consider having `start` detect and stop an orphaned listener of its own runner directory (found 2026-10-15, e2e step 10)
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
