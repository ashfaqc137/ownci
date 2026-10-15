# OwnCI — Progress

> Status and cross-task pointers only. Details belong in each task's `## Implementation notes`.
> The **LLM** updates "Implemented", "Open items" and "Decision changes". The **user** owns "Verified" and "Commit".

## Status

| Task | Title | Implemented (LLM) | Verified (user) | Commit | Notes |
|------|-------|-------------------|-----------------|--------|-------|
| t01 | Scaffolding, CLI skeleton, build tooling | ✅ | – | – | |
| t02 | Platform, data dir & config, logging | ✅ | – | – | |
| t03 | Exec seam, Docker checks, `doctor` | ✅ | – | – | |
| t04 | Runner download, verify, extract, fake runner | – | – | – | |
| t05 | GitHub client, `tools/fakegh` | – | – | – | |
| t06 | `setup` | – | – | – | |
| t07 | `start` / `stop` / `status` | – | – | – | |
| t08 | Isolation enforcement + cleanup hook | – | – | – | |
| t09 | `uninstall` / `logs` | – | – | – | |
| t10 | Real E2E, docs, release | – | – | – | |

Legend: – not started · 🚧 in progress · ✅ done · ⚠️ done with caveats (see Notes)

## Open items (cross-task)

<!-- Anything a later task must pick up. Format: - [ ] (tNN → tMM) description. Tick when resolved and name the resolving task. -->
- [ ] (t03 → t06) Enable Docker Desktop WSL integration for this distro before t06 (setup/start require Docker)
- [ ] (t06 → t07) `setup --force` must refuse while the runner is running (lock check; TODO left in t06)
- [ ] (t08 → t10) Document the remaining isolation gaps (network access, sibling containers via `services:`/`docker://`, shared Docker daemon, root in the job container) in the README
- [ ] (t08 → t10) Verify on real GitHub: the job-completed hook timing relative to the job-container stop
- [ ] (t08 → t10) Verify on real GitHub: wiping `_work` (except `_temp`, `_PipelineMapping`) doesn't break the runner
- [ ] (t10) Decide on a license (README placeholder until then)

## Decision changes

<!-- Any change to a decision (D#) in 00-context.md or to a task spec made during review. Also edit the source doc. -->
| Date | Change | Affects |
|------|--------|---------|

## Environment notes

<!-- Machine facts that matter to later tasks. -->
- Dev machine: WSL2 (kernel 6.18, `microsoft-standard-WSL2`), Go 1.27.2, `gh` installed.
- Docker: Docker Desktop on Windows; WSL integration **off** as of 2026-10-14.
- Pinned runner version: _(set in t04)_
- Cleanup image digest: _(set in t08)_
