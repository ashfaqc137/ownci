# OwnCI — Shared Context for All Tasks

> **Read this file before starting any task (`tNN-*.md`).** It holds the decisions, layout, conventions and runner/GitHub facts that every task depends on. Task files reference this document instead of repeating it.
> The full product requirements are in `/plan.md` (the PRD). Where this file and the PRD differ, **this file wins**: it records decisions made after the PRD was written.

---

## 1. What OwnCI is

OwnCI is an open-source Go CLI (`ownci`) that turns a developer's machine into a GitHub Actions self-hosted runner for one repository, safely and with one command. It does **not** implement a CI protocol. It downloads, registers, supervises and cleans up the **official** `actions/runner`, and configures it so that every job runs inside a Docker container.

MVP target: **Linux inside WSL2** (and therefore native Linux), amd64 and arm64, GitHub Actions, Docker.

Success = a real PR check in GitHub runs on the developer's machine inside a job container, and a job without a container is rejected. That is verified in t10. Since t11 (D13) a runner can instead run inside a container, which jobs without `container:` use as their image.

---

## 2. Decision record (binding)

| ID | Decision | Rationale |
|----|----------|-----------|
| D1 | **Execution model:** the official runner runs on the host, managed by OwnCI. OwnCI sets `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`, so every job **must** declare `container:`. Jobs without one fail with *"Jobs without a job container are forbidden on this runner…"*. | Verified in runner source (`src/Runner.Worker/JobExtension.cs` → `ValidateJobContainer`; not gated on container hooks). It makes the isolation boundary explicit and testable. |
| D2 | **Opt-in label:** register with `--no-default-labels --labels ownci[,extra…]`. Workflows target the runner with `runs-on: [ownci]`. | Without this, any `runs-on: self-hosted` workflow could land on a developer laptop by accident. |
| D3 | **Trust gate:** `setup` refuses **public** repositories unless `--allow-public-repo` is passed, and prints a warning even then. | Fork PRs on public repos can run arbitrary code on self-hosted runners. |
| D4 | **No persisted GitHub token.** Token resolution order: `GITHUB_TOKEN` → `GH_TOKEN` → `gh auth token`. A token is needed only for `setup`, `uninstall --unregister` and `status --remote`. After `config.sh`, the runner holds its own credentials. | Smaller blast radius. The registration token is single-use and short-lived. |
| D5 | **Runner version:** OwnCI pins a version + SHA256 per arch for the **initial download** and verifies it before extraction. After that, the runner's built-in **self-update stays enabled** (no `--disableupdate`). `status` reports the version actually installed. | GitHub refuses jobs to runners that fall too far behind, so a hard pin would break users. |
| D6 | **Process model:** `ownci start` runs in the **foreground** by default (Ctrl-C = graceful stop). `--detach` runs the same supervisor in the background. **No systemd** in the MVP. | Simple and observable, and it works on WSL without systemd. |
| D7 | **No speculative abstractions:** one provider (GitHub) and one backend (Docker), so no provider/backend interfaces or plugin registries. Use package boundaries (`internal/github`, `internal/docker`). Small interfaces are allowed only for **test seams** (e.g. `execx.Runner`, HTTP base URL). | PRD principle "introduce interfaces only where implementations genuinely vary". |
| D8 | **Dev-only overrides:** a binary built with `-tags dev` (`make build-dev`) honors `OWNCI_DEV_GITHUB_API_URL`, `OWNCI_DEV_GITHUB_WEB_URL`, `OWNCI_DEV_RUNNER_URL`, `OWNCI_DEV_RUNNER_SHA256`. Release builds (`make build`) **must not** compile this code. The dev build prints a one-line `DEV BUILD: overrides active` warning to stderr when any override is set. | Lets the user drive the real CLI end to end against `tools/fakegh` + a fake runner without real GitHub. |
| D9 | **Platforms:** Linux only (incl. WSL2), `amd64` → runner `linux-x64`, `arm64` → runner `linux-arm64`. Anything else fails with a clear "unsupported platform" error. | PRD phase 1/2. |
| D10 | **Per-job cleanup:** the runner's job-completed hook (`ACTIONS_RUNNER_HOOK_JOB_COMPLETED`) runs an OwnCI script that wipes the job workspace, `_actions` and `_tool` under the runner's `_work` dir, using a throwaway container so root-owned files can be removed. | Job containers mount `_work`, `_actions`, `_tool`, `_temp` read-write and run as root by default. On a persistent runner, one job could otherwise poison the next. |
| D11 | **One runner per data dir for the MVP**, but the on-disk layout is per-name (`runners/<name>/`) so multiple runners can come later without migration. | Cheap future-proofing. |
| D12 | **Testing:** unit tests + fakes only (no network, no Docker, no real GitHub) through t09. Real GitHub E2E happens in **t10** only. | User decision. |
| D13 | **Runner-in-container mode (opt-in, t11):** `setup --mode container-runner` runs the official runner inside a container from a pinned image (`ghcr.io/actions/actions-runner`, digest-pinned in `internal/runnercontainer`). The runner directory is mounted at the **same path**, the Docker socket is mounted, the entrypoint maps the container user to the **host uid** (with passwordless sudo) and the runner runs `run.sh --once`, so every job gets a **fresh container**. Jobs without `container:` run in that image; jobs with one use theirs. `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` is removed from `.env` in this mode. Host mode (D1) stays the default. | Container hooks are not called for no-container jobs (spiked 2026-10-22), so a default image needs the runner itself in a container. Same-path mounts are required because the runner hands its own paths to the host daemon. Per-job recycling keeps D10's "nothing persists" true for no-container jobs. |

