# APM span-metrics / service-graph (→ Mimir) — ScopeBlueprint

Tempo's metrics-generator derives these from the trace stream; synthkit fabricates them as
pre-mangled Mimir series. All families are blueprint-scoped (carry the `blueprint` label);
service-graph edges instead carry `client_blueprint` and `server_blueprint` (one per edge-side —
bare `blueprint` is absent from service-graph series). For the environment-label-key contract see
[`00-canon.md`](00-canon.md) `[slug: env-label-keys]`: **synthkit's native promrw lanes**
(span-metrics, service-graph, http_server_*, gen_ai_client_*, target_info) all stamp
`deployment_environment_name` (the OTEL semconv `_name` form). The legacy `deployment_environment`
form is **gone from synthkit's own emit**. Note: the GC OTLP gateway independently auto-promotes
the `deployment.environment.name` resource attr into BOTH `deployment_environment` and
`deployment_environment_name` labels on Tempo-derived metrics — so on a live stack with a real
gateway, BOTH forms may appear on gateway-derived series; that is a gateway behaviour, not
synthkit's.

*Provenance update 2026-06-23 — aligned to the OpenTelemetry semantic conventions
(`deployment.environment.name` v1.42.0 Stable); synthkit-native lanes standardize on
`deployment_environment_name`.*

---

## Two real producers, and which one synthkit models [slug: apm-producers]

*Provenance: grafana/tempo `modules/generator/processor/{spanmetrics,servicegraphs}/config.go` +
`modules/generator/AGENTS.md`; opentelemetry-collector-contrib
`connector/spanmetricsconnector/{README.md,connector.go}` +
`internal/coreinternal/traceutil/traceutil.go`; grafana/k8s-monitoring-helm
`charts/k8s-monitoring/charts/feature-application-observability/{values.yaml,templates/_connector_span_metrics.tpl}`.
Read 2026-08-27 (SKT-0008). Live-verified generator shape: SK-28, 2026-06-13.*

Span-derived RED metrics reach a Grafana stack from one of two producers with **materially
different shapes**. Which one a deployment uses is a deployment choice, and the two are normally
mutually exclusive to avoid double-counting.

**Producer A — Tempo metrics-generator (server-side).** Derives metrics from the trace stream
inside Tempo. Families: `traces_spanmetrics_{calls_total,latency,size_total}` and
`traces_service_graph_request_{total,failed_total,server_seconds,client_seconds}`. Intrinsic
dimensions `service`, `span_name`, `span_kind`, `status_code` (all default on; `status_message`
opt-in; `job`/`instance` only with `enable_target_info`, default off). Grafana Cloud stamps
`source="tempo"` as a registry external label and promotes resource attributes into flat labels.
Classic explicit histograms by default (`histogram_buckets`, span-metrics default
`ExponentialBuckets(0.002, 2, 14)`, service-graphs default `ExponentialBuckets(0.1, 2, 8)` — the
two are NOT the same set); on a live Grafana Cloud stack both a native and a classic
representation are emitted simultaneously (SK-28). Cumulative. Cardinality is bounded per tenant
by `max_active_series`, which DROPS excess series with no marker label.
`traces_service_graph_connection_info` and `traces_service_graph_request_messaging_system_seconds`
are opt-in subprocessors and are absent by default.

**Producer B — collector-side spanmetrics connector.** `otelcol.connector.spanmetrics`, wired by
the k8s-monitoring chart's `applicationObservability.connectors.spanMetrics`. Families are
`traces.span.metrics.calls` (monotonic Sum, no unit) and `traces.span.metrics.duration` (unit `s`),
from the connector's default `namespace: traces.span.metrics` — NOT the `traces_spanmetrics_*`
names. There is no size family, and **the chart ships no service-graph connector at all**, so a
deployment on this path has no `traces_service_graph_*` unless Tempo's service-graphs processor
is left running. Common dimensions are `service.name`, `span.name`, `span.kind`, `status.code`,
`collector.instance.id`; the chart's transform additionally stamps `source="spanmetrics"` on the
resource, sets `collector.id` to the collector hostname, and backfills `service.instance.id` from
`k8s.pod.name`/`k8s.pod.uid`. A `span_metrics_prefilter` drops Beyla-marked spans (`skipBeyla`,
default true — see `beyla.md`) and INTERNAL spans (`skipInternal`, default true). Cumulative
(`aggregation_temporality` default `AGGREGATION_TEMPORALITY_CUMULATIVE`; the chart does not render
the field).

