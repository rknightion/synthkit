---
title: Self-Observability
description: How to configure synthkit's own process telemetry — RED metrics, traces, logs, and continuous profiles — shipped to a separate Grafana Cloud stack.
---

# Self-Observability

synthkit ships its own operational telemetry alongside (and completely separate from) the synthetic data it produces. This is the generator's self-observability: RED metrics on the synthetic-push pipeline, Go runtime metrics, per-tick traces, the operational log stream, and continuous process profiles via Pyroscope — all sent to a **different** Grafana Cloud stack through their own credential triplets, never `GC_TOKEN`.

Self-observability is off by default and is suppressed under `DRY_RUN=true`. The profiling path has no master flag of its own; it follows `SELFOBS_ENABLED`.

---

## What it ships

### OTLP signals (metrics + traces + logs)

All three signal types flow over a single OTLP/HTTP endpoint to the self-obs stack:

| Signal | What it covers |
|---|---|
| **Metrics** | `synthkit.push` (count/items/bytes/duration per sink/blueprint/outcome), `synthkit.tick` (invocations + duration), `synthkit.cycle.duration`, `synthkit.dropped_ticks`, delivery-queue depth/flush metrics (`synthkit.queue.*`), Fleet Management operations (`synthkit.fleet.*`), and observable gauges: `synthkit.ledger.size`, `synthkit.volume.multiplier`, `synthkit.blueprint.count`, `synthkit.cardinality.series` |
| **Traces** | Per-tick spans wrapping each construct invocation; `cycle` spans (backdated to the generation window); `push <sink>` child spans for each live push; `flush <sink>` spans for the decoupled delivery queue; `fleet <op>` spans for FM registration and heartbeat round-trips |
| **Logs** | Structured OTLP LogRecords for push failures (event=`push_error`), tick errors (event=`tick_error`), FM failures (event=`fleet_error`), config-change events (event=`config_change`), and the operational heartbeat (every 5 minutes) |

Go runtime metrics (goroutines, memory, GC) are collected via the OTel contrib runtime instrumentation against the self-obs `MeterProvider`.

### Continuous profiles (Pyroscope)

When `GC_PYROSCOPE_*` credentials are set, the generator's own process is profiled continuously and sent to a Grafana Cloud Profiles (Pyroscope) instance via its own `GC_PYROSCOPE_*` credential triplet — independent of the OTLP self-obs triplet, though both are typically the same staff stack. Profile types: CPU, heap allocation/inuse (objects + bytes), goroutines, and optionally mutex and block profiles. Application name is `synthkit`.

---

## Isolation design

`internal/selfobs` is the **sole** package in synthkit that imports the OpenTelemetry SDK. The SDK is banned on the synthetic-data path — the synthetic OTLP sink hand-encodes proto and never touches the OTel API.

The seam is stdlib-only in both directions:

- Sinks report push outcomes through `internal/pushhook` (a plain function type). `selfobs.PushObserver()` returns an observer that `internal/selfobs` registers; the sinks themselves never import `selfobs`.
- The runner reports per-tick outcomes through `runner.TickFunc` (also a plain function type). The runner stores a method value from `selfobs`; it never links the SDK.

`selfobs` builds its own `TracerProvider`, `MeterProvider`, and `LoggerProvider`, and **never installs them as OTel globals** (`otel.SetTracerProvider` is never called). The synthetic OTLP sink, which bypasses the OTel global API entirely, is therefore completely unaffected.

See [Architecture](architecture.md) §6.1 for the full isolation rationale.

---

## Configuration

Enable self-observability by setting `SELFOBS_ENABLED=true` and providing the OTLP credential triplet. All three vars are required; if any is missing, synthkit logs a warning and falls back to a no-op handle.

### OTLP self-observability

