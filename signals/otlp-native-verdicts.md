# OTel-native metric-form verdicts — per catalog kind

**Read this before adding an OTLP metrics lane to any construct or workload.** It records, for
every kind in the catalog, whether the thing being modelled has a real OTel-native metric form in
the world, or whether it only ever exists as a Prometheus scrape target that Alloy or a cloud
scraper converts.

This file asserts no emitted family and contains no `yaml signals` block. It is a gate on
`signals/otlp-metrics.md`, which is where an OTel-native family's contract is recorded once a lane
is actually built.

Study performed 2026-08-27 for SKT-0007.01. The measured baseline it starts from: of the 45 catalog
packages (42 under `internal/construct/`, 3 under `internal/workload/`), 44 declare `core.Metrics`
(promrw) and exactly one — `web_service` under `otel.metrics: true` — declares `core.OTLPMetrics`.
`k8s_profiling` declares no metrics class at all.

---

## The decision rule

A kind has a **real OTel-native metric form** only when some real producer emits its telemetry as
OTLP metrics under names and shapes that are **not** the Prometheus exposition names. That producer
is either an instrumented process (an OTel SDK, an eBPF agent with an OTLP exporter, a proxy with an
OpenTelemetry stats sink) or a collector receiver that talks to the upstream API directly and builds
OTel metrics from it (`k8sclusterreceiver`, `hostmetricsreceiver`, `awscloudwatchreceiver`).

A kind is **Prometheus-scrape-only** when the sole route to OTLP is
`prometheusreceiver` / `otelcol.receiver.prometheus` wrapping a `/metrics` scrape. That path
preserves the Prometheus names exactly: it is a transport change, not an OTel-native form. Building
an OTLP lane for such a kind would emit a shape no real deployment produces, which
[`AGENTS.md`](../AGENTS.md) forbids outright.

**Never decide this from the construct's name.** Decide it from the producer the construct models —
which is recorded in each kind's `Registration()` `Doc` string and its `signals/<area>.md` family.

> A "has a real OTel-native form" verdict is **not** permission to re-emit the construct's existing
> promrw families over OTLP. For the CloudWatch, Azure, GCP, Kubernetes and host groups the
> OTel-native form is a **different metric namespace**, so an OTLP lane means sourcing a second name
> catalogue with its own provenance — not switching the transport under the names already in
> `signals/`.

---

## Verdicts

Legend: **OTEL-NATIVE** = has a real OTel-native metric form · **SCRAPE-ONLY** = Prometheus scrape
target only, must NOT gain an OTLP metrics lane · **UNRESOLVED** = evidence not obtained, see the
`cantfind.md` SK row.