⚠ **`span_kind` and `status_code` values are identical on both producers**: the proto enum
strings `SPAN_KIND_SERVER`/`SPAN_KIND_CLIENT`/… and `STATUS_CODE_UNSET`/`STATUS_CODE_OK`/
`STATUS_CODE_ERROR`. The connector README's prose examples showing `span.kind="SERVER"` and
`status.code="Ok"` are wrong — `traceutil.SpanKindStr`/`StatusCodeStr` return the proto form.

⚠ **`exclude_dimensions` and `aggregation_cardinality_limit` are producer-B-only.** The reference
deployment sets `excludeDimensions: [span.name]`, so its RED metrics carry **NO span-name
dimension** — any panel or rule doing `by (span_name)` silently matches nothing there. It also
sets `aggregationCardinalityLimit: 5000` (chart default 1000; connector default 0 = unlimited);
past the limit the connector does not drop, it folds further combinations into ONE entry labelled
`otel.metric.overflow="true"` (Prometheus-mangled `otel_metric_overflow`), so an aggregate series
can look like a real one. Tempo has neither mechanism.

### Which one synthkit models

**synthkit's opt-in self-emission models Producer A, the Tempo metrics-generator.** The family
names, `source="tempo"`, the `service`+`service_name` dual, the four service-graph families and
the dual native+classic histogram form are all generator properties that Producer B does not have.
Consequences to keep in mind:

- A dashboard built against synthkit's self-emission does **not** port unchanged to a stack whose
  span metrics come from a `span.name`-excluding connector. That is a property of that deployment,
  not a synthkit defect.
- synthkit emits no `otel_metric_overflow` series and no cardinality cap, correctly — its series
  count is bounded by graph topology × declared routes.
- The default-off posture (opt-in per blueprint) is not merely conservative: on a Producer-B
  deployment, self-emission would add a family set that stack does not have and omit the one it
  does.

### synthkit's deliberate differences from Producer A

- **Classic bucket bounds** are the empirically captured Grafana Cloud set
  `[0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0]`, used for
  both the span-metric and the service-graph latency families — not the two different OSS defaults.
  `histogram_buckets` is a per-tenant override and the capture outranks the OSS default; the exact
  live boundaries are re-verification item SK-96.
- **`latency_count` is a bounded sample of `calls_total`.** A real producer observes the duration of
  every span it counts, so the two are equal; synthkit observes `min(row calls, 200)` per tick
  (the shared `web_service` per-call budget). Quantiles are unaffected;
  `histogram_count(rate(...))` under-reads `rate(calls_total)` above the budget.
- **`traces_spanmetrics_size_total` is `calls × 256` bytes**, a constant — synthkit has no per-span
  wire size. The family is present and monotone; its magnitude is not meaningful.
- **Status and error fraction are trace-derived.** Span-metric `status_code` is the proto enum of
  the exact status helper used by the workload trace lane (so an unset successful HTTP span remains
  `STATUS_CODE_UNSET`, while instrumentation that explicitly marks success remains
  `STATUS_CODE_OK`). The app workload derives its `STATUS_CODE_ERROR` fraction and service-graph
  failed edge from active ledger outcomes using those same helpers; an active failure scenario
  therefore moves trace and span-metric errors together. A dashboard success denominator selects
  `status_code!="STATUS_CODE_ERROR"`, never an arbitrary success enum.
- **`connection_type=""` is emitted as a present, empty dimension**, which is the generator's real
  behaviour (`store/edge.go` `Unknown ConnectionType = ""`) and a deliberate exception to the
  omit-absent-dimensions rule.
- **`span_name` on a synthesized SERVER/root row is the span name this workload actually projects**
  — the node's declared route, not the node name — and the entry row carries the entry's real ROOT
  span kind (CLIENT for a browser entry, CONSUMER for worker/stream, INTERNAL for job), because a
  real producer reads both off the span.