---

## 3. Repository layout (target state after t10)

```
ownci/
├── go.mod                      # module github.com/ashfaqc137/ownci
├── Makefile
├── README.md                   # t10
├── plan.md                     # PRD (do not edit)
├── tasks/                      # these specs (append Implementation notes only)
│   └── progress.md             # status + cross-task open items (updated every task)
├── cmd/ownci/main.go           # tiny: calls internal/cli.Execute()
├── internal/
│   ├── buildinfo/              # Version, Commit, Date (ldflags)          t01
│   ├── cli/                    # cobra commands, one file per command      t01+
│   ├── platform/               # OS/arch/WSL detection                    t02
│   ├── config/                 # data-dir resolution, Paths, config.json  t02
│   ├── logging/                # slog setup + secret redaction            t02
│   ├── execx/                  # command-execution seam + fake            t03
│   ├── docker/                 # docker CLI checks + cleanup container    t03, t08
│   ├── doctor/                 # check framework + checks                 t03
│   ├── runner/                 # versions, download, extract, install,
│   │                           # config.sh wrapper, .runner, .env         t04, t06, t08
│   ├── github/                 # REST client, token resolution            t05
│   ├── setup/                  # setup flow orchestration                 t06
│   ├── supervisor/             # lock, pidfile, state machine, start/stop t07
│   ├── hooks/                  # embedded hook scripts + cleanup image    t08
│   ├── isolation/              # install/verify enforcement + cleanup     t08
│   ├── runnercontainer/        # container-runner mode: docker run args,
│   │                           # entrypoint, path checks, image probe     t11
│   ├── uninstall/              # stop/unregister/purge                    t09
│   ├── logtail/                # tail / follow for `ownci logs`           t09
│   └── testutil/               # fake runner tarball, helpers             t04+
│       └── fakegh/             # fake GitHub API http.Handler             t05
├── tools/fakegh/               # dev fake GitHub API + fake runner server t05
├── docs/e2e.md                 # t10
└── e2e/workflows/*.yml         # t10
```

Commands in the final CLI: `version`, `doctor`, `setup`, `start`, `stop`, `status`, `logs`, `uninstall` (plus hidden `__supervise`, t07).

