# Native OTLP application metrics (web_service otel:) — gateway-shape contract

Native OTLP/HTTP application metrics emitted by the `web_service` workload when
`otel: { metrics: true }` is declared. Unlike all other synthkit metrics (which ship as
pre-mangled Prometheus Remote-Write v2), this lane ships **OTel semantic instrument names** to
`/v1/metrics` and lets the OTLP gateway own the full normalization pipeline (target_info
promotion, resource-attribute → Prometheus label promotion, unit suffix).

> **This is a documented exception to the pre-mangled-names rule.** The emitted OTLP names are
> correct and must NOT be pre-mangled. All shapes documented here are the expected
> post-gateway Prometheus forms per the OTel → Prom translation spec.

---

## Kubernetes native receiver metrics [slug: k8s-native-otlp-metrics]

The `k8s_cluster` construct adds this surface only when its cluster declaration sets
`otel: {metrics: true}`. These are collector-native semantic names. They are not derived from,
aliases of, or replacements for the construct's `kube_*`, Prometheus `container_*`, `node_*`, or
other scrape-shaped remote-write families.

**Provenance and date:** all 55 family names and attribute keys below were observed on the OTLP
wire in the `otel-receivers` k3d permutation on 2026-08-27, recorded in
`reality-corpus/k8s/k3d-lab-otel-receivers.json` and `signals/k8s.md`
`[slug: k8s-otel-native-permutation]`. The lab used the
`open-telemetry/opentelemetry-collector` chart at `0.171.0`, running `grafana/alloy:v1.18.0`
as `bin/otelcol`.
Instrument wire kinds and units were checked on 2026-08-28 against the corresponding current
OpenTelemetry Collector Contrib `kubeletstatsreceiver/metadata.yaml` and
`k8sclusterreceiver/metadata.yaml`. The generated builders set scopes to
`github.com/open-telemetry/opentelemetry-collector-contrib/receiver/kubeletstatsreceiver` and
`.../receiver/k8sclusterreceiver`, with the collector build version.

### `kubeletstatsreceiver` default-enabled surface (37 families)

Only the default `container`, `pod`, and `node` groups are modelled. Volume metrics and the
opt-in `*.node.utilization` families were not observed in this permutation and therefore are not
emitted by this lane; that absence does not narrow the receiver's configurable contract.

| Families | OTLP instrument | Unit |
|---|---|---|
| `container.cpu.time` | monotonic cumulative Sum | `s` |
| `container.cpu.usage` | Gauge | `{cpu}` |
| `container.filesystem.available`, `container.filesystem.capacity`, `container.filesystem.usage` | Gauge | `By` |
| `container.memory.available`, `container.memory.rss`, `container.memory.usage`, `container.memory.working_set` | Gauge | `By` |
| `container.memory.major_page_faults`, `container.memory.page_faults` | Gauge | `1` |
| `k8s.pod.cpu.time` | monotonic cumulative Sum | `s` |
| `k8s.pod.cpu.usage` | Gauge | `{cpu}` |
| `k8s.pod.filesystem.available`, `k8s.pod.filesystem.capacity`, `k8s.pod.filesystem.usage` | Gauge | `By` |
| `k8s.pod.memory.available`, `k8s.pod.memory.rss`, `k8s.pod.memory.usage`, `k8s.pod.memory.working_set` | Gauge | `By` |
| `k8s.pod.memory.major_page_faults`, `k8s.pod.memory.page_faults` | Gauge | `1` |
| `k8s.pod.network.io` | monotonic cumulative Sum | `By` |
| `k8s.pod.network.errors` | monotonic cumulative Sum | `{error}` |
| `k8s.node.cpu.time` | monotonic cumulative Sum | `s` |
| `k8s.node.cpu.usage` | Gauge | `{cpu}` |
| `k8s.node.filesystem.available`, `k8s.node.filesystem.capacity`, `k8s.node.filesystem.usage` | Gauge | `By` |
| `k8s.node.memory.available`, `k8s.node.memory.rss`, `k8s.node.memory.usage`, `k8s.node.memory.working_set` | Gauge | `By` |
| `k8s.node.memory.major_page_faults`, `k8s.node.memory.page_faults` | Gauge | `1` |
| `k8s.node.network.io` | monotonic cumulative Sum | `By` |
| `k8s.node.network.errors` | monotonic cumulative Sum | `{error}` |

