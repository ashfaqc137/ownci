# OwnCI — Future work (post initial release)

> Ideas deliberately kept out of the MVP (t01–t10). Pick them up as separate tasks after the first release. Add new entries at the bottom; move an entry into its own `tNN-*.md` spec when it is scheduled.

## F1 — User-supplied runner (`--runner-archive` / `--runner-url`)

**Status:** not started · **Raised:** 2026-10-15 (during t04 review)

**Problem.** `ownci setup` only installs the runner version pinned in `internal/runner/versions.go` (D5). Some users need a different source: air-gapped machines or internal mirrors, GitHub Enterprise Server (which may want the runner version it ships with), or staying on an older runner for reproducibility.

**Proposed design**
- Explicit **flags** on `setup` (not environment variables, so it is a deliberate action): `--runner-archive <file>` or `--runner-url <url>`.
- Either flag **requires `--runner-sha256 <hex>`**. No unverified installs, the same rule the dev override follows (D8).
- Reuse `runner.Fetch` / `runner.Extract` / `runner.Install`. Only the `Asset` passed in changes (extend `AssetFor` or build the asset in `cli/setup.go`). A local archive skips the download but still verifies the checksum before extraction.
- Print a one-line warning that a non-default runner is in use, and record the source (`runnerSource`: `pinned` | `url` | `archive`, plus URL/path) in `config.json` so `status` can show it. That is a config schema addition, so bump or extend `schemaVersion` handling accordingly.
- Keep the D8 rule intact: release builds must still not honor `OWNCI_DEV_RUNNER_*`.

**Open questions**
- Should `--runner-version <v>` (a known GitHub release, with its checksum fetched from the release notes) exist as well? That trusts the same server as the download, so it needs a clear warning or a documented trade-off.
- GHES: does the runner download URL differ, and does registration need extra flags? Needs its own investigation.

**Touches:** `internal/runner/versions.go`, `internal/cli/setup.go`, `internal/config/config.go` (+ `status` in t07), README (trust model section).

## F2 — Default job image / runner container hooks

**Status:** **implemented in t11** (runner-in-container, `setup --mode container-runner`, D13) · hooks spiked: no-go for a default image, still an option for isolation hardening · **Raised:** 2026-10-22

**Goal.** Let workflows run without `container:` by using a default image (`ubuntu:24.04`, set at `setup`, overridable at `start`), while a job that declares `container:` keeps its own image.

**Spike result (runner 2.338.0, private repo `ashfaqc137/blog`, throwaway runner + PR, all removed afterwards).** A logging script was set as `ACTIONS_RUNNER_CONTAINER_HOOKS`, with `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` off.
- A job **with** `container:` called the hook: `prepare_job` (payload has `args.container` with `image`, `workingDirectory`, `systemMountVolumes`, …) and `cleanup_job`. Exiting 1 failed the job ("Executing the custom container implementation failed").
- A job **without** `container:` called the hook **zero times** and ran directly on the host. The 2022 ADR says the hook is "always called"; that does not hold for this runner.
- So hooks cannot supply a default image, and with hooks on and the require flag off, no-container jobs run on the host unchecked.

**Other options considered.** Runner-in-container (the ARC model) works for no-container jobs but conflicts with "use the workflow's image if it declares one", because that needs Docker inside the runner container. A host-run opt-in mode (`--allow-host-jobs`, separate label) is possible but removes the container boundary; not recommended.

**Side finding (important).** The `prepare_job` payload lists `/var/run/docker.sock` in `systemMountVolumes`, and a real job confirmed the socket is present in the job container (`srw-rw---- root 1001 /var/run/docker.sock`, job runs as root). OwnCI's earlier "socket not mounted" claim was wrong and has been corrected in the README and task docs. A custom hook implementation (the official repo has a Docker hook, `packages/docker`) could drop that mount, which is the one reason to revisit hooks: it would be a real hardening step, but it means implementing the whole hook contract (`prepare_job`, `run_script_step`, `run_container_step`, `cleanup_job`).