| Kind | Package | Verdict | Evidence | Date |
|---|---|---|---|---|
| `k8s_cluster` | `construct/k8scluster` | OTEL-NATIVE | `k8sclusterreceiver/metadata.yaml` defines `k8s.pod.phase`, `k8s.deployment.available`, `k8s.container.cpu_limit`, `k8s.statefulset.ready_pods` … as OTel instruments built from the API server, not from KSM text; `kubeletstatsreceiver` supplies the container/pod/node CPU + memory groups (`k8s.container.cpu.node.utilization`, `k8s.pod.memory.node.utilization`, node/pod/container metric groups). Vendor docs via ctx7 `/open-telemetry/opentelemetry-collector-contrib`. | 2026-08-27 |
| `host` | `construct/host` | OTEL-NATIVE | `hostmetricsreceiver` scrapers emit `system.cpu.time` (8 Linux `state` values), `system.memory.usage`, `system.filesystem.usage`, `system.network.io`/`.packets`/`.errors`/`.dropped`/`.connections` — OTel names, not `node_exporter` names. Vendor docs via ctx7 `/open-telemetry/opentelemetry-collector-contrib`. | 2026-08-27 |
| `web_service` | `workload/webservice` | OTEL-NATIVE — **built** | `signals/otlp-metrics.md`, live-validated 2026-06-18: `http.server.request.duration`, `http.server.active_requests`. The only kind declaring `core.OTLPMetrics` today. | 2026-06-18 |
| `app` | `workload/app` | OTEL-NATIVE | The modelled producer is an instrumented service graph. `signals/genai.md` records the `app` LangGraph backend as an **app-emitted OTel** producer captured live 2026-06-16; its custom DSL instruments are SDK instruments, so their OTLP form is the same names the blueprint declares. | 2026-06-16 |
| `ai_agent` | `workload/aiagent` | OTEL-NATIVE | `signals/genai.md` states outright that the `gen_ai_client_*` promrw families are the **OTLP→Prom translation** of gen_ai semconv instruments (`.`→`_`, unit `s`→`_seconds`, annotation units dropped), validated 2026-06-15 against `open-telemetry/semantic-conventions` and live-captured. The OTel-native names are therefore already known. | 2026-06-15 |
| `beyla_agent` | `construct/beylaagent` | OTEL-NATIVE | `grafana/beyla` `configure/internal-metrics-reporter.md`: the internal metrics reporter's `exporter` setting takes `disabled` \| `prometheus` \| `otel`; `otel` ships Beyla's own internal metrics over OTLP via `otel_metrics_export`. The construct currently models only the `prometheus` branch (`/internal/metrics`). Vendor docs via ctx7 `/grafana/beyla`. | 2026-08-27 |
| `envoy_gateway` | `construct/envoygateway` | OTEL-NATIVE | `gateway.envoyproxy.io/docs/tasks/observability/proxy-metric` and `…/gateway-observability`: both the `EnvoyProxy` data plane and the `EnvoyGateway` control plane accept `telemetry.metrics.sinks: [{type: OpenTelemetry, openTelemetry: {host, port, protocol: grpc}}]`, and `telemetry.metrics.prometheus.disable: true` turns the scrape endpoint off entirely — so an OTLP-only deployment is a supported, documented configuration. Vendor docs via ctx7 `/websites/gateway_envoyproxy_io`. | 2026-08-27 |
| `cw_infra` | `construct/cwinfra` | OTEL-NATIVE (different namespace) | `awscloudwatchreceiver/README.md`: "All metrics collected by the receiver follow the CloudWatch Metric Streams OpenTelemetry 1.0.0 format. The metric name is structured as `amazonaws.com/{Namespace}/{MetricName}`", resource attrs `cloud.provider=aws`, `cloud.region`, datapoint attrs `Namespace`/`MetricName`/`Dimensions`. Vendor docs via ctx7 `/open-telemetry/opentelemetry-collector-contrib`. | 2026-08-27 |
| `ec2` | `construct/ec2` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced, `amazonaws.com/AWS/EC2/{MetricName}`. | 2026-08-27 |
| `rds` | `construct/rds` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `docdb` | `construct/docdb` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `neptune` | `construct/neptune` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `elasticache` | `construct/elasticache` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `aoss` | `construct/aoss` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `mwaa` | `construct/mwaa` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `glue` | `construct/glue` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced (namespace `Glue`, no `AWS/` prefix). | 2026-08-27 |
| `bedrock` | `construct/bedrock` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `agentcore` | `construct/agentcore` | OTEL-NATIVE (different namespace) | As `cw_infra` — CloudWatch-sourced. | 2026-08-27 |
| `csp_azure` | `construct/cspazure` | OTEL-NATIVE (different namespace) | `azuremonitorreceiver` collects from Azure Monitor and emits OTel metrics with resource attributes `azuremonitor.subscription`, `azuremonitor.subscription_id`, `azuremonitor.tenant_id`; per-metric dimension overrides are configured per Azure resource type. Vendor docs via ctx7 `/open-telemetry/opentelemetry-collector-contrib`. Emitted name shape not yet captured — SK-88. | 2026-08-27 |
| `csp_gcp` | `construct/cspgcp` | OTEL-NATIVE (different namespace) | `googlecloudmonitoringreceiver/README.md`: collects time series from GCP services via the Monitoring REST API and "convert[s] it into OTel Format Pipeline Data". Vendor docs via ctx7 `/open-telemetry/opentelemetry-collector-contrib`. Emitted name shape not yet captured — SK-88. | 2026-08-27 |
| `alloy_health` | `construct/alloyhealth` | SCRAPE-ONLY | `grafana/alloy` `reference/http/_index.md` + `collect/metamonitoring.md`: Alloy exposes its internal metrics **only** in Prometheus exposition format on `/metrics`; the documented meta-monitoring route is `prometheus.exporter.self` → `prometheus.scrape` → optionally `otelcol.receiver.prometheus` for OTLP conversion. The `otelcol_*` families are therefore Prometheus-named at the source. Vendor docs via ctx7 `/grafana/alloy`. | 2026-08-27 |
| `fleet_management` | `construct/fleetmgmt` | SCRAPE-ONLY | Models an Alloy collector roster's self-metrics; same producer and same Prometheus-only exposition as `alloy_health`. `signals/fm.md` families `fm_fleet`/`fm_alloy_health`/`fm_content_sentinel`. | 2026-08-27 |
| `argocd` | `construct/argocd` | SCRAPE-ONLY | Argo CD controllers expose `controller_runtime_*`/`workqueue_*`/`rest_client_*` families. `sigs.k8s.io/controller-runtime` `pkg/metrics/server` serves one thing: a `/metrics` HTTP endpoint (`defaultMetricsEndpoint = "/metrics"`, `DefaultBindAddress = ":8080"`) backed by a Prometheus registry. There is no OTLP metric export in controller-runtime. Vendor docs via ctx7 `/kubernetes-sigs/controller-runtime`. | 2026-08-27 |
| `cert_manager` | `construct/certmanager` | SCRAPE-ONLY | controller-runtime `/metrics` only — as `argocd`. `signals/k8s-addons.md` family `certmanager`. | 2026-08-27 |
| `cluster_autoscaler` | `construct/clusterautoscaler` | SCRAPE-ONLY | client_golang `/metrics` only — as `argocd`. `signals/k8s-addons.md` family `cluster_autoscaler`. | 2026-08-27 |
| `karpenter` | `construct/karpenter` | SCRAPE-ONLY | `karpenter_*` + `controller_runtime_*` + `go_*`/`process_*` — controller-runtime `/metrics` only, as `argocd`. `signals/k8s-addons.md` family `karpenter`. | 2026-08-27 |
| `load_balancer_controller` | `construct/lbc` | SCRAPE-ONLY | `awslbc_*`/`aws_api_*`/`controller_runtime_*`/`workqueue_*`/`rest_client_*` — controller-runtime `/metrics` only. `signals/k8s-addons.md` family `aws_load_balancer_controller`. | 2026-08-27 |
| `external_dns` | `construct/extdns` | SCRAPE-ONLY | `external_dns_controller_*`/`registry_*`/`source_*`/`build_info` — client_golang `/metrics` only. `signals/k8s-addons.md` family `external_dns`. | 2026-08-27 |
| `ebs_csi` | `construct/ebscsi` | SCRAPE-ONLY | `aws_ebs_csi_*` — client_golang `/metrics` only. `signals/k8s-addons.md` family `aws_ebs_csi`. | 2026-08-27 |
| `vpc_cni` | `construct/vpccni` | SCRAPE-ONLY | `awscni_*` — client_golang `/metrics` only. `signals/k8s-addons.md` family `awscni`. | 2026-08-27 |
| `core_dns` | `construct/coredns` | SCRAPE-ONLY | CoreDNS emits metrics only through its `prometheus` plugin's scrape endpoint; its OpenTelemetry surface is the `trace` plugin (traces), not metrics. `signals/k8s-addons.md` family `coredns`. | 2026-08-27 |
| `etcd` | `construct/etcd` | SCRAPE-ONLY | etcd exposes `/metrics` in Prometheus exposition format; its OpenTelemetry integration is gRPC tracing, not metrics. `signals/k8s-addons.md` family `etcd`. | 2026-08-27 |
| `ksm_ingress` | `construct/ksmingress` | SCRAPE-ONLY | `kube_ingress_*` comes from kube-state-metrics' Prometheus endpoint. `k8sclusterreceiver/metadata.yaml` — the OTel-native equivalent for cluster objects — defines **no** ingress metric at all (its `k8s.service.*` entries cover Services, not Ingresses), so there is no OTel-native form to emit. Vendor docs via ctx7 `/open-telemetry/opentelemetry-collector-contrib`. | 2026-08-27 |
| `dbo11y_mysql` | `construct/dbo11ymysql` | SCRAPE-ONLY | The modelled producer is Grafana's Database Observability Alloy component. Its families (`database_observability_*`, `mysql_global_status_*`, `mysql_perf_schema_*`) are product- and `mysqld_exporter`-coined Prometheus names with no OTel semconv counterpart. `signals/dbo11y.md` family `dbo11y_mysql`, `sink: promrw`. Note: `mysqlreceiver` produces `mysql.*` OTel names — that is a **different product surface** and would be a new construct, not an OTLP lane here. | 2026-08-27 |
| `dbo11y_postgres` | `construct/dbo11ypg` | SCRAPE-ONLY | As `dbo11y_mysql`: `database_observability_*`/`pg_*`/`pg_stat_statements_*` are product- and `postgres_exporter`-coined Prometheus names. `signals/dbo11y.md` family `dbo11y_postgres`. `postgresqlreceiver`'s `postgresql.*` is a different surface. | 2026-08-27 |
| `synthetic_monitoring` | `construct/sm` | SCRAPE-ONLY | Grafana Synthetic Monitoring probes publish `probe_*`/`sm_*` Prometheus series into the stack; no OTLP metric egress is modelled or documented for the probe pipeline. `signals/sm.md` families `sm_checks`/`sm_logs`. Residual uncertainty recorded as SK-90. | 2026-08-27 |
| `cloudflare` | `construct/cloudflare` | SCRAPE-ONLY | The modelled producer is the `lablabs` Cloudflare exporter — a Prometheus exporter polling Cloudflare's GraphQL analytics API. Prometheus exposition is its only output. `signals/cloudflare.md`. | 2026-08-27 |
| `snowflake` | `construct/snowflake` | SCRAPE-ONLY | The modelled producer is `prometheus.exporter.snowflake` (27 gauges derived from `ACCOUNT_USAGE`) — a Prometheus exporter by construction, and collector-contrib has no Snowflake metrics receiver. `signals/snowflake.md`. | 2026-08-27 |
| `langsmith_platform` | `construct/langsmithplatform` | SCRAPE-ONLY | Models a self-hosted LangSmith `/metrics` scrape of standard `process_*`/`python_*`/ClickHouse/redis/pg/nginx exporter families — all Prometheus exposition. `signals/langsmith.md`. | 2026-08-27 |
| `langsmith_eval` | `construct/langsmitheval` | SCRAPE-ONLY | Models an **API poller**: the LangSmith feedback API is polled and projected into `langsmith_eval_*` gauges. The poller is the producer and it is a Prometheus exporter; the upstream API is not a telemetry emitter at all. `signals/langsmith.md`. | 2026-08-27 |
| `portkey_poller` | `construct/portkeypoller` | SCRAPE-ONLY | As `langsmith_eval`: Portkey's Analytics API is polled and projected into `portkey_api_*` windowed-aggregate gauges plus `poller_*` self-telemetry. `signals/portkey.md`. | 2026-08-27 |
| `qualification_pipeline` | `construct/qualificationpipeline` | SCRAPE-ONLY | `gitlab_ci_pipeline_*` comes from the GitLab CI pipelines Prometheus exporter; GitLab's own OpenTelemetry support for CI/CD is trace export, not metrics. The coined `qualification_*` suite is synthkit-side and has no OTel form. `signals/qualification.md`. | 2026-08-27 |
| `network_topology` | `construct/nettopo` | SCRAPE-ONLY | Mirrors a specific external SNMP topology exporter (`~/repos/network-topology-exporter`, the source of truth for this construct) whose only output is a Prometheus `/metrics` endpoint. `signals/nettopo.md`. Contrib's `snmpreceiver` exists but does not produce this exporter's families, so emitting one under this kind would fabricate. | 2026-08-27 |
| `k8s_profiling` | `construct/k8sprofiling` | SCRAPE-ONLY (n/a) | Declares `core.PyroscopeProfiles` and no metrics signal class whatsoever — there is nothing for an OTLP **metrics** lane to carry. Out of scope for SKT-0007 by construction. Measured from `Signals()` 2026-08-27. | 2026-08-27 |
| `portkey_gateway` | `construct/portkeygateway` | **UNRESOLVED** | The construct models a `/metrics` scrape of the Portkey LLM gateway (`portkey_*` custom metrics + a `node_*` runtime subset). Whether Portkey's gateway can also export those same counters as OTLP instruments — and under what names — was not established from vendor documentation. Do not build a lane until SK-84 resolves. | 2026-08-27 |

