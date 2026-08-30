#!/bin/sh
# Execute the public clean-host Compose path in order. Invoke this script from a
# disposable Go 1.27 container that already has Git, Docker Engine access, and
# Docker Compose 2.24.4+; it deliberately starts without Bash, Python, or just.

set -eu

if [ "${1:-}" != "--after-bootstrap" ]; then
  apt-get update
  apt-get install -y --no-install-recommends bash ca-certificates curl python3
  exec bash "$0" --after-bootstrap
fi

set -euo pipefail

cleanup_workspace=""
cleanup_root=""
cleanup_project=""
cleanup() {
  if [ -n "$cleanup_root" ] && [ -n "$cleanup_project" ]; then
    docker compose --project-name "$cleanup_project" -f "$cleanup_root/docker-compose.yml" \
      down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  if [ -n "$cleanup_workspace" ]; then
    rm -rf -- "$cleanup_workspace"
  fi
}
trap cleanup EXIT

command -v git >/dev/null
command -v docker >/dev/null
docker compose version >/dev/null

curl --proto '=https' --tlsv1.2 -fsSL https://just.systems/install.sh \
  | bash -s -- --tag 1.58.0 --to "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"

bash --version
python3 --version
just --version
docker --version
docker compose version

cleanup_workspace="$(mktemp -d)"
cleanup_root="$cleanup_workspace/synthkit"
git clone https://github.com/rknightion/synthkit.git "$cleanup_root"
cd "$cleanup_root"

install -m 600 .env.example .env

state_dir="$PWD/control-state-data"
if [ -L "$state_dir" ] || { [ -e "$state_dir" ] && [ ! -d "$state_dir" ]; }; then
  echo "refusing non-directory or symlink state path: $state_dir" >&2
  exit 1
fi
mkdir -p "$state_dir"
chmod 700 "$state_dir"
docker run --rm --volume "$state_dir:/data" --entrypoint /bin/sh \
  node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 \
  -ceu 'chown 65532:65532 /data; chmod 0700 /data'

cleanup_project="synthkit-clean-${RANDOM}-${RANDOM}"
just compose-check
BLUEPRINT_NAMES=otlp-native docker compose --project-name "$cleanup_project" \
  run --rm --no-deps synthkit -once -dump

docker compose --project-name "$cleanup_project" up -d --wait
docker compose --project-name "$cleanup_project" ps --format json \
  | python3 -c 'import json, sys; items = [json.loads(line) for line in sys.stdin if line.strip()]; rows = [row for item in items for row in (item if isinstance(item, list) else [item])]; assert any(row.get("Service") == "synthkit" and row.get("Health") == "healthy" for row in rows)'
docker run --rm --network "${cleanup_project}_default" --entrypoint /bin/sh \
  node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 \
  -ceu 'wget -qO- http://synthkit:8088/control/readiness' \
  | python3 -c 'import json, sys; r = json.load(sys.stdin); assert r["ready"] is True and r["setup_required"] is True and r["live_ready"] is False and r["blueprints"]["loaded"] == 0 and r["persisted_state"]["writable"] is True'
