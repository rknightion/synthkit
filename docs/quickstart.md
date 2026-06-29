---
title: Quick Start
description: From binary to live synthetic telemetry in Grafana Cloud — build, dry-run, configure credentials, push live, and verify.
---

# Quick Start

Five steps from a fresh clone to verified synthetic telemetry in Grafana Cloud.

---

## Step 1: Build

```bash
git clone https://github.com/rknightion/synthkit.git
cd synthkit
go build ./cmd/synthkit
```

Requires Go 1.26.4 or later. See [Installation](installation.md) for the Docker path.

---

## Step 2: Dry run — validate blueprints offline

Before touching credentials, confirm the blueprints load and inspect the full series inventory:

```bash
DRY_RUN=true ./synthkit -once -dump
```

`-once` runs a single tick and exits. `-dump` prints the complete series/label inventory to stdout — every metric name, label set, and example value that would be pushed. No network calls are made.

Expected output: a `loaded blueprint "<name>"` line per `blueprints/*.yaml` file, a `synthkit up: N blueprints` startup line, then `[dry-run promrw|loki|otlp]` summaries with the series/streams/spans that would be sent.

!!! tip "Use -dump to verify signal contracts"
    Spot-check a few metric names against the [`signals/`](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md) catalogue. synthkit never invents names — anything unexpected is a bug, not a configuration choice.

---

## Step 3: Configure credentials

```bash
cp .env.example .env
```

Open `.env` in your editor. The minimum set for a live push:

```
GC_TOKEN=<your-CAP-token>
GC_PROM_RW=https://prometheus-prod-XX-<region>.grafana.net/api/prom/push
GC_PROM_USER=<mimir-instance-id>
GC_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
GC_OTLP_USER=<stack-id>
GC_LOKI=https://logs-prod-XXX.grafana.net/loki/api/v1/push
GC_LOKI_USER=<loki-instance-id>
DRY_RUN=false
```

A single Cloud Access Policy token with `metrics:write`, `logs:write`, `traces:write` scopes covers all three sinks. See [Credentials](credentials.md) for the full table including optional RUM, Synthetic Monitoring, Fleet Management, and self-observability destinations.

!!! warning "DRY_RUN defaults to true"
    The shipped `.env.example` has `DRY_RUN=true`. You must explicitly set `DRY_RUN=false` to push live. This is intentional — a dry run can never accidentally write to a production stack.

---

## Step 4: Push live

```bash
./synthkit
```

synthkit loads the `.env` file automatically on startup. It runs the emit loop continuously (default tick: every 5 seconds). Let it run for a few ticks so cumulative counter series accumulate before querying.

To run a single tick and exit:

```bash
DRY_RUN=false ./synthkit -once
```

---

## Step 5: Verify

**Fastest signal — the operator UI:**

Open [http://localhost:8088/control/ui](http://localhost:8088/control/ui) in your browser. The sink-readiness strip shows the last push result for each sink (`promrw`, `loki`, `otlp`). Green = pushing successfully. If any sink shows failures, check the error detail there before querying Grafana.

**Via the JSON API:**

```bash
curl -s http://localhost:8088/control/status | jq
```

Each sink shows `last_success_ms` advancing and `failures: 0`. `dry_run: true` means `DRY_RUN` is still set — re-check your `.env`.

**In Grafana:**

1. Open Explore in your Grafana Cloud stack.
2. Query a metric from a declared construct, for example:
   ```promql
   kube_node_info
   ```
   or for an RDS construct:
   ```promql
   aws_rds_cpuutilization_average
   ```
3. For traces, open Explore → Tempo and search `service.name="<your-workload-name>"`. Confirm a root request span with a child DB span.
4. For logs, query Loki with `{source="app"}`.

!!! info "Ingestion lag"
    Mimir and Loki typically ingest within seconds. Tempo trace search has a short ingestion lag (30s–2m) before new traces appear in search results.

---

## Next steps

- [RUNBOOK.md](RUNBOOK.md) — deep verification walkthrough (sink readiness, gcx queries, end-to-end trace correlation check, log correlation, SM/FM verification)
- [Deployment](deployment.md) — standing production deploy with docker-compose, the persistent state volume, and the host bind setup
- [Blueprints overview](blueprints.md) — write your own blueprint
- [Incidents & Scenarios](incidents.md) — declare and activate failure scenarios
