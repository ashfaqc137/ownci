# Acceptance record (MVP)

> The MVP acceptance checklist. Tick an item **only** with evidence from running
> [`e2e.md`](e2e.md) against real GitHub: a run URL, a log excerpt or a screenshot path, with the date.
>
> **Status: EXECUTED 2026-10-22 for v0.1.0 and 2026-10-31 for v0.2.0 — all items pass on WSL2 + Docker Desktop (amd64).**
> Run with the **release binary** (`make release VERSION=v0.1.0`, the amd64
> archive) on private `ashfaqc137/blog`, throwaway PR #6 (closed, branch deleted). The workflows were the
> `e2e/workflows/` files with `pull_request` triggers and an extra label, so the sandbox's default branch
> was not touched. Native Linux (Docker Engine) and arm64 are **not** covered by this record.

## v0.2.0 run (2026-10-31)

Run with the **release binary** (`make release VERSION=v0.2.0`, amd64 archive, commit `941d98a`; Go code
identical to `main` at `bed1383`) on private `ashfaqc137/blog`, throwaway PRs #7 (host) and #8
(container-runner), both closed with their branches deleted. Workflows: the `e2e/workflows/` files with
`pull_request` triggers and an extra label. Same machine as below; Docker Desktop 29.5.3; runner 2.338.0
pinned = actual.

| Check | Result | Evidence |
|---|---|---|
| Doctor (step 2) | ✅ | 7 passed, 0 warnings |
| Setup, token passed through `ACTIONS_RUNNER_INPUT_TOKEN` (step 3) | ✅ | `registered on GitHub (id 28)`, labels `ownci,v020e2e`; `drwx------` data/config, `-rw-------` config.json; `.env` has `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`, the hook and the pinned busybox |
| Public repo refused (step 4) | ✅ | `ashfaqc137/ownci is a public repository …`, hint `--allow-public-repo`, exit 1, empty `runners/` |
| PR check (step 6) | ✅ | https://github.com/ashfaqc137/blog/actions/runs/41177285398: `Ubuntu 24.04.5 LTS`, `runner=ownci-ashfaq`, `ownci: workspace cleaned` |
| No-container rejection (step 7) | ✅ | https://github.com/ashfaqc137/blog/actions/runs/41177285390 ✗ (expected): `Jobs without a job container are forbidden on this runner …` |
| Poison test (step 8) | ✅ | https://github.com/ashfaqc137/blog/actions/runs/41177285429: `ok, absent:` for `_tool`, `../poison`, `_actions`; `_work` = `_PipelineMapping`, `_temp` |
| Hook pulls a missing cleanup image | ✅ | busybox image removed before the jobs; the first cleanup succeeded (`last-cleanup.json` `ok`) and the image was present again |
| Stop / restart, no re-registration (step 9) | ✅ | runner id 28 before and after; re-run of 41177285398 (attempt 2) ✓ |
| Kill test (step 10) | ✅ | `Process stopped (unexpectedly …)`; `start` killed the orphaned `Runner.Listener` and left another data dir's runner alone; online after the known session conflict |
| No tokens in logs (step 11) | ✅ | `no tokens found` in both data dirs |
| `start` re-checks visibility | ✅ | public `ashfaqc137/ownci` runner with `allowPublicRepo` removed from its config: `Error: ashfaqc137/ownci is now a public repository …`, exit 1; started normally once restored |
| `setup --force` replaces an outdated runner | ✅ | `Runner.Listener --version` stubbed to 2.300.0 → `The extracted runner 2.300.0 is older than 2.338.0: replacing it`, same runner id 28 and labels; re-run of 41177285429 (attempt 2) ✓ |
| Uninstall (step 12, C5) | ✅ | both data dirs gone, 0 runners on `blog`, no `ownci-*` container, no `/home/runner` |
| Container-runner C1/C2 | ✅ | runner id 29, `.env` without `REQUIRE_JOB_CONTAINER`; runs 41177968884 (pass ✓), 41177968909 (default-image ✓: `Ubuntu 24.04.5 LTS`, `uid=1000(ubuntu)`, `docker server 29.5.3`, `ok, absent:` markers), 41177968871 (poison ✓); 5× `starting a fresh runner`, `restarts 0` |