---

## 4. Data directory

Resolution precedence (t02): `--data-dir` flag → `OWNCI_DATA_DIR` env → `$XDG_DATA_HOME/ownci` → `~/.local/share/ownci`.

```
<data-dir>/
├── .ownci-data-dir           # marker file; uninstall --purge refuses to delete without it
├── config/config.json        # 0600; OwnCI config (no secrets)
├── logs/ownci.log            # OwnCI diagnostics (redacted)
├── logs/runner-<name>.log    # runner stdout/stderr captured by supervisor (t07)
├── cache/                    # downloaded runner archives (verified)
├── run/<name>.lock           # flock held by the running supervisor (t07)
├── run/<name>.pid            # supervisor pid JSON (t07)
├── run/<name>.state.json     # last known runner state (t07)
└── runners/<name>/           # extracted official runner (t04)
    ├── config.sh, run.sh, bin/, externals/ …
    ├── .runner, .credentials* # written by config.sh (runner-owned secrets; never read/log)
    ├── .env                   # env loaded by Runner.Listener at startup (t08)
    ├── _work/                 # job work dir (--work _work)
    ├── _diag/                 # runner's own diagnostic logs
    └── ownci/                 # OwnCI-managed files inside runner dir (hooks, t08)
```

Permissions: directories `0700`, files containing config `0600`. The data dir should live on the Linux filesystem, not `/mnt/c/...` (WSL), for performance and permissions. `doctor` warns about this.

`config/config.json` (schema owned by t02, filled by t06):
```json
{
  "schemaVersion": 1,
  "runner": {
    "name": "ownci-myhost",
    "provider": "github",
    "repo": "owner/name",
    "url": "https://github.com/owner/name",
    "labels": ["ownci"],
    "installedVersion": "2.xxx.x",
    "isolation": "docker-job-container",   // "docker-runner-container" in container-runner mode
    "mode": "container-runner",            // t11; absent = "host"
    "image": "ghcr.io/actions/actions-runner:…@sha256:…",  // container-runner mode only
    "createdAt": "RFC3339"
  }
}
```
`runner` is null/absent until setup completes.

---

## 5. Conventions

- **Go version:** the one installed (`go version` → 1.27.x). `go.mod` `go` directive matches.
- **Dependencies:** `github.com/spf13/cobra` and the standard library. Anything else needs a justification in the task's Implementation notes. Use plain `net/http` for GitHub, not go-github.
- **Package boundaries:** `internal/cli` is the only package that prints user-facing output or reads flags. Other packages return values and errors and take dependencies through structs/options (e.g. an `execx.Runner`, an `io.Writer`, a `*http.Client`, a base URL).
- **Errors:** wrap with `fmt.Errorf("doing X: %w", err)`. User-facing failures include an actionable hint. Use a small `cli.UserError{Msg, Hint}` type (introduced in t01) that the root command renders as `Error: <msg>\nHint: <hint>`.
- **Exit codes:** `0` OK, `1` operational failure, `2` usage error / not implemented, `3` `doctor` found a FAIL check.
- **Logging:** `log/slog` (t02). User output goes to stdout/stderr through the cli layer. Diagnostics go to `logs/ownci.log`. **All log output passes through the redactor.**
- **Secrets:** never log, print or persist GitHub tokens, registration/removal tokens, or runner credentials. When printing a command line for `--dry-run` or debugging, replace the token argument with `***`.
- **Shelling out:** every external command (`docker`, `config.sh`, `gh`, `ldconfig`…) goes through `internal/execx` (t03) so tests can fake it. Exception: the supervisor (t07) uses `os/exec` directly for the long-running `run.sh` process, because it needs process groups and signals.
- **Filesystem writes:** write to a temp file + `os.Rename` for anything important (config, state, `.env`).
- **Tests:** table-driven where natural. Use `t.TempDir()`, `httptest`, and fake scripts written in tests. **No network, no Docker, no real GitHub** in `go test ./...`. Tests that need `bash` may assume it exists (Linux only).
- **Style:** `gofmt`, `go vet` clean. Keep files and functions small. Comments explain *why*, not *what*.
- **Platform guard:** code may assume Linux. Use `//go:build linux` where syscalls (flock, setsid, Setpgid) are used.

