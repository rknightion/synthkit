---
title: Configuration
description: Complete environment-variable reference for synthkit, grouped by function with defaults and purpose.
---

# Configuration

All synthkit configuration is supplied via environment variables — either in a `.env` file (loaded from the working directory) or as process-level env vars that override the file. The `.env.example` at the repo root is the authoritative list of every variable synthkit reads.

## The `.env` contract

```bash
install -m 600 .env.example .env
# fill values in-place, then:
docker compose up -d --wait     # or: ./synthkit
```

**Keep comments on their own line.** Docker Compose's `env_file` does NOT strip inline comments — `TOKEN=abc123 # my token` sets the variable to the literal string `abc123 # my token`. Put comments above the value, never beside it.

`DRY_RUN` defaults to **`true`**. A live push is always an explicit opt-in (`DRY_RUN=false`). This
is a deliberate safety default: after explicitly selecting blueprints, you can inspect their full
series inventory offline with no risk of pushing synthetic data.

Cross-references: for where to obtain the sink credentials see [credentials.md](credentials.md); for self-observability tuning see [self-observability.md](self-observability.md); for Synthetic Monitoring setup see [synthetic-monitoring.md](synthetic-monitoring.md); for Fleet Management setup see [fleet-management.md](fleet-management.md).

---

## Synthetic data sinks

One Grafana Cloud Access Policy (CAP) token with `metrics:write`, `logs:write`, `traces:write`, and `profiles:write` covers all synthetic sinks. The `GC_*_USER` values are numeric data-source instance IDs, not email addresses.

| Variable | Default | Purpose |
|---|---|---|
| `GC_TOKEN` | _(empty)_ | Shared CAP token for metrics, logs, traces, and profiles pushes. Required for live mode. |
| `GC_PROM_RW` | _(empty)_ | Mimir Remote-Write v2 push URL, e.g. `https://prometheus-prod-XX-<region>.grafana.net/api/prom/push` |
| `GC_PROM_USER` | _(empty)_ | Mimir instance ID (HTTP Basic username for `GC_PROM_RW`). |
| `GC_OTLP_ENDPOINT` | _(empty)_ | OTLP gateway base URL, e.g. `https://otlp-gateway-<region>.grafana.net/otlp`. Traces only; `/v1/traces` is appended automatically. |
| `GC_OTLP_USER` | _(empty)_ | Stack ID (HTTP Basic username for `GC_OTLP_ENDPOINT`). |
| `GC_LOKI` | _(empty)_ | Loki push URL, e.g. `https://logs-prod-XXX.grafana.net/loki/api/v1/push` |
| `GC_LOKI_USER` | _(empty)_ | Loki instance ID (HTTP Basic username for `GC_LOKI`). |
| `GC_PROFILES_URL` | _(empty)_ | Pyroscope ingest endpoint for **synthetic** profiles (the target stack, not the self-obs stack). |
| `GC_PROFILES_USER` | _(empty)_ | Pyroscope instance ID for the synthetic profiles sink. |

!!! note "Live-push validation"
    When `DRY_RUN=false`, synthkit validates that `GC_TOKEN`, `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`, `GC_LOKI`, and `GC_LOKI_USER` are all set and exits with an error if any are missing. RUM, profiles, SM, and FM are optional and validated independently.

---

## Faro / RUM

RUM is disabled when either variable is empty.

| Variable | Default | Purpose |
|---|---|---|
| `GC_FARO_COLLECTOR` | _(empty)_ | Faro collector URL including the app key path, e.g. `https://faro-collector-<region>.grafana.net/collect/<app-key>` |
| `GC_FARO_APP_KEY` | _(empty)_ | Faro application key. Both must be set to enable RUM emission. |

---

## Behaviour

| Variable | Default | Purpose |
|---|---|---|
| `DRY_RUN` | `true` | Set to `false` to push live data. **Defaults to `true`** — live push is always opt-in. |
| `TICK_DEFAULT` | `5s` | Master-clock cadence. Go duration string (`5s`, `1m`, `30s`). All constructs tick at a multiple of this. |
| `MAX_DPM_PER_SERIES` | `6` | Maximum per-series cadence an explicit blueprint `high_dpm.metric_interval` may request. The default permits a 10-second interval. This is a validation ceiling, not a series-count cap. |
| `SERIES_CAP` | _(empty, unlimited)_ | Optional global per-push series backstop. Set a positive integer to truncate an individual metric push — a kill switch for runaway cardinality. It does not change cadence or enforce DPM per series. |
| `BLUEPRINTS` | `./blueprints` | Directory containing available bundled-style `*.yaml` blueprints. In Docker Compose this is `/app/blueprints`. Availability does not enable emission. |
| `BLUEPRINT_NAMES` | _(empty)_ | Runtime selection: empty/unset starts setup mode and emits nothing; a comma-separated exact-name list loads only those identities; `*` explicitly loads the complete available catalog. |
| `JSON_HTTP_ADDR` | `127.0.0.1:8088` | Address the process binds for the control plane and Infinity JSON host. In Docker compose this is overridden to `0.0.0.0:8088` (bind all interfaces inside the container; host exposure is controlled by `SYNTHKIT_BIND`). |
| `CONFIG_SNAPSHOT_PATH` | `./control-state.json` | Path where control-plane state is persisted across restarts. In Docker compose this is overridden to `/data/control-state.json` (on the `/data` volume). |
| `CONTROL_TOKEN` | _(empty)_ | HTTP Basic password (username `control`) for sensitive control/Infinity reads and all mutations. Empty is supported for loopback-only use. |
| `CONTROL_EXPOSURE_ACK` | _(empty)_ | Required for non-loopback exposure: exactly `trusted-network` for an isolated plaintext path or `tls-proxy` for a trusted HTTPS proxy. Invalid non-empty values fail startup, including on loopback. |
| `TICK_TIMEOUT` | _(empty, disabled)_ | Optional per-blueprint per-tick backstop in seconds (integer). Set `>0` only as a coarse safety net for a stuck tick. The per-sink 15 s HTTP timeout already bounds hung pushes under normal operation. |

