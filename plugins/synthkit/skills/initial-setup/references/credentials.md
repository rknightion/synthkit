# synthkit credential & env reference

Every var below is consumed by synthkit (`internal/config`) or docker-compose and is documented in
`.env.example`. `TestEnvSurfaceAligned` keeps these surfaces in lockstep — do not invent names.

Two stacks are involved and **never share a token**:
- **Customer / synthetic-data stack** — the destination for the fake telemetry synthkit emits.
- **Staff stack(s)** — where synthkit's OWN telemetry (self-observability, profiling) goes.

## Lane → variables → where to get them

### Customer sinks — MANDATORY
| Var | Meaning | Source (Grafana Cloud) |
|---|---|---|
| `GC_PROM_RW` | Prometheus remote-write URL | Stack → Connections → Prometheus → "Sending metrics" |
| `GC_PROM_USER` | Mimir instance ID (Basic-auth user) | same panel |
| `GC_OTLP_ENDPOINT` | OTLP gateway base URL (`…/otlp`) | Connections → OpenTelemetry |
| `GC_OTLP_USER` | OTLP stack ID | same panel |
| `GC_LOKI` | Loki push URL | Connections → Loki |
| `GC_LOKI_USER` | Loki instance ID | same panel |
| `GC_TOKEN` | **secret** — Cloud Access Policy token, scopes `metrics:write` + `logs:write` + `traces:write` | Connections → Access Policies → create token |

### Self-observability — OPTIONAL (staff stack, separate token)
| Var | Meaning |
|---|---|
| `SELFOBS_ENABLED` | `true` to enable (non-secret flag) |
| `GC_SELF_OTLP_ENDPOINT` | staff OTLP gateway base URL |
| `GC_SELF_OTLP_USER` | staff OTLP stack ID |
| `GC_SELF_OTLP_PASSWORD` | **secret** — token (`metrics/logs/traces:write`) on the STAFF stack — never `GC_TOKEN` |

### Profiling (Pyroscope) — OPTIONAL (staff stack, separate token)
| Var | Meaning |
|---|---|
| `PYROSCOPE_ENABLED` | `true` to enable |
| `GC_PYROSCOPE_URL` | staff Profiles ingest URL |
| `GC_PYROSCOPE_USER` | staff Profiles instance ID |
| `GC_PYROSCOPE_PASSWORD` | **secret** — token with `profiles:write` |

### Fleet Management — OPTIONAL
| Var | Meaning |
|---|---|
| `GC_FM_URL` | FM API base URL |
| `GC_FM_STACK_ID` | Grafana Cloud **stack ID** (Basic-auth user — NOT `GC_PROM_USER`) |
| `GC_FM_TOKEN` | **secret** — token with `fleet-management:write` |
Also requires a `fleet_management` construct in the active blueprint → hand to `setup-fleet-management`.
Empty `GC_FM_URL` ⇒ collectors emit metrics only (no registration).

### Synthetic Monitoring — OPTIONAL
| Var | Meaning |
|---|---|
| `GC_SM_URL` | SM API URL |
| `GC_SM_TOKEN` | **secret** — SM API token (separate; used by `cmd/sm-provision`) |

### RUM (Faro) — OPTIONAL
| Var | Meaning |
|---|---|
| `GC_FARO_COLLECTOR` | Faro collector URL (includes app key) |
| `GC_FARO_APP_KEY` | Faro app key |


### Control plane / behaviour
| Var | Meaning |
|---|---|
| `DRY_RUN` | `true` (default) = no live push; flip to `false` to emit |
| `CONTROL_TOKEN` | **secret** — HTTP Basic password (user `control`) for POST /control/*; generate `openssl rand -hex 24` |
| `SYNTHKIT_BIND` | host port exposure; `127.0.0.1` (default, safe) or `0.0.0.0` |
| `SYNTHKIT_IMAGE_TAG` | published GHCR image tag; default `latest` (last release); `main` for edge, `vX.Y.Z` to pin — compose-only, app ignores it |
| `TICK_DEFAULT` | master tick cadence (default `5s`) |