---

## `traces_spanmetrics_calls_total` [slug: apm-calls]

*Provenance: predecessor SIGNALS §2.5 + `research/apm-spanmetrics-k8s.md` (empirical) + `emit/app_apm.go`.*

One series per (service × span_name × span_kind × status_code × env × namespace). Full label set:
`service` (the Tempo intrinsic — dashboards filter/group on this), `service_name` (dual, same value),
`span_name`, `span_kind` ∈ {SPAN_KIND_SERVER, SPAN_KIND_CLIENT, SPAN_KIND_INTERNAL, SPAN_KIND_PRODUCER,
SPAN_KIND_CONSUMER}, `status_code` ∈ {STATUS_CODE_OK, STATUS_CODE_ERROR, STATUS_CODE_UNSET},
`deployment_environment_name` (✅ synthkit-native form — `_name` suffix; see note in file header re: gateway
dual-promotion), `service_namespace`,
`service_version`, `namespace`, `cluster`+`k8s_cluster_name` (dual), `k8s_namespace_name`,
`job`=`{service_namespace}/{service_name}`, `source="tempo"`, **`telemetry_sdk_language`** (∈
{webjs,go,python,nodejs} — ⚠ **`calls_total` ONLY**).
`asserts_*` labels added platform-side (do not emit); `instance` omitted/empty on Tempo-sourced series.

```yaml signals
family: traces_spanmetrics_calls_total
scope: blueprint
sink: promrw
labels:
  blueprint: <blueprint>
  service: <service-name>
  service_name: <service-name>
  span_name: <span-name>
  span_kind: SPAN_KIND_SERVER|SPAN_KIND_CLIENT|SPAN_KIND_INTERNAL|SPAN_KIND_PRODUCER|SPAN_KIND_CONSUMER
  status_code: STATUS_CODE_OK|STATUS_CODE_ERROR|STATUS_CODE_UNSET
  deployment_environment_name: <env>              # synthkit-native _name form (2026-06-23)
  service_namespace: <namespace>
  service_version: <version>
  namespace: <k8s-namespace>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <k8s-namespace>
  job: "{service_namespace}/{service_name}"
  source: tempo
  telemetry_sdk_language: webjs|go|python|nodejs   # ⚠ calls_total ONLY
metrics:
  - {root: traces_spanmetrics_calls_total, type: counter, unit: count, v: ok}
```

> **Exemplars:** `traces_spanmetrics_calls_total` and `traces_spanmetrics_size_total` counters carry
> `trace_id` exemplars sourced from real ledger requests (the same trace_ids ProjectBatch ships as
> spans — click-through lands on a real Tempo trace). Gated by the per-blueprint `span_metrics_blueprints` control opt-in (default OFF); absent when
> not opted in (metrics-generator/beyla own the exemplars in that case).

---

## `traces_spanmetrics_latency` — histogram (native + classic) [slug: apm-latency]

Query `histogram_quantile(q, sum by(…)(rate(…[w])))`, `histogram_count(rate(…))`,
`histogram_sum(rate(…))`. Label set = `calls_total` **minus `telemetry_sdk_language`**.
> ✅ **Live-verified (staff stack otel-demo Tempo metrics-generator, 2026-06-13 — SK-28).** A real
> metrics-generator emits **BOTH** representations simultaneously: a native histogram (bare
> `traces_spanmetrics_latency`, no `le`) **and** classic `_bucket`/`_count`/`_sum` series. The classic
> form is always present and query-compatible — emitting it (or both) is safest so `_bucket`-based
> dashboards work; native-only under-represents reality. ⚠ `span_kind`/`status_code` ride as **proto
> enum** strings (`SPAN_KIND_CLIENT`, `STATUS_CODE_ERROR`).
> **synthkit** now emits the native form via `state.ObserveDual` at exponential schema 3 (bucket factor
> ≈1.1, the Tempo metrics-generator default) alongside the classic buckets — see
> `internal/state/nativehist.go` (`NativeSchemaSpanMetrics`). Requires native-histogram ingestion
> enabled on the Grafana Cloud stack (see `docs/native-histograms-setup.md`).

