#!/usr/bin/env bash
# Convenience driver for the real GitHub end-to-end check. docs/e2e.md is the
# source of truth; this script automates its steps 6-8 using only `gh`.
#
#   OWNCI_E2E_REPO=owner/sandbox ./e2e/run.sh
#   OWNCI_E2E_MODE=container-runner OWNCI_E2E_REPO=owner/sandbox ./e2e/run.sh
#
# OWNCI_E2E_MODE is the mode the runner was set up with (default: host). In
# host mode a job without a container must be rejected; in container-runner
# mode it must run in the runner's image, in a fresh container per job.
#
# Needs: gh (logged in, admin on the repo), a running runner ('ownci start
# --detach'), and a PRIVATE sandbox repo with a commit on its default branch.
#
# What it changes in the sandbox repo: it commits the three workflows in
# e2e/workflows/ to the default branch (workflow_dispatch workflows only work
# from there), then opens a pull request with a throwaway file.
set -euo pipefail

: "${OWNCI_E2E_REPO:?set OWNCI_E2E_REPO=owner/name of the private sandbox repo}"
repo="$OWNCI_E2E_REPO"
mode="${OWNCI_E2E_MODE:-host}"
case "$mode" in
  host | container-runner) ;;
  *) echo "OWNCI_E2E_MODE must be host or container-runner" >&2; exit 2 ;;
esac
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
stamp="$(date -u +%Y%m%d-%H%M%S)"
timeout_s="${OWNCI_E2E_TIMEOUT:-600}"

results=()
record() { results+=("$1|$2|$3"); } # name | PASS/FAIL | detail

say() { printf '==> %s\n' "$*"; }

visibility="$(gh repo view "$repo" --json visibility -q .visibility)"
if [ "$visibility" = "PUBLIC" ]; then
  echo "refusing: $repo is public; use a private sandbox repo" >&2
  exit 1
fi
base="$(gh repo view "$repo" --json defaultBranchRef -q .defaultBranchRef.name)"

# put_file <path-in-repo> <local-file> <branch>: create or update a file.
put_file() {
  local path="$1" file="$2" branch="$3" sha=""
  sha="$(gh api "repos/$repo/contents/$path?ref=$branch" -q .sha 2>/dev/null || true)"
  local args=(-X PUT "repos/$repo/contents/$path"
    -f message="ownci e2e: $path" -f content="$(base64 -w0 "$file")" -f branch="$branch")
  if [ -n "$sha" ]; then args+=(-f sha="$sha"); fi
  gh api "${args[@]}" > /dev/null
}

# wait_run <run-id>: block until the run ends; print its conclusion.
wait_run() {
  local id="$1" waited=0 status
  while [ "$waited" -lt "$timeout_s" ]; do
    status="$(gh run view "$id" -R "$repo" --json status -q .status)"
    if [ "$status" = "completed" ]; then
      gh run view "$id" -R "$repo" --json conclusion -q .conclusion
      return 0
    fi
    sleep 5
    waited=$((waited + 5))
  done
  echo "timed_out"
}