---

## Counts

| Verdict | Kinds |
|---|---|
| OTEL-NATIVE | 20 (of which 13 are "different namespace": the 11 CloudWatch kinds + `csp_azure` + `csp_gcp`) |
| SCRAPE-ONLY — must NOT gain an OTLP metrics lane | 24 (including `k8s_profiling`, which has no metrics class at all) |
| UNRESOLVED | 1 (`portkey_gateway`) |
| **Total** | **45** |

Of the 20 OTEL-NATIVE kinds, exactly one (`web_service`) is built today.

---

## Priority

Ordered by how much of a normal deployment the kind covers, and by how much of the OTel-side name
catalogue is already known.

1. **`k8s_cluster` — the base blueprint surface.** Every k8s blueprint carries it, including
   `blueprints/k8s-minimal.yaml`, whose whole estate is one cluster with
   `k8s_monitoring.features.cluster_metrics: true`. Nothing else can be emitted OTel-native
   end to end until this exists.
2. **`app` and `ai_agent`.** Highest confidence and lowest risk: `signals/genai.md` already records
   the `gen_ai_client_*` families as pre-mangled OTLP names, so the OTel-side spelling is derivable
   from a file already in the repo rather than from a new capture.
3. **`host`.** Second substrate; `hostmetricsreceiver` `system.*` names are stable and documented.
4. **`beyla_agent` and `envoy_gateway`.** Small, self-contained, each a single documented config
   switch in the real product.
