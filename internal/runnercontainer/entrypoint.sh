#!/usr/bin/env bash
# Entrypoint of the runner container (container-runner mode), managed by ownci
# (internal/runnercontainer). It runs as root, passed inline:
#   docker run … --entrypoint bash <image> -c <this script> ownci-entrypoint ./run.sh --once
#
# It makes the host user exist inside the container, so the runner and jobs
# without `container:` create files the host user owns, gives that user sudo
# (workflows written for GitHub-hosted runners use it) and the Docker socket,
# then drops root and execs the runner.
set -euo pipefail

uid="${OWNCI_UID:?}"
gid="${OWNCI_GID:?}"
if [ "$uid" = 0 ]; then
  echo "ownci: refusing to run the runner as root" >&2
  exit 64
fi

grp="$(getent group "$gid" | cut -d: -f1)" || true
if [ -z "$grp" ]; then
  groupadd -g "$gid" ownci
fi
user="$(getent passwd "$uid" | cut -d: -f1)" || true
if [ -z "$user" ]; then
  useradd -u "$uid" -g "$gid" -m -d /home/ownci -s /bin/bash ownci
  user=ownci
fi
home="$(getent passwd "$user" | cut -d: -f6)"
mkdir -p "$home"
chown "$uid:$gid" "$home"

# The socket's group differs per machine: join whichever group owns it.
if [ -S /var/run/docker.sock ]; then
  sgid="$(stat -c %g /var/run/docker.sock)"
  sgrp="$(getent group "$sgid" | cut -d: -f1)" || true
  if [ -z "$sgrp" ]; then
    groupadd -g "$sgid" ownci-docker
    sgrp=ownci-docker
  fi
  usermod -aG "$sgrp" "$user"
fi

if command -v sudo >/dev/null 2>&1 && [ -d /etc/sudoers.d ]; then
  echo "$user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/ownci
  chmod 0440 /etc/sudoers.d/ownci
fi

# config.sh ran on the host and saved the host's PATH in .path; jobs in this
# container need the container's. (rm first: never write through a symlink.)
rm -f .path
printf '%s\n' "$PATH" > .path
chown "$uid:$gid" .path

# The official image asks the runner to print its diagnostic log to stdout;
# ownci parses stdout for state markers, so keep the log in _diag as on the host.
unset ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT

exec setpriv --reuid "$uid" --regid "$gid" --init-groups \
  env HOME="$home" USER="$user" LOGNAME="$user" "$@"
