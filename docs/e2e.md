# End-to-end runbook (real GitHub)

This is the MVP acceptance test: a real pull request check in a real repository runs on
**this machine**, inside a job container, a job without a container is rejected, and
nothing persists between jobs. Everything before it (`make check`) is tested with fakes;
only this runbook touches real GitHub.

`e2e/run.sh` automates steps 6–8 with `gh`. This document is the source of truth.

## Prerequisites

- A **private** sandbox repository with at least one commit on its default branch
  (e.g. `ashfaqc137/ownci-sandbox`). Do not use a repository you care about: the runbook
  commits workflows to its default branch and opens throwaway PRs.
- A token with admin on it: `gh auth login` (default scopes) or a fine-grained PAT with
  *Administration*, *Contents*, *Pull requests* (read & write) and *Actions* (read).
- Docker with a Linux daemon reachable from this shell (on WSL: Docker Desktop with the
  WSL integration enabled for this distro).
- `gh` installed and logged in (for the PR/dispatch steps; the UI works too).

```bash
export OWNCI_E2E_REPO=ashfaqc137/ownci-sandbox     # your sandbox
export OWNCI_DATA=~/ownci-e2e-data                 # a throwaway data dir for the test
O="./bin/ownci --data-dir $OWNCI_DATA"             # NOT bin/ownci-dev: the release configuration is under test
```

Record anything unexpected in **Findings** at the bottom, and the results in
[`acceptance.md`](acceptance.md).

## Steps

### 1. Build the release configuration
```bash
make build && ./bin/ownci version
```
Expect: `ownci <version> (commit …, built …)`, `go: … linux/…`, and the line `runner:` only
after setup. There must be **no** `build: dev` line.

### 2. Doctor
```bash
$O doctor
```
Expect all `[PASS]` (a `[WARN]` for `git` is fine): platform, wsl, user, data-dir,
runner-deps, docker. No `isolation` line yet (nothing is set up).

### 3. Set up the sandbox
```bash
$O setup --repo $OWNCI_E2E_REPO
```
Expect, in order: preflight passed; GitHub token source; `Repository … (private)`; runner
downloaded, **verified** and extracted (the pinned version); `Runner configured`; `Runner
registered on GitHub (id N)`; `OwnCI is set up for … as runner 'ownci-<host>' (labels: ownci)`.

Check:
- GitHub → repo → Settings → Actions → Runners shows the runner with **only** the `ownci` label.
- `cat $OWNCI_DATA/runners/*/.env` has the config.sh lines plus the managed block with
  `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`, `ACTIONS_RUNNER_HOOK_JOB_COMPLETED=…`,
  `OWNCI_RUNNER_ROOT=…`, `OWNCI_CLEANUP_IMAGE=busybox:1.37.0@sha256:…`.
- `ls -ld $OWNCI_DATA $OWNCI_DATA/config` → `drwx------`; `$OWNCI_DATA/config/config.json` is `-rw-------`.
- `$O version` now prints `runner: <version>`.

### 4. A public repository is refused (skip if you have none you administer)
Use a **separate** data dir: this one already holds a runner, and OwnCI refuses a second
repository there before it even looks at visibility.
```bash
./bin/ownci --data-dir /tmp/ownci-e2e-public setup --repo <owner>/<some-public-repo>
```
Expect: `Error: <repo> is a public repository: pull requests from forks could run arbitrary code …`,
a hint about `--allow-public-repo`, exit 1; nothing downloaded or registered (`ls /tmp/ownci-e2e-public/runners` is empty).
Remove the directory afterwards.

### 5. Start and check
```bash
$O start --detach
$O status --remote
```
Expect: `✓ Runner '…' is listening for jobs on <repo>`; status shows `Process running (… detached …)`,
`State listening`, `Isolation container jobs required ✓ cleanup hook ✓ cleanup image ✓ last cleanup: never`,
`GitHub online, idle`, `Docker ok`. The runner shows **Idle** in the GitHub UI.

### 6. A pull request check runs locally, in a container
Commit `e2e/workflows/ownci-pass.yml` to the sandbox as `.github/workflows/ownci-pass.yml`
(on the default branch), then open a PR (any trivial change). Or: `./e2e/run.sh`.

While the job runs, in another terminal:
```bash
docker ps                # a job container (ubuntu:24.04) is listed
$O status                # State busy: running job "pass"
```
Expect: the check is **green** in the PR UI; the job log shows the container's `/etc/os-release`
(Ubuntu 24.04), `runner=ownci-<host>`, your `hostname`/`nproc`; afterwards `$O status` shows
`Last job pass → Succeeded` and `last cleanup: ok at …`.

### 7. A job without a container is rejected
Dispatch `ownci-nocontainer.yml` (Actions tab → *ownci-nocontainer* → Run workflow, or
`gh workflow run ownci-nocontainer.yml -R $OWNCI_E2E_REPO`).

