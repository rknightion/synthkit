# Grafana Beyla (eBPF auto-instrumentation) — ScopeBlueprint + ScopeSubstrate

Beyla is Grafana's eBPF-based auto-instrumentation agent. It **observes existing services** at the
kernel boundary — no SDK changes required — and emits its own RED metrics, span/service-graph
metrics, boundary-only traces, network-flow metrics, and internal self-metrics. Synthkit models
Beyla as an **observation lane on `web_service`** (request-correlated families gated by
`observability.beyla`) plus a separate **`beyla_agent` substrate construct** (internal/self metrics).

*Provenance: **captured live from a Beyla 3.20.0 DaemonSet (grafana-k8s-monitoring-beyla),
OBI telemetry_sdk_version v1.43.0, 2026-06-15; raw /metrics + /internal/metrics exposition.**
Cross-checked against `github.com/grafana/beyla` source (OBI vendor + chart, code-verified 2026-06-15):
`pkg/beyla/config_obi.go:89` (VendorPrefix="beyla"), `metric.go:59-79` (RED names),
`bucket.go:22-25` (bucket arrays), `tracesgen.go:336,1265` (span.metrics.skip stamping),
`pkg/export/alloy/traces.go:73` (resource form), `export/otel/metrics.go:890-896`
(ExcludeOTelInstrumentedServices), `attrs.go:121-124` / `otelcfg/common.go:102-113` /
`prom.go:94-97` (distro name marker), `iprom.go:94-96,216` (beyla_avoided_services), `iprom.go`
(internal metrics), `pkg/webhook/metrics.go:18-25` (auto-inject, documented-only).
k8s-monitoring-helm chart: `values.yaml:292,295` (`autoInstrumentation` default `enabled: false`),
`feature-auto-instrumentation/values.yaml:139-144` (feature list), `_module.alloy.tpl:58`
(`job=integrations/beyla`). Resolves cantfind SK-41/42/43 (k8s spelling, network labels +
direction, span-metric source); SK-44 (standalone host keys) stays PENDING — only k8s mode was
captured.*

> ⚠ **ABSENT-DIM TRAP — applies to ALL Beyla families.** Beyla emits absent k8s/cloud
> dimensions as **EMPTY STRING** (e.g. `k8s_job_name=""`, `k8s_cronjob_name=""`), **NOT
> omitted**. This is an **EXCEPTION** to synthkit's "an absent dimension is OMITTED" invariant.
> Every k8s_* / cloud_* / host_* envelope key in the label sets below is present on every series;
> the lane emits `""` for an absent dim rather than dropping the label. Live-confirmed
> 2026-06-15.

---

## Deployment differentiators

### Mode: k8s (kubernetes) vs standalone

| Dimension | `mode: kubernetes` | `mode: standalone` |
|---|---|---|
| Resource labels | `service_name`, `service_namespace`, `k8s_namespace_name`, `k8s_pod_name`, `k8s_deployment_name`, `k8s_node_name`, `k8s_cluster_name` | `service_name`, `service_namespace`, `host_name`, `host_id` |
| k8s labels on network-flow | `k8s_src_*` / `k8s_dst_*` owner/namespace/cluster present | absent; src/dst by name only |
| Scrape job label | `job=integrations/beyla` (set in chart Alloy module) | user-configured (⚠ synthkit pins a SYNTHETIC `job=integrations/beyla` in standalone too — real standalone job is user-configured and varies) |
| Deployed by | k8s-monitoring chart (`autoInstrumentation.enabled: true`) | Beyla binary/container on host |

⚠ The k8s-monitoring chart defaults `autoInstrumentation.enabled: false` — a deployment that does
not set this `true` emits **zero Beyla telemetry**, even if other chart components (k8s-monitoring,
alloy) are active.

### Context: ebpf_only vs coexist_sdk

Beyla's `ExcludeOTelInstrumentedServices` (default `true`) detects a service's outbound OTLP calls
and suppresses its own RED/span/trace signals **per signal** for that service. Network-flow (netolly)
is **never gated** — it emits regardless of SDK presence.