Container and pod resources carry the observed pod identity keys:
`k8s.cluster.name`, `k8s.cluster.uid`, `k8s.namespace.name`, `k8s.node.name`, `k8s.pod.name`,
`k8s.pod.uid`, `k8s.pod.start_time`, `k8s.container.name`, the applicable observed owner pair
(`k8s.deployment.name`, `k8s.replicaset.name`/`.uid`, or `k8s.daemonset.name`/`.uid`),
`container.image.name`, `container.image.tag`, `service.name`, `service.namespace`,
`service.instance.id`, `service.version`, `host.name`, and `os.type`. Node resources carry only
`k8s.cluster.name`, `k8s.node.name`, `host.name`, and `os.type`. Network points add exactly
`interface` and `direction`, with `direction` in `{receive, transmit}`.

### `k8sclusterreceiver` observed surface (18 families)

The permutation explicitly enabled `k8s.container.status.reason`; its closed nine-value reason
enum is documented in `signals/k8s.md` `[slug: k8s-otel-native-permutation]`.

| Families | OTLP instrument | Unit | Attributes beyond `k8s.cluster.name` |
|---|---|---|---|
| `k8s.container.cpu_limit`, `k8s.container.cpu_request` | Gauge | `{cpu}` | full observed pod identity plus `container.id` |
| `k8s.container.memory_limit`, `k8s.container.memory_request` | Gauge | `By` | full observed pod identity plus `container.id` |
| `k8s.container.ready` | Gauge | empty | full observed pod identity plus `container.id` |
| `k8s.container.restarts` | Gauge | `{restart}` | full observed pod identity plus `container.id` |
| `k8s.container.status.reason` | non-monotonic cumulative Sum | `{container}` | full observed pod identity plus `container.id` and `k8s.container.status.reason` |
| `k8s.pod.phase` | Gauge | empty | full observed pod identity |
| `k8s.deployment.available`, `k8s.deployment.desired` | Gauge | `{pod}` | `k8s.deployment.name`, `k8s.deployment.uid`, `k8s.namespace.name`, `service.namespace` |
| `k8s.replicaset.available`, `k8s.replicaset.desired` | Gauge | `{pod}` | `k8s.replicaset.name`, `k8s.replicaset.uid`, `k8s.namespace.name`, `service.namespace` |
| `k8s.daemonset.current_scheduled_nodes`, `k8s.daemonset.desired_scheduled_nodes`, `k8s.daemonset.misscheduled_nodes`, `k8s.daemonset.ready_nodes` | Gauge | `{node}` | `k8s.daemonset.name`, `k8s.daemonset.uid`, `k8s.namespace.name`, `service.namespace` |
| `k8s.namespace.phase` | Gauge | empty | `k8s.namespace.name`, `k8s.namespace.uid`, `service.namespace` |
| `k8s.node.condition_ready` | Gauge | empty | `k8s.node.name`, `k8s.node.uid`, `host.name` |

The reference composition is `blueprints/k8s-otel-native.yaml`. It keeps the existing
Prometheus-shaped cluster lane and adds these native OTLP resources; switching `otel.metrics` off
or omitting it leaves the established promrw family and label-key surface unchanged.

---

## Standalone host native receiver metrics [slug: host-native-otlp-metrics]

The `host` construct adds this hostmetricsreceiver-shaped surface only when a host declaration
sets `otel: {metrics: true}`. These are semantic OTel names, not renamed `node_*` or
`windows_*` Prometheus families. The existing Prometheus exporter lane remains enabled alongside
the native lane.

**Provenance/date for every family in the table:** observed on the OTLP wire at collector egress
in the k3d `otel-receivers` permutation on **2026-08-27**. The deployment used the
`open-telemetry/opentelemetry-collector` Helm chart at `0.171.0`; its collector process was the
`bin/otelcol` binary in the `grafana/alloy:v1.18.0` image. The family set and observed attributes are recorded in
`reality-corpus/host/k3d-lab-otel-receivers.json` and `signals/host.md` (`host-otel-native-permutation`).
The instrument shape, unit, and monotonicity annotations below are sourced from the current
hostmetricsreceiver metadata for the same receiver. The generated scope is
`github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver`, version
`0.171.0`.

