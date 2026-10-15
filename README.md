# OwnCI

**Your PR. Your Compute.**

OwnCI is a small Go CLI that turns your Linux machine into a GitHub Actions
self-hosted runner for one repository, with one command each to set it up,
start it, stop it and remove it. It does not implement a CI protocol: it
downloads, registers, supervises and cleans up the **official**
[`actions/runner`](https://github.com/actions/runner), and configures it so
that **every job must run inside a Docker container**.

> **Status: MVP. Linux and WSL2 only** (amd64 and arm64). Native Windows and
> macOS are not supported. The first real-GitHub validation is described in
> [`docs/e2e.md`](docs/e2e.md); results go in [`docs/acceptance.md`](docs/acceptance.md).

## How it works

```
 GitHub Actions ◄──────────────► official actions/runner     (runs on your host, as you)
 (queues jobs, owns the PR check)        │  supervised by OwnCI: start / stop / status
                                         │  .env: ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true
                                         ▼
                                job container (your local Docker)
                                         │
                                         ▼  after every job: OwnCI's job-completed hook
                                wipes the runner's _work (in a throwaway container)
```

- GitHub still owns workflows, queuing, scheduling and the check shown on the PR.
- `ownci` owns the runner's lifecycle: download + checksum verification, registration,
  process supervision, status, cleanup, uninstall.
- A job that does not declare `container:` is **rejected** by the runner. That is the
  isolation boundary OwnCI enforces and verifies; read [Security](#security-and-trust-model)
  for what it does *not* protect against.

## Prerequisites

- **Linux or WSL2** on amd64 or arm64, and a **non-root** user (the runner refuses root).
- **Docker** with a Linux daemon, reachable by your user:
  - *WSL2 with Docker Desktop:* Docker Desktop → Settings → Resources → **WSL integration** →
    enable your distro → Apply & restart. Without it, `docker` in WSL is a stub that fails.
  - *Native Linux / Docker Engine in WSL:* [install Docker Engine](https://docs.docker.com/engine/install/)
    and add yourself to the `docker` group (`sudo usermod -aG docker $USER`, then log in again).
- **ICU** for the runner: Debian/Ubuntu `sudo apt-get install -y libicu-dev`,
  Fedora `sudo dnf install libicu`. (`bin/installdependencies.sh` in the runner directory does the same.)
- A **GitHub token with admin access to the repository**, only for `setup`,
  `uninstall --unregister` and `status --remote`. In order of preference: `GITHUB_TOKEN`,
  `GH_TOKEN`, or `gh auth login` (the default scopes work for repositories you administer).
  A fine-grained PAT needs *Administration: Read and write* on the repository. The token is
  never stored.

Run `ownci doctor` to check all of this.

## Install

Download the archive for your architecture and `SHA256SUMS` from the
[Releases page](https://github.com/ashfaqc137/ownci/releases), then:

```bash
sha256sum -c --ignore-missing SHA256SUMS        # must print: ownci_<version>_linux_<arch>.tar.gz: OK
tar -xzf ownci_<version>_linux_<arch>.tar.gz
install -m 0755 ownci ~/.local/bin/ownci        # make sure ~/.local/bin is on your PATH
ownci version
```

Data lives in `~/.local/share/ownci` by default (`--data-dir`, `$OWNCI_DATA_DIR` or
`$XDG_DATA_HOME/ownci` override it). On WSL keep it on the Linux filesystem, not under `/mnt/c`.

## Quick start

```bash
ownci doctor                              # everything PASS?
ownci setup --repo owner/name             # downloads + verifies the runner, registers it, installs isolation
ownci start --detach                      # runs in the background (without --detach: foreground, Ctrl-C stops)
ownci status --remote                     # online, idle
```

Then target the runner from a workflow. **Both lines are required:**

```yaml
jobs:
  build:
    runs-on: [ownci]            # the opt-in label OwnCI registers (not "self-hosted")
    container: ubuntu:24.04     # mandatory: jobs without a container are rejected
    steps:
      - uses: actions/checkout@v4
      - run: make test
```

Stop contributing compute with `ownci stop`; `ownci start` brings the same registration back.

## Commands

| Command | What it does |
|---|---|
| `ownci version [--json]` | OwnCI version, commit, date, and the runner version when set up. |
| `ownci doctor [--json]` | Read-only health checks (platform, user, data dir, ICU, Docker, isolation). Exit 3 if one fails. |
| `ownci setup --repo owner/name` | Download and verify the pinned runner, register it with label `ownci` only, install the isolation settings and cleanup hook. Flags: `--name`, `--labels a,b`, `--force` (re-register in place; refused while the runner runs), `--allow-public-repo`, `--dry-run`, `--download-only`. |
| `ownci start [--detach]` | Re-asserts isolation, cleans leftovers, starts the runner under supervision (restarts on crashes with backoff). `--wait` sets how long `--detach` waits for "listening". |
| `ownci stop [--force]` | Gracefully stops the runner (a running job is cancelled); registration and files are kept. `--timeout` (default 45 s). |
| `ownci status [--remote] [--json]` | What is *actually* true: installed, registered, process (from a held lock, never from files), runner state, last job, isolation, Docker, last error. `--remote` also asks GitHub. |
| `ownci logs [--source runner\|ownci\|supervisor\|diag] [-n N] [-f]` | Tail OwnCI's and the runner's logs (secrets are redacted). |
| `ownci uninstall [--unregister] [--purge] [--local-only] [--yes] [--name n]` | Stop the runner; optionally remove the GitHub registration and OwnCI's files. Without flags it only stops and explains what is left. |

Global flags: `--data-dir`, `-v/--verbose`.

## Security and trust model

OwnCI runs code from your workflows on your machine. Read this section before pointing
it at anything you do not control.

**Supported:** *private* repositories whose workflows you trust (your own, your team's).
`ownci setup` **refuses public repositories** unless you pass `--allow-public-repo`, because
pull requests from forks can run arbitrary code on a self-hosted runner. If you accept that
risk, set the repository's *Fork pull request workflows from outside collaborators* to
*Require approval for all outside collaborators*.

**What OwnCI enforces and checks (every `start`, and in `status` / `doctor`):**

- Jobs must declare `container:` (`ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`); others fail
  with *"Jobs without a job container are forbidden on this runner"*. A `start` that cannot
  establish this refuses to run the runner.
- After every job the runner's `_work` directory (workspace, `_actions`, `_tool`) is wiped
  by a hook that uses a throwaway, network-less container from a digest-pinned image, so
  root-owned files cannot carry over to the next job. If the cleanup fails, the **job
  fails visibly** rather than silently leaving files behind.
- The runner is opt-in: it carries only the label `ownci`, so a stray `runs-on: self-hosted`
  never lands on your laptop.
- The Docker socket is **not** mounted into job containers, and OwnCI uses no privileged
  containers.
- Your GitHub token is never stored; the registration token is single-use. The runner's own
  credentials live in the runner directory inside the data dir (directories `0700`, secrets
  `0600`). Tokens are redacted from OwnCI's logs and the runner log it captures.

**What the container boundary does *not* protect against** (a container is not a security
sandbox for hostile code):

1. **Network:** job containers use Docker's default network and can reach the internet and
   your LAN. OwnCI sets no network policy.
2. **Sibling containers:** `services:`, `uses: docker://…` and Dockerfile actions run as
   *siblings* on your Docker daemon, outside the job container's boundary and outside
   OwnCI's control.
3. **Shared Docker daemon:** job containers share the daemon, images and build cache with
   everything else on the machine (with Docker Desktop, with Windows too). Images persist
   between jobs; only `_work` is wiped.
4. **Root in the container:** containers run as root by default. They have read-write access to
   the runner's `_work`, `_temp`, `_actions` and `_tool` directories while a job runs.
5. **The runner itself runs on your host as you.** OwnCI only allows container jobs, but a
   vulnerability in the runner, Docker or a container image is a vulnerability on your machine.
6. No CPU, memory or disk limits are applied.

OwnCI does **not** claim to safely run arbitrary untrusted pull request code.

## Limitations

- One job at a time (one runner per data directory).
- The runner is a plain process: it stops when you stop it, when WSL shuts down (the WSL VM
  can idle-shut-down after your terminals close) or when the machine sleeps. Queued jobs wait
  until it reconnects. Use `ownci start --detach` and keep WSL running (e.g. leave a terminal open).
- The runner self-updates (GitHub refuses jobs to runners that fall too far behind), so the
  installed version can be newer than the one OwnCI pinned. `ownci status` shows the real one.
- Linux and WSL2 only; no systemd service and no auto-start on boot yet.
- Docker Desktop's WSL integration or a Docker Engine in WSL must be set up by you; OwnCI does
  not install Docker.

## Troubleshooting

Start with `ownci doctor` and `ownci status`. Logs: `ownci logs`, `ownci logs --source ownci -v`.

| Symptom | Fix |
|---|---|
| `docker is not available in this WSL 2 distro` | Docker Desktop → Settings → Resources → WSL integration → enable the distro. |
| `cannot connect to the Docker daemon` | Start Docker (Docker Desktop app, or `sudo systemctl start docker`). |
| `permission denied connecting to the Docker socket` | `sudo usermod -aG docker $USER`, then log out and in. |
| `Docker server OS is "windows"` | Docker Desktop tray icon → Switch to Linux containers. |
| `running as root` | Run ownci as a normal user. |
| `ICU library (libicuuc) not found` / runner crashes at start | Install ICU (see Prerequisites) or run `<data-dir>/runners/<name>/bin/installdependencies.sh` with sudo. |
| `data-dir … is on a Windows mount` | Use a Linux path: `--data-dir ~/.local/share/ownci`. |
| `… is a public repository` | Use a private repo, or `--allow-public-repo` if you accept the risk. |
| `the GitHub token has no admin access` | Use a token with admin on the repository (see Prerequisites). |
| `no GitHub token found` | `export GITHUB_TOKEN=…` or `gh auth login`. |
| `[FAIL] isolation` in `doctor` | `ownci start` repairs the `.env`/hook; otherwise `ownci setup --repo … --force`. |
| `[WARN] isolation` (image missing) | `docker pull <image shown by doctor>` (`ownci start` also pulls it). |
| `cleanup FAILED` in a job log | Docker unavailable or the image missing; see `<runner>/ownci/last-cleanup.json`, fix Docker, `ownci doctor`. |
| status phase **`registration_lost`** | The runner was removed on GitHub: `ownci setup --repo … --force` to register again. |
| status phase **`version_deprecated`** | The runner could not self-update: upgrade ownci, then `ownci setup --repo … --force`. |
| status phase **`duplicate_session`** | Another process uses this registration (another machine or a leftover process): stop it. |
| status phase **`crashed`** | Repeated crashes; read `ownci logs` (the last 20 lines are in `status`), run `ownci doctor`. |
| status says *stopped (unexpectedly)* | The supervisor was killed or the machine rebooted: `ownci start` recovers. |
| A job stays queued | Is the runner online (`ownci status --remote`)? Does the workflow have `runs-on: [ownci]`? Did WSL or the machine sleep? |
| `already running (pid N)` | `ownci status`, then `ownci stop`. |

## Development

```bash
make check          # gofmt, go vet (also -tags dev), go test -race, builds
make build          # bin/ownci (release configuration)
make build-dev      # bin/ownci-dev: honors OWNCI_DEV_* overrides (never in release builds)
make release VERSION=v0.1.0   # dist/ archives + SHA256SUMS (see docs/releasing.md)
```

Everything up to `make check` is tested without network, Docker or real GitHub, using fakes:
a fake runner tarball, a fake GitHub API (`tools/fakegh`) and a fake `docker`. To drive the
real CLI against them:

```bash
make build-dev
go run ./tools/fakegh --state /tmp/fakegh.json &      # prints the env to use
eval "$(go run ./tools/fakegh --print-env)"
./bin/ownci-dev --data-dir /tmp/ownci-dev setup --repo me/demo && ./bin/ownci-dev --data-dir /tmp/ownci-dev start --detach
```

The real end-to-end check against GitHub is [`docs/e2e.md`](docs/e2e.md). The design decisions
are recorded in [`tasks/00-context.md`](tasks/00-context.md) and the product requirements in
[`plan.md`](plan.md).

## License

TODO: no license has been chosen yet. Until one is added, all rights are reserved by the author.