| Signal family | `context: ebpf_only` | `context: coexist_sdk` |
|---|---|---|
| RED histograms (`http_server_*`, etc.) | ✅ Emitted | ❌ Suppressed (SDK owns) |
| Span-metrics (`traces_spanmetrics_*`) | ✅ Emitted (`source="beyla"`) | ❌ Suppressed |
| Service-graph (`traces_service_graph_*`) | ✅ Emitted (`source="beyla"`) | ❌ Suppressed |
| Boundary traces (OTLP) | ✅ Emitted | ❌ Suppressed (SDK owns traces) |
| Network-flow (`beyla_network_flow_bytes_total`) | ✅ Emitted | ✅ Emitted |
| `target_info` / `traces_target_info` | ✅ Emitted (distro=ebpf) | ✅ Emitted (distro=ebpf) |
| `beyla_avoided_services` | ❌ Not set | ✅ Set to 1 |

In `coexist_sdk`, the workload's existing `source="tempo"` span-metric/service-graph/trace lane runs
**unchanged and in full**. The Beyla lane adds only the network/target_info/avoided footprint on top.

### span.metrics.skip dedup mechanism

When `application_span` and/or `application_service_graph` features are enabled (both on by chart
default), Beyla stamps `span.metrics.skip=true` on its own emitted spans
(`tracesgen.go:336,1265`; resource attr form `pkg/export/alloy/traces.go:73`). The chart's Alloy
`span_metrics_prefilter` drops these spans before the `otelcol.connector.spanmetrics` connector
(chart gate `connectors.spanMetrics.skipBeyla: true`, default). This means **the trace-derived
metrics-generator deliberately skips Beyla spans** → Beyla self-emits `traces_spanmetrics_*` and
`traces_service_graph_*` directly; the generator does not double-count them.

### Origin marker

Beyla stamps `telemetry.distro.name = "opentelemetry-ebpf-instrumentation"` on every resource it
produces (`attrs.go:121-124`; OTLP `otelcfg/common.go:102-113`; Prom `target_info` `prom.go:94-97`).
This is the load-bearing "came from Beyla" flag, distinct from an SDK's `telemetry.sdk.*` attrs.
In synthkit the Prom-mangled form is `telemetry_distro_name="opentelemetry-ebpf-instrumentation"`.

⚠ **Live-confirmed origin values (target_info / traces_target_info, Beyla 3.20.0):**
- `telemetry_sdk_name="beyla"` — **NOT `"opentelemetry"`** (the OBI vendor prefix surfaces here too).
- `telemetry_distro_name="opentelemetry-ebpf-instrumentation"` (v: ok).
- `telemetry_distro_version="unset"` (v: ok — Beyla does not stamp a distro version).
- `telemetry_sdk_version="v1.43.0"` (OBI vendored version), `telemetry_sdk_language="go"`.
- `source="beyla"` on target_info and the span-metric/service-graph families (v: ok).

---

## Beyla RED (request, error, duration) — application metrics [slug: beyla-red-application]

HTTP/gRPC/DB/gen_ai RED metrics emitted by Beyla for each observed service. Emitted only in
`context: ebpf_only`. Metric names are **semconv v1.38.0 Prometheus-mangled** (verified `metric.go:59-79`).
Histograms use verified bucket arrays (verified `bucket.go:22-25`):
- Duration buckets: `[0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10]`
- Size buckets: `[0, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192]`

Beyla vendors OBI and sets `VendorPrefix="beyla"` (`config_obi.go:89`) — ALL `obi_*` names surface
as `beyla_*` for internal/agent metrics. The RED metric names below are **not** prefixed with `beyla_`
because they are standard OTel semconv names (same names as OTel SDK would emit).

⚠ **k8s resource label spelling RESOLVED:** `k8s_namespace_name` (OBI default) is **live-confirmed**
(NOT `k8s_namespace`) — `v: ok`. The full k8s envelope below (every `k8s_*` key) is present on every
RED series; absent dims emit `""` (see ABSENT-DIM TRAP above). The RED label envelope is **identical**
for `http_server_request_duration_seconds` and `http_client_request_duration_seconds`.

