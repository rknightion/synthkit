# Beyla internal metrics + EnvoyGateway control plane, OTLP wire form — 2026-09-04

Answers `cantfind.md` **SK-86** and the control-plane half of **SK-87**. Both were questions about
the OTLP wire form, which a read-back cannot answer: Grafana Cloud normalises dots to underscores on
ingest, so the queryable name is not the emitted one and un-mangling either direction is guesswork.

Raw capture SHA-256: `588571dc6a53c4e4717de43171399a99646cfed98641fcad0ce8b6f60b69ff35`.

Taken through a disposable in-cluster `otel/opentelemetry-collector-contrib:0.137.0` debug exporter
at detailed verbosity on the live EKS lab, reverted and deleted afterwards.

## SK-86 — Beyla internal metrics: DOTTED semconv spelling

The question was whether `internal_metrics.exporter: otel` emits the dotted semconv spelling of the
`beyla_internal_*` / `beyla_bpf_*` families or a different name set. **It is dotted semconv**, and
the set is small:

```text
beyla.bpf.map.entries_total            Gauge   unit: (empty)
beyla.bpf.map.max_entries_total        Gauge   unit: (empty)
beyla.bpf.probe.executions             Sum     unit: {call}
beyla.bpf.probe.latency_seconds_total  Sum     unit: s
beyla.internal.build.info              Gauge   unit: (empty)
```

Units ARE populated here (`{call}`, `s`), unlike the Envoy data-plane sink where every unit was
empty. Do not assume "OTLP path means no units".

### It is NOT capturable through the k8s-monitoring chart

`feature-auto-instrumentation/templates/_beyla-config.tpl:17` builds
`$internalMetrics := dict "prometheus" (dict "port" $targetPort)` and merges it as an **override**
over user config, so `internal_metrics.prometheus.port` is injected unconditionally — nulling the
block and nulling the key were both tried and the port returns. Beyla then exits with
`wrong Beyla configuration: you can't enable both OTEL and Prometheus internal metrics`. Verified the
expensive way: it crashlooped 2 of 4 DaemonSet pods on the live lab before revert.

This capture used a **standalone** Beyla v3.32.0 Deployment with its own config, alongside the
chart-managed DaemonSet, untouched.

Second trap: Beyla refuses to start with only `internal_metrics` configured —
`you need to define at least one exporter: trace_printer, grafana, otel_metrics_export,
otel_traces_export or prometheus_export`. A data exporter must be present even when only internal
metrics are wanted.

## SK-87 control plane — UNDERSCORE, verbatim

The mirror image of the data-plane result in the same product. Where `EnvoyProxy` ships dotted
native stat names, the `EnvoyGateway` **controller** ships underscore names identical to its
Prometheus spelling. 12 names, all `unit: 1`:

```text
resource_apply_duration_seconds     Histogram
resource_apply_total                Sum
resource_delete_duration_seconds    Histogram
resource_delete_total               Sum
status_update_duration_seconds      Histogram
status_update_total                 Sum
watchable_depth                     Gauge
watchable_event_total               Sum
watchable_publish_total             Sum
watchable_subscribe_duration_seconds Histogram
watchable_subscribe_total           Sum
xds_snapshot_create_total           Sum
```

**The OTLP sink is NOT a replacement for the controller's Prometheus surface.** It carries none of
`controller_runtime_*`, `workqueue_*`, `rest_client_*` or `certwatcher_*`, which
`signals/k8s-addons.md [slug: k8s-envoy-gateway]` documents on the scrape.

Measured aside, because the documentation and the stack disagree: **no Envoy Gateway metric,
control plane or data plane, was present in the reference stack** at the time of capture or 12 hours
earlier. `prometheus.disable: false` means the controller EXPOSES :19001; nothing collects it into
that stack.

## The API asymmetry that costs an hour

`EnvoyGateway.telemetry.metrics.sinks[].openTelemetry` accepts `protocol`.
`EnvoyProxy.telemetry.metrics.sinks[].openTelemetry` does **not** — it is a strict-decoding error,
and **ArgoCD reports the application `Synced` while the API rejects the resource**, so the sink
silently never reaches the CR. The two APIs look alike and differ. Validate with
`kubectl apply --dry-run=server`, never with sync status.