Expect: the run **fails** with *"Jobs without a job container are forbidden on this runner,
please add a 'container:' to your job or contact your self-hosted runner administrator."*
Keep a log excerpt or screenshot. `$O status` → `Last job … → Failed`.

### 8. Nothing persists between jobs
Dispatch `ownci-poison.yml`.

Expect: both jobs green; the `second` job log prints `ok, absent:` for all three markers.
The `first` job's *Complete job* / hook output shows `ownci: workspace cleaned`.
```bash
cat $OWNCI_DATA/runners/*/ownci/last-cleanup.json     # {"result":"ok", …}
ls -A $OWNCI_DATA/runners/*/_work                      # only _temp (and _PipelineMapping)
```
Record in Findings: *when* the hook ran relative to the job container stopping, and whether
the runner kept working after `_work` was wiped (the next job started fine = yes).

### 9. Stop, restart, no re-registration
```bash
$O stop                  # ✓ Runner '…' stopped (registration kept …)
$O status                # Process not running; State stopped
$O start --detach        # listening again, WITHOUT a new registration (same runner id in the UI)
```
Open a new PR (or re-run the PR check): it passes. Configuration persisted (PRD §12).

### 10. Kill test
During an idle period:
```bash
kill -9 "$(sed -n 's/.*"pid": \([0-9]*\).*/\1/p' $OWNCI_DATA/run/*.pid)"
$O status
```
Expect: `Process stopped (unexpectedly; last state: listening at …)` — **not** running. Also run
`pgrep -af 'runners/.*/bin'`: the runner process may remain after a SIGKILL of the supervisor
(record it in Findings); `$O stop --force` / `$O start` recovers (start refuses nothing; if an
orphaned runner holds the GitHub session you may see `duplicate_session` — kill it, then start).

### 11. No secrets in logs
```bash
$O logs -n 50
$O logs --source ownci -n 50
grep -rEI 'gh[pousr]_[A-Za-z0-9]{20,}|github_pat_' "$OWNCI_DATA" && echo LEAK || echo "no tokens found"
```
Expect: `no tokens found`. (Registration/removal tokens are single-use and masked as `***`.)

### 12. Uninstall
```bash
$O uninstall --unregister --purge --yes
```
Expect: `✓ Runner unregistered from <repo>`; summary: `Runner process stopped`, `GitHub unregistered`,
`Local files removed (… is gone)`. The runner has disappeared from Settings → Actions → Runners,
and `ls $OWNCI_DATA` fails. Clean up the sandbox's `ownci-e2e-*` branches/PRs if you like.

## Acceptance checklist (PRD §12)

Tick in [`acceptance.md`](acceptance.md) with evidence (log excerpt, run URL, screenshot path).

**CLI and installation**
- [ ] `ownci setup` provisions the official runner — step 3
- [ ] The runner archive is verified before extraction — step 3 ("downloaded, verified and extracted"; tampering is covered by unit tests)
- [ ] Runner files and OwnCI configuration are stored locally — step 3
- [ ] GitHub registration succeeds with appropriate credentials — step 3
- [ ] Configuration persists across restarts — step 9
- [ ] `ownci start` starts the runner — step 5
- [ ] `ownci stop` stops the runner gracefully — step 9
- [ ] `ownci status` reports meaningful state — steps 5, 6, 10
- [ ] `ownci uninstall` supports safe cleanup — step 12

**End-to-end CI**
- [ ] A real GitHub Actions workflow targets the local runner — step 6
- [ ] A pull request displays the expected check result in GitHub — step 6
- [ ] Job compute runs on the developer's local machine — step 6 (hostname, `docker ps`)
- [ ] The selected Docker execution mode is validated — steps 6, 7
- [ ] The actual execution boundary is verified rather than assumed — steps 7, 8
- [ ] Cleanup and repeated job execution are tested — steps 8, 9

**WSL distribution**
- [ ] The Linux OwnCI binary runs in the supported WSL2 configuration — steps 1, 2
- [ ] Docker connectivity is documented and validated — step 2 + README prerequisites
- [ ] Runner setup and lifecycle work from WSL — steps 3, 5, 9
- [ ] The end-to-end PR check passes — step 6
- [ ] Installation and troubleshooting instructions are available — README

**Security and documentation**
- [ ] Secrets are not written to ordinary logs — step 11
- [ ] The trust model and isolation limitations are documented — README "Security and trust model"
- [ ] Runner and CLI versions can be inspected — steps 1, 3 (`version`, `status`)
- [ ] Unsupported configurations fail clearly — step 4, and `doctor` failures

## Findings

Record anything that differed from the expectations above. Template:

| Step | Expected | Actual | Impact / follow-up |
|---|---|---|---|
| 8 | The hook runs after the job container stops (D10 risk) | | |
| 8 | Wiping `_work` (except `_temp`, `_PipelineMapping`) does not break the runner | | |
| 5/9 | The runner version is the pinned one / self-updated to … | | |
| 10 | The runner process after `kill -9` of the supervisor | | |
| … | | | |

Also note: runner version before and after the run (`$O status`), Docker version, WSL distro and kernel.