```yaml signals
family: traces_spanmetrics_latency
scope: blueprint
sink: promrw
labels:
  blueprint: <blueprint>
  service: <service-name>
  service_name: <service-name>
  span_name: <span-name>
  span_kind: SPAN_KIND_SERVER|SPAN_KIND_CLIENT|SPAN_KIND_INTERNAL|SPAN_KIND_PRODUCER|SPAN_KIND_CONSUMER
  status_code: STATUS_CODE_OK|STATUS_CODE_ERROR|STATUS_CODE_UNSET
  deployment_environment_name: <env>              # synthkit-native _name form (2026-06-23)
  service_namespace: <namespace>
  service_version: <version>
  namespace: <k8s-namespace>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <k8s-namespace>
  job: "{service_namespace}/{service_name}"
  source: tempo
  # ⚠ telemetry_sdk_language ABSENT (calls_total only)
metrics:
  - {root: traces_spanmetrics_latency, type: histogram, unit: seconds, v: ok, native: true,
     note: "BOTH native (no le) AND classic _bucket/_count/_sum emitted simultaneously (SK-28 live-verified)"}
buckets: [0.0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0]
# ⚠ NOT the Tempo OSS defaults (span-metrics ExponentialBuckets(0.002,2,14); service-graphs
# ExponentialBuckets(0.1,2,8)). This is the empirically captured Grafana Cloud shape carried from
# the predecessor; histogram_buckets is a per-tenant override. Exact live boundaries: SK-96.
```

> **Exemplars:** `_bucket` series carry `trace_id` exemplars (real ledger trace_ids, routed
> newest-first, route-filtered per span_name). Each exemplar attaches to the single landing bucket
> (smallest `le` ≥ value — OpenMetrics convention). Gated by the per-blueprint `span_metrics_blueprints` control opt-in (default OFF).

---

## `traces_spanmetrics_size_total` [slug: apm-size]

Span bytes; same label set as `_latency` (no `telemetry_sdk_language`).

```yaml signals
family: traces_spanmetrics_size_total
scope: blueprint
sink: promrw
labels:
  blueprint: <blueprint>
  service: <service-name>
  service_name: <service-name>
  span_name: <span-name>
  span_kind: SPAN_KIND_SERVER|SPAN_KIND_CLIENT|SPAN_KIND_INTERNAL|SPAN_KIND_PRODUCER|SPAN_KIND_CONSUMER
  status_code: STATUS_CODE_OK|STATUS_CODE_ERROR|STATUS_CODE_UNSET
  deployment_environment_name: <env>              # synthkit-native _name form (2026-06-23)
  service_namespace: <namespace>
  service_version: <version>
  namespace: <k8s-namespace>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <k8s-namespace>
  job: "{service_namespace}/{service_name}"
  source: tempo
  # ⚠ telemetry_sdk_language ABSENT (calls_total only)
metrics:
  - {root: traces_spanmetrics_size_total, type: counter, unit: bytes, v: ok}
```

---

## `traces_service_graph_request_total` / `_failed_total` — Counters [slug: apm-service-graph]

One series per directed service edge. Labels: `client`, `server`, `connection_type` ∈ {``(empty),
`database`, `virtual_node`, `messaging_system`} (the full generator enum — grafana/tempo
`modules/generator/processor/servicegraphs/store/edge.go`, read 2026-08-27; synthkit emits only
`` and `database`);
`_deployment_environment_name`, `_k8s_cluster_name`, `_k8s_namespace_name`, `_service_namespace`,
`_service_version`, **`_blueprint`**); `namespace` (client side), `service` (client name),
`source="tempo"`, `cluster`+`k8s_cluster_name`, `job`=`{client_namespace}/{client_service}`. ⚠ **No
bare `blueprint` on service-graph** — the metrics-generator prefixes promoted dims per edge-side, so
it is `client_blueprint` AND `server_blueprint`; `group by(…,blueprint)` silently matches nothing (§3.4).
Edge env labels are `client_deployment_environment_name` / `server_deployment_environment_name`
(synthkit-native `_name` form; 2026-06-23).

