#!/usr/bin/env bash
# Fake of actions/runner config.sh (behavior contract: tasks/t04-runner-download.md).
VERSION="__FAKE_VERSION__"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR" || exit 1

if [ "$(id -u)" = "0" ] && [ "${RUNNER_ALLOW_RUNASROOT:-}" != "1" ]; then
  echo "Must not run with sudo"
  exit 1
fi

if [ "${1:-}" = "--version" ]; then
  echo "$VERSION"
  exit 0
fi

mode=configure
if [ "${1:-}" = "remove" ]; then
  mode=remove
  shift
fi

url="" token="" name="" labels="" work="_work" replace=0
args=("$@")
while [ $# -gt 0 ]; do
  case "$1" in
    --url) url="${2:-}"; shift 2 ;;
    --token) token="${2:-}"; shift 2 ;;
    --name) name="${2:-}"; shift 2 ;;
    --labels) labels="${2:-}"; shift 2 ;;
    --work) work="${2:-}"; shift 2 ;;
    --replace) replace=1; shift ;;
    *) shift ;;
  esac
done

if [ "$mode" = "remove" ]; then
  if [ ! -f .runner ]; then
    echo "Runner is not configured"
    exit 1
  fi
  if [ -n "${OWNCI_DEV_GITHUB_API_URL:-}" ]; then
    agent=$(sed -n 's/.*"agentName":"\([^"]*\)".*/\1/p' .runner)
    if ! curl -fsS -X DELETE "$OWNCI_DEV_GITHUB_API_URL/_fake/register/$agent?token=$token" > /dev/null; then
      echo "Http response code: Unauthorized"
      exit 1
    fi
  fi
  rm -f .runner .credentials
  echo "√ Runner removed successfully"
  exit 0
fi

if [ -z "$url" ] || [ -z "$token" ] || [ -z "$name" ]; then
  echo "Error: --url, --token and --name are required"
  exit 1
fi
if [ -f .runner ] && [ "$replace" != 1 ]; then
  echo "Cannot configure the runner because it is already configured. To reconfigure the runner, run './config.sh remove' first."
  exit 1
fi

if [ -n "${OWNCI_DEV_GITHUB_API_URL:-}" ]; then
  labels_json="[]"
  if [ -n "$labels" ]; then
    labels_json="[\"${labels//,/\",\"}\"]"
  fi
  body=$(printf '{"token":"%s","name":"%s","labels":%s,"url":"%s"}' "$token" "$name" "$labels_json" "$url")
  if ! curl -fsS -X POST -H 'Content-Type: application/json' -d "$body" "$OWNCI_DEV_GITHUB_API_URL/_fake/register" > /dev/null; then
    echo "Http response code: Unauthorized"
    exit 1
  fi
fi

# The real runner writes .runner with a UTF-8 BOM.
printf '\xef\xbb\xbf{"agentId":%s,"agentName":"%s","poolId":1,"serverUrl":"https://fake","gitHubUrl":"%s","workFolder":"%s"}\n' \
  "$((RANDOM % 900 + 100))" "$name" "$url" "$work" > .runner
echo '{"scheme":"OAuth","data":{"fake":"credential"}}' > .credentials
chmod 600 .credentials

# Record the arguments with the token masked so tests can assert on them.
masked=() prev=""
for a in "${args[@]}"; do
  if [ "$prev" = "--token" ]; then masked+=("***"); else masked+=("$a"); fi
  prev="$a"
done
echo "${masked[*]}" > .fake-config-args

echo "√ Runner successfully added"
echo "√ Settings Saved."
exit 0