```yaml signals
family: beyla_red_application
scope: blueprint
sink: promrw
labels:
  # ⚠ ALL keys below present on every series; absent dims emit "" (ABSENT-DIM TRAP). v: ok (live).
  # k8s mode FULL resource envelope (live-confirmed Beyla 3.20.0):
  service_name: <service-name>
  service_namespace: <namespace>
  instance: <instance>
  job: integrations/beyla              # chart-set; v: ok
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <k8s-namespace>  # v: ok (OBI default — confirmed, not k8s_namespace)
  k8s_node_name: <node-name>
  k8s_pod_name: <pod-name>
  k8s_pod_uid: <pod-uid>
  k8s_pod_start_time: <rfc3339>
  k8s_container_name: <container>
  k8s_kind: <kind>                     # Deployment|DaemonSet|StatefulSet|Job|… (owner kind)
  k8s_owner_name: <owner>
  k8s_deployment_name: <name>          # "" if not owned by a Deployment
  k8s_replicaset_name: <name>          # "" if absent
  k8s_daemonset_name: <name>           # "" if absent
  k8s_statefulset_name: <name>         # "" if absent
  k8s_job_name: <name>                 # "" if absent
  k8s_cronjob_name: <name>             # "" if absent
  # standalone mode resource labels (instead of k8s_* above — NOT live-captured, SK-44):
  # host_name: <hostname>              # v: assumed; omitted in k8s mode
  # host_id: <host-id>                 # v: assumed; omitted in k8s mode
  # ⚠ NO telemetry_distro_name on RED (live ground truth — distro marker is target_info only).
  # ⚠ NO `source` label on RED (live ground truth — source is span/service-graph/target_info only).
  # ⚠ SYNTHKIT-ISM: `blueprint` is stamped on every Beyla RED/span/target series by the scoped
  #   writer (web_service is ScopeBlueprint). Real Beyla RED carries NO `blueprint` label — this is
  #   a synthkit divergence (the blueprint selector), unavoidable on this lane. Documented, not real.
  # per-metric semconv labels (present on the specific family):
  http_request_method: <method>        # GET|POST|PUT|DELETE|…
  http_response_status_code: <code>    # 200|404|500|…
  http_route: <route>                  # /api/v1/… (server only)
  url_scheme: <scheme>                 # http|https (v: ok, live)
  server_address: <host>
  server_port: <port>                  # v: ok (live; present on RED — synthkit pins :8080)
  error_type: <error>                  # present on error cases
  # RPC labels (rpc_* metrics only):
  rpc_method: <method>
  rpc_grpc_status_code: <code>
  # DB labels (db_client_* metrics only):
  db_operation_name: <op>
  db_system_name: <system>             # postgresql|mysql|redis|…
  # gen_ai labels (gen_ai_client_* metrics only): see signals/genai.md
metrics:
  # HTTP server (observed inbound traffic)
  - {root: http_server_request_duration_seconds, type: histogram, unit: seconds, v: ok,
     buckets: "[0,0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10]",
     note: "ebpf_only only; semconv v1.38.0; bucket.go:22"}
  - {root: http_server_request_body_size_bytes, type: histogram, unit: bytes, v: ok,
     buckets: "[0,32,64,128,256,512,1024,2048,4096,8192]",
     note: "ebpf_only only; size buckets bucket.go:25"}
  - {root: http_server_response_body_size_bytes, type: histogram, unit: bytes, v: ok,
     buckets: "[0,32,64,128,256,512,1024,2048,4096,8192]",
     note: "ebpf_only only"}
  # HTTP client (observed outbound traffic)
  - {root: http_client_request_duration_seconds, type: histogram, unit: seconds, v: ok,
     buckets: "[0,0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10]",
     note: "ebpf_only only"}
  - {root: http_client_request_body_size_bytes, type: histogram, unit: bytes, v: ok,
     buckets: "[0,32,64,128,256,512,1024,2048,4096,8192]",
     note: "ebpf_only only"}
  - {root: http_client_response_body_size_bytes, type: histogram, unit: bytes, v: ok,
     buckets: "[0,32,64,128,256,512,1024,2048,4096,8192]",
     note: "ebpf_only only"}
  # gRPC (rpc_grpc_status_code + rpc_method labels)
  - {root: rpc_server_duration_seconds, type: histogram, unit: seconds, v: ok,
     buckets: "[0,0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10]",
     note: "ebpf_only only; gRPC server side"}
  - {root: rpc_client_duration_seconds, type: histogram, unit: seconds, v: ok,
     buckets: "[0,0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10]",
     note: "ebpf_only only; gRPC client side"}
  # Database client (db_operation_name discriminator + db_system_name/db_collection_name/db_namespace)
  - {root: db_client_operation_duration_seconds, type: histogram, unit: seconds, v: assumed,
     buckets: "[0,0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10]",
     note: "ebpf_only only; also covers cache (redis/memcached) hops via db_system_name; source-confirmed only — no live instance in capture fleet (no DB-observed service captured)"}
  # gen_ai client (reuses internal/genai; see signals/genai.md for full label set)
  - {root: gen_ai_client_operation_duration_seconds, type: histogram, unit: seconds, v: ok,
     buckets: "[0,0.005,0.01,0.025,0.05,0.075,0.1,0.25,0.5,0.75,1,2.5,5,7.5,10]",
     note: "ebpf_only only; gen_ai semconv labels from internal/genai"}
  - {root: gen_ai_client_token_usage, type: histogram, unit: tokens, v: ok,
     note: "ebpf_only only; input_tokens / output_tokens split; see signals/genai.md"}
```

