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
- `runtime/` — owner-only Synthetic Monitoring snapshot, activation registration, durable ownership
  ledger, adoption-preview marker, lock, and pending journal used by the version-matched one-shot
  provisioner.

The container image runs as **uid 65532** (distroless nonroot). The bind-mount directory must be owned by this uid or state saves fail:

```bash
if [ -L control-state-data ] || { [ -e control-state-data ] && [ ! -d control-state-data ]; }; then echo 'refusing unsafe state path' >&2; exit 1; fi
sudo install -d -o 65532 -g 65532 -m 700 control-state-data
```

If a control-plane change made in the operator UI doesn't survive a restart, check `persist.last_error` in `/control/status` — a `permission denied` there confirms the ownership problem.

Resetting only control-plane choices means stopping synthkit, deleting (or truncating)
`control-state-data/control-state.json`, and restarting. A full `/data` reset is different: stop the
container first, then remove the entire `control-state-data/runtime/` directory as well. That also
removes the Synthetic Monitoring ownership, registration, adoption-preview, and crash-recovery
state, so existing remote resources become foreign until they are explicitly previewed and
adopted. Never remove runtime state from a running container.

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

## Docker-only Synthetic Monitoring provisioning

`docker compose up -d` never provisions Synthetic Monitoring. With an SM blueprint selected and
`GC_SM_URL`/`GC_SM_TOKEN` configured, the emitter first writes the exact private snapshot and keeps
SM emission suppressed. Preview and apply the opt-in one-shot job from the same image:

    docker compose --profile sm-provision run --rm sm-provision
    SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
    docker compose restart synthkit

The job receives only `GC_SM_URL`, `GC_SM_TOKEN`, its three explicit control flags, and `/data`; it
does not inherit the emitter's other credentials. It has no port, restart policy, host Go toolchain,
or long-running role. The restart validates the matching registration and activates the lane. If a
collision, stale binding, or pending/ambiguous journal is reported, leave the emitter suppressed
and inspect ownership before retrying. The provisioner never deletes remote resources.

Credential or endpoint rotation requires a new emitter snapshot plus an explicit
`SM_PROVISION_MIGRATE_TARGET=true` preview. Apply within 15 minutes with both the migration and apply
flags; the preview-bound migration revalidates every owned resource before atomically rebinding the
ledger and makes no remote API writes. Reconcile resource/configuration changes separately. An
interrupted post-rebind migration retains its marker and requires the same migration-plus-
apply command to resume. See [Synthetic Monitoring](synthetic-monitoring.md) for the exact sequence.

---

## Capacity, queue memory, and container limits

Each configured sink has its own in-memory queue. `SEND_QUEUE_CAPACITY` is the maximum queued item
count **per sink**, divided across `SEND_SHARDS`; it is not a byte limit. Items differ substantially:
a metric series, Loki stream, OTLP resource, Faro beacon, profile, and Sigil export retain different
maps, slices, strings, and encoded payloads. Do not estimate memory as capacity times one universal
struct size.

Use a controlled fake-sink pressure test for the selected blueprint set and calculate:

```text
retained bytes/item for sink ~= (heap_at_depth - steady_heap) / (depth_at_sample - steady_depth)
queue memory budget          ~= sum(capacity_per_sink * measured bytes/item for that sink)
container memory             >= steady_heap + queue budget + encoding/retry headroom
```

`/control/health` exposes `process.heap_bytes`; `/control/status` exposes each queue's `depth`,
`blocked_enqueues`, cumulative `dropped_items`, and current/recovered loss state. Sample after several
ticks at steady state, then during an isolated fake-sink stall. Never stall a real Grafana endpoint
to size the queue.

On 2026-08-21 the Jules single-blueprint standing deployment measured about 300.5 MiB resident
memory and 0.53% of one CPU in a one-shot `docker stats` sample. That is a baseline, not a full-queue
capacity guarantee. A practical initial allocation for a small explicit blueprint set is 1 CPU and
1 GiB, with at least 2x the measured steady heap free; increase memory or reduce
`SEND_QUEUE_CAPACITY` when the pressure calculation exceeds that headroom. For a larger catalog,
measure first rather than copying the small-deployment limit.

Example operator override:

```yaml
services:
  synthkit:
    cpus: "1.0"
    mem_limit: 1g
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

Backpressure is intentional: when one shard buffer fills, the producer tick blocks instead of
growing memory without bound. A slow or unavailable sink can therefore lengthen a cycle and cause
later scheduled ticks to be dropped. OTLP has the longest retry window (up to five minutes), so an
OTLP outage can hold a shard and apply backpressure much longer than the other HTTP sinks. After
retry exhaustion, failed sub-batches are discarded and counted; there is no disk replay. Keep Docker
log rotation bounded because repeated outage warnings otherwise compete with queue memory and state
on the host filesystem.

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
