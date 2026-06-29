---
title: Getting Started
description: Mental model and vocabulary for synthkit — blueprints, constructs, workloads, sinks, and the emit loop — before you install.
---

# Getting Started

Before installing synthkit, read this page. It defines the vocabulary used everywhere else in the documentation so you can read a blueprint YAML, understand what gets emitted and why, and diagnose problems without guessing.

## Core concepts

### Blueprint

A blueprint is a single YAML file that declares the infrastructure and applications you want to model. It is the only place blueprint-specific config lives. One blueprint file = one deletable unit of synthetic telemetry: add a file to the `blueprints/` directory and synthkit loads it; remove it and that telemetry disappears, with no effect on anything else.

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

A workload generates correlated request traffic: metrics, traces, logs, and optionally RUM, sharing one end-to-end correlation ID per request. Two kinds:

- **`web_service`** — a single service with a browser→backend→DB hop tree. The simple, common case.
- **`app`** — a blueprint-declared multi-service graph whose nodes each emit their own custom telemetry via a DSL. Use `app` when you need multiple backend services with their own metrics/logs/spans, per-service incident targeting, or per-service scaling.

Both kinds can coexist in one blueprint.

### Signal family

A signal family is a named group of metrics (or logs, or spans) that a construct emits together. The complete list of families, their metric names, label sets, and example values lives in the `signals/` catalogue — see [Reading the Catalogue](signals.md). synthkit never invents a name: every metric name, label key, and label value is sourced from a real stack with provenance citations.

### Sinks

Sinks are where data goes. synthkit has four signal-type sinks:

| Sink | Protocol | Destination |
|---|---|---|
| `promrw` | Prometheus Remote-Write v2 | Grafana Cloud Mimir (metrics) |
| `otlp` | OTLP ResourceSpans | Grafana Cloud Tempo (traces) |
| `loki` | Loki push | Grafana Cloud Loki (logs) |
| `faro` | Faro collector | Grafana Cloud Frontend Observability (RUM) |

Each sink has its own credential triplet. See [Credentials](credentials.md).

### DRY_RUN

`DRY_RUN` defaults to `true`. In dry-run mode synthkit loads all blueprints, builds the full series inventory, and logs what it would push — but makes no network calls. Live pushing requires an explicit opt-in: `DRY_RUN=false`. Use dry-run mode to validate blueprints and inspect the series inventory without needing credentials.

## The emit loop

synthkit runs a two-cadence scheduler:

1. **On each tick** (default every 5 seconds, controlled by `TICK_DEFAULT`): the runner calls every registered construct and workload in parallel, each producing its batch of time-series, log streams, and/or spans for that tick.
2. **Delivery queue** (`internal/sink/queue`): the batches are enqueued and flushed to the sinks asynchronously, decoupled from tick timing. This prevents a slow network push from blocking the next tick.
3. **Cumulative state** (`internal/state`): counters and histograms accumulate across ticks. The sink always receives running totals, not per-tick deltas — matching how real Prometheus exporters work.

The full cycle: declare in YAML → synthkit loads and validates at startup → each tick produces signals → sinks push to Grafana Cloud.

## Determinism

Fixtures (pod names, node IDs, instance keys, IP addresses) are derived deterministically from the blueprint name and path using a seeded hash. The same blueprint produces the same identities on every run, on every machine. This is load-bearing: rate() windows, join queries, and dashboard variables all depend on label values being stable.

## What to read next

- [Installation](installation.md) — prerequisites and build instructions
- [Quick Start](quickstart.md) — from binary to live data in five steps
- [Blueprints overview](blueprints.md) — the full blueprint schema
- [Credentials](credentials.md) — which env vars go where