| Family | OTLP instrument | Unit | Datapoint attributes |
|---|---|---|---|
| `system.cpu.load_average.1m`, `.5m`, `.15m` | Gauge | `{thread}` | — |
| `system.cpu.logical.count` | non-monotonic cumulative Sum | `{cpu}` | — |
| `system.cpu.time` | monotonic cumulative Sum | `s` | `cpu`, `state` |
| `system.disk.io` | monotonic cumulative Sum | `By` | `device`, `direction` = `read` or `write` |
| `system.disk.io_time` | monotonic cumulative Sum | `s` | `device` |
| `system.disk.merged` | monotonic cumulative Sum | `{operations}` | `device`, `direction` = `read` or `write` |
| `system.disk.operation_time` | monotonic cumulative Sum | `s` | `device`, `direction` = `read` or `write` |
| `system.disk.operations` | monotonic cumulative Sum | `{operations}` | `device`, `direction` = `read` or `write` |
| `system.disk.pending_operations` | non-monotonic cumulative Sum | `{operations}` | `device` |
| `system.disk.weighted_io_time` | monotonic cumulative Sum | `s` | `device` |
| `system.memory.limit` | non-monotonic cumulative Sum | `By` | — |
| `system.memory.usage` | non-monotonic cumulative Sum | `By` | `state` = `buffered`, `cached`, `free`, `slab_reclaimable`, `slab_unreclaimable`, or `used` |
| `system.network.connections` | non-monotonic cumulative Sum | `{connections}` | `protocol` = `tcp`, `state` = the 12 TCP states in `signals/host.md` |
| `system.network.dropped` | monotonic cumulative Sum | `{packets}` | `device`, `direction` = `receive` or `transmit` |
| `system.network.errors` | monotonic cumulative Sum | `{errors}` | `device`, `direction` = `receive` or `transmit` |
| `system.network.io` | monotonic cumulative Sum | `By` | `device`, `direction` = `receive` or `transmit` |
| `system.network.packets` | monotonic cumulative Sum | `{packets}` | `device`, `direction` = `receive` or `transmit` |

The `state` attribute on `system.cpu.time` is platform-specific: Linux emits
`user`, `system`, `idle`, `interrupt`, `nice`, `softirq`, `steal`, and `wait`; Windows and macOS
emit only `user`, `system`, `idle`, and `interrupt`. A standalone host resource carries only
`host.name` and `os.type`; Kubernetes enrichment attributes from the capture permutation are not
fabricated for this top-level host construct.

`system.filesystem.usage` is intentionally not emitted. The authoritative permutation enabled the
filesystem scraper but produced no datapoints because all k3d mounts were excluded; this is an
environmental capture absence, not a claim that the receiver can never produce filesystem data.

The reference `blueprints/hostfleet.yaml` enables the native lane on one Linux, one macOS, and one
Windows host so the platform-specific CPU state contract is exercised without changing any host's
Prometheus exporter configuration.

---

## App and AI-agent native metrics [slug: workload-native-otlp-metrics]

The `app` and `ai_agent` workloads add native OTLP metrics only when their workload declaration
sets `otel: {metrics: true}`. Their existing Prometheus Remote-Write lanes remain enabled and are
unchanged when the switch is absent or false.

For `app`, the native family set is the workload's own inline `services[].metrics[]` DSL surface,
with the declared name, instrument kind, unit, bounds, and dimension keys unchanged. Catalog
profiles are Prometheus-oriented and are not re-spelled into the OTLP envelope. Provenance is the
blueprint's SDK-instrument declaration itself, dated **2026-08-28**. The exercised reference in
`blueprints/profiling-demo.yaml` declares:

| Family | OTLP instrument | Unit | Datapoint attributes |
|---|---|---|---|
| `app_queue_depth` | Gauge | `{item}` | — |

For `ai_agent`, the two families below are the semantic-convention instruments whose documented
OTLP-to-Prometheus translation is already reconciled in `signals/genai.md`: dots become underscores,
the seconds unit adds `_seconds`, and annotation units do not add a suffix. Provenance: live capture
and OpenTelemetry semantic-convention reconciliation recorded in `signals/genai.md`, **2026-06-22**;
native emission added **2026-08-28**.

| Family | OTLP instrument | Unit | Datapoint attributes |
|---|---|---|---|
| `gen_ai.client.token.usage` | cumulative Histogram | `{token}` | `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `server.address`, `server.port`, `gen_ai.token.type` |
| `gen_ai.client.operation.duration` | cumulative Histogram | `s` | `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `server.address`, `server.port`; `error.type` when the operation ends in error |