**Runner-in-container spike (2026-10-22, official image `ghcr.io/actions/actions-runner`, Ubuntu 24.04.5, runner 2.338.0; throwaway runner on `ashfaqc137/blog`, everything removed afterwards).** The container ran the runner with the host Docker socket (`--group-add <socket gid>`) and the work directory bind-mounted.
- A job **without** `container:` ran inside the runner container as uid 1001 (`runner`, passwordless sudo, in the `docker` group): Ubuntu 24.04, checkout worked, the Docker daemon was reachable through the socket, and the host's `~/.ssh` was not visible. So a default image works.
- A job **with** `container:` failed the first time: `/__e/node24/bin/node: no such file`. The runner passes its own paths (`externals`, `_work`, `_temp`, `_actions`, `_tool`) to the **host** daemon as bind-mount sources, so each must exist at the **same path on the host**. Mounting `_work` at its host path was not enough; the daemon also created an empty `/home/runner/externals` on the host. After running the runner **from a host directory mounted at the identical path** (the whole runner tree, like OwnCI's `runners/<name>` dir), the same job passed: `ubuntu:24.04`, root in the job container, workspace writable, socket present.
- Ownership: files the runner container writes are uid 1001 and files from container jobs are root, so the host user (uid 1000) cannot delete them. Cleanup must stay container-based (as the `_work` wipe hook already is).
- Replacing a runner of the same name hits a session "Conflict" for about a minute until the old session expires.
- Not tested: ephemeral per-job containers (needs a registration token per job, which OwnCI does not store), runner self-update in the container, running as the host uid, supervision/`status`/`doctor` changes, and the cleanup hook inside the container.

**Conclusion.** Runner-in-container is feasible with the official image, provided the runner directory and work directory are mounted at identical host paths and the Docker socket is shared. It changes supervision (`docker run` instead of a process), cleanup and status, so it needs its own task spec before implementation.

## F3 — Replace `run.sh --once` before the runner drops it

**Status:** not started · **Raised:** 2026-10-22 (t11)

**Problem.** Container-runner mode (t11) gives every job a fresh container by starting the runner with `run.sh --once`. The runner prints *"'--once' is going to be deprecated in the future, please consider using '--ephemeral'"*. Runners self-update (D5), so a future version could remove the flag; the runner would then fail at start and OwnCI would report `crashed` with the runner's error.

**Options**
- **Ephemeral or JIT runners** (`config.sh --ephemeral`, or `POST /repos/{o}/{r}/actions/runners/generate-jitconfig`): GitHub's supported "one job per runner" model. Each needs a fresh registration per job, which needs a token with admin per job; that conflicts with D4 (no stored token) unless a GitHub App or a long-lived fine-grained token is introduced.
- **Fallback in the supervisor:** without `--once`, stop the runner gracefully on `Job … completed` and relaunch. Racy: the listener may accept the next job before the SIGINT arrives (that job is then cancelled), so only acceptable as a fallback.
- Detect the removal (the runner's "unknown argument" error) and print a clear hint to switch to host mode until OwnCI is updated.

**Touches:** `internal/runnercontainer` (`Args`), `internal/supervisor` (recycle), possibly `internal/github` (JIT config).

## F4 — Container-runner mode: protect the runner's credentials from jobs

**Status:** not started · **Raised:** 2026-10-22 (t11)

**Problem.** In container-runner mode a job without `container:` runs as the runner's uid with the whole runner directory mounted read-write, so it can read `.credentials*` or modify `bin/`. The Docker socket already gives such a job control of the host, so this is a hardening item, not a new boundary. Ideas: mount the runner tree read-only except `_work`, `_diag` and the paths self-update writes (needs investigation of what the runner writes at runtime); run jobs as a second uid; or move the credentials out of the mounted tree.