| Env var | Required | Default | Description |
|---|---|---|---|
| `SELFOBS_ENABLED` | — | `false` | Master on/off switch |
| `GC_SELF_OTLP_ENDPOINT` | When enabled | — | OTLP gateway base URL for the self-obs stack (e.g. `https://otlp-gateway-<region>.grafana.net/otlp`) |
| `GC_SELF_OTLP_USER` | When enabled | — | Self-obs stack ID (HTTP Basic username) |
| `GC_SELF_OTLP_PASSWORD` | When enabled | — | CAP token with `metrics:write`, `logs:write`, `traces:write` — **never** `GC_TOKEN` |
| `SELFOBS_TAGS` | — | — | Extra resource attributes as `key=value,key=value` CSV |
| `GC_SELF_GRAFANA_URL` | — | — | Staff Grafana base URL (e.g. `https://your-stack.grafana.net`) — enables deep-links to the self-obs dashboard in the operator UI; leave empty to hide them |
| `SELFOBS_METRIC_INTERVAL` | — | `15s` | Metric flush cadence (Go duration string); traces and logs use their own batchers and are unaffected |

### Continuous profiling (Pyroscope)

Profiling has no master flag of its own. It follows `SELFOBS_ENABLED` and is also suppressed under `DRY_RUN=true`. Set the three `GC_PYROSCOPE_*` vars to activate it; an incomplete triplet is a silent no-op (logged at startup).

| Env var | Required | Default | Description |
|---|---|---|---|
| `GC_PYROSCOPE_URL` | When profiling | — | Profiles ingest URL (e.g. `https://profiles-prod-XXX.grafana.net`) |
| `GC_PYROSCOPE_USER` | When profiling | — | Profiles instance ID (Basic-auth username) |
| `GC_PYROSCOPE_PASSWORD` | When profiling | — | CAP token with `profiles:write` scope |
| `PYROSCOPE_TAGS` | — | — | Extra profile tags as `key=value,key=value` CSV |
| `PYROSCOPE_MUTEX_FRACTION` | — | `5` | `runtime.SetMutexProfileFraction`; `0` = mutex profiling off |
| `PYROSCOPE_BLOCK_RATE` | — | `5` | `runtime.SetBlockProfileRate` in nanoseconds; `0` = block profiling off |

A value of `5` for both is high-fidelity and fine for a lab or demo generator. Set both to `0` to reduce overhead on resource-constrained hosts.

---

## Example `.env` snippet

```bash
# Self-observability — ships to a SEPARATE stack (never GC_TOKEN)
SELFOBS_ENABLED=true
GC_SELF_OTLP_ENDPOINT=https://otlp-gateway-prod-eu-west-0.grafana.net/otlp
GC_SELF_OTLP_USER=123456
GC_SELF_OTLP_PASSWORD=glc_...
SELFOBS_TAGS=env=prod,team=platform
GC_SELF_GRAFANA_URL=https://myorg.grafana.net
SELFOBS_METRIC_INTERVAL=15s

# Continuous profiling (also ships to a separate stack)
GC_PYROSCOPE_URL=https://profiles-prod-006.grafana.net
GC_PYROSCOPE_USER=78910
GC_PYROSCOPE_PASSWORD=glc_...
PYROSCOPE_TAGS=env=prod
PYROSCOPE_MUTEX_FRACTION=5
PYROSCOPE_BLOCK_RATE=5
```

---

## Relationship to DRY_RUN

`SELFOBS_ENABLED` is independent of `DRY_RUN`: the composition root gates self-obs off under `DRY_RUN=true` before calling `selfobs.Start`, so any telemetry that does reach the self-obs stack is from a live-push run. Under `DRY_RUN=true` the self-obs handle is always a no-op regardless of `SELFOBS_ENABLED`.

---

## See also

- [Configuration](configuration.md) — full environment variable reference
- [Architecture](architecture.md) — §6.1 for the OTel SDK isolation design
- [Credentials](credentials.md) — the two-stacks model and token scoping
