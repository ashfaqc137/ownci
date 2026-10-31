#!/usr/bin/env bash
# Serve this repository's own "ownci" check (.github/workflows/ownci.yml) from a runner built
# from your checkout: make dogfood
#
# Builds bin/ownci from HEAD, copies it out of the tree (so a later build does not replace the
# running binary), and (re-)registers the runner with the labels dev-<your login> and
# build-<HEAD>, then starts it. The check of a pull request runs only on a runner with its head
# commit's label, so run this again after every push.
#
# Environment:
#   OWNCI_SELF_REPO   repository to serve (default: ashfaqc137/ownci)
#   OWNCI_SELF_DATA   data directory of this runner (default: ~/.local/share/ownci-self)
#   OWNCI_SELF_LOGIN  your GitHub login (default: from 'gh api user')
#
# The repository is public, so setup needs --allow-public-repo. The workflow runs only for
# pull requests from the owner, members and collaborators, and on its author's runner.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

repo="${OWNCI_SELF_REPO:-ashfaqc137/ownci}"
data="${OWNCI_SELF_DATA:-$HOME/.local/share/ownci-self}"
bin_dir="${XDG_CACHE_HOME:-$HOME/.cache}/ownci-self"

if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "refusing: uncommitted changes; the runner's label names the commit it was built from" >&2
  exit 1
fi
login="${OWNCI_SELF_LOGIN:-$(gh api user -q .login)}"
sha="$(git rev-parse HEAD)"

make build
mkdir -p "$bin_dir"
install -m 0755 bin/ownci "$bin_dir/ownci"
ownci=("$bin_dir/ownci" --data-dir "$data")
"${ownci[@]}" version

force=()
if [ -f "$data/config/config.json" ]; then
  "${ownci[@]}" stop
  force=(--force)
fi
"${ownci[@]}" setup --repo "$repo" --allow-public-repo --labels "dev-$login,build-$sha" "${force[@]}"
"${ownci[@]}" start --detach
echo "Serving the ownci check for $(git rev-parse --short HEAD) on $repo. Push this commit if you have not."
