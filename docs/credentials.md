---
title: Credentials
description: How to configure Grafana Cloud credentials for synthetic-data sinks, RUM, Synthetic Monitoring, Fleet Management, and self-observability.
---

# Credentials

synthkit reads credentials from a `.env` file (gitignored — never commit secrets). Each signal type uses its own credential triplet. The self-observability path uses an entirely separate Grafana Cloud stack — never `GC_TOKEN`.

## The two stacks

| Stack | Purpose | Token var |
|---|---|---|
| **Synthetic-data stack** | Where your fake telemetry lands (dashboards, alerts, demos) | `GC_TOKEN` |
| **Self-obs stack** | Where synthkit's own process telemetry lands (RED metrics, traces, profiles) | `GC_SELF_OTLP_PASSWORD` / `GC_PYROSCOPE_PASSWORD` — **never** `GC_TOKEN` |

Keep these separate. Using `GC_TOKEN` for self-observability would intermingle the generator's own signals with the synthetic data.

---

## Credential reference

### Synthetic data sinks

A single Cloud Access Policy (CAP) token with `metrics:write`, `logs:write`, `traces:write`, and `profiles:write` scopes covers all four synthetic-data sinks.

| Purpose | Env vars | Notes |
|---|---|---|
| Metrics (Mimir, Remote-Write v2) | `GC_TOKEN`, `GC_PROM_RW`, `GC_PROM_USER` | `GC_PROM_RW` = push URL; `GC_PROM_USER` = Mimir instance ID |
| Traces (Tempo, OTLP) | `GC_TOKEN`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER` | `GC_OTLP_ENDPOINT` = base OTLP gateway URL (`…/otlp`); `GC_OTLP_USER` = stack ID |
| Logs (Loki) | `GC_TOKEN`, `GC_LOKI`, `GC_LOKI_USER` | `GC_LOKI` = Loki push URL; `GC_LOKI_USER` = Loki instance ID |
| Profiles (Pyroscope) | `GC_TOKEN`, `GC_PROFILES_URL`, `GC_PROFILES_USER` | Optional; absent = profiles disabled |

`GC_TOKEN` is the password for all four sinks. The user ID for each sink differs (Mimir ID vs stack ID vs Loki ID — they are different numbers).

### RUM / Faro (optional)

Needed only for blueprints with `rum: true` on a workload, or `app` workload nodes with a RUM lane.

| Env var | Value |
|---|---|
| `GC_FARO_COLLECTOR` | Faro collector URL, e.g. `https://faro-collector-<region>.grafana.net/collect/<app-key>` |
| `GC_FARO_APP_KEY` | Faro application key |

### Synthetic Monitoring (optional)

Used only by the `cmd/sm-provision` provisioner, not by the main emitter. The SM token is a separate bearer token — not `GC_TOKEN`.

| Env var | Value |
|---|---|
| `GC_SM_URL` | SM API URL, e.g. `https://synthetic-monitoring-api-<region>.grafana.net` |
| `GC_SM_TOKEN` | SM API bearer token |

See [Synthetic Monitoring](synthetic-monitoring.md) for the two-phase startup.

### Fleet Management (optional)

| Env var | Value |
|---|---|
| `GC_FM_URL` | FM API URL, e.g. `https://fleet-management-prod-0NN.grafana.net` |
| `GC_FM_STACK_ID` | FM basic-auth username = Grafana Cloud stack ID (NOT `GC_PROM_USER`) |
| `GC_FM_TOKEN` | CAP token with `fleet-management:write` scope |

See [Fleet Management](fleet-management.md).

### Self-observability — OTLP (optional)

Sends synthkit's own RED metrics, traces, and operational logs to a **separate** stack.

| Env var | Value |
|---|---|
| `SELFOBS_ENABLED` | `true` to enable (default `false`) |
| `GC_SELF_OTLP_ENDPOINT` | OTLP gateway base URL for the self-obs stack |
| `GC_SELF_OTLP_USER` | Self-obs stack ID |
| `GC_SELF_OTLP_PASSWORD` | Self-obs CAP token — **never** `GC_TOKEN` |

### Self-profiling — Pyroscope (optional)

Sends the synthkit process's continuous profiles to a **separate** stack. Follows `SELFOBS_ENABLED` and is suppressed under `DRY_RUN`.

| Env var | Value |
|---|---|
| `GC_PYROSCOPE_URL` | Profiles endpoint, e.g. `https://profiles-prod-XXX.grafana.net` |
| `GC_PYROSCOPE_USER` | Profiles instance ID |
| `GC_PYROSCOPE_PASSWORD` | Profiles CAP token — **never** `GC_TOKEN` |

---

## Getting credentials from Grafana Cloud

1. Open your Grafana Cloud stack → **Security → Access policies**.
2. Create a policy with the scopes you need (at minimum: `metrics:write`, `logs:write`, `traces:write`).
3. Generate a token.
4. Find the endpoint URLs under **Details** for each data source (Mimir, Loki, Tempo, Profiles).

If you use `gcx`, `gcx config get --context <your-stack>` prints the endpoint URLs for the configured context.

---

## Filling in `.env`

```bash
cp .env.example .env
# open .env in your editor and fill in the values
```

!!! warning "Comment placement"
    Docker Compose's `env_file` does **not** strip inline comments. Put comments on their own line — `VALUE=foo # comment` makes `# comment` part of the value. `.env.example` demonstrates the correct style throughout.

The minimum set for a live synthetic push: `GC_TOKEN` + `GC_PROM_RW`/`GC_PROM_USER` + `GC_OTLP_ENDPOINT`/`GC_OTLP_USER` + `GC_LOKI`/`GC_LOKI_USER`. Leave optional blocks empty to disable RUM, SM, FM, and self-obs.

See [Configuration](configuration.md) for the full environment variable reference including behaviour knobs (`TICK_DEFAULT`, `SEND_SHARDS`, queue tunables, etc.).
