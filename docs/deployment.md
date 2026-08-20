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
    if [ -L control-state-data ] || { [ -e control-state-data ] && [ ! -d control-state-data ]; }; then echo 'refusing unsafe state path' >&2; exit 1; fi
    sudo install -d -o 65532 -g 65532 -m 700 control-state-data

    # 3. Configure credentials
    install -m 600 .env.example .env
    # Edit .env: set BLUEPRINT_NAMES=otlp-native (or other exact names), set
    # DRY_RUN=false, and fill GC_TOKEN, GC_PROM_RW/USER, GC_OTLP_ENDPOINT/USER,
    # GC_LOKI/USER at minimum. Empty BLUEPRINT_NAMES emits nothing.

    # 4. Start (pulls ghcr.io/rknightion/synthkit:latest from GHCR)
    docker compose up -d

    # 5. Verify
    open http://127.0.0.1:8088/control/ui
    curl -s -u control http://127.0.0.1:8088/control/status | jq
    ```

=== "Local binary"

    ```bash
    go build ./cmd/synthkit

    # Dry run (offline, no push):
    DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump

    # Live run:
    install -m 600 .env.example .env   # fill credentials
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
if [ -L control-state-data ] || { [ -e control-state-data ] && [ ! -d control-state-data ]; }; then echo 'refusing unsafe state path' >&2; exit 1; fi
sudo install -d -o 65532 -g 65532 -m 700 control-state-data
```

If a control-plane change made in the operator UI doesn't survive a restart, check `persist.last_error` in `/control/status` — a `permission denied` there confirms the ownership problem.

To wipe state and start clean, delete (or truncate) `control-state-data/control-state.json` on the host and restart.

---

## Networking and exposure

By default `SYNTHKIT_BIND=127.0.0.1` — the control plane is loopback-only and may remain token-free.

!!! danger "Non-loopback exposure requires an explicit policy"
    Startup fails unless a non-loopback deployment has `CONTROL_TOKEN` and
    `CONTROL_EXPOSURE_ACK=trusted-network` or `tls-proxy`. Basic auth protects sensitive reads and
    mutations but does not encrypt them. Never send it over untrusted plaintext HTTP.

| Scenario | What to do |
|---|---|
| Grafana Cloud Infinity datasource on a **different host** | Set a non-loopback `SYNTHKIT_BIND`, `CONTROL_TOKEN`, and the appropriate `CONTROL_EXPOSURE_ACK`; configure the datasource with Basic auth. |
| Grafana Cloud reaching it **privately** | Use a PDC Tailscale connection — Grafana Cloud reaches the Tailscale IP directly; no public exposure needed. |
| Browser-trusted HTTPS endpoint | Run `tailscale serve https:443 / http://127.0.0.1:8088` alongside synthkit. |
| Secure remote access | SSH-tunnel: `ssh -L 8088:localhost:8088 <host>` and access `http://localhost:8088/control/ui` locally. |

The compose file uses the same interpolated `${SYNTHKIT_BIND:-127.0.0.1}` for port publication and
inside the container's exposure check. The binary itself binds `0.0.0.0:8088` inside the container.

---

## Container image

The published multi-arch image (amd64 + arm64) is at:

```text
ghcr.io/rknightion/synthkit:<vX.Y.Z>
ghcr.io/rknightion/synthkit:latest
ghcr.io/rknightion/synthkit:main
```

Built by CI on each push to `main` and each tagged release. The image is signed with cosign and ships with SBOM and provenance attestations.

The `docker-compose.yml` pulls this image by default — no local build step is required. The tag is controlled by `SYNTHKIT_IMAGE_TAG` in `.env`:

| Value | Image | Notes |
|---|---|---|
| `latest` (default) | last tagged release | Only exists once the first release has been cut. Until then, set `SYNTHKIT_IMAGE_TAG=main`. |
| `main` | bleeding-edge default-branch build | Always available; rebuilt on every push to `main`. |
| `vX.Y.Z` | pinned release | Use to lock a specific version. |

**Building from source (opt-in).** If you need to test local changes, override the compose file:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

The `VERSION` build-arg is stamped as `service.version` in self-observability and profiling data; the published image already has this stamped by CI.

---

## Updating

!!! warning "Selection default changed to emit nothing"
    Empty or unset `BLUEPRINT_NAMES` now starts setup mode. Before upgrading an existing deployment
    that relied on the former implicit full catalog, set `BLUEPRINT_NAMES=*` to preserve that
    behavior, or preferably list the exact blueprint identities you intend to emit.

```bash
# On the host:
git pull --ff-only
docker compose up -d
```

`git pull` picks up any changes to `docker-compose.yml` itself; `docker compose up -d` re-checks the registry and pulls the newest digest for the configured tag (`pull_policy: always` is set in the compose file). The `.env` file is gitignored and survives the pull. State in `control-state-data/` survives the restart (the compose `restart: unless-stopped` policy keeps the container running through host reboots).

---

## Counter resets and rate windows

Container restart = counter reset = a clean `rate()` window in Grafana. This is intentional. No counter-state volume exists or should — synthetic counters restart from zero on each run, which produces a brief stale window in `rate()` queries after a restart but no stale-series accumulation. Plan maintenance windows accordingly or use `increase()` with a long lookback.

---

## See also

- [configuration.md](configuration.md) — all environment variables
- [RUNBOOK.md](RUNBOOK.md) — credentials → telemetry end-to-end walkthrough
- [control-plane.md](control-plane.md) — operator UI and HTTP API
