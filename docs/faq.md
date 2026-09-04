---
title: FAQ
description: Frequently asked questions about running, configuring, and securing synthkit, each answer pointing to the authoritative documentation page.
---

# Frequently Asked Questions

Short answers to common questions. Each answer links to the authoritative page for the full
detail — treat those linked pages as the source of truth.

## Getting started

### Does synthkit send data to my real production Grafana Cloud stack?

Only if you configure it to and only synthetic data — it never reads from or writes to your
production systems. `DRY_RUN` defaults to `true`, so a fresh checkout produces no network traffic
at all until you explicitly set `DRY_RUN=false` and fill in credentials. Empty `BLUEPRINT_NAMES`
also selects no blueprints, so even live mode emits no synthetic telemetry until you choose exact
runtime names. See [Getting
Started](getting-started.md) and [Credentials](credentials.md).

### Do I need a Kubernetes cluster or cloud account to try synthkit?

No. synthkit doesn't touch real infrastructure — it generates telemetry that *looks like* it came
from an EKS cluster, RDS database, or Cloudflare account, entirely from a YAML blueprint. Run
`DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump` to see a focused series inventory
with no credentials and no cloud account at all. See [Quick Start](quickstart.md).

### What's the smallest thing I need to get real data into Grafana Cloud?

`GC_TOKEN` plus the `GC_PROM_RW`/`GC_PROM_USER`, `GC_OTLP_ENDPOINT`/`GC_OTLP_USER`, and
`GC_LOKI`/`GC_LOKI_USER` pairs, an exact `BLUEPRINT_NAMES` selection, then `DRY_RUN=false`. RUM, profiles, Synthetic Monitoring, and
Fleet Management are all optional add-ons layered on top. See [Credentials](credentials.md).

### Why is synthkit healthy but emitting nothing?

A fresh installation intentionally starts in setup mode. The UI and startup log report that no
blueprints are selected; this is operationally healthy but never live-delivery-ready. Set
`BLUEPRINT_NAMES=otlp-native` (or other exact names) and restart. `BLUEPRINT_NAMES=*` explicitly
restores the complete-catalog behavior used by older versions.

## Blueprints

### Can I edit a blueprint while synthkit is running?

Bundled and staged blueprints only take effect on restart — there's no hot-reload for blueprint
files themselves. What *does* apply live, without a restart, is scenario activation, volume
scaling, and ad-hoc failure injection through the control plane. See [Control
Plane](control-plane.md).

### If I delete a blueprint file, does anything else break?

No. Constructs and workloads know nothing about which blueprint they belong to — deleting a
blueprint file removes exactly that blueprint's telemetry and leaves every other blueprint
untouched. This is a deliberate architectural invariant, not an incidental property. See
[Architecture](architecture.md).

### Why did my blueprint fail to load with no telemetry at all?

Blueprint decoding is strict: an unknown top-level key, an unknown construct kind, or an unknown
field inside a construct's config is a loud load error, not a silently-ignored one. Check `GET
/control/diagnostics` for the exact cause. See [Blueprints Overview](blueprints.md).

### Do I have to hand-write the YAML myself?

Not necessarily. The `/create-blueprint` skill walks through an interactive authoring session in
Claude Code, and `skcapture` + `skforge` can generate a draft blueprint from a real Kubernetes
environment's inventory (with the option to encrypt the capture). `skcapture` never captures Secret
or arbitrary ConfigMap data values; its optional identity lookup reads only one named ConfigMap's
`cluster` key. See [Capture & Tooling](tools.md).

## Metrics and signal accuracy

### Are the metric names and labels made up, or copied from a real exporter?

Copied, deliberately. Every metric name, label key, and label value is sourced from the `signals/`
catalogue, which is lifted from production-validated stacks with provenance citations — synthkit
never invents a name. See [Reading the Catalogue](signals.md).

### Why do my dashboard's rate() queries look wrong right after a restart?

Container restart resets synthkit's counters to zero, which produces one clean `rate()` window
after the restart rather than a spike or a gap — this is intentional, not a bug. Use
`increase()` with a long lookback across a restart boundary, or plan maintenance windows around
it. See [Deployment](deployment.md#counter-resets-and-rate-windows).

## Operations

### Is the control plane authenticated?

When `CONTROL_TOKEN` is set, HTTP Basic auth (username `control`) protects every mutation,
sensitive control reads, and Infinity data routes. Only `/healthz` and sanitized
`/control/readiness` stay public. Loopback can remain token-free; non-loopback startup additionally
requires `CONTROL_EXPOSURE_ACK=trusted-network|tls-proxy`. See [Control Plane](control-plane.md).

### My control-plane changes aren't surviving a restart — why?

Almost always a `/data` volume problem: either it's a single-file bind mount (which breaks the
atomic write-then-rename save) or the directory isn't owned by uid 65532, the distroless
container's nonroot user. Check `persist.last_error` in `GET /control/status`. See
[Troubleshooting](troubleshooting.md).

### Some constructs have data and others don't, even though DRY_RUN is false — why?

Check `SERIES_CAP`. When set, it silently truncates how many series synthkit pushes per tick
across *all* sinks — a global kill switch for runaway cardinality, not a per-blueprint limit. See
[Troubleshooting](troubleshooting.md#series-cap-kill-switch).

## Security

### Where do I put my Grafana Cloud credentials?

In a gitignored `.env` file, never in the committed `docker-compose.yml` or a blueprint. The
compose file and blueprints are meant to be shared or committed; `.env` is not. See
[Security](security.md) and [Credentials](credentials.md).

### If someone compromised my synthkit instance, could they see real customer data?

No — there isn't any. synthkit reads blueprint YAML and emits synthetic telemetry; it has no
inbound integration with a real production system to steal data from. The real risk is credential
misuse (pushing synthetic data as if it were legitimate, or exhausting the `GC_TOKEN`'s write
quota) and control-plane abuse if left unauthenticated on a reachable network. See
[Security](security.md).