`blueprints/otlp-native.yaml` enables the AI-agent lane; the workload's existing activity settings
ensure both histograms receive observations in one-shot inventory runs.

---

## Emitter configuration

| YAML field | Values | Effect |
|---|---|---|
| `otel.metrics` | `true` / `false` (default false) | Gates OTLP-metrics emission entirely |
| `otel.mode` | `naked` (default) / `k8s_monitoring` | Resource-attribute shape (see below) |

**naked** — SDK-default resource attrs only (app → OTLP gateway direct).  
**k8s_monitoring** — adds k8sattributes + resourcedetection-enriched attrs (app → in-cluster
Alloy → OTLP gateway pipeline, the production path when
`k8s_monitoring.features.application_observability: true`).

---

## Emitted OTLP metrics [slug: otlp-metrics-emitted]

*Provenance: v: ok — captured on a live Grafana Cloud stack 2026-06-18 (gateway-owned naming)*

Two instruments per workload instance per tick (60 s interval):

### `http.server.request.duration` — explicit-bound histogram [slug: otlp-duration]

Cumulative histogram (DELTA not used; cumulative since cold-start). One series per
`(http.request.method, http.route, http.response.status_code)` triple.

**Datapoint attributes (per series):**

| Attribute | Example values |
|---|---|
| `http.request.method` | `GET`, `POST` |
| `http.route` | `/api/v1/health`, `/api/v1/process` |
| `http.response.status_code` | `200`, `500` |

**Explicit bounds (seconds):** `[0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10]`  
plus implicit `+Inf` bucket.

Unit: `s`

```yaml signals
family: http_server_request_duration_seconds
scope: blueprint
sink: otlp
labels:
  http_request_method: GET|POST|…
  http_route: /api/v1/…
  http_response_status_code: "200"|"500"
  service_name: <workload-name>
  service_namespace: <k8s-namespace>
  service_version: "1.0.0"
  deployment_environment_name: <env>        # gateway-promoted from resource attr (synthkit _name form; 2026-06-23)
  job: "<k8s-namespace>/<workload-name>"   # gateway-promoted from service.namespace/service.name
  instance: <pod-name>                      # gateway-promoted from service.instance.id
  # k8s_monitoring mode also promotes:
  k8s_namespace_name: <k8s-namespace>
  k8s_pod_name: <pod-name>
  k8s_deployment_name: <workload-name>
  k8s_cluster_name: <cluster-name>
  k8s_node_name: <node-name>
metrics:
  - {root: http_server_request_duration_seconds, type: histogram, unit: seconds, v: ok (2026-06-18),
     note: "explicit-bound cumulative histogram; 14 + implicit +Inf bucket; gateway appends _bucket/_sum/_count"}
```

### `http.server.active_requests` — UpDownCounter (non-monotonic Sum) [slug: otlp-active-requests]