---

## Beyla network-flow metrics [slug: beyla-network]

Network-flow metrics are emitted in **both** `ebpf_only` and `coexist_sdk` contexts — they are
never gated by `ExcludeOTelInstrumentedServices`. Driven by the ledger's service-call topology
(src = observed service, dst = each hop target). TCP stats are feature-gated and off in the
k8s-monitoring chart default (`prometheus_export.features` list does not include `application_host`
TCP stats in the default values.yaml).

⚠ **Network-flow labels RESOLVED — `v: ok`** (live-confirmed Beyla 3.20.0). The
chart-curated allow-list (the realistic production shape) is exactly:
`direction, k8s_cluster_name, k8s_src_name, k8s_src_namespace, k8s_src_owner_name,
k8s_src_owner_type, k8s_dst_name, k8s_dst_namespace, k8s_dst_owner_name, k8s_dst_owner_type`.
Note the **extra `k8s_src_owner_type`/`k8s_dst_owner_type` and `k8s_src_name`/`k8s_dst_name`** beyond
the original assumption. `direction ∈ {request, response, unknown}` (live-confirmed). Destination
fields are often `""` for egress to non-k8s / virtual targets (ABSENT-DIM TRAP).

```yaml signals
family: beyla_network
scope: blueprint
sink: promrw
labels:
  # Chart-curated allow-list — exactly these 10 keys (v: ok, live-confirmed):
  direction: request|response|unknown   # v: ok (live value set)
  k8s_cluster_name: <cluster>           # v: ok
  k8s_src_name: <pod/workload>          # v: ok
  k8s_src_namespace: <namespace>        # v: ok
  k8s_src_owner_name: <owner>           # v: ok
  k8s_src_owner_type: <kind>            # v: ok (Deployment|DaemonSet|StatefulSet|…)
  k8s_dst_name: <pod/workload>          # v: ok; often "" for non-k8s/virtual dst
  k8s_dst_namespace: <namespace>        # v: ok; often ""
  k8s_dst_owner_name: <owner>           # v: ok; often ""
  k8s_dst_owner_type: <kind>            # v: ok; often ""
  # Off-by-default (opt-in only — NOT in the chart allow-list, NOT emitted in v1 synthkit):
  # src_address, dst_address, src_port, dst_port, transport, src_zone, dst_zone
metrics:
  - {root: beyla_network_flow_bytes_total, type: counter, unit: bytes, v: ok,
     note: "emitted in BOTH ebpf_only and coexist_sdk; never gated; metric.go verified"}
  # TCP stats — feature-gated (FeatureStats / application_host); NOT in chart default; modelled only:
  - {root: beyla_stat_tcp_rtt_seconds, type: histogram, unit: seconds, v: ok,
     note: "feature-gated (stats); off in chart default; NOT emitted in v1 synthkit unless stats feature configured"}
  - {root: beyla_stat_tcp_failed_connections, type: counter, unit: count, v: ok,
     note: "feature-gated (stats); off in chart default"}
  - {root: beyla_stat_tcp_retransmits, type: counter, unit: count, v: ok,
     note: "feature-gated (stats); off in chart default"}
  - {root: beyla_stat_tcp_io_bytes_total, type: counter, unit: bytes, v: ok,
     note: "feature-gated (stats); off in chart default"}
```

---

## Beyla span-metrics and service-graph [slug: beyla-spanmetrics]

Beyla self-emits the `traces_spanmetrics_*` and `traces_service_graph_*` families **directly** (it
does not rely on a Tempo metrics-generator). These are the **same metric names** as the APM families
in [`signals/apm.md`](./apm.md) ([slug: apm-calls], [slug: apm-latency], [slug: apm-service-graph]).
The only Beyla-specific deltas from the `apm.md` contract are:

- **`source` label value:** `"beyla"` instead of `"tempo"`. **`v: ok`** — live-confirmed
  (Beyla 3.20.0, 2026-06-15).
- **Resource attributes:** `telemetry_distro_name="opentelemetry-ebpf-instrumentation"` instead of
  SDK `telemetry_sdk_*` attrs. `telemetry_sdk_language="go"` IS present on span-metrics (live).
