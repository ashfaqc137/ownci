# Acceptance record (MVP)

> A working copy of PRD §12 (`plan.md` is not edited). Tick an item **only** with evidence from running
> [`e2e.md`](e2e.md) against real GitHub: a run URL, a log excerpt or a screenshot path, with the date.
>
> **Status: NOT YET EXECUTED.** Everything below is unchecked: the offline parts (unit/integration tests with
> fakes, the fake-GitHub flow, a real Docker cleanup check) are covered by `make check` and the task notes in
> `tasks/`, but none of the real-GitHub items has been verified yet.

| Field | Value |
|---|---|
| Date | _(fill in)_ |
| Machine / WSL distro / kernel | |
| Docker version | |
| OwnCI version (commit) | |
| Runner version (pinned → actual) | |
| Sandbox repository | |

## Checklist (from PRD §12)

### CLI and installation

- [ ] `ownci setup` provisions the official runner.
- [ ] The runner archive is verified before extraction.
- [ ] Runner files and OwnCI configuration are stored locally.
- [ ] GitHub registration succeeds with appropriate credentials.
- [ ] Configuration persists across restarts.
- [ ] `ownci start` starts the runner.
- [ ] `ownci stop` stops the runner gracefully.
- [ ] `ownci status` reports meaningful state.
- [ ] `ownci uninstall` supports safe cleanup.

### End-to-end CI

- [ ] A real GitHub Actions workflow targets the local runner.
- [ ] A pull request displays the expected check result in GitHub.
- [ ] Job compute runs on the developer's local machine.
- [ ] The selected Docker execution mode is validated.
- [ ] The actual execution boundary is verified rather than assumed.
- [ ] Cleanup and repeated job execution are tested.

### WSL distribution

- [ ] The Linux OwnCI binary runs in the supported WSL2 configuration.
- [ ] Docker connectivity is documented and validated.
- [ ] Runner setup and lifecycle work from WSL.
- [ ] The end-to-end PR check passes.
- [ ] Installation and troubleshooting instructions are available.

### Security and documentation

- [ ] Secrets are not written to ordinary logs.
- [ ] The trust model and isolation limitations are documented.
- [ ] Runner and CLI versions can be inspected.
- [ ] Unsupported configurations fail clearly.

## Evidence

| Item | Evidence (URL / excerpt / path) | Date |
|---|---|---|
| PR check (step 6) | | |
| No-container rejection (step 7) | | |
| Poison test (step 8) | | |
| Stop / restart without re-registration (step 9) | | |
| Kill test (step 10) | | |
| No tokens in logs (step 11) | | |
| Uninstall (step 12) | | |

## Findings

_(copy the Findings table from `e2e.md`)_

## t11: container-runner mode (executed 2026-10-22)

Run by the implementing LLM on private `ashfaqc137/blog`, throwaway PR #4 (closed, branch deleted).
The workflows were the `e2e/workflows/` files with `pull_request` triggers and `runs-on: [ownci, t11e2e]`.
The MVP checklist above is still the user's to tick.

| Field | Value |
|---|---|
| Machine | WSL2 openSUSE-Tumbleweed, kernel 6.18.54.1-microsoft-standard-WSL2 |
| Docker | Docker Desktop, server 29.5.3 (socket gid 1001 inside containers) |
| OwnCI | `dc03cd9-dirty` (t11 working tree), release build (`make build`) |
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
| No tokens in the data dir | ✅ | `grep -rEI 'gh[pousr]_…|github_pat_'` → no tokens found |
| Uninstall in container mode | ✅ | `uninstall --unregister --purge --yes`: stopped, unregistered (0 runners on GitHub), data dir gone, no `ownci-*` container, no `/home/runner` |
| Custom image refused: plain `ubuntu:22.04` | ✅ | `image ubuntu:22.04 cannot host the runner; it lacks: docker libicu`, before registration (0 runners on GitHub) |
| Custom image: Ubuntu 22.04 (`e2e/images/ubuntu22.Dockerfile`, built locally) | ✅ | PR #5, runs 39771315949 (pass ✓), 39771315977 (default-image ✓: `Ubuntu 22.04.5 LTS`, `uid=1000(ownci)`), 39771315948 (poison ✓) |
| Session override `start --image <default>` | ✅ | runs 39771651599/747/738 green, default-image asserted `VERSION_ID=24.04`; next plain `start` back on 22.04 |
| Runner self-update in the container | ⚠️ not exercised | 2.338.0 is the latest release |
