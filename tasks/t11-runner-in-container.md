# t11 — Runner-in-container mode (every job runs in a container, default image)

> Read `tasks/00-context.md` first (D1, D4, D5, D6, D10, §7), then `tasks/progress.md`, then the Implementation notes of t06–t10. **Also read `tasks/future.md` F2**: it holds the spike results this spec is built on. **Status: implemented 2026-10-22; see Implementation notes (they override the spec where they differ).**

## Goal
Today every job must declare `container:` or the runner rejects it (D1). Make the following possible **without changing any workflow file**:
- A job that declares `container:` runs in that image, exactly as today.
- A job that declares no `container:` runs in a **default image** (`ubuntu:24.04`-based), because the *runner itself* runs inside a container.
- The default image is chosen at `setup` and can be overridden for a session at `start`.

The mechanism is **runner-in-container**: `ownci start` launches the official runner inside a Docker container instead of as a host process. The existing host mode stays available and stays the default until the user decides otherwise (see Decisions to confirm).

## Why this mechanism (do not re-litigate)
- The official runner has no "default job container" setting, and the container hook (`ACTIONS_RUNNER_CONTAINER_HOOKS`) is **not called** for a job without `container:` (spiked 2026-10-22, runner 2.338.0). So hooks cannot supply a default image. Patching/forking the runner is rejected (D5: the runner stays official and self-updating).
- A spike ran the official runner image in a container with the host Docker socket and showed both job kinds working. See "Verified facts" below.

## Verified facts (from the spike; treat as ground truth, but re-verify in step 1 of Scope)
Official image `ghcr.io/actions/actions-runner:latest` = Ubuntu 24.04.5, runner 2.338.0, user `runner` (uid/gid 1001, in `sudo` with `NOPASSWD`, in `docker` group gid 123), Docker CLI present, **no** `dockerd`, no ENTRYPOINT/CMD (ARC starts it with `/home/runner/run.sh`), env `RUNNER_MANUALLY_TRAP_SIG=1`, `ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=1`.