## v0.1.0 run (2026-10-22)

The MVP checklist below was ticked by this run.

| Field | Value |
|---|---|
| Date | 2026-10-22 |
| Machine / WSL distro / kernel | WSL2 openSUSE-Tumbleweed, 6.18.54.1-microsoft-standard-WSL2, amd64 |
| Docker version | Docker Desktop, server 29.5.3 |
| OwnCI version (commit) | `ownci v0.1.0 (commit a6a2c02)`, release archive (0 `OWNCI_DEV` strings) |
| Runner version (pinned → actual) | 2.338.0 → 2.338.0 (no self-update offered) |
| Sandbox repository | `ashfaqc137/blog` (private), PR #6; step 4 against public `ashfaqc137/ownci` |

## Checklist

### CLI and installation

- [x] `ownci setup` provisions the official runner. — step 3
- [x] The runner archive is verified before extraction. — step 3 ("downloaded, verified and extracted")
- [x] Runner files and OwnCI configuration are stored locally. — step 3 (`drwx------` data/config, `-rw-------` config.json)
- [x] GitHub registration succeeds with appropriate credentials. — step 3 (runner id 26, labels `ownci,v010e2e`)
- [x] Configuration persists across restarts. — step 9 (same runner id 26, re-run check green)
- [x] `ownci start` starts the runner. — step 5
- [x] `ownci stop` stops the runner gracefully. — step 9
- [x] `ownci status` reports meaningful state. — steps 5, 6, 9, 10
- [x] `ownci uninstall` supports safe cleanup. — step 12 (both instances)

### End-to-end CI

- [x] A real GitHub Actions workflow targets the local runner. — step 6
- [x] A pull request displays the expected check result in GitHub. — step 6, PR #6
- [x] Job compute runs on the developer's local machine. — step 6 (`runner=ownci-v010-e2e`)
- [x] The selected Docker execution mode is validated. — steps 6–7 (host), C2 (container-runner)
- [x] The actual execution boundary is verified rather than assumed. — step 7 (no-container job rejected)
- [x] Cleanup and repeated job execution are tested. — step 8, C2

### WSL distribution

- [x] The Linux OwnCI binary runs in the supported WSL2 configuration. — steps 1–12
- [x] Docker connectivity is documented and validated. — step 2 (`doctor`), README Prerequisites
- [x] Runner setup and lifecycle work from WSL. — steps 3, 5, 9, 10, 12
- [x] The end-to-end PR check passes. — step 6
- [x] Installation and troubleshooting instructions are available. — README Install / Troubleshooting

### Security and documentation

- [x] Secrets are not written to ordinary logs. — step 11
- [x] The trust model and isolation limitations are documented. — README "Security and trust model"
- [x] Runner and CLI versions can be inspected. — steps 1, 3 (`version`, `status`)
- [x] Unsupported configurations fail clearly. — step 4, and the refusals below

## Evidence

