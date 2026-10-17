---
title: Getting Started
description: Mental model and vocabulary for synthkit — blueprints, constructs, workloads, sinks, and the emit loop.
---

# Getting Started

Start with the [Quick Start](quickstart.md) to run one blueprint, or the
[emission-only local run](emission-only.md) to push a bundled set to an existing stack with no
Synthetic Monitoring or Fleet Management side effects. This page explains the vocabulary
when you want to understand or customise that example. synthkit models systems and emits their
telemetry; it does not deploy the systems it models.

<div class="diagram-scroll" markdown>

![Blueprint declarations are composed into synthetic signals and sent to configured destinations](assets/diagrams/telemetry-flow.svg)

</div>

[Open the diagram at full size](assets/diagrams/telemetry-flow.html).

## Core concepts

### Blueprint

A blueprint is a single YAML file that declares the infrastructure and applications you want to
model. It is the only place blueprint-specific config lives. One blueprint file is one deletable
unit of synthetic telemetry: add its exact runtime identity to `BLUEPRINT_NAMES` (or explicitly use
`*`) to load it at startup; remove that identity and restart to stop its telemetry without affecting
anything else.

A blueprint contains:

- **Environments** — cloud accounts with optional clusters, databases, and caches
- **Workloads** — services that generate request traffic and correlated signals
- **Features** — Grafana Cloud products you want represented (Synthetic Monitoring, Fleet Management)
- **Integrations** — external systems Grafana Cloud ingests (Cloudflare, Azure CSP, GCP CSP)
- **Incidents/Scenarios** — named failure bundles you can schedule or activate live

### Construct

A construct is an isolated module that emits the real telemetry of one technology. Examples: `ec2`, `rds`, `k8s_cluster`, `cloudflare`, `fleet_management`. Each construct knows only its own signal contract — it never imports another construct, the blueprint package, or any OTel SDK. The blueprint wires construct instances together; the constructs themselves are unconditional.

Constructs are categorised by **scope**:

- **Blueprint-scoped** — each blueprint gets its own instance; emitted series carry a `blueprint=<name>` selector label (e.g. `rds`, `web_service`)
- **Substrate-scoped** — shared across blueprints; series are disambiguated by the declared identity (cluster name, account ID) rather than a blueprint label (e.g. `k8s_cluster`, `dbo11y`, CSP constructs)

### Workload

A workload generates correlated request traffic: metrics, traces, logs, and optionally RUM, sharing one end-to-end correlation ID per request. The registered kinds are:

- **`web_service`** — a single service with a browser→backend→DB hop tree. The simple, common case.
- **`app`** — a blueprint-declared multi-service graph whose nodes each emit their own custom telemetry via a DSL. Use `app` when you need multiple backend services with their own metrics/logs/spans, per-service incident targeting, or per-service scaling.

- **`ai_agent`** — conversational agent sessions, tool calls and model generations with correlated
  signals. See [Workloads](workloads.md).

These kinds can coexist in one blueprint.

### Signal family

A signal family is a named group of metrics (or logs, or spans) that a construct emits together. The complete list of families, their metric names, label sets, and example values lives in the `signals/` catalogue — see [Reading the Catalogue](signals.md). synthkit never invents a name: signal names and label keys follow sourced contracts; synthetic identities and values come from the blueprint and model.

### Sinks

Sinks deliver the generated signals:

| Sink | Protocol | Destination |
|---|---|---|
| `promrw` | Prometheus Remote-Write v2 | Grafana Cloud Mimir (metrics) |
| `otlp` | OTLP traces, native metrics and logs | Grafana Cloud OTLP gateway |
| `loki` | Loki push | Grafana Cloud Loki (logs) |
| `faro` | Faro collector | Grafana Cloud Frontend Observability (RUM) |
| `pyroscope` | Profile ingest | Grafana Cloud Profiles (optional synthetic profiles) |
| `sigil` | Generation ingest | Grafana Cloud AI Observability (optional) |

Metrics, logs, traces and synthetic profiles share `GC_TOKEN`, with destination-specific endpoints
and user IDs. Faro uses a collector/application-key pair; Sigil uses its own credentials. The
generator’s self-observability is separately configured. See [Credentials](credentials.md).

### DRY_RUN

`DRY_RUN` defaults to `true`. Empty or unset `BLUEPRINT_NAMES` also selects no blueprints, so a
fresh process runs in setup mode and emits nothing. Select one or more exact names (for example,
`BLUEPRINT_NAMES=otlp-native`) before using `-once -dump`; use `BLUEPRINT_NAMES=*` only when you
intentionally want the complete catalog. Dry-run suppresses synthetic delivery. Independently enabled self-observability can still export;
the Quick Start disables it explicitly for the preview.
Live pushing additionally requires the explicit opt-in `DRY_RUN=false`.

## The emit loop

synthkit runs a two-cadence scheduler:

1. **On each master tick** (default every 5 seconds, controlled by `TICK_DEFAULT`): the runner mints
   request batches and projects workload traces, logs and RUM. Metric lanes run at their own
   intervals, normally at least 60 seconds; declared `high_dpm` settings can lower that interval
   within the configured ceiling.
2. **Delivery queue** (`internal/sink/queue`): the batches are enqueued and flushed to the sinks asynchronously, decoupled from tick timing. Delivery is decoupled while queue capacity remains; full shards apply backpressure, and failed sends can lose queued data.
3. **Cumulative state** (`internal/state`): counters and histograms accumulate across ticks. The sink always receives running totals, not per-tick deltas — matching how real Prometheus exporters work.

The full cycle: declare in YAML → synthkit loads and validates at startup → each tick produces signals → sinks push to Grafana Cloud.

## Determinism

Fixtures (pod names, node IDs, instance keys, IP addresses) are derived deterministically from the blueprint name and path using a seeded hash. The same blueprint produces the same identities on every run, on every machine. This is load-bearing: rate() windows, join queries, and dashboard variables all depend on label values being stable.

## What to read next

- [Installation](installation.md#clean-public-clone-to-healthy-compose) — the single supported clean-host path from a public clone to a healthy Compose deployment
- [Quick Start](quickstart.md) — from binary to live data in five steps
- [Blueprints overview](blueprints.md) — the full blueprint schema
- [Credentials](credentials.md) — which env vars go where
