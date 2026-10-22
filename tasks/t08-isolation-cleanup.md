# t08 — Isolation enforcement (`.env`) and per-job cleanup hook

> Read `tasks/00-context.md` first (D1, D10, §7 "`.env` file", "What a container job exposes", "run.sh"), then the Implementation notes of t01–t07.

## Goal
Make the isolation policy **real and verifiable**:
1. Every job on this runner must declare `container:` (`ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`).
2. After every job, the runner's `_work` contents (workspace, `_actions`, `_tool`) are wiped, including root-owned files, so one job cannot poison the next.
3. `start` re-asserts both settings every time. `status` and `doctor` report them.

## Depends on / current state
- t03 `execx`, `docker.Check`, doctor framework. t04 fake runner (runs the `.env` job-completed hook, creates marker files including in `_work/_temp`). t06 setup insertion point `installIsolation(...)`. t07 `beforeLaunch` hook point and `status` output.

## What later tasks build on
- `isolation.Install(...)`, `isolation.Verify(...) Report`, and the cleanup image constant. t09 uses the cleanup container to delete root-owned files during `--purge`.

## Scope

### 1. `.env` manager (`internal/runner/envfile.go`)
- Parse `<runnerDir>/.env` preserving order and unknown lines. **`config.sh` itself writes `.env`** (e.g. `LANG=…`, `PATH=…` from `env.sh`), so never overwrite it wholesale.
- `EnsureEnv(dir string, managed map[string]string) (changed bool, err error)`: sets or replaces each managed key (keeping one occurrence; drop duplicates of managed keys), keeps every other line as is, and appends missing managed keys under a marker comment line **without `=`** (the Listener ignores lines without `=`): `# --- managed by ownci: do not edit the keys below ---`. Atomic write, mode 0600. Handle CRLF and a missing trailing newline.
- `ReadEnv(dir) (map[string]string, error)` (last occurrence wins, like the Listener).
- Managed keys:
  | Key | Value |
  |---|---|
  | `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` | `true` |
  | `ACTIONS_RUNNER_HOOK_JOB_COMPLETED` | `<runnerDir>/ownci/job-completed.sh` (absolute) |
  | `OWNCI_RUNNER_ROOT` | `<runnerDir>` (absolute) |
  | `OWNCI_CLEANUP_IMAGE` | the pinned cleanup image reference |

### 2. Cleanup hook (`internal/hooks/job-completed.sh`, embedded with `embed`)
- Installed to `<runnerDir>/ownci/job-completed.sh` (dir 0700, file 0700). It runs **on the host** as the runner user, and its output appears in the GitHub job log. Keep output to one or two lines and never print env values.
- Behavior (bash, `set -euo pipefail`):
  1. `root="${OWNCI_RUNNER_ROOT:?}"`, `work="$root/_work"`. Sanity: `work` is an absolute path, exists, is a directory and is not a symlink. Otherwise print `ownci: cleanup skipped: unexpected work dir` and exit 1.
  2. Run **one** throwaway container:
     `docker run --rm --pull never --network none --label ownci.managed=true -v "$work:/w" "$OWNCI_CLEANUP_IMAGE" sh -c '<script>'` where the script:
     - deletes every entry in `/w` (mindepth 1, maxdepth 1) **except** `_temp` and `_PipelineMapping`. The runner manages and cleans these itself. `_PipelineMapping` is small metadata;
     - `chown -R <uid>:<gid> /w/_temp` (uid/gid of the runner user, from `id -u`/`id -g` on the host), so the runner's own per-job temp cleanup works on files the root job container left behind.
  3. Write `<root>/ownci/last-cleanup.json` `{"time":"…","result":"ok"|"failed","detail":"…"}`.
  4. Print `ownci: workspace cleaned` and exit 0. On any failure, print `ownci: cleanup FAILED (<short reason>)` and **exit 1**: the job fails visibly rather than silently leaving contamination (document this choice).
- **Cleanup image:** `busybox` pinned by **multi-arch index digest** (e.g. `busybox:1.37.0@sha256:…`). The implementer looks up the digest (`docker buildx imagetools inspect busybox:<tag>` or `curl` the registry) and records it in `internal/hooks/image.go` + the Implementation notes.

