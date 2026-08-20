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

Requires Go 1.26.5 or later. See [Installation](installation.md) for the Docker path.

---

## Step 2: Dry run one focused workload offline

Before touching credentials, select the bundled `otlp-native` reference blueprint and inspect its
full series inventory:

```bash
DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump
```

`BLUEPRINT_NAMES` is an optional comma-separated allowlist of exact runtime blueprint names. An
empty or absent value keeps the established all-blueprint behavior; an unknown name stops startup
and lists the names that are available. `-once` runs a single tick and exits. `-dump` prints the
complete series/label inventory to stdout — every metric name, label set, and example value that
would be pushed. No network calls are made.

Expected output includes `selected blueprints: 1 [otlp-native]` before any sink can push, a
`loaded blueprint "otlp-native"` line, `synthkit up: 1 blueprints [otlp-native]`, and `[dry-run
promrw|loki|otlp]` summaries.

The focused runtime identities are:

- Blueprint: `otlp-native`
- Cluster: `otlp-native-prod-euw1`
- Workloads and Tempo service names: `otlp-api-enriched` and `otlp-api-naked`
- Loki stream selector for the enriched service:
  `{source="app",service_name="otlp-api-enriched",cluster="otlp-native-prod-euw1"}`

To prove the Docker path is using your authored checkout rather than a cached published image,
build with the local-source Compose override and then run the same isolated dry run:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml build synthkit
docker compose -f docker-compose.yml -f docker-compose.build.yml run --rm --no-deps \
  -e DRY_RUN=true -e BLUEPRINT_NAMES=otlp-native synthkit -once -dump
```

The build override tags the result `synthkit:local` and disables registry pulling, so this command
uses the current checkout's `Dockerfile`, binary, and `blueprints/` directory.

!!! tip "Use -dump to verify signal contracts"
    Spot-check a few metric names against the [`signals/`](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md) catalogue. synthkit never invents names — anything unexpected is a bug, not a configuration choice.

---

## Step 3: Configure credentials

```bash
if test -e .env; then
  printf '%s\n' '.env already exists; review it before changing live-mode settings.' >&2
else
  install -m 600 .env.example .env
  ./plugins/synthkit/skills/initial-setup/scripts/set-env.sh DRY_RUN false .env
fi
```

This creates `.env` with mode `0600`, sets live mode through the non-secret helper, and prints no
credential values. Open it in your editor. The minimum set for a live push:

```dotenv
GC_TOKEN=<your-CAP-token>
GC_PROM_RW=https://prometheus-prod-XX-<region>.grafana.net/api/prom/push
GC_PROM_USER=<mimir-instance-id>
GC_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
GC_OTLP_USER=<stack-id>
GC_LOKI=https://logs-prod-XXX.grafana.net/loki/api/v1/push
GC_LOKI_USER=<loki-instance-id>
BLUEPRINT_NAMES=otlp-native
DRY_RUN=false
```

If `.env` already existed, review it first and then run
`./plugins/synthkit/skills/initial-setup/scripts/set-env.sh DRY_RUN false .env` to make the same
explicit live-mode transition without replacing the file.

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

## Step 5: Verify the focused workload

**Fastest signal — the operator UI:**

Open [http://localhost:8088/control/ui](http://localhost:8088/control/ui) in your browser. The sink-readiness strip shows the last push result for each sink (`promrw`, `loki`, `otlp`). Green = pushing successfully. If any sink shows failures, check the error detail there before querying Grafana.

**Via the JSON API:**

```bash
curl -fsS http://localhost:8088/control/status | jq -e '.dry_run == false'
```

This must print `true`; otherwise `DRY_RUN` is still set incorrectly. Each sink should also show `last_success_ms` advancing and `failures: 0` in the operator UI.

**In Grafana:**

1. Open Explore in your Grafana Cloud stack.
2. In Prometheus, query the enriched service's native-OTLP histogram count:
   ```promql
   http_server_request_duration_seconds_count{service_name="otlp-api-enriched"}
   ```
   Expect one or more non-empty series. To also confirm the k8s-enriched identity, query:
   ```promql
   target_info{service_name="otlp-api-enriched",k8s_cluster_name="otlp-native-prod-euw1"}
   ```
   Expect a gauge-1 series. The native active-request metric is also non-empty and is a gauge,
   not a `_total` counter:
   ```promql
   http_server_active_requests{service_name="otlp-api-enriched"}
   ```
3. In Loki, query:
   ```logql
   {source="app",service_name="otlp-api-enriched",cluster="otlp-native-prod-euw1"}
   ```
   Expect log lines after a few ticks.
4. In Tempo, use TraceQL:
   ```traceql
   { resource.service.name = "otlp-api-enriched" }
   ```
   Expect one or more traces; open one to confirm a root HTTP request span for the selected
   service. Repeat the three checks with `otlp-api-naked` to compare its non-enriched resource
   shape.

!!! info "Ingestion lag"
    Mimir and Loki typically ingest within seconds. Tempo trace search has a short ingestion lag (30s–2m) before new traces appear in search results.

---

## Next steps

- [RUNBOOK.md](RUNBOOK.md) — deep verification walkthrough (sink readiness, gcx queries, end-to-end trace correlation check, log correlation, SM/FM verification)
- [Deployment](deployment.md) — standing production deploy with docker-compose, the persistent state volume, and the host bind setup
- [Blueprints overview](blueprints.md) — write your own blueprint
- [Incidents & Scenarios](incidents.md) — declare and activate failure scenarios