1. **No-container job** ran inside the runner container: Ubuntu 24.04, uid 1001, `actions/checkout@v4` worked, Docker daemon reachable through the mounted socket, host `~/.ssh` not visible.
2. **Container job** (`container: ubuntu:24.04`) **failed** when only `_work` was mounted at its host path: `exec: "/__e/node24/bin/node": no such file`. Reason: the runner passes its own paths (`externals`, `_work`, `_temp`, `_actions`, `_tool`) to the **host** Docker daemon as bind-mount *sources*. Every such path must therefore exist **at the same absolute path on the host**. The failed attempt made the daemon create an empty `/home/runner/externals` on the host.
3. **Fix that worked:** run the runner **from a host directory mounted at the identical path** inside the container (the whole runner tree, i.e. what OwnCI already keeps in `runners/<name>/`), with the work dir inside it: `-v R:R -w R`, `config.sh … --work R/_work`, then `./run.sh`. The same container job then passed (root in the job container, workspace writable, socket present in the job container).
4. **Socket:** the host socket had gid 1001 on this machine (Docker Desktop + WSL); `--group-add 1001` gave the container user access. The gid differs per machine, so it must be detected (`stat -c %g /var/run/docker.sock` or `docker info`), never hard-coded.
5. **Ownership:** files written by the runner container are uid 1001; files written by container jobs are root. The host user (uid 1000) could not delete them (`rm -rf` failed). Deletion needs a container (same trick as the t08 cleanup hook).
6. **Session conflict:** re-registering/replacing a runner of the same name while an old session exists gives `Runner connect error: Error: Conflict. Retrying until reconnected.` for roughly a minute; the runner then connected by itself (it does not exit).
7. **Docker socket exposure:** the runner mounts `/var/run/docker.sock` into **every** job container already (verified; the README was corrected). Mounting it into the runner container therefore adds no new exposure for `container:` jobs, but it does mean no-container jobs also reach the host daemon.
8. **Not tested yet:** running as the host uid, ephemeral runners, runner self-update inside the container, the t08 cleanup hook inside the container, plain `ubuntu:24.04` (without the official image's dependencies) as the base, `--init` signal behavior, Docker Engine on native Linux (only Docker Desktop + WSL was used).

## Decisions to confirm with the user before implementing
Implement the **Proposed** option unless the user says otherwise; record the answers in `progress.md` → Decision changes.
1. **Default mode.** Proposed: keep host mode (D1) as the default for existing and new setups; runner-in-container is opt-in via `ownci setup --mode container-runner`. Later switch of the default is a separate decision.
2. **Default image.** Proposed: the official `ghcr.io/actions/actions-runner` image pinned by **tag + multi-arch index digest** (like `hooks.CleanupImage` in `internal/hooks/image.go`; look up with `docker buildx imagetools inspect`). It is Ubuntu 24.04 and already has the runner's dependencies (ICU, git, curl).
3. **Custom images.** Proposed: `--image <ref>` is accepted but must already contain the runner's dependencies; `doctor` verifies this by running a dependency probe in the image (see Scope 5). Building derived images is out of scope.
4. **Docker socket.** Proposed: always mounted into the runner container (needed for `container:`, `services:`, `docker://` actions). A future `--no-docker` mode that omits it is in `future.md`, not here.
5. **Run as which user.** Proposed: try `--user <host uid>:<host gid>` first so files in the runner dir stay owned by the host user; if the runner or checkout misbehaves without a passwd entry/home, fall back to the image user (uid 1001) plus `chown`/cleanup via a root container. Step 1 must decide and record which one works.

## Depends on / current state
- t04 download/verify/extract into `runners/<name>/`; t06 `setup` (config.sh on the host, writes `config.json` with `Isolation: "docker-job-container"`); t07 `internal/supervisor` (process supervision, state file, stdout markers, `Stop`, orphan sweep `orphans.go`); t08 `.env` manager + cleanup hook + `isolation.Verify/Repair`; t09 `uninstall`/`logs`; `internal/docker` (`Check`), `internal/execx` (+ `Fake`).
- `config.RunnerConfig.Isolation` is a free string today. Use new values, do not repurpose the old one.

## Scope

### 1. Mini-spike first (before any production code; record results in Implementation notes)
With a throwaway runner on a private sandbox repo (as in `tasks/future.md` F2), answer the "Not tested yet" items that decide the design: (a) host-uid vs image-uid; (b) the t08 hook script running inside the container (it calls `docker run` for the wipe: does the bind path resolve?); (c) plain `ubuntu:24.04` vs the official image; (d) `docker run --init` + SIGINT → graceful runner stop; (e) runner self-update inside the container (does it persist in the mounted dir and restart cleanly?); (f) native Linux Docker Engine if available. If (a)–(e) cannot be made to work, stop and report; do not build on a guess.

### 2. Config and setup (`internal/config`, `internal/setup`, `internal/cli/setup.go`)
- `RunnerConfig` gains optional `Mode` (`"host"` default / `"container-runner"`) and `Image` (string, pinned ref). Absent = host mode, so existing `config.json` files keep working (bump `schemaVersion` only if the loader requires it).
- `ownci setup --mode container-runner [--image <ref>]`. In container-runner mode setup still downloads, verifies and registers on the host exactly as today (D4, D5 unchanged), then: pulls the image (digest-pinned), records mode/image, installs the `.env` **without** `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` (jobs without `container:` must be accepted). `Isolation` value: `"docker-runner-container"`.
- `--dry-run` shows the image pull and the mode.

### 3. Launching the runner (`internal/supervisor`, `internal/cli/supervise.go`, new `internal/runnercontainer`)
- Build the `docker run` argument list in one tested function: `--name ownci-<runner-name>` (fixed, so it can be found and removed), `-v R:R -w R` with `R` the **resolved absolute, symlink-free** runner dir, `-v /var/run/docker.sock:/var/run/docker.sock`, `--group-add <detected socket gid>`, `--init`, the user decided in Scope 1, `-e RUNNER_MANUALLY_TRAP_SIG=1 -e ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1`, then `<image> ./run.sh`. No `-d`, no `--rm` surprises: the supervisor keeps `docker run` attached so stdout markers (t07 `ParseLine`) and exit codes work unchanged.
- The supervisor spawns `docker run …` as the "runner process" in container-runner mode. `Options` gains the command builder so tests can substitute the fake runner; keep host mode untouched.
- **Before every launch:** `docker rm -f ownci-<name>` (replaces the t07 `/proc` orphan sweep in this mode, and removes the "leftover listener holds the session" problem by construction). Stop = signal the attached `docker run` (SIGINT, same grace as t07), then `docker rm -f` after the grace period.
- Fail fast, with `UserError` + hint, if the runner dir is not a plain absolute path the daemon can mount: symlinked path, `/mnt/<drive>` (Docker Desktop cannot bind these reliably; see §7 "Docker Desktop + WSL"), or not under a location Docker Desktop shares.
- The job-completed hook (t08) runs inside the container; keep it working (Scope 1b decides whether it needs changes).

### 4. Isolation policy in this mode (`internal/isolation`)
- `Verify`/`Repair` become mode-aware: in container-runner mode the **enforcement** items are: runner is launched only via the container path (no host `run.sh` start), `.env` does not weaken anything else, cleanup hook + cleanup image present. `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` is deliberately **absent**.
- `start` must refuse to start the host process for a container-runner config and vice versa (one code path per mode, selected from config).

### 5. `doctor`, `status`, `logs`, `uninstall`
- `doctor`: in container-runner mode check the image is present, runnable and has the runner dependencies (run `docker run --rm --entrypoint bash <image> -c 'ldconfig -p | grep -q libicu && command -v git'` or the equivalent decided in Scope 1c), the socket gid is detectable, and the runner dir path is mountable. Host-mode ICU check is skipped in this mode only if setup was done in-container (registration still runs `config.sh` on the host in this spec, so keep the ICU prerequisite).
- `status`: show `Mode`, `Image`, and the container name/state (`docker inspect`); keep all existing lines.
- `logs`: the runner log source continues to work (supervisor still writes `logs/runner-<name>.log` from the attached stdout); add nothing else.
- `uninstall`: stop → `docker rm -f` the container → existing flow. `--purge` already deletes root-owned files with a container (t09); verify it also handles uid 1001 files.

### 6. Docs
- README: new section "Run every job in a container (no `container:` needed)": what it does, how to enable, the **path-identity requirement**, and the honest security note (Docker socket in the runner container, so a job controls the host daemon; this is the same exposure container jobs have today). Keep the host-mode text accurate.
- `tasks/00-context.md`: add a D13 (see below) and update §7 with the verified facts above. Update `docs/e2e.md` with the manual steps (Verification).

## Proposed new decision (add as D13 once the user confirms)
| D13 | **Runner-in-container mode (opt-in):** `setup --mode container-runner` runs the official runner inside a container from a pinned image; jobs without `container:` run in that image, jobs with `container:` use theirs. Host mode (D1) remains. | Spiked 2026-10-22: container hooks are not called for no-container jobs; runner-in-container with identical host paths works for both job kinds. |

## Out of scope
- Ephemeral per-job containers (needs a registration token per job; conflicts with D4). Note it in `future.md` if still wanted.
- Building derived images, a `--no-docker`/no-socket mode, resource limits, network policy, systemd.
- Making container-runner the default.
- Windows/macOS.

## Files (expected)
`internal/runnercontainer/` (new: arg builder, socket-gid detection, path checks), changes in `internal/supervisor/`, `internal/cli/{setup,start,supervise,status,stop,uninstall}.go`, `internal/setup/`, `internal/isolation/`, `internal/config/`, `internal/doctor/`, `internal/hooks/image.go` (a second pinned image constant if the runner image is pinned here), README, `docs/e2e.md`, `tasks/00-context.md`.

## Tests (required, no network/Docker/real GitHub, per D12)
- `runnercontainer`: arg builder (golden test for the exact `docker run` argv incl. group-add, user, mounts, name), socket-gid detection with `execx.Fake`/temp files, path validation (symlink, `/mnt/c`, relative).
- `setup`: container-runner mode records `Mode`/`Image`, writes `.env` without the require-container key, pulls the image (fake docker), `--dry-run` output; host mode unchanged (existing tests stay green).
- `supervisor`: with a fake `docker` binary on PATH that execs the fake runner, start/stop/restart, `docker rm -f` before launch, stop escalation, state/markers identical to host mode.
- `isolation`/`doctor`/`status`: mode-aware Verify/Repair and output; host mode regression tests.
- `uninstall`: removes the container, purge works with root/uid-1001 files (fake docker).

## Manual verification (real Docker; then real GitHub on a private sandbox repo)
Reuse the probe workflows from the spike (`runs-on: [<label>]`; one job without `container:`, one with `container: ubuntu:24.04`; each prints `/etc/os-release`, `id`, workspace write test, `ls -ld /var/run/docker.sock`; the no-container one also checks that the host `~/.ssh` is not visible).
1. `ownci setup --repo <sandbox> --mode container-runner`, `ownci start --detach`, `ownci status --remote` shows mode/image/container and `online, idle`.
2. Both probe jobs pass; after them `_work` is wiped by the hook and `ownci status` shows `last cleanup: ok`.
3. `ownci stop` removes the container; `start` again keeps the same runner id (no re-registration); `kill -9` the supervisor, then `start` recovers with no leftover container or session conflict loop.
4. `ownci uninstall --unregister --purge --yes` leaves no container, no registration, no data dir, and no stray directories on the host (e.g. `/home/runner`).
5. Host mode still works end to end (`setup` without `--mode`).

## Definition of done
Global DoD (see `00-context.md` §6) plus: [ ] Scope 1 mini-spike results written in Implementation notes with the decisions it forced; [ ] the five "Decisions to confirm" answered and recorded; [ ] host mode untouched and its tests green; [ ] README states the Docker-socket exposure and the path-identity requirement; [ ] no stray host directories after a failed or successful run.

## Known risks to watch
- Path identity breaks on `/mnt/c`, symlinked homes, or custom Docker Desktop sharing setups: fail fast with a clear message, never silently mount an empty directory.
- A different image uid/gid than the host user: file ownership in the runner dir; never `chmod -R a+rwX` as a fix.
- The runner's self-update replaces files in the mounted runner dir while the container runs: confirm in Scope 1e that the restart-after-update path works inside the container.
- Same-name session conflict after a quick restart: rely on the runner's own retry, do not treat the first "Conflict" line as terminal in this mode (check how t07's `PhaseDuplicateSession` handling interacts; the spike saw the runner reconnect by itself after ~1 minute).

