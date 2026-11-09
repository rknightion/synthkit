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
    - A normal shell and browser. Docker Desktop includes the engine and Compose on macOS and Windows; use a WSL terminal for the shell commands on Windows.
    - Git **2.39 or later** to obtain the public checkout.
    - No Go installation needed — the prebuilt image is a distroless static binary.

Host Go, Node.js, Python, Bash 5, `just`, `gcx` and external documentation-query plugins are not prerequisites for the basic container start. The complete [Docker Quick Start](quickstart.md) includes configuration, state preparation, dry run, live delivery and browser readback.

Python 3.11+ and Docker Buildx are additional prerequisites for the strict running-image identity helper. Independent publication/signature verification also uses GitHub CLI. These checks have their own evidence requirements in [Deployment](deployment.md#container-image); basic Docker observations do not replace them.

## Platform choices

- **Linux:** use the source path below, or Docker Engine with the Compose plugin. The clean-container
  recipe uses a Debian-family root shell; it is not an unprivileged package-install command.
- **macOS:** the source path uses the same commands once the prerequisites are installed. Ensure
  Bash 5+ is on `PATH` (`/bin/bash` is older). For containers, use a running Docker-compatible
  engine with Compose and check its versions before following the Compose steps. The Docker Quick Start does not require the host source-build toolchain. Docker Desktop and other compatible engines may differ in image-store identity; retain the exact image and runtime observations.

Choose the [Quick Start](quickstart.md) for the Docker path. Published images already contain the operator UI. Use [Build from source](#build-from-source) when developing synthkit or when deliberately selecting features absent from the published image.

## Clean public clone to healthy Compose

Follow the complete [Docker Quick Start](quickstart.md). It is the canonical first-run procedure for a fresh public clone on a Docker-capable workstation, including macOS. It needs Git, Docker Engine 24+, Compose 2.24.4+, a shell and a browser. It uses the published pin from `.env.example`, prepares the private state directory through a public helper container, and selects one blueprint explicitly.

A credential-free preview is useful on its own. Live delivery additionally needs the seven ingest settings described in that walkthrough and access to the destination Grafana stack. Empty `BLUEPRINT_NAMES` is intentional setup mode, not a telemetry demo. Keep self-observability off for this first path; it is a separate destination and credential configuration.

The full contributor environment and its `just compose-check` validation remain in [Contributing](contributing.md). They are not prerequisites for an operator's first container run. This section retains its public anchor for existing links; the command sequence lives in the Quick Start rather than in a second copy here.

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

For a first deployment, follow the [Docker Quick Start](quickstart.md) through state preparation, offline preview, credential preflight and live readback. It preserves an existing `.env` and does not require the contributor toolchain.

After that setup, the ordinary start command is:

```bash
docker compose up -d --wait --wait-timeout 180 synthkit
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