Instantaneous in-flight request count (derived via Little's Law: rps × service time). One
series per distinct `http.request.method` value drawn from the declared endpoints.

**Datapoint attributes:**

| Attribute | Example values |
|---|---|
| `http.request.method` | `GET`, `POST` |
| `url.scheme` | `https` |

Unit: `{request}`

```yaml signals
family: http_server_active_requests
scope: blueprint
sink: otlp
labels:
  http_request_method: GET|POST|…
  url_scheme: https
  service_name: <workload-name>
  service_namespace: <k8s-namespace>
  service_version: "1.0.0"
  deployment_environment_name: <env>        # gateway-promoted from resource attr (2026-06-23)
  job: "<k8s-namespace>/<workload-name>"
  instance: <pod-name>
  # k8s_monitoring mode also promotes k8s_* labels (same as duration above)
metrics:
  - {root: http_server_active_requests, type: gauge, unit: "{request}", v: ok (2026-06-18),
     note: "UpDownCounter → Prometheus gauge after gateway normalization; non-monotonic cumulative Sum"}
```

---

## Resource attributes per mode [slug: otlp-resource-attrs]

*Provenance: v: ok — captured on a live Grafana Cloud stack 2026-06-18*

These are the OTLP **resource attributes** on the emitted ResourceMetrics. The gateway
promotes them onto every Prometheus series it produces (plus `target_info`).

### naked mode

| Attribute | Value form |
|---|---|
| `service.name` | workload name (e.g. `otlp-api`) |
| `service.namespace` | k8s namespace derived from blueprint |
| `service.version` | `1.0.0` |
| `service.instance.id` | pod name (e.g. `otlp-api-enriched-6c9a537dc6-74b6f`) |
| `deployment.environment.name` | env name (e.g. `prod`) — synthkit-native form only (legacy `deployment.environment` dropped 2026-06-23) |
| `telemetry.sdk.name` | `opentelemetry` |
| `telemetry.sdk.language` | `go` |
| `telemetry.sdk.version` | `1.34.0` |

### k8s_monitoring mode (superset of naked)

All naked attrs PLUS:

| Attribute | Value form |
|---|---|
| `k8s.namespace.name` | k8s namespace |
| `k8s.pod.name` | pod name |
| `k8s.deployment.name` | workload name |
| `k8s.cluster.name` | cluster name |
| `k8s.node.name` | node hostname |
| `host.name` | pod name (resourcedetection(system): in-pod `os.Hostname()` == pod name) |
| `host.arch` | GOARCH of the placed node's instance type — `amd64` (x86) or `arm64` (Graviton); derived via `fixture.LookupInstanceSpec(<node instance_type>).KubeArch()`, matching the node's `kubernetes.io/arch`. Defaults `amd64` when no node placement resolves. |
| `os.type` | `linux` (every modelled EKS node) |

---

## Expected post-gateway Prometheus shape [slug: otlp-gateway-prom]

*Provenance: v: ok — captured on a live Grafana Cloud stack 2026-06-18 (corrected to observed shape)*

The OTLP gateway (Grafana Cloud's OTLP endpoint) normalizes the ResourceMetrics into:

For ordinary application datapoint series (excluding `target_info`), the native gateway promotes
exactly five measured resource attributes: `k8s_deployment_name`, `k8s_namespace_name`, `k8s_pod_name`, `service_name`, and
`service_version`. The Alloy-converted promrw lane keeps these resource attributes on
`target_info`; it does not promote them onto each application datapoint series. Metric names are identical
on both lanes. `target_info` remains present on the native lane and continues to carry the full
resource identity below.

*Provenance: same-source dual-ingest capture, 2026-08-31. The promoted set is enumerated from the
capture rather than inferred from the OTel specification or gateway documentation.*

### `target_info` [slug: otlp-target-info]

One gauge-1 series per resource (service instance), carrying ALL promoted resource attrs as
labels. Expected label set (k8s_monitoring mode):

```
target_info{
  service_name="otlp-api-enriched",
  service_namespace="otlp-api-enriched",
  service_version="1.0.0",
  service_instance_id="otlp-api-enriched-6c9a537dc6-74b6f",
  deployment_environment_name="prod",     # synthkit-native _name form (legacy deployment_environment dropped 2026-06-23)
  telemetry_sdk_name="opentelemetry",
  telemetry_sdk_language="go",
  telemetry_sdk_version="1.34.0",
  k8s_namespace_name="otlp-api-enriched",
  k8s_pod_name="otlp-api-enriched-6c9a537dc6-74b6f",
  k8s_deployment_name="otlp-api-enriched",
  k8s_cluster_name="otlp-native-prod-euw1",
  k8s_node_name="ip-10-0-254-253.eu-west-1.compute.internal",
  host_name="otlp-api-enriched-6c9a537dc6-74b6f",
  host_arch="amd64",
  os_type="linux",
  job="otlp-api-enriched/otlp-api-enriched",
  instance="otlp-api-enriched-6c9a537dc6-74b6f"
} = 1
```

### `http_server_request_duration_seconds_{bucket,count,sum}`

Classic histogram series; dot-separated OTLP names → underscore-separated Prometheus names:
`http.server.request.duration` → `http_server_request_duration_seconds` (unit suffix added by
gateway). Promoted labels on every series:

```
http_server_request_duration_seconds_bucket{
  http_request_method="GET",
  http_route="/api/v1/health",
  http_response_status_code="200",
  service_name="otlp-api-enriched",
  job="otlp-api-enriched/otlp-api-enriched",
  instance="otlp-api-enriched-6c9a537dc6-74b6f",
  le="0.005"|"0.01"|…|"+Inf"
}
```

### `http_server_active_requests`

UpDownCounter → Prometheus gauge (no `_total` suffix; non-monotonic). Label set mirrors
`http_server_request_duration_seconds` but with `url_scheme` instead of `http_route`/`status_code`/`le`:

```
http_server_active_requests{
  http_request_method="GET",
  url_scheme="https",
  service_name="otlp-api-enriched",
  job="otlp-api-enriched/otlp-api-enriched",
  instance="otlp-api-enriched-6c9a537dc6-74b6f"
}
```

### Instrumentation scope labels — NOT added by Grafana Cloud

Observed 2026-06-18: the GC OTLP gateway does **NOT** surface `otel_scope_name`/
`otel_scope_version` as Prometheus labels (the OTel→Prom spec adds them by default, but GC's
gateway has scope-label injection disabled by default). The scope IS sent on the wire
(ScopeMetrics name `…/otelhttp`, version `0.58.0`) but does not become a label — do not rely on
`otel_scope_*` for querying GC-ingested OTLP metrics.

### Instrumentation scope on the wire — what synthkit sends [slug: otlp-scope-on-wire]

*Provenance: emitter contract, recorded 2026-08-27 (SKT-0007.02); gateway behaviour live-validated
2026-06-18 — see the section above.*

The section above records what Grafana Cloud does with scope. This records what synthkit puts on
the wire, so the two are not confused:

| Field | synthkit behaviour |
|---|---|
| `ScopeMetrics` per resource | Exactly **one**. Every metric for one resource shares one scope. |
| `InstrumentationScope.name` | The **real** instrumentation library name of the modelled producer, e.g. `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`. A caller that leaves `otlp.Scope` zero gets the fallback name `synthkit` rather than an unnamed scope. |
| `InstrumentationScope.version` | The real library version, e.g. `0.58.0`. Empty when the caller supplies none — omitted, never `""` filler. |
| `InstrumentationScope.attributes` | **Never emitted.** No modelled producer sets scope attributes, so emitting any would invent telemetry. |
| `ScopeMetrics.schema_url` | **Never emitted.** |

**Consequence for queries and dashboards:** because Grafana Cloud does not surface
`otel_scope_name`/`otel_scope_version` as labels (live-validated 2026-06-18 above), the scope
synthkit sends is *not* queryable on a GC-ingested series. No synthkit-emitted shape, generated
dashboard or alert may key on `otel_scope_*`. Scope is sent because a real SDK sends it, not
because it is retrievable.

**Aggregation temporality:** every synthkit OTLP metric — Sum, explicit histogram and exponential
histogram alike — is **cumulative**. Evidence: the OTLP exporter spec default is `Cumulative`
(`OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`, opentelemetry.io metrics SDK exporter spec,
read 2026-08-27); the reference k8s-monitoring spanmetrics connector leaves
`aggregation_temporality` unset and so inherits Alloy's `CUMULATIVE` default; and Mimir lists
delta OTLP ingestion as an experimental, opt-in feature. Delta is encodable in the sink but no
lane sets it. Whether the gateway accepts, converts or drops a delta point is unverified — see
`cantfind.md` SK-91.

---

## LIVE VALIDATION — VALIDATED (2026-06-18)

Status: **v: ok** — captured against a Grafana Cloud stack on 2026-06-18 with only the
`otlp-native` blueprint running. Both shapes confirmed end-to-end. Observed deltas vs the
spec-derived expectation (the shapes above are corrected to match, per the realism-direction rule):

- **`otel_scope_name`/`otel_scope_version` are NOT added** by GC's OTLP gateway (scope-label
  injection off on this stack).
- **Promotion onto metric SERIES is a SUBSET of `target_info`.** Promoted to every series:
  `service_name`, `service_namespace`, `service_version`, `service_instance_id`, `instance`, `job`,
  `deployment_environment_name` (synthkit emits only the `_name` form; the GC gateway may also
  promote `deployment_environment` from the same resource attr — see `[slug: env-label-keys]`);
  **enriched** additionally promotes
  `k8s_cluster_name`, `k8s_deployment_name`, `k8s_namespace_name`, `k8s_pod_name`. **NOT promoted**
  (live only in `target_info`): `k8s_node_name`, `host_name`, `host_arch`, `os_type`,
  `telemetry_sdk_*`. **naked** series carry NO `k8s_*` at all.
- **Two `target_info` series coexist per service**: one from synthkit's promrw lane (has `cluster`,
  `k8s_*`, no host/os) and one gateway-generated from the OTLP resource (has
  `host_*`/`os_type`/`telemetry_sdk_*`/`instance`; enriched also `k8s_*`). Distinguish OTLP-origin
  by `telemetry_sdk_name`/`host_arch`/`os_type`; promrw-origin by `cluster`.
