# t10 — Real GitHub end-to-end validation, documentation, release artifacts

> Read `tasks/00-context.md` first (all of it: this task validates every decision), then the Implementation notes of t01–t09, especially t08's list of remaining isolation gaps.

## Goal
Prove the MVP success criterion against **real GitHub**: a pull request check in a private sandbox repo runs on this machine inside a job container, a job without `container:` is rejected, and nothing persists between jobs. Then write the user documentation and produce versioned, checksummed release binaries. This is the PRD §12 acceptance checklist.

## Depends on / current state
- Everything from t01–t09. The release build (`make build`) has no dev overrides.
- **Prerequisites the user provides:**
  - a **private** sandbox repo, e.g. `ashfaqc137/ownci-sandbox`, with at least one commit on `main`;
  - a token with admin on it: `gh auth login` (default scopes) or a fine-grained PAT with *Administration: Read and write*, *Contents: Read and write*, *Pull requests: Read and write*, *Actions: Read*;
  - Docker Desktop WSL integration enabled (`ownci doctor` all PASS).

## Scope

### 1. E2E assets (`e2e/`)
- `e2e/workflows/ownci-pass.yml`: on `pull_request` + `workflow_dispatch`, `runs-on: [ownci]`, `container: ubuntu:24.04`. Steps: `actions/checkout@v4`; print `uname -a`, `cat /etc/os-release`, `hostname`, `nproc`, and `echo "runner=$RUNNER_NAME"`; and a check that we're inside a container (`test -f /.dockerenv || grep -qE 'docker|containerd' /proc/1/cgroup`).
- `e2e/workflows/ownci-nocontainer.yml`: `workflow_dispatch` only, `runs-on: [ownci]`, **no** `container:`. Expected: the job **fails** with "Jobs without a job container are forbidden on this runner".
- `e2e/workflows/ownci-poison.yml`: `workflow_dispatch`, two jobs, `second` `needs: first`, both `container: ubuntu:24.04`. `first` writes markers to `$RUNNER_TOOL_CACHE/poison`, `$GITHUB_WORKSPACE/../poison`, and the action cache dir (`$RUNNER_WORKSPACE/../_actions/poison`, resolve the path and print it). `second` asserts all of them are **absent** and fails otherwise.
- `e2e/run.sh`: a helper that, given `OWNCI_E2E_REPO`, uses `gh` to copy the workflows into the sandbox repo on a branch, open a PR, dispatch the dispatch-only workflows, poll with `gh run watch`/`gh run view --json conclusion`, and print a PASS/FAIL table. Keep it readable. Manual steps in `docs/e2e.md` are the source of truth; the script is a convenience.

### 2. Runbook `docs/e2e.md`
Step-by-step, with the expected output at each step and a checkbox list mapped 1:1 to PRD §12. Steps:
1. `make build && ./bin/ownci version` (no `build: dev`).
2. `ownci doctor` → all PASS.
3. `ownci setup --repo <sandbox>` → registered. GitHub UI (Settings → Actions → Runners) shows the runner with **only** the `ownci` label.
4. `ownci setup --repo <some public repo you admin>` → refused (D3). Skip this step if there is no such repo.
5. `ownci start --detach` → listening. `ownci status --remote` → online, idle.
6. PR with `ownci-pass.yml` → the check is green in the PR UI. The job log shows the container OS and the runner name. `docker ps` during the run shows the job container. `ownci status` shows busy, then the last job Succeeded.
7. Dispatch `ownci-nocontainer.yml` → the run fails with the forbidden message (screenshot or log excerpt).
8. Dispatch `ownci-poison.yml` → `second` passes (markers absent). The "Complete runner" / job-completed hook step shows `ownci: workspace cleaned`. `last-cleanup.json` ok.
9. `ownci stop` → `ownci start --detach` → no re-registration, a new PR check passes (configuration persists, PRD §12).
10. Kill test: `kill -9 <supervisor pid>` during an idle period → `ownci status` reports stopped (unexpectedly), not running. `ownci start` recovers.
11. `ownci logs -n 50` and `ownci logs --source ownci`: no tokens (`grep -E 'gh[pousr]_|github_pat_'` finds nothing in `<data-dir>`).
12. `ownci uninstall --unregister --purge --yes` → the runner is gone from the GitHub UI and the data dir is removed.
- Include a **Findings** section template where the executor records anything that differed from expectations, especially the hook timing relative to container stop (D10 risk) and whether the runner self-updated.

