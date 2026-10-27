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
| t10 | Real E2E, docs, release | ✅ | – | – | Real-GitHub runbook executed 2026-10-22 with the v0.1.0 release binary (`ashfaqc137/blog` PR #6, host + container-runner side by side); all 24 items in `docs/acceptance.md` ticked with evidence. Tested platform: WSL2 + Docker Desktop, amd64. |
| t11 | Runner-in-container mode (default job image) | ✅ | – | – | `setup --mode container-runner` (D13). Real-GitHub e2e passed 2026-10-22 (`ashfaqc137/blog` PR #4; custom Ubuntu 22.04 image and `start --image`: PR #5). Runner self-update inside the container not exercised (see open items). |

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
- [x] (t09 → user) Resolved 2026-10-27: install-then-uninstall left everything behind: with no runner set up, `uninstall --purge` printed "Nothing to uninstall" and kept the data dir, `status`/`logs`/`stop` recreated a missing data dir, and nothing said how to remove the binary. Now `--purge` without a runner removes the data dir (orphaned per-runner files included), those commands are `noLayout` and never create a missing data dir, and after a purge the summary prints `rm <ownci binary>` (ownci never deletes itself). Tests in `internal/uninstall` and `internal/cli/uninstall_test.go`
- [x] (t10 → user) Resolved 2026-10-22: `docs/e2e.md` steps 1–12 plus container-runner C1/C2 executed with the v0.1.0 release binary on `ashfaqc137/blog` (PR #6), step 4 against public `ashfaqc137/ownci`; evidence and findings in `docs/acceptance.md`. Original item: execute `docs/e2e.md` against a private sandbox repo and record the evidence in `docs/acceptance.md`
- [x] (t10 → future) Resolved (2026-10-15): before every launch the supervisor (which holds the runner lock) kills processes still running from the runner directory and logs a notice (`internal/supervisor/orphans.go`, test `TestRunKillsLeftoverRunnerProcesses`). Not yet re-verified against real GitHub. Original item: After `kill -9` of the supervisor the `Runner.Listener` survives and holds the GitHub session; `ownci start` then spawns a second listener that loops on "A session for this runner already exists … Conflict" and `start` only says "still starting". Recovery worked after killing the orphan by hand. Consider having `start` detect and stop an orphaned listener of its own runner directory (found 2026-10-15, e2e step 10)
- [x] (t10) Resolved (2026-10-15): MIT, `LICENSE` added and linked from the README. Original item: decide on a license (README placeholder until then)
- [ ] (t10 → future) The runner mounts the Docker socket into every job container (verified 2026-10-22), so a job can control the host Docker daemon. README and task docs are corrected. Decide whether to harden (custom container hook that drops the mount, see `future.md` F2) or keep it as a documented limitation
- [ ] (t11 → user/future) Runner self-update inside the runner container is untested: 2.338.0 is the latest release, so GitHub offered no update during the e2e. Re-check when the next runner release ships (`ownci status` shows the new version and jobs keep running) — or force it by setting up with an older runner through a dev build (`OWNCI_DEV_RUNNER_URL`/`_SHA256`)
- [x] (t11) Resolved 2026-10-22: `setup --force` on a registered runner failed with the real `config.sh` (it refuses an existing `.runner` even with `--replace`); the old registration is now set aside and restored on failure. Also `--force` no longer drops extra labels. Both found by the t11 e2e; details in t11 Implementation notes
- [ ] (t11 → future) `run.sh --once` is deprecated by the runner; replace it before a runner release removes it (`future.md` F3)
- [ ] (t11 → future) Container-runner mode lets no-container jobs read/modify the runner dir (incl. `.credentials*`); hardening ideas in `future.md` F4
- [ ] (t11 → post-release) Container-runner mode (and host mode) verified on Docker Desktop + WSL2, amd64 only; v0.1.0 ships native Linux and arm64 as "untested" (README). Re-run `docs/e2e.md` on native Linux Docker Engine and on arm64 to claim them

## Decision changes

<!-- Any change to a decision (D#) in 00-context.md or to a task spec made during review. Also edit the source doc. -->
| Date | Change | Affects |
|------|--------|---------|
| 2026-10-22 | **D13 added** (runner-in-container mode, opt-in). The five t11 "Decisions to confirm" taken as proposed, except #5 (user): the runner runs as the **host uid** through a root entrypoint that adds a passwd entry, sudo and the socket group (the `0700` data dir rules out the image's uid 1001). Added beyond the spec: a **fresh container per job** (`run.sh --once` + supervisor recycle), `.path` rewrite, graceful `docker stop` before removing a container. D1 unchanged and still the default. | t11, 00-context D13/§7, README |

## Environment notes

<!-- Machine facts that matter to later tasks. -->
- Dev machine: WSL2 (kernel 6.18, `microsoft-standard-WSL2`), Go 1.27.2, `gh` installed.
- Docker: Docker Desktop on Windows; WSL integration **on** as of 2026-10-15 (the real `docker` preflight passed).
- Pinned runner version: 2.338.0 (pinned 2026-10-15)
- Cleanup image: `busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e` (set in t08, `internal/hooks/image.go`)
- Runner image (container-runner mode): `ghcr.io/actions/actions-runner:2.338.0@sha256:4ffadc0002b2581327e06101fc8c06cd189232baf79fe561fac9caeb76f5e807` (set in t11, `internal/runnercontainer`); Docker Desktop 29.5.3 at the time, socket gid 1001 inside containers
