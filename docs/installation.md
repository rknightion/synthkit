---
title: Installation
description: Build synthkit from source with Go 1.27, or pull the prebuilt multi-arch container image from GHCR.
---

# Installation

synthkit ships as a single self-contained binary. Build from source with Go, or pull the prebuilt container image from GitHub Container Registry.

## Prerequisites

=== "Source build"

    - **Go 1.27.0 or later** — synthkit's `go.mod` specifies `go 1.27.0`. Earlier toolchain versions will be rejected.
    - Git (to clone the repo).

    No CGO. The binary is fully static and cross-compiles cleanly.

=== "Docker"

    - Docker Engine **24.0 or later** (the supported local daemon).
    - Docker Compose **2.24.4 or later** for the committed standing-deployment contract.
    - Bash **5.0 or later**, Python **3.11 or later**, and `just` **1.58.0 or later**.
    - Git **2.39 or later** to obtain the public checkout.
    - No Go installation needed — the prebuilt image is a distroless static binary.

`gcx` **1.2.0 or later** is needed only for the optional remote Grafana verification commands in
the [runbook](RUNBOOK.md); it is not needed to start Compose or inspect the local control plane.

## Clean public clone to healthy Compose

This is the one supported Linux path from a clean public clone to a healthy Compose deployment.
It is also the path exercised by the clean-container regression. The supported clean-container
baseline is Go 1.27 with Git, Docker Engine 24+, and Docker Compose 2.24.4+ available; the
commands below install the remaining required host tools without assuming `sudo`.

```bash
apt-get update
apt-get install -y --no-install-recommends bash ca-certificates curl python3
curl --proto '=https' --tlsv1.2 -fsSL https://just.systems/install.sh \
  | bash -s -- --tag 1.58.0 --to "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"

bash --version
python3 --version
just --version
docker --version
docker compose version

git clone https://github.com/rknightion/synthkit.git
cd synthkit
```

The version commands are deliberate gates: use Bash 5.0+, Python 3.11+, `just` 1.58.0+, Docker
Engine 24.0+, and Docker Compose 2.24.4+. Do not continue with a distribution-packaged `just` that
rejects the repository's `working-directory` recipe attribute.

Create `.env` without printing a secret. The credential-free path deliberately leaves
`BLUEPRINT_NAMES` empty so the standing service starts in healthy setup mode. The separate
one-shot command below selects `otlp-native` only for its offline inventory proof. For a live
deployment, first follow [Credentials](credentials.md#finding-the-three-required-numeric-identifiers),
add the required secret through the hidden-prompt helper, and select the intended blueprint before
starting Compose.

```bash
install -m 600 .env.example .env
```

Prepare the bind-mounted state directory before Compose starts. This uses a pinned public helper
container to set the distroless runtime uid, so it works from a Docker-socket-mounted clean
container and on a normal Docker host without `sudo` or a host-side ownership workaround:

```bash
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
```

Validate the committed Compose artifact with fake inputs, prove the selected inventory offline,
then start it and wait for its healthcheck:

```bash
just compose-check
BLUEPRINT_NAMES=otlp-native docker compose run --rm --no-deps synthkit -once -dump
docker compose up -d --wait
curl -fsS http://127.0.0.1:8088/control/readiness
```

The offline path must report the selected `otlp-native` inventory. The final readiness response
must contain `"ready":true`, `"setup_required":true`, `"live_ready":false`, zero loaded blueprints,
and `"persisted_state":{"writable":true}`. This is an honest setup-mode health proof, not a delivery
claim. After live credentials and an exact blueprint selection are supplied, readiness becomes
delivery-aware and `live_ready` remains false until every intended lane has pushed successfully.
Do not run raw `docker compose config` with a real `.env`.

---

## Build from source

```bash
git clone https://github.com/rknightion/synthkit.git
cd synthkit
go build ./cmd/synthkit
```

This produces a `synthkit` binary in the current directory.

!!! tip "Full gate"
    Before shipping changes, run `just check` — the pre-commit gate. Use `just ci` on a Docker-capable host for its CI-only Docker checks. For a quick sanity check, `just test` is sufficient.

---

## Run the prebuilt container image

The multi-arch image (linux/amd64 + linux/arm64) is published to GHCR on each release:

```text
ghcr.io/rknightion/synthkit:<X.Y.Z>
ghcr.io/rknightion/synthkit@sha256:<index-digest>
```

The image is distroless (based on `gcr.io/distroless/static-debian12:nonroot`) and runs as **uid 65532 (nonroot)**. It has no shell, no package manager, and no writable filesystem except the `/data` volume.

```bash
# Dry run — prints the series inventory, pushes nothing
docker run --rm \
  -e DRY_RUN=true \
  -e BLUEPRINT_NAMES=otlp-native \
  ghcr.io/rknightion/synthkit@sha256:<verified-index> -once -dump
```

For a persistent run with credentials, use the docker-compose path below or mount a `.env` file.

---

## docker-compose (recommended for standing deployments)

The repository ships a `docker-compose.yml` that reads all configuration from a `.env` file (gitignored — never commit secrets).

**First-time setup:**

```bash
# Follow the literal public-clone path above first. For a live deployment, add the required
# credentials with the hidden-prompt helper, set DRY_RUN=false, then rerun these final commands:
just compose-check
docker compose up -d --wait
```

`SYNTHKIT_IMAGE_REF` is preferred and should be the verified release index digest for a standing
deployment. `SYNTHKIT_IMAGE_TAG` is a legacy bare-tag fallback used only when the preferred value is
empty. `main` and `latest` are mutable edge tags: use them only deliberately, pull explicitly, and
record the digest actually deployed. Existing deployments must follow the
[snapshot/CAS upgrade and rollback lifecycle](deployment.md#reproducible-upgrade), not a plain
`docker compose up`.

The container binds the control plane on port **8088** inside the container. Host exposure is controlled by `SYNTHKIT_BIND` in `.env` (defaults to `127.0.0.1` — loopback only, safe by default). The operator UI is available at:

```text
http://localhost:8088/control/ui
```

!!! warning "Non-loopback exposure is fail-closed"
    Keep `SYNTHKIT_BIND=127.0.0.1` for frictionless local use. Any non-loopback value requires a
    non-empty `CONTROL_TOKEN` plus `CONTROL_EXPOSURE_ACK=trusted-network` or `tls-proxy`. Prefer an
    SSH tunnel or browser-trusted HTTPS proxy; never send Basic credentials over untrusted HTTP.

To build from source instead of pulling the published image (e.g. to test local changes):

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

For the full production deployment guide — including the persistent volume setup, live credential rotation, and upgrade path — see [Deployment](deployment.md).

---

## Next steps

- [Credentials](credentials.md) — fill in `.env` correctly
- [Quick Start](quickstart.md) — from binary to live data
- [Deployment](deployment.md) — standing production deploy on a host