### 3. Wiring
- `isolation.Install(ctx, exec, runnerDir, image)`: write the hook script, `EnsureEnv`, then `docker pull <image>` (via execx; failure → error with hint).
  - Called by **setup** (t06's `installIsolation` insertion point) after a successful configure.
- `beforeLaunch` (t07) in `start`:
  1. `EnsureEnv` again (repairs tampering or a runner-rewritten `.env`), rewrite the hook script if its content differs from the embedded one, and log what changed.
  2. Run the hook script once before launching (cleans leftovers from a crash or kill mid-job). A failure aborts start with the hint `ownci doctor`.
  3. `Verify` and refuse to start if any **enforcement** item is not OK.
- `isolation.Verify(runnerDir) Report`: items `job container required` (`.env` value == `true`), `cleanup hook` (file present, 0700, content matches the embedded one), `cleanup image` (`docker image inspect` ok), `last cleanup` (from `last-cleanup.json`: ok / failed / never; informational).
- **status:** new section
  ```
  Isolation    container jobs required ✓   cleanup hook ✓   cleanup image ✓   last cleanup: ok at 10:05:41
  ```
  plus the same data in `--json`.
- **doctor:** an `isolation` check (only when a runner is configured): FAIL if enforcement or the hook is missing (hint: `ownci start` repairs it, or `ownci setup --force`), WARN if the image is missing (hint: `docker pull <image>`).
- **Docs string in setup's Next steps:** mention that job containers can still reach the network, and that `services:` / `docker://` actions run as sibling containers. Full docs come in t10.

## Out of scope
Container hooks (`ACTIONS_RUNNER_CONTAINER_HOOKS`), rootless/userns-remap Docker, network policies, resource limits (future). Ephemeral/JIT runners (future).

## Files
`internal/runner/{envfile.go,envfile_test.go}`, `internal/hooks/{hooks.go,image.go,job-completed.sh,hooks_test.go}`, `internal/isolation/{isolation.go,isolation_test.go}`, edits in `internal/setup/setup.go`, `internal/cli/{start.go,status.go,doctor.go}`, `internal/doctor/checks.go`.

## Tests (required)
- envfile: table tests: empty file; config.sh-style content (`LANG=C.UTF-8\nPATH=/usr/bin:/bin\n`) preserved; existing managed key with the wrong value replaced in place; duplicates removed; CRLF input; no trailing newline; the marker comment contains no `=`; mode 0600; idempotent (a second `EnsureEnv` → `changed == false`).
- hook script (bash test via `go test`): put a **fake `docker`** first on PATH. It records its args to a file, emulates `-v SRC:/w` by running the `sh -c` script with `/w` rewritten to `SRC`, and ignores the image. Then:
  - after the fake runner's marker files exist, the hook leaves `_work/_temp` and `_work/_PipelineMapping` and removes everything else;
  - args include `--network none`, `--pull never`, `--rm` and the pinned image;
  - fake docker exiting 1 → hook exits 1 and `last-cleanup.json` says failed;
  - `OWNCI_RUNNER_ROOT` unset or `_work` a symlink → exit 1, nothing deleted.
- End to end with the fake runner + supervisor (t07 test harness) + fake docker on PATH: start → simulated job → `LastJob.Result == "Succeeded"`, markers gone (except `_temp`), `last-cleanup.json` ok; simulated `nocontainer` job → `Failed` and the fake job log contains the "forbidden" message.
- start re-assert: delete `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` from `.env` and corrupt the hook file → `start` restores both (log says what changed). If `EnsureEnv` cannot write (read-only dir) → start refuses.
- `Verify`/status/doctor outputs for ok/missing/failed combinations.

## Manual verification (dev build + fakegh + **real Docker**, WSL integration ON)
```bash
make check && make build-dev
rm -rf /tmp/ownci-t08 /tmp/fakegh-t08.json
go run ./tools/fakegh --state /tmp/fakegh-t08.json & sleep 2; eval "$(go run ./tools/fakegh --print-env)"
O="./bin/ownci-dev --data-dir /tmp/ownci-t08"; N=t08; R=/tmp/ownci-t08/runners/$N
$O setup --repo me/demo --name $N           # pulls the busybox cleanup image
cat $R/.env                                 # config.sh lines preserved + managed block with the 4 keys
$O start --detach && $O status              # Isolation: container jobs required ✓ cleanup hook ✓ cleanup image ✓
# root-owned leftover, like a real job container would leave:
docker run --rm -v $R/_work:/w busybox sh -c 'mkdir -p /w/_tool/evil && touch /w/_tool/evil/rootfile'
ls -l $R/_work/_tool/evil                   # owned by root
echo build > $R/.fake-job-request; sleep 3
ls -A $R/_work                              # only _temp (and _PipelineMapping if present) remain
ls -ln $R/_work/_temp                       # owned by your uid, not 0
cat $R/ownci/last-cleanup.json              # result ok
echo "build2 nocontainer" > $R/.fake-job-request; sleep 2
$O status                                   # Last job build2 → Failed
grep -h forbidden $R/_diag/fake-job-*.log   # the runner's real rejection message
$O stop
sed -i '/ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER/d' $R/.env
$O start --detach && grep REQUIRE_JOB_CONTAINER $R/.env   # restored on start
$O stop; kill %1
```

## Definition of done
- [ ] Global DoD.
- [ ] Enforcement is re-asserted on every start, and start refuses to run without it.
- [ ] Cleanup removes root-owned files and never touches paths outside `<runnerDir>/_work`.
- [ ] Implementation notes: the pinned image + digest, the hook behavior, the `isolation` API, and an explicit list of **remaining isolation gaps** (network access, sibling containers via `services:`/`docker://`, shared Docker daemon, root inside the job container) for t10's docs.

## Implementation notes

**Pinned cleanup image** (`internal/hooks/image.go`, `hooks.CleanupImage`): `busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e` (multi-arch OCI *index* digest; looked up with `docker buildx imagetools inspect busybox:1.37.0` on 2026-10-15). The script was also run by hand against this exact image (busybox `find -mindepth/-maxdepth/-exec … {} +`, `chown -R` all work).

**APIs for later tasks**
- `runner.EnsureEnv(dir, managed map[string]string) (changed bool, err error)`, `runner.ReadEnv(dir)`, `runner.EnvPath(dir)`, `runner.EnvMarker` (`internal/runner/envfile.go`).
- `hooks.JobCompletedScript` (embedded), `hooks.Dir/JobCompletedPath/LastCleanupPath(runnerDir)`, `hooks.CleanupImage`.
- `isolation.Install(ctx, ex, runnerDir, image) error` (setup), `isolation.Repair(ctx, ex, runnerDir, image) (fixed []string, err error)` (start; also pulls a missing image), `isolation.Verify(ctx, ex, runnerDir, image) Report`, `isolation.RunCleanup(ctx, ex, runnerDir, image) error` (runs the hook once; a no-op when `_work` does not exist yet). t09 can run the same cleanup container for `--purge` by reusing `hooks.CleanupImage` (the hook script wipes everything except `_temp`/`_PipelineMapping`; purge needs *all* of `_work`, so it should run its own `docker run … rm -rf` with the same flags).
- `Report{JobContainerRequired, CleanupHook, CleanupImage Item{OK, Detail}; LastCleanup{Result ok|failed|never, Time, Detail}}`, `Report.EnforcementOK()` (job-container requirement + hook), `Report.Problems()`. `*isolation.Error{Msg, Hint}`.
- `doctor.IsolationCheck(ex, runnerDir, image)`; `statusReport.Isolation` + `isolationLine` in `internal/cli/status.go`; `testutil.UseFakeDocker(t) FakeDocker{Dir, Log}` + `.SetImagePresent`, `.Calls()` (fake `docker` first on PATH; env knobs `FAKE_DOCKER_RUN_FAIL`, `FAKE_DOCKER_PULL_FAIL`; inherited by re-executed supervisors).

**Hook behavior** (`internal/hooks/job-completed.sh`): validates `OWNCI_RUNNER_ROOT` (absolute) and `_work` (exists, directory, not a symlink) → else prints `ownci: cleanup skipped: unexpected work dir`, exit 1, deletes nothing and does not call docker. Then one `docker run --rm --pull never --network none --label ownci.managed=true -v <work>:/w <image> sh -c <script> sh <uid> <gid>`: removes every entry of `/w` except `_temp` and `_PipelineMapping`, then `chown -R uid:gid /w/_temp`. Writes `ownci/last-cleanup.json` (`ok`/`failed` + short detail) and prints one line. **Failure exits 1 on purpose**: the job fails visibly rather than silently leaving contamination (a failing cleanup also aborts the next `start`). A missing `_work` is *not* an error for the pre-launch cleanup (`RunCleanup` skips it) but is for the hook itself.

**Behavior decisions / deviations**
- `beforeLaunch` order: `Repair` → `Verify` (refuse if enforcement is not OK) → `RunCleanup` (the spec had cleanup before verify; verifying first avoids running a hook that is not trusted). Repairs are logged (`isolation repaired`) and printed as `ownci: <what>` (stderr, or the supervisor log when detached). A `beforeLaunch` error in the detached supervisor surfaces as phase `crashed` with `ExitCode == nil`; `failureMessage` then says "could not be started: <reason>" instead of "keeps crashing".
- `Repair` also pulls the cleanup image when it is missing (so a pruned image does not break every job). An image problem alone is not "enforcement" (Verify/doctor/status report it; `doctor` WARN).
- setup: isolation failures after registration say the runner is registered but not ready and give the exact `ownci setup --repo … --force` retry; `--dry-run` mentions the hook, `.env` and image pull; the Next steps text mentions network access and sibling containers.
- `status`/`status --json` get an `Isolation` section whenever the runner directory exists, not only while running. `doctor` adds the `isolation` check only when a runner is configured.
- Test infrastructure: `cli` tests now put a fake docker on PATH in `installRunner`, so the re-executed supervisor never touches real Docker.

**Remaining isolation gaps (for t10's README)**
1. **Network:** job containers use Docker's default bridge network and can reach the internet and the host's LAN; OwnCI sets no network policy.
2. **Sibling containers:** `services:` containers, `uses: docker://…` and Dockerfile actions run as *siblings* on the host's Docker daemon, outside the job container's boundary; (CORRECTION 2026-10-22: the runner does mount the Docker socket into the job container, see 00-context) these containers are not isolated from each other or constrained by OwnCI.
3. **Shared Docker daemon:** job containers share the host's daemon, image store and build cache with everything else on the machine; images/layers persist between jobs (only `_work` is wiped). With Docker Desktop on WSL the daemon is shared with Windows.
4. **Root in the job container:** containers run as root by default; files they create in the bind mounts (`_work`, `_work/_temp`, `_actions`, `_tool`) are root-owned on the host, which is why cleanup uses a container. The job container still has read-write access to those directories while the job runs.
5. **Host-run steps:** only jobs with `container:` are allowed, but the runner itself (and the job-completed hook) runs on the host as the user; a malicious workflow cannot run steps on the host, yet a compromised container image/daemon could. Public repos / fork PRs remain gated by D3.
6. **Hook timing:** unverified on real GitHub: whether the job-completed hook runs after the job container stops, and whether wiping `_work` (except `_temp`, `_PipelineMapping`) is safe for the runner (see progress.md open items for t10). Resource limits (CPU/memory/disk) are not enforced.

**Known limitations / TODOs**
- The REQUIRE flag is enforced by the Listener reading `.env` at startup; editing `.env` while the runner runs only takes effect at the next start (start re-asserts it every time).
- `Repair` rewrites the hook only when content or mode differ; it does not detect a hook replaced by a symlink to identical content (Lstat requires a regular file, so that *is* treated as tampering and replaced).

**Verification run (2026-10-15)**: `make check` green (new: `runner/envfile_test.go`, `hooks/hooks_test.go` with the fake docker, `isolation/isolation_test.go`, `cli/isolation_test.go` for end-to-end job + `nocontainer` rejection, repair-on-start, refusing to start with a read-only runner dir, status and doctor outputs, plus `TestForceRefusedWhileRunnerRuns` unaffected). Manual flow with `bin/ownci-dev` + `tools/fakegh` + **real Docker 29.5.3** (pulled the pinned busybox): setup wrote `.env` with the managed block and the cleanup hook; `start --detach`, `status` showed `Isolation … ✓ ✓ ✓`; a root-owned `_tool/evil/rootfile` and `_temp/rootdir/f` created by a busybox container were gone/chowned to uid 1000 after a simulated job (only `_temp` remained); `last-cleanup.json` ok; `build2 nocontainer` → `Failed` and the log held "Jobs without a job container are forbidden on this runner…"; after deleting the REQUIRE line, `start` restored it and logged `ownci: restored the isolation settings in .env`; `doctor` showed `[PASS] isolation`.
