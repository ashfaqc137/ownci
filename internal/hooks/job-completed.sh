#!/usr/bin/env bash
# Job-completed hook installed and managed by ownci (internal/hooks).
# ownci rewrites this file on 'ownci start'; do not edit it.
#
# Wipes the runner's _work directory after every job, so one job cannot leave
# files for the next. Job containers run as root, so the deletion happens in a
# throwaway container. Output is kept to a line because it appears in the
# GitHub job log; no environment values are printed.
#
# A failure exits 1 on purpose: the job then fails visibly instead of silently
# leaving contamination behind.
set -euo pipefail

root="${OWNCI_RUNNER_ROOT:-}"
image="${OWNCI_CLEANUP_IMAGE:-}"

record() { # <ok|failed> <detail>
  [ -d "$root/ownci" ] || return 0
  local detail="${2//[\"\\]/}"
  detail="${detail//$'\n'/ }"
  printf '{"time":"%s","result":"%s","detail":"%s"}\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "${detail:0:200}" \
    > "$root/ownci/last-cleanup.json.tmp" &&
    mv "$root/ownci/last-cleanup.json.tmp" "$root/ownci/last-cleanup.json" || true
}

fail() {
  echo "ownci: cleanup FAILED ($1)"
  record failed "$1"
  exit 1
}

case "$root" in
  /*) ;;
  *) echo "ownci: cleanup skipped: unexpected work dir"; exit 1 ;;
esac
work="$root/_work"
if [ -L "$work" ] || [ ! -d "$work" ]; then
  fail "unexpected work dir"
fi
[ -n "$image" ] || fail "no cleanup image configured"

# Everything in _work goes except _temp and _PipelineMapping, which the runner
# manages itself, unless a job replaced them with symlinks (on the host those
# would point elsewhere and the runner would empty their target). _temp is
# handed back to the runner user because the job container left root-owned
# files there. The pinned image is pulled if it is missing (e.g. after a
# 'docker system prune -a'), so cleanup keeps working until the next start.
script='set -eu
for d in _temp _PipelineMapping; do if [ -L "/w/$d" ]; then rm -f "/w/$d"; fi; done
find /w -mindepth 1 -maxdepth 1 ! -name _temp ! -name _PipelineMapping -exec rm -rf {} +
if [ -d /w/_temp ]; then chown -R "$1:$2" /w/_temp; fi'

if ! out="$(docker run --rm --pull missing --network none --label ownci.managed=true \
  -v "$work:/w" "$image" sh -c "$script" sh "$(id -u)" "$(id -g)" 2>&1)"; then
  reason="${out##*$'\n'}"
  fail "${reason:-docker run failed}"
fi

record ok "workspace cleaned"
echo "ownci: workspace cleaned"