- **`span.metrics.skip` dedup:** Beyla stamps this on its own spans so the Alloy spanmetrics
  connector skips them (see dedup mechanism section above). The generator never double-counts.
- **Emitted in `ebpf_only` only.** In `coexist_sdk`, the SDK's own `source="tempo"` lane emits
  these families; the Beyla lane suppresses them.
- ⚠ **Service-graph uses PREFIXED keys** `client_k8s_*` / `server_k8s_*` — **NOT bare `k8s_*`** —
  plus `client`/`server` endpoints, `client_service_namespace`/`server_service_namespace`, and
  `connection_type` (`∈ {virtual_node, ""}`). Live-confirmed.

> For the full label contract (value sets, ⚠ traps like `deployment_environment`
> vs `deployment_environment_name`, the native+classic histogram dual-emit, and the
> `client_blueprint`/`server_blueprint` edge-side labels), **refer to `signals/apm.md`** — they are
> not repeated here. Apply the Beyla deltas above. The live span-metric + service-graph label sets
> are spelled out below.

**Span-metrics label set (live-confirmed, `traces_spanmetrics_calls_total` / `_latency`):**
`cloud_availability_zone, cloud_region, deployment_environment_name, instance, job,
k8s_cluster_name, k8s_namespace_name, k8s_node_name, service_name, service_namespace,
service_version, source, span_kind, span_name, status_code, telemetry_sdk_language`. Values:
`span_kind ∈ {SPAN_KIND_CLIENT, SPAN_KIND_SERVER}`, `status_code ∈ {STATUS_CODE_UNSET, STATUS_CODE_OK,
STATUS_CODE_ERROR}`, `telemetry_sdk_language="go"`, `source="beyla"`.

**Service-graph label set (live-confirmed, `traces_service_graph_request_total` / `_failed_total` /
`_client_seconds` / `_server_seconds`):**
`client, client_k8s_cluster_name, client_k8s_namespace_name, client_service_namespace,
connection_type, server, server_k8s_cluster_name, server_k8s_namespace_name,
server_service_namespace, source`. `connection_type ∈ {virtual_node, ""}`, `source="beyla"`.

```yaml signals
family: beyla_spanmetrics
scope: blueprint
sink: promrw
labels:
  # span-metrics live label set (v: ok, Beyla 3.20.0):
  source: beyla                         # v: ok (live; "tempo" in the SDK lane)
  span_kind: SPAN_KIND_CLIENT|SPAN_KIND_SERVER   # v: ok
  span_name: <span-name>                # v: ok
  status_code: STATUS_CODE_UNSET|STATUS_CODE_OK|STATUS_CODE_ERROR   # v: ok
  service_name: <service-name>
  service_namespace: <namespace>
  service_version: <version>
  deployment_environment_name: <env>    # v: ok (NOT deployment_environment — see apm.md trap)
  instance: <instance>
  job: integrations/beyla
  k8s_cluster_name: <cluster>
  k8s_namespace_name: <namespace>
  k8s_node_name: <node>
  cloud_availability_zone: <az>         # v: ok
  cloud_region: <region>                # v: ok
  telemetry_sdk_language: go            # v: ok (present on span-metrics, value "go")
  # ⚠ NO telemetry_distro_name on span-metrics (live ground truth — distro marker is target_info
  #   only; the SPAN-METRIC set carries telemetry_sdk_language but NOT telemetry_distro_name).
  # ⚠ SYNTHKIT-ISM: `blueprint` is stamped on span-metric series by the scoped writer
  #   (ScopeBlueprint). Real Beyla span-metrics carry NO `blueprint` label — synthkit divergence.
  # ⚠ STANDALONE span-metric shape is v:assumed (uncaptured live): standalone DROPS
  #   k8s_cluster_name / k8s_namespace_name / k8s_node_name and carries host/service identity only.
  # service-graph instead uses client_k8s_*/server_k8s_* prefixed keys (see set above):
  # client, server, connection_type (virtual_node|""), client_service_namespace,
  # server_service_namespace, client_k8s_cluster_name, client_k8s_namespace_name,
  # server_k8s_cluster_name, server_k8s_namespace_name — all v: ok (live).
  # ⚠ NO `job` on service-graph (live ground truth — service-graph carries no job label).
metrics:
  # Cross-reference apm.md — same root names, Beyla-emitted with source="beyla":
  - {root: traces_spanmetrics_calls_total, type: counter, unit: count, v: ok,
     note: "same name as apm.md; source=beyla; ebpf_only context only; see signals/apm.md [slug: apm-calls]"}
  - {root: traces_spanmetrics_latency, type: histogram, unit: seconds, v: ok, native: true,
     note: "same name as apm.md; BOTH native AND classic _bucket/_count/_sum; ebpf_only only; see [slug: apm-latency]"}
  - {root: traces_spanmetrics_size_total, type: counter, unit: bytes, v: assumed,
     note: "DEFERRED — not emitted by the v1 Beyla lane; APM-derived, unconfirmed for Beyla (no live capture). same name as apm.md; see [slug: apm-size]"}
  - {root: traces_service_graph_request_total, type: counter, unit: count, v: ok,
     note: "same name as apm.md; source=beyla; ebpf_only only; see [slug: apm-service-graph]"}
  - {root: traces_service_graph_request_failed_total, type: counter, unit: count, v: ok,
     note: "same name as apm.md; ebpf_only only"}
  - {root: traces_service_graph_request_server_seconds, type: histogram, unit: seconds, v: ok, native: true,
     note: "same name as apm.md; BOTH native AND classic; ebpf_only only; see [slug: apm-service-graph-latency]"}
  - {root: traces_service_graph_request_client_seconds, type: histogram, unit: seconds, v: ok, native: true,
     note: "same name as apm.md; BOTH native AND classic; ebpf_only only"}
  # target_info / traces_target_info (Beyla-origin variant; identical label sets):
  - {root: target_info, type: gauge, unit: info, v: ok,
     note: "=1; both contexts; telemetry_sdk_name=beyla, telemetry_distro_name=opentelemetry-ebpf-instrumentation, telemetry_distro_version=unset, telemetry_sdk_version=v1.43.0, source=beyla; full label set below; see [slug: apm-target-info]"}
  - {root: traces_target_info, type: gauge, unit: info, v: ok,
     note: "=1; identical label set to target_info; Beyla origin marker present"}
```