`SERIES_CAP` and `MAX_DPM_PER_SERIES` protect different dimensions. `SERIES_CAP` truncates the
number of series in an individual push after a construct emits it. `MAX_DPM_PER_SERIES` bounds how
often each series may be sampled when a blueprint explicitly declares `high_dpm.metric_interval`.
It does not make a blueprint high-DPM on its own. The per-blueprint `series_budget` is separate again:
it is a fixed one-minute data-point allowance, so a 115-series blueprint at 6 DPM needs a budget of
at least 690 to sustain every scheduled sample.

---

## External / custom blueprint sources

These variables support pulling blueprints from git repositories or custom uploads via the control plane. See [custom-blueprints.md](custom-blueprints.md) for full usage.

| Variable | Default | Purpose |
|---|---|---|
| `BLUEPRINT_DATA_DIR` | `./data/blueprints` | Staging directory for custom and git-sourced blueprints. In Docker compose this is `/data/blueprints` (on the `/data` volume). |
| `GIT_POLL_INTERVAL` | `0` | Seconds between "update available" polls for git blueprint sources. `0` = polling off; sources are fetched on demand or at startup. |
| `GIT_TOKEN` | _(empty)_ | Default HTTPS PAT for private git blueprint repos whose source config does not specify a `token_env_var`. Leave empty for public repos. |

---

## Decoupled delivery queue

The delivery queue (`internal/sink/queue`) decouples construct rendering from network I/O, allowing constructs to run at their declared cadence regardless of sink latency. All five vars have safe defaults; tune only if you see backpressure warnings in [self-observability](self-observability.md) or the operator UI.

| Variable | Default | Purpose |
|---|---|---|
| `SEND_SHARDS` | `8` | Parallel shard workers per sink. Higher values allow more concurrent HTTP requests to a sink. |
| `SEND_BATCH_MAX` | `5000` | Maximum series per flush batch sent to a sink in one request. |
| `SEND_BATCH_DEADLINE` | `5s` | Maximum age before a partial batch is flushed, even if `SEND_BATCH_MAX` is not reached. Go duration string. |
| `SEND_QUEUE_CAPACITY` | `500000` | Ring-buffer depth in series slots. Memory is consumed only when the buffer actually fills under backpressure; this is cheap headroom. Raise for very high cluster counts. |
| `SEND_DRAIN_DEADLINE` | `30s` | Graceful-shutdown drain budget. synthkit waits up to this long for queued series to flush before exiting. Go duration string. |

---

## Host bind (Docker Compose only)

These values are consumed by Docker Compose, not by the synthkit binary itself.

| Variable | Default | Purpose |
|---|---|---|
| `SYNTHKIT_IMAGE_REF` | committed eligible release | Preferred complete GHCR reference, ideally `ghcr.io/rknightion/synthkit@sha256:<index>`. A malformed or unavailable preferred value fails; it never falls back silently. |
| `SYNTHKIT_IMAGE_TAG` | _(empty)_ | Legacy bare-tag fallback used only when `SYNTHKIT_IMAGE_REF` is absent or empty. A non-empty legacy value is ignored when the preferred reference exists. |
| `SYNTHKIT_ENV_FILE` | `.env` | Service env-file path used by Compose. Keep one value for every command in a deployment. |
| `SYNTHKIT_BIND` | `127.0.0.1` | Host interface on which Docker Compose publishes port 8088. Any non-loopback value requires `CONTROL_TOKEN` plus `CONTROL_EXPOSURE_ACK`; Compose passes this exact interpolated value into the container for validation. |

The committed default is a published image with the current healthcheck and rollback contract, not
`main` or `latest`. Prefer the verified index digest. Never run raw `docker compose config` against a
real credential file; `make compose-check` renders the deployment using `.env.example` as fake input.
Selector assignments may be quoted or prefixed with `export`, matching Compose, but the deployment
helper requires the selected value itself to be a direct literal image reference. Do not build it
through interpolation from another environment variable.

---

## Self-profiling (Pyroscope)