```yaml signals
family: traces_service_graph_request
scope: blueprint
sink: promrw
labels:
  client: <client-service>
  server: <server-service>
  connection_type: ""|database|virtual_node|messaging_system   # synthkit emits ""|database only
  client_blueprint: <blueprint>
  server_blueprint: <blueprint>
  client_cluster: <cluster-name>
  server_cluster: <cluster-name>
  client_deployment_environment_name: <env>    # synthkit-native _name form (2026-06-23)
  server_deployment_environment_name: <env>    # synthkit-native _name form (2026-06-23)
  client_k8s_cluster_name: <cluster-name>
  server_k8s_cluster_name: <cluster-name>
  client_k8s_namespace_name: <k8s-namespace>
  server_k8s_namespace_name: <k8s-namespace>
  client_service_namespace: <namespace>
  server_service_namespace: <namespace>
  client_service_version: <version>
  server_service_version: <version>
  namespace: <k8s-namespace>    # client side
  service: <client-service>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: "{client_namespace}/{client_service}"
  source: tempo
  # ⚠ NO bare `blueprint` label — use client_blueprint / server_blueprint
metrics:
  - {root: traces_service_graph_request_total, type: counter, unit: count, v: ok}
  - {root: traces_service_graph_request_failed_total, type: counter, unit: count, v: ok}
```

> **Exemplars:** `traces_service_graph_request_total` carries `trace_id` exemplars (real ledger
> trace_ids). Gated by the per-blueprint `span_metrics_blueprints` control opt-in (default OFF).

---

## `traces_service_graph_request_{server,client}_seconds` — histograms (native + classic) [slug: apm-service-graph-latency]

Same query form as `_latency`; same label set as `_request_total`. ✅ live-verified to emit BOTH native
and classic `_bucket`/`_count`/`_sum` (SK-28, as §2.8.2).
> **synthkit** emits the native form for both families via `state.ObserveDual` at exponential schema 3
> alongside the classic buckets (same mechanism as `traces_spanmetrics_latency` — see SK-28 callout above
> and `docs/native-histograms-setup.md`).

```yaml signals
family: traces_service_graph_request_server_seconds
scope: blueprint
sink: promrw
labels:
  client: <client-service>
  server: <server-service>
  connection_type: ""|database|virtual_node|messaging_system   # synthkit emits ""|database only
  client_blueprint: <blueprint>
  server_blueprint: <blueprint>
  client_cluster: <cluster-name>
  server_cluster: <cluster-name>
  client_deployment_environment_name: <env>    # synthkit-native _name form (2026-06-23)
  server_deployment_environment_name: <env>    # synthkit-native _name form (2026-06-23)
  client_k8s_cluster_name: <cluster-name>
  server_k8s_cluster_name: <cluster-name>
  client_k8s_namespace_name: <k8s-namespace>
  server_k8s_namespace_name: <k8s-namespace>
  client_service_namespace: <namespace>
  server_service_namespace: <namespace>
  client_service_version: <version>
  server_service_version: <version>
  namespace: <k8s-namespace>
  service: <client-service>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: "{client_namespace}/{client_service}"
  source: tempo
metrics:
  - {root: traces_service_graph_request_server_seconds, type: histogram, unit: seconds, v: ok, native: true,
     note: "BOTH native AND classic _bucket/_count/_sum emitted (SK-28 live-verified, as apm-latency)"}
  - {root: traces_service_graph_request_client_seconds, type: histogram, unit: seconds, v: ok, native: true,
     note: "BOTH native AND classic _bucket/_count/_sum emitted (SK-28 live-verified, as apm-latency)"}
```

> **Exemplars:** `_server_seconds_bucket` and `_client_seconds_bucket` series carry `trace_id`
> exemplars (real ledger trace_ids, landing-bucket placement). Gated by the per-blueprint `span_metrics_blueprints` control opt-in (default OFF).

---

## `target_info` and `traces_target_info` — distinct metrics [slug: apm-target-info]