**target_info / traces_target_info full label set (live-confirmed, identical for both; =1):**
`cloud_account_id, cloud_availability_zone, cloud_platform, cloud_provider, cloud_region, host_id,
host_image_id, host_name, host_type, instance, job, k8s_cluster_name, k8s_container_name,
k8s_cronjob_name, k8s_daemonset_name, k8s_deployment_name, k8s_job_name, k8s_kind, k8s_namespace_name,
k8s_node_name, k8s_owner_name, k8s_pod_name, k8s_pod_start_time, k8s_pod_uid, k8s_replicaset_name,
k8s_statefulset_name, os_type, service_name, service_namespace, source, telemetry_distro_name,
telemetry_distro_version, telemetry_sdk_language, telemetry_sdk_name, telemetry_sdk_version`.
⚠ Corrected values (live): `telemetry_sdk_name="beyla"` (NOT "opentelemetry"),
`telemetry_distro_name="opentelemetry-ebpf-instrumentation"`, `telemetry_distro_version="unset"`,
`telemetry_sdk_version="v1.43.0"`, `telemetry_sdk_language="go"`, `source="beyla"`. Absent
k8s/cloud dims emit `""` (ABSENT-DIM TRAP).

**traces_host_info** (gauge=1): label **`grafana_host_id`** (e.g. `grafana_host_id="i-…"`) — NOT
`cloud_host_id`. Live-confirmed. ⚠ **DEFERRED — NOT emitted by the v1 Beyla lane or agent
construct** (documented for fidelity; no synthkit emitter wires it yet).

---

## Beyla internal / self-metrics [slug: beyla-internal]

Emitted by the `beyla_agent` substrate construct (NOT the web_service lane). Substrate-scoped —
never carries a `blueprint` label. Disambiguated by `k8s_cluster_name` + `k8s_node_name` (k8s mode)
or `host_name` (standalone). All names carry the `beyla_` prefix (OBI VendorPrefix). Scraped via
`/internal/metrics`, `job=integrations/beyla` in the chart Alloy module.

`beyla_avoided_services` is a special case: it is emitted **per observed service** and carries
`service_name`/`service_namespace`/`service_instance_id`/`telemetry_type` labels. In synthkit it is
emitted by the **Beyla lane on `web_service`** (not the agent construct), because the lane has the
service identity; the agent construct emits only node/agent-global metrics.

