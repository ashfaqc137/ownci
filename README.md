# OwnCI

**Your PR. Your Compute.**

OwnCI is a small Go CLI that turns your Linux machine into a GitHub Actions
self-hosted runner for one repository, with one command each to set it up,
start it, stop it and remove it. It does not implement a CI protocol: it
downloads, registers, supervises and cleans up the **official**
[`actions/runner`](https://github.com/actions/runner), and configures it so
that **every job runs inside a Docker container**: either the job declares
`container:` (host mode, the default), or OwnCI runs the runner itself in a
container that jobs without `container:` use as their image
([container-runner mode](#run-every-job-in-a-container-no-container-needed)).

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
  for what it does *not* protect against. (In container-runner mode such a job runs in
  the runner's container instead; see below.)

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

### Run every job in a container (no `container:` needed)

Workflows written for GitHub-hosted runners usually have no `container:` line. With
**container-runner mode** they run unchanged:

```bash
ownci setup --repo owner/name --mode container-runner    # or: --force --mode container-runner to switch
ownci start --detach
```

```yaml
jobs:
  build:
    runs-on: [ownci]           # no container: needed
    steps:
      - uses: actions/checkout@v4
      - run: sudo apt-get install -y jq && make test
```

How it works:

```
 GitHub Actions ◄──────────► official actions/runner, inside a container       (one per job)
                              image: ghcr.io/actions/actions-runner (Ubuntu 24.04, pinned)
                              runner dir mounted at the SAME path, Docker socket mounted
                                 │                               │
            job without container:                     job with container: X
            runs right here, as your uid,              the runner asks the host daemon
            with passwordless sudo                     for X, exactly as in host mode
```

- **Jobs without `container:`** run inside the runner container, as your user id (files
  in the runner directory stay yours), with passwordless `sudo` and the Docker CLI.
- **Jobs with `container:`** behave exactly as in host mode: the job container is a sibling
  on your Docker daemon.
- **Every job gets a fresh container.** The runner takes one job (`run.sh --once`), exits,
  and OwnCI starts a new container for the next one. Anything a job changes outside the
  workspace (`apt-get install`, `/tmp`, `$HOME`) is gone for the next job; the workspace
  itself is wiped by the same cleanup hook as in host mode. Expect a few seconds between
  jobs while the new container connects.
- The default image is `ghcr.io/actions/actions-runner`, pinned by digest (about 2 GB, pulled
  once at setup). `--image` (at `setup`, or for one session at `start`) picks another one,
  e.g. an Ubuntu 22.04 image built from [`e2e/images/ubuntu22.Dockerfile`](e2e/images/ubuntu22.Dockerfile)
  (`docker build -t my/runner:22.04 -f e2e/images/ubuntu22.Dockerfile .`; a locally built image
  is used as is, not pulled). It **must** contain `bash`, `setpriv`, `useradd`/`groupadd`/`usermod`,
  `getent`, `stat`, the `docker` CLI and the ICU library, and **should** contain `git` and `sudo`.
  `setup` refuses an image that lacks a required tool before registering anything, and
  `ownci doctor` checks it too.
- The runner directory must be mountable at its own path: a plain Linux path without
  symlinks, not under `/mnt/c` (OwnCI refuses otherwise). The default data dir is fine.
- Not the GitHub-hosted image: the default image has git, curl and the Docker CLI but not
  the hosted runners' preinstalled toolchains. `actions/setup-*` actions install what you need.

### One workflow file for a whole team

Give each developer's runner their own label (their GitHub username) and let the workflow
pick the runner of whoever triggered it, so everyone's jobs run on their own machine:

```bash
ownci setup --repo owner/name --labels <your-github-username>   # each developer, once
```

```yaml
jobs:
  build:
    runs-on: [ownci, "${{ github.actor }}"]   # the user who opened or pushed to the PR
    container: node:22
    steps:
      - uses: actions/checkout@v4
      - run: npm ci && npm run build
```

Things to know: a developer whose runner is offline gets a queued job (it waits for their
runner, it never lands on someone else's); bots such as Dependabot match no runner; and each
developer needs admin access to the repository to run `ownci setup`. Use
`github.event.pull_request.user.login` instead of `github.actor` if you want the PR *author*
rather than whoever pushed last.

### Several repositories

One data directory holds one runner for one repository. For a second repository, give it
its own data directory and a runner name of its own:

```bash
ownci --data-dir ~/.local/share/ownci-app setup --repo owner/app --name ownci-mybox-app
ownci --data-dir ~/.local/share/ownci-app start --detach
ownci --data-dir ~/.local/share/ownci-lib setup --repo owner/lib --name ownci-mybox-lib
ownci --data-dir ~/.local/share/ownci-lib start --detach
```

- Every later command (`status`, `logs`, `stop`, `uninstall`) needs the same `--data-dir`,
  or `export OWNCI_DATA_DIR=…` in that shell. Without one it acts on the default
  `~/.local/share/ownci`.
- **Container-runner mode needs distinct names:** the runner container is called
  `ownci-<runner name>`, and Docker container names are global. OwnCI refuses a name whose
  container belongs to another data directory and never stops or removes such a container.
  In host mode the same name works, but distinct names are clearer on GitHub.
- The runners are independent: each runs one job at a time, and they share the machine's
  CPU, memory and Docker daemon.

## Commands

| Command | What it does |
|---|---|
| `ownci version [--json]` | OwnCI version, commit, date, and the runner version when set up. |
| `ownci doctor [--json]` | Read-only health checks (platform, user, data dir, ICU, Docker, isolation). Exit 3 if one fails. |
| `ownci setup --repo owner/name` | Download and verify the pinned runner, register it with label `ownci` only, install the isolation settings and cleanup hook. Flags: `--name`, `--labels a,b` (default: keep the current ones), `--mode host\|container-runner` (default: keep the current mode, or host), `--image` (container-runner mode), `--force` (re-register in place, also to switch modes; refused while the runner runs), `--allow-public-repo`, `--dry-run`, `--download-only`. |
| `ownci start [--detach]` | Re-asserts isolation, cleans leftovers, starts the runner under supervision (restarts on crashes with backoff; in container-runner mode a fresh container per job). `--wait` sets how long `--detach` waits for "listening"; `--image` overrides the runner image for this session (container-runner mode). |
| `ownci stop [--force]` | Gracefully stops the runner (a running job is cancelled); registration and files are kept. `--timeout` (default 45 s). |
| `ownci status [--remote] [--json]` | What is *actually* true: installed, registered, mode (and the runner container's state), process (from a held lock, never from files), runner state, last job, isolation, Docker, last error. `--remote` also asks GitHub. |
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

- Host mode: jobs must declare `container:` (`ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true`);
  others fail with *"Jobs without a job container are forbidden on this runner"*. A `start`
  that cannot establish this refuses to run the runner.
- Container-runner mode: the runner is only ever started inside its container, so a job
  without `container:` runs there, never directly on your machine. The container is
  replaced after every job, and the cleanup hook is mounted read-only into it so a job
  cannot switch the workspace wipe off.
- After every job the runner's `_work` directory (workspace, `_actions`, `_tool`) is wiped
  by a hook that uses a throwaway, network-less container from a digest-pinned image, so
  root-owned files cannot carry over to the next job. If the cleanup fails, the **job
  fails visibly** rather than silently leaving files behind.
- The runner is opt-in: it carries only the label `ownci`, so a stray `runs-on: self-hosted`
  never lands on your laptop.
- OwnCI uses no privileged containers.
- Your GitHub token is never stored; the registration token is single-use. The runner's own
  credentials live in the runner directory inside the data dir (directories `0700`, secrets
  `0600`). Tokens are redacted from OwnCI's logs and the runner log it captures.

**What the container boundary does *not* protect against** (a container is not a security
sandbox for hostile code):

0. **The Docker socket is mounted into job containers.** The official runner mounts
   `/var/run/docker.sock` into every job container (checked on runner 2.338.0: it is present
   and the job runs as root). A job can therefore control your Docker daemon, which is
   effectively root on the machine that runs it: start privileged containers, mount host
   paths, read other containers' data. For OwnCI this means the container is **not** a
   barrier against a malicious workflow, only against accidents. Only run workflows you trust.
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
7. **Container-runner mode:** the runner directory is mounted into the runner container, so a
   job without `container:` runs as your user next to the runner's files, including its
   registration credentials (`.credentials*`), and can read or change them for the length of
   that job. With the Docker socket (item 0) it can reach anything on the machine anyway; the
   mode protects against accidents and leftovers, not against hostile workflows.

OwnCI does **not** claim to safely run arbitrary untrusted pull request code.

## Limitations

- One job at a time (one runner per data directory; use one data directory per repository,
  see [Several repositories](#several-repositories)).
- Container-runner mode starts the runner with `run.sh --once`, which the runner marks as
  deprecated in favor of ephemeral registrations. If a future runner drops it, OwnCI will
  report the runner as crashing; see [`tasks/future.md`](tasks/future.md).
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
| `OwnCI is already set up for <repo> …; this data directory holds one runner` | To run a second repository alongside it, use another `--data-dir` (see [Several repositories](#several-repositories)). |
| `… container belongs to another OwnCI data directory` / `container name … is in use` (container-runner mode) | Another data directory has a runner of the same name. Give this one another name: `ownci uninstall --unregister`, then `ownci setup --repo … --name <unique>`. |
| `… is a public repository` | Use a private repo, or `--allow-public-repo` if you accept the risk. |
| `the GitHub token has no admin access` | Use a token with admin on the repository (see Prerequisites). |
| `no GitHub token found` | `export GITHUB_TOKEN=…` or `gh auth login`. |
| `[FAIL] isolation` in `doctor` | `ownci start` repairs the `.env`/hook; otherwise `ownci setup --repo … --force`. |
| `[WARN] isolation` (image missing) | `docker pull <image shown by doctor>` (`ownci start` also pulls it). |
| `cleanup FAILED` in a job log | Docker unavailable or the image missing; see `<runner>/ownci/last-cleanup.json`, fix Docker, `ownci doctor`. |
| `runner directory … goes through a symlink` (container-runner mode) | Docker would mount the target under another name. Use the resolved path: `--data-dir $(realpath <dir>)`. |
| `image … cannot host the runner; it lacks: …` | The `--image` misses tools the runner container needs (plain `ubuntu:22.04` lacks the docker CLI and ICU); use the default image, one derived from it, or one like `e2e/images/ubuntu22.Dockerfile`. |
| `the runner '…' could not be started (exit code 125)` | Docker refused the runner container (image missing, a mount refused). `ownci doctor`, then `docker pull <image>`. |
| status phase **`registration_lost`** | The runner was removed on GitHub: `ownci setup --repo … --force` to register again. |
| status phase **`version_deprecated`** | The runner could not self-update: upgrade ownci, then `ownci setup --repo … --force`. |
| status phase **`duplicate_session`** | Another process uses this registration (another machine or a leftover process): stop it. |
| status phase **`crashed`** | Repeated crashes; read `ownci logs` (the last 20 lines are in `status`), run `ownci doctor`. |
| status says *stopped (unexpectedly)* | The supervisor was killed or the machine rebooted: `ownci start` recovers (it also stops runner processes left over from the killed supervisor, which would otherwise hold the GitHub session). |
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

[MIT](LICENSE).