Both must be emitted. `target_info` (OTLP resource): `service_name` (no bare `service`),
`service_namespace`, `service_version`, **`deployment_environment_name`**
(synthkit-native `_name` form; legacy `deployment_environment` dropped 2026-06-23), `k8s_cluster_name`, `k8s_namespace_name`, **`k8s_pod_name`**
(required for entity-graph `service→pod` join; value = the workload's first placement pod name
`fixture.PodName` → `{workload}-{rs-hash}-{pod-suffix}`, the single-source pod name shared with
k8s-monitoring `kube_pod_info` — §2.2; falls back to `{svc}-0` only when the workload is unplaced),
`k8s_deployment_name`(=service), **`k8s_node_name`** (node hosting the pod — omitted when empty per I13),
**`service_instance_id`** (= pod name — omitted when empty), `cluster`, `job`,
`telemetry_sdk_language`. ⚠ **`source` is NOT emitted here** — live OTLP-path `target_info` has no
`source` label; `source="tempo"` belongs ONLY on span-metric/service-graph series.
`traces_target_info` = same + `telemetry_sdk_name`; for the browser/RUM service additionally
`gf_feo11y_app_id`, `gf_feo11y_app_name`, `telemetry_distro_name="faro-web-sdk"`,
`browser_platform`, `browser_language`, `browser_mobile`.

```yaml signals
family: target_info
scope: blueprint
sink: promrw
labels:
  blueprint: <blueprint>
  service_name: <service-name>
  service_namespace: <namespace>
  service_version: <version>
  deployment_environment_name: <env>        # synthkit-native _name form (legacy deployment_environment dropped 2026-06-23)
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <k8s-namespace>
  k8s_pod_name: <pod-name>                  # fixture.PodName; required for service→pod entity-graph join
  k8s_deployment_name: <service-name>
  k8s_node_name: <node-hostname>            # ⚠ omitted when empty (I13); backend service only
  service_instance_id: <pod-name>           # = pod name; omitted when empty (I13); backend service only
  cluster: <cluster-name>
  job: "{service_namespace}/{service_name}"
  # ⚠ source is NOT emitted — source="tempo" is span-metrics/service-graph only
  telemetry_sdk_language: webjs|go|python|nodejs
metrics:
  - {root: target_info, type: gauge, unit: info, v: ok, note: "gauge=1; OTLP resource info series; no source label"}
---
family: traces_target_info
scope: blueprint
sink: promrw
labels:
  blueprint: <blueprint>
  service_name: <service-name>
  service_namespace: <namespace>
  service_version: <version>
  deployment_environment_name: <env>        # synthkit-native _name form (2026-06-23)
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <k8s-namespace>
  k8s_pod_name: <pod-name>
  k8s_deployment_name: <service-name>
  k8s_node_name: <node-hostname>            # omitted when empty (I13); backend service only
  service_instance_id: <pod-name>           # omitted when empty (I13); backend service only
  cluster: <cluster-name>
  job: "{service_namespace}/{service_name}"
  # ⚠ source is NOT emitted — source="tempo" is span-metrics/service-graph only
  telemetry_sdk_language: webjs|go|python|nodejs
  telemetry_sdk_name: <sdk-name>            # additional vs target_info
  # browser/RUM service only:
  gf_feo11y_app_id: <app-id>
  gf_feo11y_app_name: <app-name>
  telemetry_distro_name: faro-web-sdk
  browser_platform: <platform>
  browser_language: <language>
  browser_mobile: <bool>
metrics:
  - {root: traces_target_info, type: gauge, unit: info, v: ok, note: "gauge=1; same as target_info + telemetry_sdk_name + browser/RUM labels; no source label"}
```

---

## `traces_host_info` — node host identity [slug: apm-host-info]

*Provenance: `internal/construct/k8scluster/hostinfo.go`. Gated by `Features["application_observability"]`.*

One gauge=1 per node. Substrate-scoped (no blueprint label). Carries ONLY `grafana_host_id` — live capture
2026-06-15 confirmed `otelcol.connector.host_info` emits no other label (the EC2 instance ID is globally
unique, so it disambiguates without a cluster label). SK-55 resolved.

```yaml signals
family: traces_host_info
scope: substrate
sink: promrw
labels:
  grafana_host_id: <ec2-instance-id>      # = fixture.Node.InstanceID; e.g. i-0abcdef1234567890
metrics:
  - {root: traces_host_info, type: gauge, unit: info, v: ok, note: "gauge=1; one per node; grafana_host_id ONLY (live-confirmed 2026-06-15)"}
```