⚠ **Two distinct build-info surfaces (live-confirmed):**
- `beyla_internal_build_info{goarch, goos, goversion, revision, version}=1` — on **`/internal/metrics`**.
- `beyla_build_info{goarch, goos, goversion, revision, target_lang, version}` — on **`/metrics`**
  (NOT /internal); additionally carries **`target_lang`**.

The following internal metrics are **live-confirmed** on `/internal/metrics` (Beyla 3.20.0):
`beyla_bpf_map_entries_total{map_id,map_name,map_type}`, `beyla_bpf_map_max_entries_total{…}`,
`beyla_bpf_probe_executions_total{probe_id,probe_name,probe_type}`,
`beyla_bpf_probe_latency_seconds_total{…}`, `beyla_bpf_network_packets_total` (no labels),
`beyla_bpf_network_ignored_packets_total` (no labels), `beyla_instrumented_processes{process_name}`,
`beyla_instrumentation_errors_total{process_name,error_type}`, `beyla_ebpf_tracer_flushes`
(histogram), `beyla_kube_cache_forward_lag_seconds` (histogram), `beyla_otel_metric_exports_total`
(no labels), `beyla_otel_trace_exports_total` (no labels), `beyla_prometheus_http_requests_total{path,port}`.

```yaml signals
family: beyla_internal
scope: substrate
sink: promrw
labels:
  # k8s mode identity labels:
  k8s_cluster_name: <cluster>
  k8s_node_name: <node>
  job: integrations/beyla    # v: ok (chart _module.alloy.tpl:58)
  # standalone mode instead:
  # host_name: <hostname>    # v: assumed
metrics:
  # Build info (gauge=1, static labels carry version/build metadata). TWO surfaces:
  - {root: beyla_internal_build_info, type: gauge, unit: info, v: ok,
     note: "/internal/metrics; =1; labels: goarch, goos, goversion, revision, version"}
  - {root: beyla_build_info, type: gauge, unit: info, v: ok,
     note: "DEFERRED — NOT emitted by v1 (no synthkit emitter wires it; the agent construct emits beyla_internal_build_info only). /metrics (NOT /internal); labels: goarch, goos, goversion, revision, target_lang, version (+target_lang)"}
  # Process instrumentation
  - {root: beyla_instrumented_processes, type: gauge, unit: count, v: ok,
     note: "process_name label; count of eBPF-instrumented processes on this node"}
  # Per-service avoidance (emitted by the web_service Beyla lane, not the agent construct):
  - {root: beyla_avoided_services, type: gauge, unit: count, v: assumed,
     note: "=1 per avoided service; labels: service_name, service_namespace, service_instance_id, telemetry_type; coexist_sdk context only; iprom.go:94-96,216; source-confirmed only — no live instance in capture fleet. ⚠ SYNTHKIT-ISM: because this is emitted by the web_service Beyla lane (ScopeBlueprint, has the service identity), the scoped writer stamps a `blueprint` label on it — real Beyla /internal/metrics avoided_services has NO blueprint label (it is substrate /internal exposition). Synthkit divergence."}
  # Instrumentation errors
  - {root: beyla_instrumentation_errors_total, type: counter, unit: count, v: ok,
     note: "labels: process_name, error_type; instrumentation attach/detach errors"}
  # eBPF probe execution stats
  - {root: beyla_bpf_probe_executions_total, type: counter, unit: count, v: ok,
     note: "labels: probe_id, probe_name, probe_type; kernel eBPF probe execution count"}
  - {root: beyla_bpf_probe_latency_seconds_total, type: counter, unit: seconds, v: ok,
     note: "labels: probe_id, probe_name, probe_type; cumulative eBPF probe execution latency"}
  # eBPF map sizing
  - {root: beyla_bpf_map_entries_total, type: gauge, unit: count, v: ok,
     note: "labels: map_id, map_name, map_type; current entries across eBPF maps"}
  - {root: beyla_bpf_map_max_entries_total, type: gauge, unit: count, v: ok,
     note: "labels: map_id, map_name, map_type; configured max capacity"}
  # eBPF network packet counters (no labels)
  - {root: beyla_bpf_network_packets_total, type: counter, unit: count, v: ok,
     note: "no labels; total packets seen by the netolly eBPF path"}
  - {root: beyla_bpf_network_ignored_packets_total, type: counter, unit: count, v: ok,
     note: "no labels; packets dropped/ignored by the netolly eBPF path"}
  # tracer / kube-informer histograms
  - {root: beyla_ebpf_tracer_flushes, type: histogram, unit: count, v: ok,
     note: "histogram; eBPF ringbuf tracer flush batch sizes"}
  - {root: beyla_kube_cache_forward_lag_seconds, type: histogram, unit: seconds, v: ok,
     note: "histogram; k8s informer-cache forward lag; k8s mode only"}
  # Export / scrape counters
  - {root: beyla_otel_metric_exports_total, type: counter, unit: count, v: ok,
     note: "no labels; OTLP metric export requests"}
  - {root: beyla_otel_trace_exports_total, type: counter, unit: count, v: ok,
     note: "no labels; OTLP trace export requests"}
  - {root: beyla_prometheus_http_requests_total, type: counter, unit: count, v: ok,
     note: "labels: path, port; Prometheus /metrics scrape requests"}
```