---

## 6. How a task is executed (process for the implementing LLM)

1. Read `tasks/00-context.md` (this file), then `tasks/progress.md` (status, open items addressed to your task, decision changes), then the task file, then the **Implementation notes** sections of all earlier task files. Decision changes and notes override the original spec text.
2. Inspect the current repo state. Don't assume earlier tasks were implemented exactly as specified.
3. Implement **only** the task's scope. If something outside scope blocks you, do the minimum and record it.
4. Write the required tests. Run `make check` until it passes.
5. Run the task's **Manual verification** steps yourself where possible (anything not needing real GitHub or Docker) and report the actual output.
6. Append an `## Implementation notes` section to the **task file** with: deviations from the spec and why, new exported APIs later tasks should use (signatures), known limitations / TODOs, and the exact commands you ran to verify.
7. Update `tasks/progress.md`: set your row's **Implemented** to ✅ (or ⚠️ with a one-line reason in Notes), tick the open items you resolved (name the task), add new open items for later tasks, record any decision change, and fill in Environment notes where relevant. **Never** touch the **Verified** or **Commit** columns; those belong to the user.
8. Do not commit unless the user asks. Suggested commit message: `tNN: <title>`.

### Global definition of done (applies to every task)
- [ ] `make check` passes (gofmt clean, `go vet`, `go test -race ./...`, build).
- [ ] All task-specific tests exist and pass.
- [ ] The task's manual verification steps produce the expected output.
- [ ] No secrets are written to logs, stdout or config.
- [ ] `## Implementation notes` appended to the task file.
- [ ] `tasks/progress.md` updated (status row, open items, decision changes).

---

## 7. Facts reference (verified against `actions/runner` source, Oct 2026)

### Runner release artifacts
- Latest release: `gh api repos/actions/runner/releases/latest --jq .tag_name` → `vX.Y.Z`.
- Asset URL: `https://github.com/actions/runner/releases/download/v<ver>/actions-runner-<os>-<arch>-<ver>.tar.gz`, with `<os>-<arch>` ∈ `linux-x64`, `linux-arm64`.
- SHA256 values are in the release body between markers, e.g. `<!-- BEGIN SHA linux-x64 -->…<!-- END SHA linux-x64 -->`. Extract with:
  `gh api repos/actions/runner/releases/tags/v<ver> --jq .body | grep -o 'BEGIN SHA linux-x64 -->[0-9a-f]*' | sed 's/.*-->//'`
- Installed version: `<runnerdir>/bin/Runner.Listener --version` prints e.g. `2.3xx.x`.

### `config.sh`
- Register: `./config.sh --unattended --url https://github.com/<owner>/<repo> --token <REG_TOKEN> --name <name> --labels ownci[,x] --no-default-labels --work _work [--replace]`
- Unregister: `./config.sh remove --token <REMOVE_TOKEN>`
- Refuses to run as root unless `RUNNER_ALLOW_RUNASROOT=1`. OwnCI **refuses root** instead (doctor FAIL).
- Missing system deps (ICU, etc.) are fixed by `sudo ./bin/installdependencies.sh`. Mention this in hints.
- Writes `.runner` (JSON, **may start with a UTF-8 BOM**: strip it before parsing) with fields such as `agentId`, `agentName`, `poolId`, `serverUrl`, `gitHubUrl`, `workFolder`. Also writes `.credentials` and `.credentials_rsaparams` (secrets: never read or log them).