| Item | Evidence (URL / excerpt / path) | Date |
|---|---|---|
| Doctor (step 2) | 7 passed, 0 warnings: platform, wsl, user, data-dir, runner-deps (ICU), docker 29.5.3, git | 2026-10-22 |
| Setup (step 3) | `Repository ashfaqc137/blog (private)`, `Runner 2.338.0 downloaded, verified and extracted`, `registered on GitHub (id 26)`; `.env` has `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`, the hook and the pinned busybox image | 2026-10-22 |
| Public repo refused (step 4) | `Error: ashfaqc137/ownci is a public repository: pull requests from forks could run arbitrary code …`, hint `--allow-public-repo`, exit 1; empty `runners/`, 0 runners registered on `ashfaqc137/ownci` | 2026-10-22 |
| Start / status (step 5) | `listening`, `Isolation container jobs required ✓ cleanup hook ✓ cleanup image ✓`, `GitHub online, idle` | 2026-10-22 |
| PR check (step 6) | https://github.com/ashfaqc137/blog/actions/runs/39801588619 ✓: `PRETTY_NAME="Ubuntu 24.04.5 LTS"`, `runner=ownci-v010-e2e`; status `Last job pass → Succeeded`, `last cleanup: ok` | 2026-10-22 |
| No-container rejection (step 7) | https://github.com/ashfaqc137/blog/actions/runs/39801588494 ✗ (expected): `##[error]Jobs without a job container are forbidden on this runner, please add a 'container:' to your job …` in *Set up job* | 2026-10-22 |
| Poison test (step 8) | https://github.com/ashfaqc137/blog/actions/runs/39801588512 ✓: `ok, absent:` `/__w/_tool/poison`, `/__w/blog/blog/../poison`, `/__w/_actions/poison`; `last-cleanup.json` `{"result":"ok"}`; `_work` = `_PipelineMapping`, `_temp` | 2026-10-22 |
| Stop / restart without re-registration (step 9) | stop → `Process not running`, `State stopped`; start → listening; runner id 26 before and after; re-run of https://github.com/ashfaqc137/blog/actions/runs/39801588619 (attempt 2) ✓ | 2026-10-22 |
| Kill test (step 10) | `kill -9` of the supervisor → `Process stopped (unexpectedly; last state: listening …)`; `start --detach` recovered (`GitHub online, idle`) after ~70 s (see Findings) | 2026-10-22 |
| No tokens in logs (step 11) | `grep -rEI 'gh[pousr]_…\|github_pat_'` over the data dir → `no tokens found` | 2026-10-22 |
| Uninstall (step 12) | `Runner unregistered`, `Local files removed (… is gone)` for both instances; 0 runners on `blog`, no `ownci-*` container | 2026-10-22 |
| Container-runner mode alongside host mode (C1/C2) | second data dir, runner `ownci-v010-ctr` (id 27, label `v010ctr`), container labelled `ownci.datadir=<its data dir>`; both instances online and busy at the same time. https://github.com/ashfaqc137/blog/actions/runs/39803074928 ✓ (job without `container:`: `Ubuntu 24.04.5 LTS`, `uid=1000(ubuntu)`, `docker server 29.5.3`, `ok, absent:` `/opt`, `$HOME`, `/tmp` markers → fresh container per job); `ctr-pass` job ✓ on `ownci-v010-ctr`; host instance in parallel: https://github.com/ashfaqc137/blog/actions/runs/39803074913 ✓, https://github.com/ashfaqc137/blog/actions/runs/39803074845 ✗ (expected), https://github.com/ashfaqc137/blog/actions/runs/39803074790 ✓; `restarts 0` on both | 2026-10-22 |

## Findings

| Step | Expected | Actual | Impact / follow-up |
|---|---|---|---|
| 8 | The hook runs after the job container stops | Container removed 15:06:03.6Z (*Stop containers*), `ownci: workspace cleaned` 15:06:04.3Z (*Complete runner*) | None; same as 2026-10-15 |
| 8 | Wiping `_work` does not break the runner | `second` started fine after the wipe; later runs green | None |
| 5/9 | The runner version is the pinned one | 2.338.0 pinned = actual | Self-update not exercised (no newer release) |
| 10 | The runner process after `kill -9` of the supervisor | One runner process survived the kill; after `start` there was again exactly one (the new one). The old listener's GitHub session stayed open: the new listener logged `A session for this runner already exists … Conflict. Retrying until reconnected.` and was online ~70 s later without intervention | Documented as a known limitation; `status` keeps showing that line as *Last error* after recovery |
| C2 | Run status follows its jobs | `ctr-pass`: the only job completed `success` at 15:16:05Z, the run still showed `in_progress` on GitHub afterwards | GitHub-side roll-up delay; not an OwnCI issue |
| — | Several repositories side by side | Two data dirs, host + container-runner, ran jobs concurrently; same-name collision refusals verified with real Docker and the fake GitHub (commit `1a059c0`) | Second instance used the same sandbox repo under another name, not a second repository |

## Container-runner mode (executed 2026-10-22)

Run on private `ashfaqc137/blog`, throwaway PR #4 (closed, branch deleted).
The workflows were the `e2e/workflows/` files with `pull_request` triggers and `runs-on: [ownci, t11e2e]`.
The MVP checklist above was ticked by the v0.1.0 run.