These variables configure continuous profiling of the **synthkit process itself** — not synthetic profile data sent to the target stack (see `GC_PROFILES_URL` above). This lane ships to a **separate** self-observability stack via its own credential triplet; it never uses `GC_TOKEN`. It follows `SELFOBS_ENABLED` and is independent of synthetic `DRY_RUN`.

| Variable | Default | Purpose |
|---|---|---|
| `GC_PYROSCOPE_URL` | _(empty)_ | Pyroscope ingest server URL for the self-obs stack, e.g. `https://profiles-prod-XXX.grafana.net` |
| `GC_PYROSCOPE_USER` | _(empty)_ | Pyroscope instance ID (self-obs stack). |
| `GC_PYROSCOPE_PASSWORD` | _(empty)_ | `profiles:write` credential for the self-obs stack. Never `GC_TOKEN`. |
| `PYROSCOPE_TAGS` | _(empty)_ | CSV of `key=value` resource tag pairs attached to all self-profiling data. |
| `PYROSCOPE_MUTEX_FRACTION` | `5` | `runtime.SetMutexProfileFraction` rate. `0` = off; `5` is high-fidelity and appropriate for a lab process. |
| `PYROSCOPE_BLOCK_RATE` | `5` | `runtime.SetBlockProfileRate` in nanoseconds. `0` = off. |

---

## Self-observability (OTLP)

RED metrics on the synthetic pipeline, Go runtime metrics, per-tick traces, and the operational log stream. Ships to a **separate** stack via its own credential triplet; never uses `GC_TOKEN`. Off by default; decoupled from `DRY_RUN`. See [self-observability.md](self-observability.md) for the full signal catalogue and dashboard setup.

| Variable | Default | Purpose |
|---|---|---|
| `SELFOBS_ENABLED` | `false` | Master switch. Set to `true` to enable self-observability telemetry. |
| `GC_SELF_OTLP_ENDPOINT` | _(empty)_ | Base OTLP gateway URL for the self-obs stack (`/v1/{signal}` is appended). |
| `GC_SELF_OTLP_USER` | _(empty)_ | Self-obs stack ID (HTTP Basic username). |
| `GC_SELF_OTLP_PASSWORD` | _(empty)_ | `metrics:write`, `logs:write`, `traces:write` credential for the self-obs stack. Never `GC_TOKEN`. |
| `SELFOBS_TAGS` | _(empty)_ | CSV of `key=value` resource attribute pairs attached to all self-obs data. |
| `GC_SELF_GRAFANA_URL` | _(empty)_ | Staff Grafana base URL (e.g. `https://your-stack.grafana.net`). When set, enables deep-links from the control UI to the self-obs dashboard. Non-secret. |
| `SELFOBS_METRIC_INTERVAL` | `15s` | Self-obs metric flush cadence. Traces and logs are unaffected. Go duration string. |

---

## Synthetic Monitoring provisioner

These credentials authorize only the one-shot, version-matched `sm-provision` Compose job (or the
same binary in a source checkout). The emitter uses their presence to bind and validate its private
snapshot, but it never calls the SM API: provisioning, registration persistence, and the required
emitter restart remain separate phases. See [synthetic-monitoring.md](synthetic-monitoring.md).

| Variable | Default | Purpose |
|---|---|---|
| `GC_SM_URL` | _(empty)_ | Synthetic Monitoring API endpoint, e.g. `https://synthetic-monitoring-api-<region>.grafana.net` |
| `GC_SM_TOKEN` | _(empty)_ | SM API token (a dedicated SM token, NOT `GC_TOKEN`). |
| `SM_PROVISION_APPLY` | `false` | Compose provisioner write gate; only exact `true` permits mutations. |
| `SM_PROVISION_ADOPT_LEGACY` | `false` | Exact `true` on preview records an exact-match adoption marker; the same flag plus apply consumes it. |
| `SM_PROVISION_MIGRATE_TARGET` | `false` | Exact `true` enables the preview-bound credential/endpoint rotation path; apply must consume the identical marker within 15 minutes. |

---

## Fleet Management

| Variable | Default | Purpose |
|---|---|---|
| `GC_FM_URL` | _(empty)_ | Fleet Management API endpoint, e.g. `https://fleet-management-prod-0NN.grafana.net` |
| `GC_FM_STACK_ID` | _(empty)_ | FM basic-auth username = Grafana Cloud stack ID. **Not** `GC_PROM_USER`. |
| `GC_FM_TOKEN` | _(empty)_ | CAP token with `fleet-management:write`. Not `GC_TOKEN`. |

!!! note "FM metrics without FM registration"
    When the `GC_FM_*` triplet is empty but a blueprint declares a `fleet_management` construct, synthkit still emits `alloy_*` metrics — it just skips the FM API registration. Fill all three vars to have collectors appear in the Fleet Management app.

---

## Container runtime hint

| Variable | Default | Purpose |
|---|---|---|
| `SYNTHKIT_IN_CONTAINER` | _(empty)_ | Set to any non-empty value when running inside a container so the control-plane bind warning recognises a `0.0.0.0` bind as expected (the real host exposure is restricted by Docker's port mapping). Docker Compose can leave this blank — synthkit auto-detects `/.dockerenv`. |
