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