## Implementation notes

Implemented 2026-10-22. `make check` passes; real-GitHub e2e on private `ashfaqc137/blog` (PR #4) passed, see "Verification" below.

**Decisions to confirm (answered; user: "do the revamp with t11 design")**
1. Default mode: **host** stays the default; `setup --mode container-runner` opts in. `setup --force` without `--mode` keeps the current mode; `--mode host` switches back.
2. Default image: `ghcr.io/actions/actions-runner:2.338.0@sha256:4ffadc00…` (multi-arch index digest), `runnercontainer.DefaultImage`. Only its OS and tools are used; the runner runs from the mounted runner dir, so it keeps self-updating there (D5).
3. Custom images: `--image` at `setup` (recorded) or `start` (this session only). `setup` and `doctor` probe the image (`runnercontainer.Probe`: `setpriv useradd groupadd usermod getent stat` + ICU).
4. Docker socket: always mounted into the runner container.
5. User: **forced by the data dir's permissions**, not a preference. `config.Paths` makes the data dir `0700` and the hook/`.credentials` are host-owned, so the image's uid 1001 cannot even read the runner dir. The container starts as root; an inline entrypoint (`internal/runnercontainer/entrypoint.sh`, passed with `bash -c`, never written to the mounted dir) creates/reuses a passwd entry for the **host uid/gid**, adds it to the group that owns the socket (gid detected inside the container) and to sudoers (NOPASSWD, because workflows written for GitHub-hosted runners use `sudo apt-get`), rewrites `.path`, unsets `ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT`, then `setpriv --reuid/--regid --init-groups` execs `./run.sh --once`.

**Mini-spike results (Scope 1)**
- (a) host uid: works with the entrypoint above (`uid=1000(ubuntu)` in the image, files host-owned, `sudo -n true` ok, socket usable). Plain `--user 1000` would lack sudo and a home.
- (b) the t08 hook runs unchanged inside the container (same paths, docker CLI + socket, busybox on the shared daemon). A read-only bind mount of the hook file over itself blocks writes (EROFS) and renames (EBUSY).
- (c) only the official image was used; plain `ubuntu:24.04` lacks ICU (the probe reports it).
- (d) `docker run --init` + the CLI's signal proxy: SIGINT reaches `run.sh` (graceful, 0.2 s); exit codes propagate (7 → 7); Docker's own failures are 125 (image missing, name conflict), 126/127.
- (e) self-update inside the container: **not tested** (2.338.0 is the latest release, so GitHub offered no update). The runner dir is mounted read-write and owned by the uid the runner runs as, so the update path is the same as on the host; recorded as an open item.
- (f) native Docker Engine: not available on this machine; only Docker Desktop + WSL2 was used.

**Deviations from the spec, and additions**
- **Fresh container per job** (not in the spec): the runner runs `run.sh --once` and exits after its job; the supervisor relaunches it (`Options.RecycleAfterJob`) without counting a restart. Without it, everything a no-container job changes outside `_work` (`apt-get install`, `/tmp`, `$HOME`) would leak into the next job, breaking D10. `--once` is deprecated by the runner → `future.md` F3.
- `.path`: `config.sh` saves the host's `PATH` (WSL: `/mnt/c/...`) in `.path`; the entrypoint rewrites it with the container's.
- `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` is **removed** from `.env` (new `runner.EnsureEnv(dir, managed, absent...)`), so switching modes in either direction is repaired by `Repair`.
- Socket gid is detected **inside** the entrypoint, not on the host (Docker Desktop's socket in the VM is what is mounted).
- Leftover container: `runnercontainer.Remove` is `docker stop --signal SIGINT --time 30` then `docker rm --force`, before every launch, after every exit (`Options.AfterExit`), on `stop` (also when not running), `stop --force` (`StopOptions.AfterKill`) and `uninstall`. The graceful stop was added after the e2e kill test: a container that is only force-removed leaves its GitHub session behind (see Verification).
- `Options.LaunchFailed` (docker 125–127 before any "Connected" line) ends the run in `crashed` at once with Docker's message, instead of three retries.
- The orphan sweep (`killOrphans`) now runs **before** `BeforeLaunch`, so the workspace is never cleaned while an orphaned runner may still use it (both modes).
- `isolation.Policy{Mode, CleanupImage, RunnerImage}` replaces the image string in `Install/Repair/Verify`; `isolation.Report` gains `mode`, `runnerImage`, `image`, and `jobContainerRequired` is now set in host mode only (JSON: omitted in container mode).
- Image helpers moved to `internal/docker/images.go` (`ImagePresent`, `Pull`, `EnsureImage`; pull timeout 20 min for the ~2 GB image).
- `config.Load` rejects an unknown `mode` and container mode without `image`; `schemaVersion` unchanged (both fields are `omitempty`, absent = host).

**Pre-existing bugs found by the e2e and fixed here** (both from t06; the fakes had hidden them)
- `setup --force` on a registered runner always failed against the real runner: `config.sh` refuses an existing `.runner` *even with* `--replace` ("Cannot configure the runner because it is already configured"; `--replace` only replaces a same-named runner on GitHub). This also broke the `registration_lost` hint and mode switching. Fix: `runner.SetAsideRegistration` moves `.runner`/`.credentials*` aside before `config.sh` and restores them if registering fails (`setup.reconfigure`). The fake `config.sh` now refuses like the real one. Verified for real: the re-registration kept runner id 24.
- `setup --force` without `--labels` dropped the extra labels (e.g. the per-developer label from the README's team setup). Now `--labels` not given keeps the configured ones; `--labels ""` clears them.

**New exported APIs**
- `config.ModeHost`, `config.ModeContainerRunner`, `config.ValidMode(m)`, `(*RunnerConfig).EffectiveMode()`; fields `Mode`, `Image`.
- `runnercontainer.DefaultImage`, `ContainerName(runner)`, `Args(Spec{Runner, RunnerDir, Image, UID, GID}) []string`, `CheckPath(dir) error`, `Remove(ctx, ex, runner) error`, `Inspect(ctx, ex, runner) (Info, error)` / `ErrNoContainer`, `Probe(ctx, ex, image) (warnings []string, err error)`, `LaunchFailed(code) bool`.
- `isolation.Policy`, `HostPolicy()`, `PolicyFor(rc, imageOverride)`; `Install/Repair/Verify(ctx, ex, dir, Policy)`.
- `supervisor.Options{Command, AfterExit, LaunchFailed, RecycleAfterJob}`, `supervisor.LaunchFailedPrefix`, `StopOptions.AfterKill`.
- `doctor.RunnerContainerCheck(ex, runnerDir, image)`; `doctor.IsolationCheck(ex, dir, Policy)`.
- `setup.Options{Mode, Image}` (and `Labels == nil` = keep), `setup.IsolationRunnerContainer`.
- `runner.SetAsideRegistration(dir) (restore func() error, discard func(), err error)`.
- CLI: `setup --mode --image`, `start --image`, hidden `__supervise --image`; `status` adds a `Mode` row (+ `mode`, `image`, `container` in JSON).

**Tests added** (all offline, D12): `runnercontainer` (golden `docker run` argv, path checks incl. symlink and `/mnt/c`, entrypoint syntax and root refusal, Remove/Inspect/Probe); `config` (modes, legacy config); `runner` (absent keys); `isolation` (container policy, mode switching, `PolicyFor`); `setup` (container mode, mode kept on `--force`, switch back, custom image + probe failure, flag validation, symlinked data dir, dry run); `supervisor` (recycle after each job, exit-after-job without recycle is a crash, launch failure not retried, `AfterKill`); `cli/container_test.go` end to end with the fake docker (leftover container removed before launch, no-container job succeeds and the runner is relaunched, status/stop, `--image` override, launch failure, `--image` in host mode, `stop` removes a leftover, doctor); `uninstall` (container removed; host mode never calls docker); `e2e` shape test for `ownci-default-image.yml`. Fakes: the fake `docker` emulates runner containers (`run` with the `ownci-entrypoint` marker, `stop`, `rm`, `inspect`, the probe, `FAKE_DOCKER_RUNNER_EXIT`); the fake `run.sh` honors `--once`.

**Custom runner image follow-up (2026-10-22)**
- The probe now also **requires the `docker` CLI**: the cleanup hook and `container:` jobs use it inside the runner container, so an image without it would have passed `setup` and then failed every job at cleanup. `git` and `sudo` are now **recommended**: `Probe` returns warnings for them (`setup` prints them, `doctor` turns them into WARN). `Probe(ctx, ex, image) (warnings []string, err error)`.
- `setup` pulls and probes the runner image **before** requesting a registration token (`setup.prepareImage`), so a bad `--image` leaves nothing registered on GitHub or locally.
- `isolation.Install` pulls only images that are missing (`docker.EnsureImage`), so a locally built `--image` works.
- Example image `e2e/images/ubuntu22.Dockerfile` (Ubuntu 22.04 + the runner's dependencies from `installdependencies.sh`, git, sudo, the static docker CLI 27.5.1).
- Real GitHub, private `ashfaqc137/blog`, PR #5 (closed, branch deleted): plain `ubuntu:22.04` refused with `lacks: docker libicu` (+ git/sudo warnings) and 0 runners on GitHub; the built image (no uid 1000 in it, so the entrypoint created user `ownci`): doctor `[PASS] runner-container`; ownci-pass ✓, ownci-default-image ✓ (`Ubuntu 22.04.5 LTS`, `uid=1000(ownci) … 1001(ownci-docker)`, `docker server 29.5.3`, all markers absent in `second`), ownci-poison ✓. Then `start --image <default>` for one session: `status` showed `container …: running (image ghcr.io/actions/actions-runner:…)`, the job asserted Ubuntu 24.04 ✓ (all 3 runs green); a plain `start` afterwards was back on Ubuntu 22.04. Uninstall clean.

**Known limitations / TODOs** (also in `progress.md` open items)
- Runner self-update inside the container is not exercised (no newer runner than 2.338.0 existed). `future.md` F3: `--once` is deprecated. F4: jobs without `container:` can read/modify the runner dir (incl. `.credentials*`).
- Only Docker Desktop + WSL2 was used; native Docker Engine and rootless Docker are untested (rootless would change the uid picture).
- `status` checks the configured image; a session started with `start --image` is visible in the `Mode` row (`container …: running (image …)`), not in the Isolation row.
- A job that is still running when the user stops the runner is cancelled, as in host mode.

**Verification** (2026-10-22)
- `make check`: passes (gofmt, vet incl. `-tags dev`, `go test -race ./...`, both builds).
- Real GitHub, release build, private `ashfaqc137/blog`, PR #4 (closed, branch deleted); evidence table in `docs/acceptance.md` → "t11: container-runner mode":
  ```
  ./bin/ownci --data-dir ~/ownci-e2e-t11 setup --repo ashfaqc137/blog --mode container-runner --name ownci-t11-e2e --labels t11e2e
  ./bin/ownci --data-dir ~/ownci-e2e-t11 start --detach && ./bin/ownci --data-dir ~/ownci-e2e-t11 status --remote
  # PR with ownci-pass, ownci-default-image, ownci-poison (pull_request, runs-on: [ownci, t11e2e]): 5 jobs, all green,
  # each in a fresh runner container (~7 s between jobs, restarts 0, last cleanup ok)
  stop / start (id 24 kept); kill -9 supervisor → start recovers in 8 s (after the graceful-stop fix); re-run: 3/3 green
  setup --force --mode host (after the config.sh fix) → pass ✓, poison ✓, nocontainer ✗ and default-image ✗ with
  "Jobs without a job container are forbidden on this runner"
  setup --force --mode container-runner; uninstall --unregister --purge --yes → no container, no registration,
  no data dir, no /home/runner; no tokens in the data dir before purge
  ```
