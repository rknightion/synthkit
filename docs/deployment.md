---
title: Deployment
description: How to deploy synthkit with Docker Compose, including volume setup, networking, and the published container image.
---

# Deployment

The canonical deployment is Docker Compose on a persistent host. The committed `docker-compose.yml` is secret-free — all credentials and configuration come from a gitignored `.env` file you provision on the host.

---

## Quick deploy

=== "Docker Compose (canonical)"

    ```bash
    # 1. Clone the repo
    git clone https://github.com/rknightion/synthkit.git
    cd synthkit

    # 2. Create the state bind-mount directory, owned by the container's uid
    #    (distroless nonroot = uid 65532). This is a one-time step per host.
    mkdir -p control-state-data
    sudo chown -R 65532:65532 control-state-data

    # 3. Configure credentials
    cp .env.example .env
    # Edit .env: set DRY_RUN=false and fill GC_TOKEN, GC_PROM_RW/USER,
    # GC_OTLP_ENDPOINT/USER, GC_LOKI/USER at minimum.

    # 4. Start
    docker compose up -d --build

    # 5. Verify
    open http://127.0.0.1:8088/control/ui
    curl -s http://127.0.0.1:8088/control/status | jq
    ```

=== "Local binary"

    ```bash
    go build ./cmd/synthkit

    # Dry run (offline, no push):
    DRY_RUN=true ./synthkit -once -dump

    # Live run:
    cp .env.example .env   # fill credentials
    ./synthkit
    ```

---

## The `/data` volume contract

!!! warning "Must be a DIRECTORY — not a single-file bind mount"
    The control plane saves state atomically (write → rename). A single-file bind mount breaks the rename step and silently wipes state on every tick. Mount a **directory** and let synthkit manage the files inside it.

The `/data` directory holds:

- `control-state.json` — live control-plane state (volume multiplier, active scenarios, scaling overrides). Written lazily on the first mutation; absent at startup is normal.
- `blueprints/` — staged custom and git-sourced blueprints (subdirectories `custom/`, `git/<id>/`, `.boot-manifest.json`).

The container image runs as **uid 65532** (distroless nonroot). The bind-mount directory must be owned by this uid or state saves fail:

```bash
mkdir -p control-state-data
sudo chown -R 65532:65532 control-state-data
```

If a control-plane change made in the operator UI doesn't survive a restart, check `persist.last_error` in `/control/status` — a `permission denied` there confirms the ownership problem.

To wipe state and start clean, delete (or truncate) `control-state-data/control-state.json` on the host and restart.

---

## Networking and exposure

By default `SYNTHKIT_BIND=127.0.0.1` — the control plane binds **loopback only**. This is the safe default because the control plane accepts write mutations and has no authentication unless `CONTROL_TOKEN` is set.

!!! danger "Set CONTROL_TOKEN before exposing to a network"
    The control plane accepts POST mutations without authentication when `CONTROL_TOKEN` is empty. Never set `SYNTHKIT_BIND=0.0.0.0` on an untrusted network without also setting `CONTROL_TOKEN`.

| Scenario | What to do |
|---|---|
| Grafana Cloud Infinity datasource on a **different host** | Set `SYNTHKIT_BIND=0.0.0.0` (or a specific Tailscale/LAN IP) in `.env`, set `CONTROL_TOKEN`, and restart. |
| Grafana Cloud reaching it **privately** | Use a PDC Tailscale connection — Grafana Cloud reaches the Tailscale IP directly; no public exposure needed. |
| Browser-trusted HTTPS endpoint | Run `tailscale serve https:443 / http://127.0.0.1:8088` alongside synthkit. |
| Secure remote access | SSH-tunnel: `ssh -L 8088:localhost:8088 <host>` and access `http://localhost:8088/control/ui` locally. |

The compose file publishes `${SYNTHKIT_BIND:-127.0.0.1}:8088:8088` — the binary inside the container always binds `0.0.0.0:8088` (all interfaces inside the container) so Docker's port-mapping can reach it; the host-side IP is restricted by `SYNTHKIT_BIND`.

---

## Container image

The published multi-arch image (amd64 + arm64) is at:

```
ghcr.io/rknightion/synthkit:<vX.Y.Z>
ghcr.io/rknightion/synthkit:latest
```

Built by the [`publish.yml`](https://github.com/rknightion/synthkit/blob/main/.github/workflows/publish.yml) workflow on each release. The `VERSION` build-arg is stamped as `service.version` in self-observability and profiling data.

To pull a specific release rather than building locally, update `docker-compose.yml` to use the image reference instead of the `build:` block.

---

## Updating

```bash
# On the host:
git pull --ff-only
docker compose up -d --build
```

The `.env` file is gitignored and survives the pull. State in `control-state-data/` survives the restart (the compose `restart: unless-stopped` policy keeps the container running through host reboots).

---

## Counter resets and rate windows

Container restart = counter reset = a clean `rate()` window in Grafana. This is intentional. No counter-state volume exists or should — synthetic counters restart from zero on each run, which produces a brief stale window in `rate()` queries after a restart but no stale-series accumulation. Plan maintenance windows accordingly or use `increase()` with a long lookback.

---

## See also

- [configuration.md](configuration.md) — all environment variables
- [RUNBOOK.md](RUNBOOK.md) — credentials → telemetry end-to-end walkthrough
- [control-plane.md](control-plane.md) — operator UI and HTTP API