# find_run <workflow-file> <event> <since-iso8601> [branch]: newest matching run id.
find_run() {
  local wf="$1" event="$2" since="$3" branch="${4:-}" id="" tries=0
  local filter=(-R "$repo" -w "$wf" -e "$event" -L 10 --json databaseId,createdAt)
  if [ -n "$branch" ]; then filter+=(-b "$branch"); fi
  while [ "$tries" -lt 36 ]; do
    id="$(gh run list "${filter[@]}" -q "[.[] | select(.createdAt >= \"$since\")] | .[0].databaseId // empty")"
    if [ -n "$id" ]; then echo "$id"; return 0; fi
    sleep 5
    tries=$((tries + 1))
  done
  return 1
}

say "committing the e2e workflows to $repo@$base"
for f in "$here"/workflows/*.yml; do
  put_file ".github/workflows/$(basename "$f")" "$f" "$base"
done

# --- 1. pull request check (PRD: a real PR check runs on local compute) ------
branch="ownci-e2e-$stamp"
say "opening a pull request from $branch"
since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
base_sha="$(gh api "repos/$repo/git/ref/heads/$base" -q .object.sha)"
gh api "repos/$repo/git/refs" -f ref="refs/heads/$branch" -f sha="$base_sha" > /dev/null
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
echo "ownci e2e $stamp" > "$tmp"
put_file "e2e-$stamp.txt" "$tmp" "$branch"
pr_url="$(gh pr create -R "$repo" -H "$branch" -B "$base" -t "ownci e2e $stamp" -b "Throwaway PR for the OwnCI end-to-end check.")"
say "PR: $pr_url"

if run_id="$(find_run ownci-pass.yml pull_request "$since" "$branch")"; then
  conclusion="$(wait_run "$run_id")"
  if [ "$conclusion" = "success" ]; then
    record "PR check (ownci-pass)" PASS "run $run_id"
  else
    record "PR check (ownci-pass)" FAIL "conclusion: $conclusion (run $run_id)"
  fi
else
  record "PR check (ownci-pass)" FAIL "no run appeared; is the runner listening? ('ownci status --remote')"
fi

# --- 2. a job without a container: rejected (host) or run in the default image -
if [ "$mode" = container-runner ]; then
  say "dispatching ownci-default-image.yml"
  since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  gh workflow run ownci-default-image.yml -R "$repo" --ref "$base" > /dev/null
  if run_id="$(find_run ownci-default-image.yml workflow_dispatch "$since")"; then
    conclusion="$(wait_run "$run_id")"
    if [ "$conclusion" = "success" ]; then
      record "no container runs in the default image" PASS "run $run_id"
    else
      record "no container runs in the default image" FAIL "conclusion: $conclusion (run $run_id)"
    fi
  else
    record "no container runs in the default image" FAIL "no run appeared"
  fi
fi

if [ "$mode" = host ]; then
  say "dispatching ownci-nocontainer.yml"
  since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  gh workflow run ownci-nocontainer.yml -R "$repo" --ref "$base" > /dev/null
  if run_id="$(find_run ownci-nocontainer.yml workflow_dispatch "$since")"; then
    conclusion="$(wait_run "$run_id")"
    if [ "$conclusion" = "failure" ] &&
      gh run view "$run_id" -R "$repo" --log-failed 2>/dev/null | grep -q "Jobs without a job container are forbidden"; then
      record "no container is rejected" PASS "run $run_id failed with the expected message"
    else
      record "no container is rejected" FAIL "conclusion: $conclusion, or the message is missing (run $run_id)"
    fi
  else
    record "no container is rejected" FAIL "no run appeared"
  fi
fi

# --- 3. nothing persists between jobs ----------------------------------------
say "dispatching ownci-poison.yml"
since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
gh workflow run ownci-poison.yml -R "$repo" --ref "$base" > /dev/null
if run_id="$(find_run ownci-poison.yml workflow_dispatch "$since")"; then
  conclusion="$(wait_run "$run_id")"
  if [ "$conclusion" = "success" ]; then
    record "no leftovers between jobs (poison)" PASS "run $run_id"
  else
    record "no leftovers between jobs (poison)" FAIL "conclusion: $conclusion (run $run_id)"
  fi
else
  record "no leftovers between jobs (poison)" FAIL "no run appeared"
fi

# --- summary -----------------------------------------------------------------
echo
printf '%-38s %-5s %s\n' CHECK RESULT DETAIL
failed=0
for r in "${results[@]}"; do
  IFS='|' read -r name res detail <<<"$r"
  printf '%-38s %-5s %s\n' "$name" "$res" "$detail"
  if [ "$res" != PASS ]; then failed=1; fi
done
echo
echo "PR left open for inspection: $pr_url"
exit "$failed"