### `run.sh` / process behavior
- `run.sh` loops `run-helper.sh` → `bin/Runner.Listener run`. Exit code `2` from the helper means "restart" (self-update). `run.sh` handles that loop itself.
- **Important:** by default `run.sh` exits **0** for any other helper exit code, which hides crashes. When the env var **`RUNNER_MANUALLY_TRAP_SIG=1`** is set, `run.sh` uses `set -m`, traps `INT`/`TERM` and forwards `SIGINT` to the helper's process group, and **propagates the real exit code**. OwnCI must launch `run.sh` with `RUNNER_MANUALLY_TRAP_SIG=1`.
- Set **`ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1`**. Then exit code **7** means "runner version deprecated, must update". `status` reports it with a hint.
- `SIGINT` to the listener = graceful shutdown. A running job is cancelled.

### Runner stdout markers (for state detection, t07)
| Line (substring match) | Meaning |
|---|---|
| `Connected to GitHub` | session established |
| `Current runner version: '<v>'` | actual version |
| `<ts>: Listening for Jobs` | idle + online |
| `<ts>: Running job: <name>` | busy |
| `<ts>: Job <name> completed with result: <Result>` | job finished → back to idle |
| `Runner connect error:` | transient connectivity problem |
| `The runner no longer exists on the server` / `registration has been deleted from the server` | registration gone → needs `setup --force` |
| `A session for this runner already exists.` | another instance is running with the same registration |
| `Exiting...` | listener shutting down |

`<ts>` is UTC `yyyy-MM-dd HH:mm:ssZ`.

### `.env` file (runner root)
- `Runner.Listener` reads `<runnerdir>/.env` at startup. Each non-empty line `KEY=VALUE` (split on the first `=`) is set as an env var. **No quoting, no comment syntax**: don't write quotes, and don't write comment lines that contain `=`. Workers inherit these vars.
- `config.sh` itself writes `.env` (e.g. `LANG`, `PATH`). OwnCI must **merge** into it, never overwrite it.
- OwnCI-managed keys (t08): `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`, `ACTIONS_RUNNER_HOOK_JOB_COMPLETED=<abs path to hook>`, `OWNCI_RUNNER_ROOT=<runnerDir>`, `OWNCI_CLEANUP_IMAGE=<pinned image>`. In container-runner mode (t11) `ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER` is removed instead.
- `config.sh` (via `env.sh`) also writes **`.path`** with the `PATH` of the shell that ran it (on WSL including `/mnt/c/...` entries), for the runner to use for jobs. In container-runner mode the entrypoint rewrites it with the container's `PATH` before every launch; the `ownci-default-image` e2e job asserts its `PATH` has no `/mnt/` entries.
- Job hooks (`ACTIONS_RUNNER_HOOK_JOB_STARTED` / `_COMPLETED`) run **on the host** as the runner user (`JobHookProvider` uses the default step host), and their output appears in the job log visible to repo users. A failing hook fails the job.

### What a container job exposes (`ContainerOperationProvider.MountWellKnownDirectories`)
- Read-write bind mounts from the host: `_work` (incl. workspace), `_work/_temp`, `_work/_actions`, `_work/_tool`. `externals` is mounted read-only.
- CORRECTION (2026-10-22): the official runner **does** mount `/var/run/docker.sock` into every job container (verified on runner 2.338.0 with a real job, and visible in the container-hook `prepare_job` payload as a `systemMountVolumes` entry). The earlier claim here that it is not mounted was wrong.
- `services:`, `uses: docker://…` and Dockerfile actions run as **sibling containers** on the host Docker daemon. This is a documented limitation.
- Job containers run as **root** by default, so files they create in `_work` are root-owned on the host. The runner cleans `_work/_temp` itself per job, but may fail on root-owned files.

