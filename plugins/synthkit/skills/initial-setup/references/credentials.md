# synthkit credential and environment reference

Use the variables below for setup; the verified checkout's `.env.example` and
`$SYNTHKIT_CHECKOUT/docs/configuration.md` own the complete environment surface. Do not invent
keys. A secret goes directly into the private `.env` through the operator's editor or hidden
terminal prompt, never through chat, command arguments, logs, a blueprint or `set-env.sh`.
A value marked **credential** is sensitive even when it is not technically a password.

Two data destinations must never share a token: the **customer/synthetic-data stack** receives
the fake signals; the **staff self-observability stack** receives synthkit's own process telemetry.

## Customer synthetic-data sinks

| Variable | Classification | Purpose / source |
|---|---|---|
| `GC_PROM_RW` | endpoint | HTTPS Prometheus remote-write URL ending in `/api/prom/push`; synthetic-data stack's Prometheus connection details. |
| `GC_PROM_USER` | identifier | Positive decimal Mimir instance ID / Basic-auth user from those Prometheus details. |
| `GC_OTLP_ENDPOINT` | endpoint | HTTPS OTLP gateway base ending in `/otlp`; synthkit appends the signal path. |
| `GC_OTLP_USER` | identifier | Positive decimal stack ID / Basic-auth user from that stack's OpenTelemetry connection details. |
| `GC_LOKI` | endpoint | HTTPS Loki push URL ending in `/loki/api/v1/push`; synthetic-data stack's Loki connection details. |
| `GC_LOKI_USER` | identifier | Positive decimal Loki instance ID / Basic-auth user from those Loki details. |
| `GC_TOKEN` | **secret** | Customer CAP token with `metrics:write`, `logs:write`, and `traces:write`. |

All seven are required for a normal live synthetic sink. `DRY_RUN=true` permits offline work
without them; do not turn it off until they are present and validated.

### Obtain the seven values in the browser

Open the intended Grafana Cloud stack, then its Prometheus, OpenTelemetry and Loki connection
details. Copy each endpoint and its accompanying numeric user ID as a pair. The three IDs are
different: Mimir instance ID, OTLP stack ID and Loki instance ID. They are digits greater than
zero, not the organisation ID, stack slug, endpoint URL or token. A token does not supply these
IDs; obtain them from the stack's connection details.

In that stack's access-policy settings, create or select a policy with `metrics:write`,
`logs:write` and `traces:write`, then generate its token for `GC_TOKEN`. This is the secret
Basic-auth password; it is not one of the three numeric users. Keep all endpoint/ID pairs and the
token bound to the same selected synthetic-data stack. Do not reuse it for process
self-observability, Sigil or Synthetic Monitoring.

Browser readback uses the operator's normal signed-in Grafana session and does not require a
query CLI or another token. An optional API query needs the relevant read scopes and per-signal
instance ID; a write-only ingest token and the OTLP stack ID do not establish query access.
The browser setup and verification steps are sufficient without specialised documentation or
Grafana query tools. If the portal layout differs, use the local
`$SYNTHKIT_CHECKOUT/docs/credentials.md` reference before the harness's ordinary web search.

## Optional synthetic-data lanes

| Variable | Classification | Gate / use |
|---|---|---|
| `GC_FARO_COLLECTOR` | **secret credential** | Faro collector URL, including its app-key path. |
| `GC_FARO_APP_KEY` | **secret credential** | Faro application key; RUM needs both Faro values and a RUM-enabled workload. |
| `GC_PROFILES_URL` | endpoint | Target-stack Pyroscope ingest endpoint. |
| `GC_PROFILES_USER` | identifier | Target-stack Profiles instance ID. |
| `GC_SIGIL_ENDPOINT` | endpoint | Sigil base endpoint. |
| `GC_SIGIL_TENANT_ID` | identifier | Sigil tenant/stack ID, not `GC_PROM_USER`. |
| `GC_SIGIL_TOKEN` | **secret** | Sigil-ingest CAP token, not `GC_TOKEN`. |

Synthetic profiles use `GC_PROFILES_URL` + `GC_PROFILES_USER` + the customer `GC_TOKEN`; there is
no profiles flag. Sigil needs its triplet and an `ai_agent` workload that uses the lane.

## Staff self-observability and profiling

| Variable | Classification | Gate / use |
|---|---|---|
| `SELFOBS_ENABLED` | non-secret flag | Master switch for self-obs and process profiling. |
| `GC_SELF_OTLP_ENDPOINT` | endpoint | Staff OTLP base endpoint. |
| `GC_SELF_OTLP_USER` | identifier | Staff stack ID. |
| `GC_SELF_OTLP_PASSWORD` | **secret** | Staff CAP token with metrics/logs/traces write scopes. |
| `SELFOBS_TAGS` | non-secret config | CSV resource tags. |
| `GC_SELF_GRAFANA_URL` | endpoint | Optional control-UI deep-link base. |
| `SELFOBS_METRIC_INTERVAL` | non-secret config | Self-obs metric flush duration; default `15s`. |
| `GC_PYROSCOPE_URL` | endpoint | Staff Profiles ingest endpoint. |
| `GC_PYROSCOPE_USER` | identifier | Staff Profiles instance ID. |
| `GC_PYROSCOPE_PASSWORD` | **secret** | Staff Profiles write token. |
| `PYROSCOPE_TAGS` | non-secret config | CSV profile tags. |
| `PYROSCOPE_MUTEX_FRACTION` | non-secret config | Runtime mutex profile rate; `0` disables it. |
| `PYROSCOPE_BLOCK_RATE` | non-secret config | Runtime block profile rate; `0` disables it. |

