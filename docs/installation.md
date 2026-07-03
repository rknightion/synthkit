---
title: Installation
description: Build synthkit from source with Go 1.26, or pull the prebuilt multi-arch container image from GHCR.
---

# Installation

synthkit ships as a single self-contained binary. Build from source with Go, or pull the prebuilt container image from GitHub Container Registry.

## Prerequisites

=== "Source build"

    - **Go 1.26.4 or later** — synthkit's `go.mod` specifies `go 1.26.4`. Earlier toolchain versions will be rejected.
    - Git (to clone the repo).

    No CGO. The binary is fully static and cross-compiles cleanly.

=== "Docker"

    - Docker (or any OCI-compatible runtime).
    - No Go installation needed — the prebuilt image is a distroless static binary.

---

## Build from source

```bash
git clone https://github.com/rknightion/synthkit.git
cd synthkit
go build ./cmd/synthkit
```

This produces a `synthkit` binary in the current directory.

!!! tip "Full gate"
    Before shipping changes, run `make gate` — that runs `build`, `vet`, `test` (with the race detector), and the SPDX + hygiene checks. For a quick sanity check, `go build ./... && go vet ./... && go test ./...` is sufficient.

---

## Run the prebuilt container image

The multi-arch image (linux/amd64 + linux/arm64) is published to GHCR on each release:

```text
ghcr.io/rknightion/synthkit:latest
ghcr.io/rknightion/synthkit:<vX.Y.Z>
```

The image is distroless (based on `gcr.io/distroless/static-debian12:nonroot`) and runs as **uid 65532 (nonroot)**. It has no shell, no package manager, and no writable filesystem except the `/data` volume.

```bash
# Dry run — prints the series inventory, pushes nothing
docker run --rm \
  -e DRY_RUN=true \
  ghcr.io/rknightion/synthkit:latest -once -dump
```

For a persistent run with credentials, use the docker-compose path below or mount a `.env` file.

---

## docker-compose (recommended for standing deployments)

The repository ships a `docker-compose.yml` that reads all configuration from a `.env` file (gitignored — never commit secrets).

**First-time setup:**

```bash
# 1. Clone the repo
git clone https://github.com/rknightion/synthkit.git
cd synthkit

# 2. Create the .env file and fill in your credentials
cp .env.example .env
# edit .env — see Credentials for what to fill in

# 3. Create the state bind-mount directory and give it to the container user
#    (uid 65532 = distroless nonroot; a single-file mount breaks atomic save)
mkdir -p control-state-data && sudo chown -R 65532:65532 control-state-data

# 4. Pull the image and start
#    The image is pulled from ghcr.io/rknightion/synthkit (SYNTHKIT_IMAGE_TAG in .env,
#    defaults to "latest"). Set SYNTHKIT_IMAGE_TAG=main for the bleeding-edge build.
#    Note: "latest" only exists once the first release has been cut.
docker compose up -d
```

The container binds the control plane on port **8088** inside the container. Host exposure is controlled by `SYNTHKIT_BIND` in `.env` (defaults to `127.0.0.1` — loopback only, safe by default). The operator UI is available at:

```text
http://localhost:8088/control/ui
```

!!! warning "Control plane is unauthenticated by default"
    POST routes (`/control/scenarios`, `/control/scaling`, `/control/failures`, `/control/load`) require no authentication unless `CONTROL_TOKEN` is set in `.env`. Keep the default `SYNTHKIT_BIND=127.0.0.1` unless you are on a trusted network; use an SSH tunnel or `tailscale serve` to reach it remotely.

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