### 3. README.md
Sections: what OwnCI is (tagline), how it works (diagram: GitHub ↔ official runner (host) → job container on local Docker), **status: MVP, Linux/WSL2 only**; prerequisites (Linux/WSL2, Docker with the WSL integration instructions, ICU / `installdependencies.sh`, `gh` or a token + exact permissions); install (download the release binary + verify `SHA256SUMS` + `install -m 0755 … ~/.local/bin/ownci`); quick start (setup → start → workflow snippet); **workflow requirements** (`runs-on: [ownci]` + `container:`, with a sample); command reference (one line each + key flags); **security & trust model** (private repos only by default and why; what the container boundary does and does **not** protect against, taken from t08's gap list: network access, sibling containers via `services:`/`docker://`, shared Docker daemon, root inside the container; per-job cleanup; tokens are not stored; runner credentials live in the data dir at 0600/0700); limitations (one job at a time; the WSL VM idle shutdown and machine sleep stop the runner; jobs queue until it reconnects; runner self-update); troubleshooting (map every doctor FAIL/hint and the status phases `registration_lost`, `version_deprecated`, `duplicate_session`, `crashed` to fixes); development (`make check`, `make build-dev`, fakegh flow); license placeholder (ask the user; default to Apache-2.0 only if the user agrees, and leave a TODO otherwise).

### 4. Release build
- `make release VERSION=v0.1.0`: builds `dist/ownci_<ver>_linux_amd64.tar.gz` and `…_linux_arm64.tar.gz` (each containing the `ownci` binary, README.md, LICENSE if present) with `CGO_ENABLED=0 -trimpath` and ldflags. Writes `dist/SHA256SUMS`. Refuses a dirty git tree unless `ALLOW_DIRTY=1`.
- Verify release binaries contain **no** dev override code: `go tool nm dist/…/ownci | grep -ci OWNCI_DEV` must be 0 → add this as a `make release` check (the strings must not appear; use `strings` or `grep -a`).
- `ownci version` in a release binary shows the tag, commit and date.
- No GitHub Release publishing automation yet. Document the manual `gh release create v0.1.0 dist/*` command in `docs/releasing.md`.

### 5. Close-out
- Update `plan.md` §12 checkboxes **only in a copy** (`docs/acceptance.md`) with the evidence links/notes from the runbook. Don't edit `plan.md`.
- Append the Implementation notes, plus a "Next steps" list (candidates: ephemeral/JIT runners, systemd user service, native Linux validation pass, rootless Docker / userns-remap, resource limits, Windows/macOS phases).

## Out of scope
Package managers, auto-update of ownci itself, CI for the ownci repo itself (it can be added later; note it).

## Files
`e2e/workflows/{ownci-pass.yml,ownci-nocontainer.yml,ownci-poison.yml}`, `e2e/run.sh`, `docs/{e2e.md,releasing.md,acceptance.md}`, `README.md`, `Makefile` (release targets).

## Tests (required)
- `make check` still passes.
- A Go test (or `make` check) asserting the release build contains no `OWNCI_DEV_` strings.
- `shellcheck e2e/run.sh` if shellcheck is available.
- YAML sanity: workflows parse (`python3 -c 'import yaml…'` if PyYAML is present, else skip) and contain `runs-on: [ownci]`.
- **The real E2E runbook executed by the user**, with results recorded in `docs/acceptance.md`.

## Manual verification
```bash
make check
make release VERSION=v0.1.0 ALLOW_DIRTY=1 && (cd dist && sha256sum -c SHA256SUMS)
tar -xzf dist/ownci_v0.1.0_linux_amd64.tar.gz -C /tmp && /tmp/ownci version     # v0.1.0, no "build: dev"
grep -ac OWNCI_DEV_ /tmp/ownci                                                   # 0
# then follow docs/e2e.md end to end against the private sandbox repo (OWNCI_E2E_REPO=ashfaqc137/ownci-sandbox)
```

## Definition of done
- [ ] Global DoD.
- [ ] Every runbook step passes on the author's WSL2 machine against real GitHub, with the evidence recorded in `docs/acceptance.md`.
- [ ] The README states the trust model and isolation limitations honestly (PRD §10, §12 "Security and documentation").
- [ ] Release artifacts + SHA256SUMS are produced and verified. There is no dev code in the release binaries.
- [ ] Implementation notes + Next steps appended.

## Implementation notes

**What was done (offline) and what was not.** Everything that does not need real GitHub is implemented and tested. **The real-GitHub runbook (`docs/e2e.md`) has NOT been executed**: it needs a private sandbox repository, an admin token and a live PR, which only the user can provide (and creating repositories or pushing to GitHub is outward-facing, so it was not done on the user's behalf). `docs/acceptance.md` is therefore a copy of PRD §12 with **every box unchecked** and an explicit "NOT YET EXECUTED" banner. The task's Definition-of-done item "every runbook step passes on the author's WSL2 machine against real GitHub" is open.

**Delivered**
- `e2e/workflows/{ownci-pass,ownci-nocontainer,ownci-poison}.yml` (as specified) and `e2e/run.sh` (uses only `gh`; refuses public repos; commits the workflows to the *default branch* because `workflow_dispatch` workflows only exist from there; opens a PR from a throwaway branch; polls with `gh run view`; prints a PASS/FAIL table; exits non-zero on any FAIL). `bash -n` is clean; `shellcheck` is not installed here, so it was not run.
- `e2e/workflows_test.go` (Go, stdlib only; PyYAML is not installed): each workflow targets exactly `runs-on: [ownci]` once per job, only the no-container workflow lacks a job `container:`, no tabs, expected triggers/keys; `run.sh` is executable and has the guards.
- `docs/e2e.md` (12 steps with expected output, checklist mapped to PRD §12, Findings template incl. the hook-timing and `_work`-wipe questions from t08), `docs/releasing.md`, `docs/acceptance.md`, `README.md` (all sections of the spec; the trust-model section uses t08's six-item gap list; license is a TODO placeholder — no license was chosen, the user must decide).
- `make release VERSION=v0.1.0 [ALLOW_DIRTY=1]` → `tools/release.sh`: version format check, dirty-tree refusal (tracked changes only; the untracked `plan.md` does not block), `CGO_ENABLED=0 -trimpath` builds for linux/amd64 and linux/arm64, `grep -a OWNCI_DEV` guard (fails the build), deterministic tar.gz (binary, README.md, LICENSE if present at the archive top level), `dist/SHA256SUMS`. Verified by hand: `sha256sum -c SHA256SUMS` OK; the amd64 binary prints `ownci v0.1.0 (commit 30a9bf4, built …)`, no `build: dev` line, and `grep -ac OWNCI_DEV` → 0; a bad VERSION and a dirty tree are refused.
- `cmd/ownci/release_test.go`: builds the binary with and without `-tags dev` and asserts that only the dev binary contains `OWNCI_DEV_` (the control makes the check non-vacuous).

**A real defect found and fixed by that test:** the release binary still contained the string `OWNCI_DEV_`, from the dev-warning scanner in `internal/cli/devwarn.go` (compiled into release builds though it only acts when `DevBuild`). It is now split into `devwarn_dev.go` (`//go:build dev`) and `devwarn_release.go` (a no-op); `TestDevWarning` moved to `devwarn_dev_test.go` (`//go:build dev`), and `make test` runs it with `go test -tags dev -run TestDevWarning ./internal/cli/` (the rest of the cli tests assume a release build, e.g. `TestVersion`).

**Verification run (2026-10-15):** `make check` green; `make release VERSION=v0.1.0 ALLOW_DIRTY=1` as above.

**Next steps (post-MVP candidates)**
1. Run `docs/e2e.md` for real, fill `docs/acceptance.md`, resolve the t08→t10 open items (hook timing, `_work` wipe), pick a license, then tag `v0.1.0` (`docs/releasing.md`).
2. Ephemeral/JIT runner registration (fresh runner per job).
3. A systemd *user* service (and auto-start) for WSL with systemd / native Linux.
4. Native Linux validation pass (PRD phase 2), including arm64 on real hardware.
5. Rootless Docker / userns-remap so "root in the container" stops being host-root-equivalent on bind mounts.
6. Resource limits and concurrency (CPU/memory/disk caps; more than one job at a time).
7. Network policy for job containers; restricting `services:`/`docker://` sibling containers.
8. CI for this repository (`make check` on push, `shellcheck`, release workflow), archive signing/attestation, package managers.
9. Windows and macOS phases (PRD 3–4), which need an explicit runner/execution-model decision.
10. F1 in `tasks/future.md` (user-supplied runner archive/URL); don't create the data-dir layout inside foreign directories (t09 note); log rotation.
