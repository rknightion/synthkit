---
title: Native Histograms Setup
description: How to enable native exponential histogram ingestion in Mimir and query span-metric histogram families emitted by synthkit.
---

# Native histogram setup

synthkit emits three span-metric histogram families as **dual** (native exponential + classic
`_bucket`/`_sum`/`_count`) over Remote-Write v2. See `signals/apm.md` SK-28 for the live-verified
wire shape and the full label contracts.

Affected families:

- `traces_spanmetrics_latency`
- `traces_service_graph_request_server_seconds`
- `traces_service_graph_request_client_seconds`

All other histograms (gen_ai, CloudWatch, etc.) remain classic-only.

---

## Enable native-histogram ingestion on Grafana Cloud / Mimir

Native histograms are transmitted over **Remote-Write v2** (synthkit's only metrics sink). The
receiving Mimir instance must accept the native form; classic `_bucket` series arrive regardless.

**Grafana Cloud managed stacks** have native-histogram ingestion enabled by default — no action
required.

**Self-managed Mimir** requires the ingester flag:

```text
-ingester.native-histograms-ingestion-enabled=true
```

or via the per-tenant limits block in your Mimir config:

```yaml
overrides:
  defaults:
    native_histograms_ingestion_enabled: true
```

**Symptom when ingestion is off:** querying the bare metric name
(`traces_spanmetrics_latency`, no `le`) returns no data while the classic
`traces_spanmetrics_latency_bucket` series are present. Enable the flag above and restart the
ingester; the native series will appear on the next push interval.

---

## Querying

**Native form** (no `le`, uses the exponential schema):

```promql
# p95 latency
histogram_quantile(0.95, sum by(service)(rate(traces_spanmetrics_latency[5m])))

# request count (from native histogram)
histogram_count(rate(traces_spanmetrics_latency[5m]))

# total duration (from native histogram)
histogram_sum(rate(traces_spanmetrics_latency[5m]))
```

**Classic form** (unchanged — always present regardless of native ingestion):

```promql
histogram_quantile(0.95, sum by(service, le)(rate(traces_spanmetrics_latency_bucket[5m])))
```

Both forms are simultaneously queryable when native ingestion is on. Existing dashboards built on
`_bucket` continue to work without modification.

---

## If a real Tempo metrics-generator is the source (span-metrics toggle OFF)

When synthkit's span-metrics opt-in (`POST /control/spanmetrics`) is left OFF, a real Tempo
metrics-generator (or Grafana Alloy spanmetrics connector) is expected to produce these families
from the trace stream. To match synthkit's dual wire shape, configure the generator to emit both
forms.

Tempo 2.6+ supports a native-histogram output option for the span-metrics and service-graphs
processors. ctx7 (`/grafana/tempo`) confirmed the options are `classic`, `native`, or `both`, but
did not surface the exact YAML key in a code snippet — consult current Grafana Cloud / Tempo docs
for the native-histogram output option (commonly an option like `histogram_type: native|classic|both`
under `metrics_generator.processor.span_metrics` / `service_graphs`). The default on most managed
stacks varies; check your stack version's release notes.

synthkit uses exponential **schema 3** (bucket factor ≈1.1), which matches the Tempo
metrics-generator default (SK-28 live-verified 2026-06-13).