---

## Boundary-only traces (OTLP) [slug: beyla-traces]

Beyla emits boundary spans only — no internal business spans. SERVER span at the service entry
point, CLIENT spans per outbound hop, and INTERNAL children `"in queue"` + `"processing"` when
queue time is non-zero (`tracesgen.go:211-236`). Semconv v1.38.0 (`tracesgen.go:23`).

W3C tracecontext: when an SDK upstream propagates a trace context, Beyla's boundary span keeps the
same TraceID (unified trace); otherwise Beyla mints a new TraceID. In `coexist_sdk` synthkit does
NOT emit Beyla traces — the SDK lane already carries the full trace. In `ebpf_only`, the Beyla lane
replaces the workload's existing `source="tempo"` trace projection.

```yaml signals
family: beyla_traces
scope: blueprint
sink: otlp
resource_attributes:
  service.name: <service-name>
  service.namespace: <namespace>
  service.version: <version>
  k8s.namespace.name: <k8s-namespace>   # v: assumed (OBI dotted form)
  k8s.pod.name: <pod-name>
  k8s.deployment.name: <service-name>
  k8s.node.name: <node-name>
  k8s.cluster.name: <cluster-name>
  telemetry.distro.name: opentelemetry-ebpf-instrumentation   # v: ok (attrs.go:121-124)
span_attributes:
  # HTTP server (SERVER span kind):
  http.request.method: GET|POST|PUT|DELETE|…
  http.response.status_code: <code>
  http.route: <route>
  server.address: <host>
  server.port: <port>
  url.path: <path>
  # HTTP client (CLIENT span kind):
  http.request.method: <method>
  server.address: <target-host>
  server.port: <target-port>
  # DB client:
  db.operation.name: <op>
  db.system.name: <system>
  server.address: <db-host>
  # gen_ai client: see signals/genai.md [slug: genai-spans]
  # span.metrics.skip: true  # stamped by Beyla to prevent double-count (tracesgen.go:336,1265)
correlation_fields:
  - trace_id    # W3C tracecontext — adopted from upstream SDK if present
  - span_id
  - parent_span_id
```

---

## DEFERRED / documented-only (NOT emitted in v1 synthkit)

These families are **documented here for future reference** but are NOT emitted by any synthkit
construct or lane in v1. They are marked `v: assumed` where names have not been live-captured.

### Auto-inject webhook metrics (`beyla_sdk_injection_*`)

Beyla's SDK auto-injection webhook is flagged by the Beyla team as **"purely experimental, may be
removed"** and overlaps with the OTel Operator. Documented-only.

```yaml signals
family: beyla_sdk_injection
scope: substrate
sink: promrw
labels:
  k8s_namespace_name: <namespace>    # v: assumed
  k8s_workload_kind: <kind>          # v: assumed
  k8s_workload_name: <name>          # v: assumed
  language: <language>               # v: assumed
  outcome: <outcome>                 # v: assumed
metrics:
  - {root: beyla_sdk_injection_requests_total, type: counter, unit: count, v: assumed,
     note: "NOT emitted v1; auto-inject webhook; experimental, may be removed; pkg/webhook/metrics.go:18-25"}
  - {root: beyla_sdk_injection_restarts_total, type: counter, unit: count, v: assumed,
     note: "NOT emitted v1; auto-inject webhook; experimental"}
```

### Messaging RED (deferred)

Beyla can observe messaging systems (Kafka consumer/producer) via eBPF. The metric names follow
semconv messaging conventions but are not yet verified against the Beyla source. Deferred to a
future capture pass.

### GPU RED (deferred)

Beyla's GPU observation feature (CUDA/ROCm eBPF probes) is in experimental status. Metric names
are not yet sourced. Deferred.