| Field | Value |
|---|---|
| Machine | WSL2 openSUSE-Tumbleweed, kernel 6.18.54.1-microsoft-standard-WSL2 |
| Docker | Docker Desktop, server 29.5.3 (socket gid 1001 inside containers) |
| OwnCI | `dc03cd9-dirty` (container-runner working tree), release build (`make build`) |
| Runner | 2.338.0 pinned = actual (no self-update offered) |
| Runner image | `ghcr.io/actions/actions-runner:2.338.0@sha256:4ffadc00…` |

| Check | Result | Evidence |
|---|---|---|
| setup `--mode container-runner` | ✅ | runner id 24, labels `ownci,t11e2e`; `.env` without `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER`; doctor `[PASS] runner-container` |
| Runner runs as the host uid in the container | ✅ | `docker exec … ps`: `ubuntu (uid 1000) … Runner.Listener run --once`; PID 1 `docker-init` |
| Job with `container:` (ownci-pass) | ✅ | run 39768244338: `docker create … -v /home/ashfaq/ownci-e2e-t11/runners/ownci-t11-e2e/_work:/__w … ubuntu:24.04`, `PRETTY_NAME="Ubuntu 24.04.5 LTS"` |
| Job without `container:` (ownci-default-image) | ✅ | run 39768244412: `uid=1000(ubuntu)`, `PATH=/usr/local/sbin:…:/bin` (no `/mnt/`), `docker server 29.5.3`, `sudo -n true` ok |
| Fresh container per job | ✅ | `second`: `ok, absent:` `/opt/ownci-poison`, `/home/ubuntu/ownci-poison`, `/tmp/ownci-poison`; runner log: `[ownci] job finished; starting a fresh runner` after each of 5 jobs, ~7 s to `Listening`; `restarts 0` |
| Workspace wipe (ownci-poison) | ✅ | run 39768244267: `ok, absent:` for `_tool`, `../poison`, `_actions`; `last-cleanup.json` `ok`; `_work` = `_PipelineMapping`, `_temp` |
| Stop / start, no re-registration | ✅ | container removed on stop; start in 6 s; runner id 24 unchanged |
| `kill -9` of the supervisor | ✅ (after a fix) | first try: leftover container force-removed → "A session for this runner already exists … Conflict" for ~3 min. Fix: `docker stop --signal SIGINT` before removal. Retest: `start` recovered in 8 s, 0 conflicts; runs 39769050350/080/163 all green afterwards |
| Switch to host mode (`setup --force --mode host`) | ✅ (after a fix) | first try failed: real `config.sh` refuses an existing `.runner` even with `--replace` (pre-existing t06 bug). Fixed; id 24 kept |
| Host-mode regression | ✅ | runs 39769696425 (pass ✓), 39769696508 (poison ✓), 39769696737 (nocontainer ✗) and 39769696653 (default-image ✗), both failing with "Jobs without a job container are forbidden on this runner" |
| No tokens in the data dir | ✅ | `grep -rEI 'gh[pousr]_…\|github_pat_'` → no tokens found |
| Uninstall in container mode | ✅ | `uninstall --unregister --purge --yes`: stopped, unregistered (0 runners on GitHub), data dir gone, no `ownci-*` container, no `/home/runner` |
| Custom image refused: plain `ubuntu:22.04` | ✅ | `image ubuntu:22.04 cannot host the runner; it lacks: docker libicu`, before registration (0 runners on GitHub) |
| Custom image: Ubuntu 22.04 (`e2e/images/ubuntu22.Dockerfile`, built locally) | ✅ | PR #5, runs 39771315949 (pass ✓), 39771315977 (default-image ✓: `Ubuntu 22.04.5 LTS`, `uid=1000(ownci)`), 39771315948 (poison ✓) |
| Session override `start --image <default>` | ✅ | runs 39771651599/747/738 green, default-image asserted `VERSION_ID=24.04`; next plain `start` back on 22.04 |
| Runner self-update in the container | ⚠️ not exercised | 2.338.0 is the latest release |