5. **The 11 CloudWatch kinds.** Large and uniform — one shared naming law
   (`amazonaws.com/{Namespace}/{MetricName}`) covers all of them, so they move as one wave or not
   at all.
6. **`csp_azure` and `csp_gcp`.** Lowest priority; both need a name capture before anything can be
   emitted.

Independently of the waves, a guard is owed: an `internal/archtest` assertion that no kind marked
SCRAPE-ONLY above declares `core.OTLPMetrics`. That is what keeps this file load-bearing rather than
advisory.

---

## Open items

Recorded in [`cantfind.md`](../cantfind.md) as SK-84 … SK-90. None of them is asserted as fact here,
and none may be resolved by inference:

- **SK-84** — `portkey_gateway`: is there an OTLP metrics export at all? The one UNRESOLVED verdict.
- **SK-85** — `k8s_cluster`: the `kubeletstatsreceiver` **default-enabled** metric name set.
- **SK-86** — `beyla_agent`: the OTLP instrument names under `internal_metrics.exporter: otel`.
- **SK-87** — `envoy_gateway`: the metric names on the OpenTelemetry sink.
- **SK-88** — `csp_azure` / `csp_gcp`: the emitted metric-name shape of the Azure Monitor and Google
  Cloud Monitoring receivers.
- **SK-89** — CloudWatch group: how an OTLP gateway normalises `amazonaws.com/{Namespace}/{Name}`
  into a queryable Prometheus name.
- **SK-90** — `synthetic_monitoring`: whether the Grafana SM probe pipeline has any OTLP metric
  egress. Verdict recorded as SCRAPE-ONLY on the documented Prometheus-publish path.