- **GC Asserts/ADI adds stack-side labels** to the OTLP series (`asserts_env`, `asserts_metric_*`,
  `asserts_request_*`, `asserts_source`, + bare `namespace`/`service`) — not emitted by synthkit.
- Confirmed correct: `_seconds` suffix; `http_server_active_requests` is a plain gauge (no `_total`);
  `http_request_method`/`http_route`/`http_response_status_code`/`le` on the histogram;
  `http_request_method`+`url_scheme` on the gauge; status 200 AND 500 series; the enriched-vs-naked
  `target_info` contrast exactly as designed.
- **Metrics-generator regression: HEALTHY** — the OTLP traces these services emit are processed by
  Tempo's metrics-generator into `traces_spanmetrics_calls_total` (200+500 rows) and
  `traces_service_graph_request_total`, incl. the generator's signature `client="user",
  connection_type="virtual_node"` root edge (which synthkit never emits — proving generator origin).

The procedure below records how this was captured (re-run to re-validate after emitter changes).

The `blueprints/otlp-native.yaml` showcase runs TWO services so the capture can compare the two
gateway shapes directly:
- **`otlp-api-enriched`** (`mode: k8s_monitoring`) — `target_info` MUST carry the `k8s_*` +
  `host_*` + `os_type` labels.
- **`otlp-api-naked`** (`mode: naked`) — `target_info` MUST NOT carry any `k8s_*`/`host_*`/`os_type`
  label (only `service_*`/`deployment_environment_name`/`telemetry_sdk_*` + `job`/`instance`;
  gateway may also add `deployment_environment` from the same resource attr — see `[slug: env-label-keys]`).

`instance`/`service_instance_id`/`k8s_pod_name` are the real cluster-placement pod names (k8s-style,
e.g. `otlp-api-enriched-6c9a537dc6-74b6f`) — filter captures by `service_name`, not by pod.
(Pod/node names are deterministic per cluster identity, so they are stable across runs.)

**Validation checklist (operator step):**

1. Deploy with `blueprints/otlp-native.yaml` pointed at your target stack (ensure `GC_OTLP_ENDPOINT`,
   `GC_OTLP_USER`, `GC_TOKEN` are set in `.env`), all OTHER blueprints disabled.
2. After ~2 ticks (~2 min), query the target stack's Mimir (recipe below) for BOTH services and check:
   - **enriched** `target_info` carries `k8s_namespace_name`, `k8s_pod_name`, `k8s_deployment_name`,
     `k8s_cluster_name`, `k8s_node_name`, `host_name`, `host_arch`, `os_type` + the service/sdk attrs.
   - **naked** `target_info` carries ONLY `service_*`/`deployment_environment_name`/`telemetry_sdk_*`
     + `job`/`instance` — and NONE of the k8s/host/os labels (the key contrast). Note: the GC
     gateway may also add `deployment_environment` from the same resource attr (gateway behaviour).
   - `http_server_request_duration_seconds_{bucket,count,sum}` exists for both — confirm the gateway
     appended `_seconds`, and
     `http_request_method`/`http_route`/`http_response_status_code` labels as expected.
   - `http_server_active_requests` is a gauge (NO `_total` suffix), with `http_request_method` +
     `url_scheme` labels.
   - confirm WHICH resource attrs the gateway PROMOTED onto the series vs left only in
     `target_info` (recorded in the VALIDATED findings above).
3. Note any label-name/suffix/promotion differences vs the shapes documented here. Correct this
   file (and the emitter if needed) to match observed reality — **realism-direction rule**.
4. On any delta, correct the shapes above + bump the capture date in the VALIDATED header.

**Capture command recipe (using `gcx` with your target stack context):**
```bash
# enriched — expect k8s_*/host_*/os_type present
gcx metrics query 'target_info{service_name="otlp-api-enriched"}'
# naked — expect NO k8s_*/host_*/os_type
gcx metrics query 'target_info{service_name="otlp-api-naked"}'
# histograms (both services) + active gauge
gcx metrics query 'http_server_request_duration_seconds_bucket{le="+Inf"}'
gcx metrics query 'http_server_active_requests'
```