### GitHub REST endpoints used (base `https://api.github.com`, headers `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`, `Authorization: Bearer <token>`)
| Purpose | Request | Response highlights |
|---|---|---|
| Repo info | `GET /repos/{owner}/{repo}` | `private` (bool), `visibility`, `html_url`, `permissions.admin` |
| Registration token | `POST /repos/{owner}/{repo}/actions/runners/registration-token` | `201` `{token, expires_at}` |
| Removal token | `POST /repos/{owner}/{repo}/actions/runners/remove-token` | `201` `{token, expires_at}` |
| List runners | `GET /repos/{owner}/{repo}/actions/runners?per_page=100&page=N` | `{total_count, runners:[{id,name,os,status:"online"/"offline",busy,labels:[{name}]}]}` |
| Delete runner | `DELETE /repos/{owner}/{repo}/actions/runners/{id}` | `204` |

Required permission: repo **admin** (fine-grained PAT: *Administration: Read and write*; classic PAT: `repo`). `gh auth login` default scopes usually suffice for repos the user administers.

### Runner in a container (t11, verified 2026-10-22 on runner 2.338.0, Docker Desktop 29.5 + WSL2)
- Official image `ghcr.io/actions/actions-runner:2.338.0`: Ubuntu 24.04, users `ubuntu` (uid 1000) and `runner` (uid 1001, sudo), Docker CLI, git, `setpriv`; no ENTRYPOINT; env `ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT=1` (OwnCI unsets it so stdout keeps the §7 markers and `_diag` keeps the log).
- The runner passes its own paths (`externals`, `_work`, …) to the **host** daemon as bind-mount sources for job containers, so the runner directory must be mounted at the identical path.
- The data dir is `0700` and owned by the host user, so the runner must run as the **host uid** inside the container (the image's uid 1001 cannot even read it). The entrypoint (run as root) creates or reuses the passwd entry for that uid, adds it to the group owning the socket and to sudoers, rewrites `.path`, then `setpriv`s to it.
- `docker run --init` + the docker CLI's signal proxy deliver SIGINT to `run.sh`; exit codes (incl. 7) propagate through `docker run`. Docker's own failures are 125 (daemon: image missing, name conflict), 126/127 (entrypoint).
- `run.sh --once` still works in 2.338.0 (prints a deprecation warning): the listener takes one job and exits 0 after it.
- The cleanup hook works unchanged inside the container (same paths, docker CLI + socket); a read-only bind mount of the hook file over itself blocks writes (`EROFS`) and renames (`EBUSY`).

### Docker Desktop + WSL
- If Docker Desktop is installed but WSL integration is **off** for the distro, the `docker` command is a shim that prints *"The command 'docker' could not be found in this WSL 2 distro. We recommend to activate the WSL integration in Docker Desktop settings."* and exits non-zero. Detect this text and hint: *Docker Desktop → Settings → Resources → WSL integration → enable for this distro.* (This is the state of the author's machine at the time of writing.)
- WSL detection: `/proc/sys/kernel/osrelease` contains `microsoft` (case-insensitive) or env `WSL_DISTRO_NAME` is set.
- Documented WSL limitations: the runner stops when the WSL VM shuts down (idle shutdown after terminals close) or the machine sleeps. Queued jobs wait until it reconnects.

---

## 8. Dev harness overview (built across t04, t05, t07)

- **Fake runner tarball** (`internal/testutil`, t04; extended in t07/t08): a `.tar.gz` with the same top-level shape as the real runner (`config.sh`, `run.sh`, `bin/Runner.Listener`, `bin/installdependencies.sh`). Its bash scripts imitate the real behavior: `config.sh` writes `.runner` (with BOM) and fake `.credentials`, `run.sh` prints the stdout markers, honors `.env`, traps INT/TERM, can simulate a job (runs the `.env` job-completed hook), and can simulate a crash.
- **`tools/fakegh`** (t05): `go run ./tools/fakegh` starts an HTTP server that implements the endpoints in §7 in memory and serves the fake runner tarball. It prints the `export OWNCI_DEV_…=` lines to paste into a shell.
- **Dev build** (`make build-dev` → `bin/ownci-dev`): honors the overrides in D8, so the real CLI can be driven against fakegh.
