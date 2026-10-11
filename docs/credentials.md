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

Used by the version-matched Docker provisioner (`sm-provision` Compose profile; a source checkout
may run the binary directly), not as a synthetic sink. The SM token is a separate bearer token —
not `GC_TOKEN`. Provisioning is snapshot-bound and requires a subsequent emitter restart; see
[Synthetic Monitoring](synthetic-monitoring.md).

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

Sends the synthkit process's continuous profiles to a **separate** stack. Follows
`SELFOBS_ENABLED` and is independent of synthetic `DRY_RUN`.

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

If you use `gcx`, `gcx config view` shows the active configuration with secret values redacted;
use `gcx config list-contexts` and `gcx config current-context` to select the intended stack deliberately.

## Finding the three required numeric identifiers

The three user fields are different, but each must be a **positive decimal identifier**: digits
only, greater than zero. Do not put an endpoint URL, a Grafana organisation ID, a slug, or a token
in any of them.

1. In the synthetic-data stack, open **Connections** → **Prometheus** and copy its displayed
   instance ID into `GC_PROM_USER`.
2. Open **Connections** → **OpenTelemetry** and copy its displayed stack ID into `GC_OTLP_USER`.
3. Open **Connections** → **Loki** and copy its displayed instance ID into `GC_LOKI_USER`.

The adjacent connection panels also provide `GC_PROM_RW`, `GC_OTLP_ENDPOINT`, and `GC_LOKI`.
Use the endpoint and numeric identifier from the same stack. Presence checks may confirm that a
value is non-empty; never print `.env` or a token while diagnosing a failed identifier check.

### Reading the data back needs a different credential, and a different identifier

The ingest credentials above are write-only. Verifying what actually landed needs a token carrying
`metrics:read` (and `logs:read` for the log lanes), which is a separate Grafana Cloud access policy —
an ingest token returns `401 authentication error: invalid scope requested` on a query.

**The query username is the per-signal instance ID, not the stack ID**, and the two are different
numbers for the same stack. Mimir wants the Prometheus instance ID and Loki wants the Loki instance
ID; the stack ID belongs only to the OTLP gateway. Passing a stack ID to a query endpoint fails with

```text
401 authentication error: invalid authentication credentials
```

which is **the same body a revoked token produces**, so a perfectly good credential reads as dead
and the real fault is invisible. Resolve the identifiers at run time rather than from memory:

```bash
curl -sH "Authorization: Bearer $READ_TOKEN" \
  "https://grafana.com/api/orgs/<org-id>/instances" \
  | jq -r '.items[] | "\(.slug) prom=\(.hmInstancePromId) loki=\(.hlInstanceId)"'
```

The three 401 bodies are worth telling apart: `invalid authentication credentials` is a wrong
tenant/token pair, `invalid scope requested` means the token is live but its policy lacks the scope,
and `invalid token` means revoked or malformed.

---

## Filling in `.env`

```bash
install -m 600 .env.example .env
# open .env in your editor and fill in the values
```

Do not pass the real `.env` to raw `docker compose config`; rendered output can expose interpolated
credentials. `just compose-check` uses `.env.example` as fake input for deployment rendering.

!!! warning "Comment placement"
    Docker Compose's `env_file` does **not** strip inline comments. Put comments on their own line — `VALUE=foo # comment` makes `# comment` part of the value. `.env.example` demonstrates the correct style throughout.

The minimum set for a live synthetic push: `GC_TOKEN` + `GC_PROM_RW`/`GC_PROM_USER` + `GC_OTLP_ENDPOINT`/`GC_OTLP_USER` + `GC_LOKI`/`GC_LOKI_USER`. Leave optional blocks empty to disable RUM, SM, FM, and self-obs.

Before switching to live mode, run the explicit preflight:

```bash
go run ./cmd/synthkit -preflight
```

For a Compose deployment, run the same command through the image without starting the long-running service:

```bash
docker compose run --rm --no-deps synthkit -preflight
```

The preflight first validates configuration locally, then sends an empty authenticated request to each mandatory ingest lane. It does not emit telemetry. Each network request has a five-second timeout and the command exits non-zero unless all three lanes are ready. Output is deliberately redacted; it names only the lane, state, and a fixed reason code:

```text
prometheus: statically valid
loki: statically valid
otlp: statically valid
prometheus: ready
loki: unauthorized (http-403)
otlp: unreachable (tls)
```

`unauthorized` distinguishes HTTP 401 from 403. `unreachable` distinguishes DNS, TLS, timeout/connection, unexpected HTTP status, and endpoint-path failures. Tokens, endpoint URLs, user IDs, and response bodies are never printed.

Mandatory live values have these static requirements:

| Env var | Required shape |
|---|---|
| `GC_PROM_RW` | HTTPS URL ending exactly in `/api/prom/push` |
| `GC_LOKI` | HTTPS URL ending exactly in `/loki/api/v1/push` |
| `GC_OTLP_ENDPOINT` | HTTPS base URL ending exactly in `/otlp`; synthkit appends `/v1/traces` or `/v1/metrics` |
| `GC_PROM_USER`, `GC_LOKI_USER`, `GC_OTLP_USER` | Positive decimal instance or stack ID |

Normal dry-run commands remain credential-free and offline. They do not execute preflight probes; network/auth checks occur only when `-preflight` is supplied. Live startup (`DRY_RUN=false`) always performs the static checks before constructing sinks, so malformed or transposed endpoints fail synchronously rather than during background delivery.

See [Configuration](configuration.md) for the full environment variable reference including behaviour knobs (`TICK_DEFAULT`, `SEND_SHARDS`, queue tunables, etc.).
