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
    - For the operator UI: Node.js **24** with npm, Bash **5+**, and `just` **1.58.0+**.
      Build the UI before Go so the assets are embedded in the binary.

    No CGO. The binary is fully static and cross-compiles cleanly.

=== "Docker"

    - Docker Engine **24.0 or later** (the supported local daemon).
    - Docker Compose **2.24.4 or later** for the committed standing-deployment contract.
    - Bash **5.0 or later**, Python **3.11 or later**, and `just` **1.58.0 or later**.
    - Git **2.39 or later** to obtain the public checkout.
    - No Go installation needed — the prebuilt image is a distroless static binary.

`gcx` **1.2.0 or later** is needed only for the optional remote Grafana verification commands in
the [runbook](RUNBOOK.md); it is not needed to start Compose or inspect the local control plane.

## Platform choices

- **Linux:** use the source path below, or Docker Engine with the Compose plugin. The clean-container
  recipe uses a Debian-family root shell; it is not an unprivileged package-install command.
- **macOS:** the source path uses the same commands once the prerequisites are installed. Ensure
  Bash 5+ is on `PATH` (`/bin/bash` is older). For containers, use a running Docker-compatible
  engine with Compose and check its versions before following the Compose steps. The clean Linux
  container regression does not establish macOS container-runtime compatibility.

Choose the [Quick Start](quickstart.md) for a complete source-build walkthrough. Published images
already contain the operator UI; their host does not need Go or Node.

## Clean public clone to healthy Compose

This Linux path is exercised by the clean-container regression. Start with Git, Docker Engine 24+
and Docker Compose 2.24.4+ available. The fixture also contains Go, but the prebuilt-image path
below does not use it. Run the package installation commands as root in a Debian-family environment;
on an existing host, install the listed prerequisites using your normal administrative process.

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
if [ -L .env ] || { [ -e .env ] && [ ! -f .env ]; }; then
  echo "refusing symlink or non-file .env" >&2
  exit 1
fi
if [ ! -e .env ]; then
  install -m 600 .env.example .env
else
  echo "Keeping existing .env; review it before continuing."
fi
chmod 600 .env
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
just compose-check </dev/null
BLUEPRINT_NAMES=otlp-native docker compose run --rm --no-deps \
  -e DRY_RUN=true -e SELFOBS_ENABLED=false synthkit -once -dump
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
just ui </dev/null
go build ./cmd/synthkit
```

This produces a `synthkit` binary with the operator UI in the current directory. `just ui` uses
the npm lockfile. A Go-only build is useful for CLI-only work, but without built frontend assets
its `/control/ui` route reports that the UI is not built.

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
  -e DRY_RUN=true -e SELFOBS_ENABLED=false \
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
just compose-check </dev/null
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

The local-source override builds both the UI and Go binary inside Docker.

For the full production deployment guide — including the persistent volume setup, live credential rotation, and upgrade path — see [Deployment](deployment.md).

---

## Next steps

- [Credentials](credentials.md) — fill in `.env` correctly
- [Quick Start](quickstart.md) — from binary to live data
- [Deployment](deployment.md) — standing production deploy on a host