Profiling has no standalone enable flag. It can ship only when `SELFOBS_ENABLED=true` and the
complete `GC_PYROSCOPE_URL`/`GC_PYROSCOPE_USER`/`GC_PYROSCOPE_PASSWORD` triplet are present. It is
independent of synthetic `DRY_RUN`. The staff triplets never reuse `GC_TOKEN`.

## Optional Grafana Cloud control lanes

| Variable | Classification | Gate / use |
|---|---|---|
| `GC_FM_URL` | endpoint | Fleet Management API base. Empty means collector metrics only, no registration. |
| `GC_FM_STACK_ID` | identifier | FM Basic-auth user: Grafana Cloud stack ID, not `GC_PROM_USER`. |
| `GC_FM_TOKEN` | **secret** | CAP token with `fleet-management:write`; requires a `fleet_management` blueprint feature. |
| `GC_SM_URL` | endpoint | Synthetic Monitoring API base for the version-matched Docker `sm-provision` profile. |
| `GC_SM_TOKEN` | **secret** | Synthetic Monitoring API token for that provisioner; source checkouts or separately configured in-cluster Jobs use the same binary. The tool must match the current snapshot version; v2 activation is release-independent when specs/target/schema match. |
| `STATE_SM_CONFIGMAP` | non-secret name | Precreated aggregate SM state slot for diskless in-cluster provisioning. |
| `SM_PROVISION_LEASE_NAME` | non-secret name | Dedicated precreated Lease distinct from the emitter Lease; all writers to that SM slot use the same name. |
| `SM_PROVISION_APPLY` | non-secret flag | Exact `true` enables one-shot writes; false/absent previews. |
| `SM_PROVISION_ADOPT_LEGACY` | non-secret flag | Exact `true` records the exact-match preview and allows only that same plan on apply. |
| `SM_PROVISION_MIGRATE_TARGET` | non-secret flag | Exact `true` enables preview-bound credential/endpoint migration; apply also requires a matching marker no older than 15 minutes. |

## Control plane, blueprint sources, and delivery behaviour

| Variable | Classification | Purpose |
|---|---|---|
| `DRY_RUN` | non-secret flag | Default `true`; suppresses synthetic pushes, not self-obs/process profiling. |
| `TICK_DEFAULT` | non-secret config | Master scheduler cadence; default `5s`; affects first observation timing. |
| `TICK_TIMEOUT` | non-secret config | Optional whole-tick seconds; empty/`0` disables it. |
| `SERIES_CAP` | non-secret config | Optional global series cap. |
| `BLUEPRINTS` | non-secret path | Bundled blueprint directory. |
| `BLUEPRINT_NAMES` | non-secret selector | Empty/unset = setup mode and no emission; comma-separated exact runtime names select only those blueprints; `*` explicitly selects the complete available catalog. |
| `BLUEPRINT_DATA_DIR` | non-secret path | Persisted custom/git blueprint staging directory. |
| `GIT_POLL_INTERVAL` | non-secret config | Git source update-check seconds; `0` disables polling. |
| `GIT_TOKEN` | **secret** | Default HTTPS PAT for private blueprint repositories. |
| `JSON_HTTP_ADDR` | non-secret bind | Control plane / Infinity HTTP bind address. |
| `CONFIG_SNAPSHOT_PATH` | non-secret path | Persisted control-plane snapshot path. |
| `CONTROL_TOKEN` | **secret** | HTTP Basic password (user `control`) for sensitive control/Infinity reads and all mutations. |
| `CONTROL_EXPOSURE_ACK` | non-secret policy | Required for non-loopback exposure; exactly `trusted-network` or `tls-proxy`. |
| `SYNTHKIT_IMAGE_REF` | non-secret compose config | Preferred complete published image reference, ideally an index digest. Invalid/unavailable values fail and never fall back. |
| `SYNTHKIT_IMAGE_TAG` | non-secret compose config | Legacy bare-tag fallback used only when `SYNTHKIT_IMAGE_REF` is absent or empty. |
| `SYNTHKIT_ENV_FILE` | non-secret compose path | Service env file; normal default `.env`, fake-input override for render checks. |
| `SYNTHKIT_BIND` | non-secret compose bind | Host exposure for port 8088; default loopback. |
| `SYNTHKIT_IN_CONTAINER` | non-secret runtime hint | Optional container-runtime hint. |
| `SEND_SHARDS` | non-secret config | Delivery queue worker count. |
| `SEND_BATCH_MAX` | non-secret config | Delivery batch maximum series. |
| `SEND_BATCH_DEADLINE` | non-secret config | Delivery partial-batch duration. |
| `SEND_QUEUE_CAPACITY` | non-secret config | Delivery ring-buffer capacity. |
| `SEND_DRAIN_DEADLINE` | non-secret config | Graceful delivery-drain duration. |

Per-source `token_env_var` is intentionally operator-defined and is not a supported fixed key.
Treat the referenced process environment variable as a **secret** private-git token; do not add it
to `.env.example` or this list. There is no supported environment-variable lane for arbitrary
vendor APIs, Kubernetes credentials, cloud access keys, or a standalone profiling enable flag.
