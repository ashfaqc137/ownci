#!/usr/bin/env bash
# Fake of actions/runner run.sh (only the behavior OwnCI relies on).
VERSION="__FAKE_VERSION__"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR" || exit 1

if [ ! -f .runner ]; then
  echo "Runner is not configured."
  exit 1
fi

# Like Runner.Listener: load KEY=VALUE lines from .env (split on the first '=').
if [ -f .env ]; then
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      *=*)
        key="${line%%=*}"
        if [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
          export "$key=${line#*=}"
        fi
        ;;
    esac
  done < .env
fi

ts() { date -u +'%Y-%m-%d %H:%M:%SZ'; }

mkdir -p _diag
LOG="_diag/Runner_$(date -u +%Y%m%d-%H%M%S).log"
say() {
  echo "$@"
  echo "$@" >> "$LOG"
}

stop() {
  say "Exiting..."
  exit 0
}
trap stop INT TERM

# --once (container-runner mode): take one job, then exit like the real
# listener does after it.
once=0
for a in "$@"; do
  [ "$a" = "--once" ] && once=1
done
[ "$once" = 0 ] || say "Warning: '--once' is going to be deprecated in the future, please consider using '--ephemeral' during runner registration."

say "√ Connected to GitHub"
say "Current runner version: '$VERSION'"

if [ -n "${FAKE_RUNNER_EXIT_CODE:-}" ]; then
  exit "$FAKE_RUNNER_EXIT_CODE"
fi

say "$(ts): Listening for Jobs"

# Simulated jobs: drop a job name (optionally followed by " nocontainer") into
# .fake-job-request.
n=0
while true; do
  if [ -f .fake-job-request ]; then
    req=$(cat .fake-job-request)
    rm -f .fake-job-request
    n=$((n + 1))
    job="${req%% *}"
    nocontainer=0
    case "$req" in *" nocontainer"*) nocontainer=1 ;; esac
    say "$(ts): Running job: $job"
    joblog="_diag/fake-job-$n.log"
    result=Succeeded
    if [ "${ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER:-}" = "true" ] && [ "$nocontainer" = 1 ]; then
      echo "Jobs without a job container are forbidden on this runner, please add a 'container:' to your job or contact your self-hosted runner administrator." >> "$joblog"
      result=Failed
    else
      for d in _work/_tool _work/_actions _work/fakerepo/fakerepo _work/_temp; do
        mkdir -p "$d"
        : > "$d/fake-marker"
      done
      hook="${ACTIONS_RUNNER_HOOK_JOB_COMPLETED:-}"
      if [ -n "$hook" ] && [ -f "$hook" ]; then
        RUNNER_ROOT="$DIR" GITHUB_WORKSPACE="$DIR/_work/fakerepo/fakerepo" \
          bash "$hook" >> "$joblog" 2>&1 || result=Failed
      fi
    fi
    say "$(ts): Job $job completed with result: $result"
    if [ "$once" = 1 ]; then
      exit 0
    fi
  fi
  sleep 0.2 &
  wait $!
done
