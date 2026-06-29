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
