---
title: Signal Areas
description: Index of every synthkit signal area — scope, sinks, and links to the source catalogue file for each construct family.
---

# Signal Areas

This page maps every signal area synthkit can emit. Each entry links to the authoritative [`signals/<area>.md`](https://github.com/rknightion/synthkit/blob/main/signals/) source file. For how to read the `yaml signals` blocks, the verification legend, and the scope/sink concepts, see [Reading the Catalogue](signals.md). For the constructs that emit these signals, see [Constructs](constructs.md). For the blueprint declarations that gate which constructs are active, see [Emission Switches](emission-switches.md).

The catalogue is organised into groups below. The **Scope** column is `blueprint` (carries a `blueprint=` selector label) or `substrate` (disambiguated by declared identity — no blueprint label). The **Sink(s)** column uses the short identifiers: `promrw` = Prometheus Remote-Write v2 → Mimir; `otlp` = hand-encoded OTLP → Tempo; `loki` = Loki push; `faro` = Faro collector; `pyroscope` = Pyroscope push.

---

## AWS / CloudWatch

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/cw.md`](https://github.com/rknightion/synthkit/blob/main/signals/cw.md) | AWS CloudWatch metric-stream families: ALB, NLB, EC2, EBS, NAT Gateway, S3, EKS control-plane, Firehose, RDS, ElastiCache, MWAA, DocumentDB, Neptune, OpenSearch Serverless, Glue, PrivateLink. 5-stat gauge expansion (`_sum/_average/_maximum/_minimum/_sample_count`) per metric. The CW naming convention (`aws_<namespace>_<metric>_<stat>`) is stated here as LAW and referenced everywhere else. | blueprint | promrw |

!!! note "CloudWatch naming convention"
    Every `_sum`-suffixed series is a **per-period gauge**, not a monotonic counter. Never apply `rate()` or `increase()` to any `_sum` series — use the raw value or `delta()`. There are no `_bucket` series on any CW family.

---

## Kubernetes + add-ons

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/k8s.md`](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md) | Kubernetes monitoring substrate: KSM (`kube_*`), node-exporter (`node_*`), cAdvisor (`container_*`), kubelet (`kubelet_*`), conformance/discovery, Kubernetes Events log stream, and add-on pod correlation labels. The `k8s-addon-pod-correlation` family joins add-on pod identity into the k8s series via shared `fixture.PodIP`. | substrate | promrw (+ loki for events) |
| [`signals/k8s-addons.md`](https://github.com/rknightion/synthkit/blob/main/signals/k8s-addons.md) | k8s controller add-ons: AWS Load Balancer Controller, ExternalDNS, CoreDNS, VPC CNI, cert-manager, cluster-autoscaler, EBS-CSI driver, KSM ingress extension, Karpenter, ArgoCD, Envoy Gateway. Each emits its own Prometheus metrics families; all substrate-scoped, disambiguated by `cluster` + `k8s_cluster_name`. | substrate | promrw |

---

## Databases / dbo11y

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/dbo11y.md`](https://github.com/rknightion/synthkit/blob/main/signals/dbo11y.md) | Grafana Cloud Database Observability — MySQL (`dbo11y_mysql_*`) and Postgres (`dbo11y_pg_*`) metric families plus their matching structured log streams. Substrate-scoped; disambiguated by `instance` (connection URI), `server_id` (64-hex SHA-256), and `db_instance_identifier`. Live-validated against the Grafana Cloud Database Observability product stack. Two independently-gated lanes; both default off; cross-signal join key is the DB `Name`, which also links to the CSP hosting metrics and trace db-leaf spans. | substrate | promrw + loki |

!!! note "Emission switch"
    A `databases:` entry in a blueprint can gate the CloudWatch RDS lane (`cloudwatch: true/false`) and the dbo11y lane (`dbo11y: true/false`) independently. Both default to their own values; neither forces the other on.

---

## Cloud providers (CSP)

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/cspazure.md`](https://github.com/rknightion/synthkit/blob/main/signals/cspazure.md) | Azure Monitor metrics via two paths: the GC managed serverless scraper and `azure_exporter`. Covers: Compute (VMs), SQL Database, Postgres Flexible Server, Storage Accounts, Load Balancer, Application Gateway, Front Door, VNet, Event Hubs, Service Bus. Label shapes differ between the two scraper paths (resourceID casing, resourceName join form). Substrate-scoped; disambiguated by `subscriptionID` + `resourceID`. | substrate | promrw |
| [`signals/cspgcp.md`](https://github.com/rknightion/synthkit/blob/main/signals/cspgcp.md) | GCP Cloud Monitoring families via Alloy `prometheus.exporter.gcp` (embeds `stackdriver_exporter`). Covers: Compute Engine, Cloud SQL, AlloyDB, Cloud Storage, Networking, Load Balancing, Pub/Sub, Cloud Run, Bigtable. Substrate-scoped; disambiguated by `project_id` + `database_id` / resource identity. | substrate | promrw |
| [`signals/cloudflare.md`](https://github.com/rknightion/synthkit/blob/main/signals/cloudflare.md) | Cloudflare zone analytics (`cloudflare_zone_*`) and tunnel metrics (`cloudflare_tunnel_*`). Unlike the Azure/GCP CSP families, Cloudflare is blueprint-scoped and carries the `blueprint` selector label. | blueprint | promrw |

---

## APM / Traces / Logs / RUM

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/apm.md`](https://github.com/rknightion/synthkit/blob/main/signals/apm.md) | APM span-metrics and service-graph families derived by Tempo's metrics-generator: `traces_spanmetrics_calls_total`, `traces_spanmetrics_latency_*`, `traces_spanmetrics_size_*`, `traces_service_graph_request_total`, `traces_service_graph_request_duration_seconds_*`, `traces_target_info`. Blueprint-scoped; uses `deployment_environment_name` (the OTel semconv `_name` form). | blueprint | promrw |
| [`signals/traces.md`](https://github.com/rknightion/synthkit/blob/main/signals/traces.md) | Distributed traces — one connected span tree per request: browser ROOT → server CLIENT → db/cache leaf spans, plus typed AI hops (`gateway`/`model`/`agent`/`tool`/`workflow`/`retrieval`). Hand-encoded multi-Resource `ResourceSpans` protobuf delivered via OTLP. Span attributes, resource attributes, and the correlation key-set (`app.correlation_id`, `trace_id`, `span_id`) are defined here. Blueprint-scoped (carried by `web_service` workload). | blueprint | otlp |
| [`signals/logs.md`](https://github.com/rknightion/synthkit/blob/main/signals/logs.md) | All log emission: blueprint-scoped application logs, Faro/RUM frontend beacons (via Faro collector), user-action events, browser spans (OTLP), k8s-addon log streams (substrate), derived cloud log streams, and SM probe logs. Covers both blueprint-scoped (`logs-app`, `logs-faro-rum`, `logs-user-actions`, `logs-browser-spans`) and substrate-scoped (`logs-k8s-addons`, `logs-derived-cloud`) families. | blueprint + substrate | loki + faro + otlp |

---

## AI & LLM

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/genai.md`](https://github.com/rknightion/synthkit/blob/main/signals/genai.md) | OTel gen_ai semantic-convention span attributes and `gen_ai_client_*` / `gen_ai_server_*` metric families. Workload-emitted (the `web_service` AI hops); vocabulary lives in `internal/genai`. Two core client metrics (`gen_ai_client_token_usage`, `gen_ai_client_operation_duration_seconds`) are live-confirmed; streaming families are `v: assumed`. Labels use `gen_ai_provider_name` (current OTel spelling; replaces the legacy `gen_ai_system`). | blueprint | promrw + otlp |
| [`signals/genai-models.md`](https://github.com/rknightion/synthkit/blob/main/signals/genai-models.md) | The per-model LLM catalogue — model IDs, families, cost per 1M input/output tokens, and volume weights for all four platforms: AWS Bedrock, Azure OpenAI, OpenAI, Vertex AI. This is a reference table (mirror of `internal/genai/models.go`), not an emitted signal family. No scope or sink. | — | — |
| [`signals/portkey.md`](https://github.com/rknightion/synthkit/blob/main/signals/portkey.md) | Portkey LLM gateway telemetry: the gateway pods' `/metrics` scrape (`portkey_*` + `node_*`), the Portkey Analytics API poll (`portkey_api_*` gauges), the export log stream, and derived recording rules. Two constructs share this contract (`portkey_gateway` and `portkey_poller`). Substrate-scoped; disambiguated by gateway instance identity. | substrate | promrw + loki |
| [`signals/bedrock.md`](https://github.com/rknightion/synthkit/blob/main/signals/bedrock.md) | AWS Bedrock CloudWatch metric-stream families: `AWS/Bedrock` (model invocations, latency, token usage, guardrail evaluations), `AWS/Bedrock/Agents` (agent step execution), `AWS/Bedrock/Guardrails` (guardrail evaluations). Plus the Bedrock invocation JSON log stream. Blueprint-scoped; uses the standard CW 5-stat gauge expansion. | blueprint | promrw + loki |
| [`signals/agentcore.md`](https://github.com/rknightion/synthkit/blob/main/signals/agentcore.md) | AWS Bedrock AgentCore CloudWatch families: `AWS/Bedrock-AgentCore` invocation-class metrics and resource-usage metrics. Plus the AgentCore JSON log stream. Blueprint-scoped; same CW naming convention as other AWS namespaces. | blueprint | promrw + loki |
| [`signals/langsmith.md`](https://github.com/rknightion/synthkit/blob/main/signals/langsmith.md) | LangSmith platform telemetry: self-hosted LangSmith services' `/metrics` scrape (standard process/Python/ClickHouse/Redis/Postgres/nginx exporters), the LangSmith feedback API poll (`langsmith_eval_*` quality gauges), and the LangSmith runs log stream. Substrate-scoped; two constructs (`langsmith_platform`, `langsmith_eval`). | substrate | promrw + loki |
| [`signals/snowflake.md`](https://github.com/rknightion/synthkit/blob/main/signals/snowflake.md) | Snowflake account telemetry via Alloy `prometheus.exporter.snowflake` — 27 gauge families from `ACCOUNT_USAGE` views. Not a CloudWatch source. Substrate-scoped; disambiguated by Snowflake account identity. | substrate | promrw |

---

## Network topology

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/nettopo.md`](https://github.com/rknightion/synthkit/blob/main/signals/nettopo.md) | SNMP-based network topology exporter (`github.com/colinedwardwood/network-topology-exporter`): device inventory (`nettopo_device_info`), reconciled topology edges, edge-change/conflict events, discovery health, freshness gauges, exporter self-observability. Gated sub-families: session-pool metrics, federation hub/spoke metrics, OTLP-push metrics. Plus a structured log stream. Substrate-scoped; disambiguated by device and topology identity labels. The `model` label is absent (no ENTITY-MIB upstream); uptime is bounded to < 497 days. | substrate | promrw + loki |

---

## Profiling

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/profiles.md`](https://github.com/rknightion/synthkit/blob/main/signals/profiles.md) | Grafana Pyroscope continuous profiling — four emitter shapes: eBPF system profiling, pprof-scrape (Go/Python/.NET), Java async-profiler, and SDK push (Go/Python). Profile types, `__profile_type__` selectors, `pyroscope_spy` values, `source` label shapes, and per-runtime label sets. Both blueprint-scoped (SDK-instrumented services) and substrate-scoped (eBPF node-level) families. | blueprint + substrate | pyroscope |

---

## Synthetic Monitoring

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/sm.md`](https://github.com/rknightion/synthkit/blob/main/signals/sm.md) | Grafana Cloud Synthetic Monitoring fake check results — `sm_check_*` metric families and SM probe log streams. Substrate-scoped; disambiguated by check identity (`job`, `instance`, `probe`, `config_version`, `check_name`). Never carries a `blueprint` label. | substrate | promrw + loki |

---

## Fleet Management

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/fm.md`](https://github.com/rknightion/synthkit/blob/main/signals/fm.md) | Grafana Fleet Management + Alloy meta-health + content sentinel. Three families: `fm-fleet` (fake collector-agent inventory metrics, `fleet_*`), `fm-alloy-health` (in-cluster Alloy pod health metrics), `fm-content-sentinel` (`synthkit_content_dropped_total` + `synthkit_content_leak_test` — proves the content strip is running and nothing is leaking). Substrate-scoped; disambiguated by `cluster` + `collector_id`. | substrate | promrw |

---

## Host exporters

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/host.md`](https://github.com/rknightion/synthkit/blob/main/signals/host.md) | Standalone (non-Kubernetes) host telemetry via Grafana Alloy integrations: Linux `node_exporter` (`node_*`), macOS `node_exporter` build (`node_*` mac profile), `windows_exporter` (`windows_*`), Docker `cAdvisor` (`container_*`). Per-OS log streams. Two profiles (`integration` / `full`). Shares `internal/nodeexp` with the k8s node-exporter profile. Substrate-scoped; disambiguated by host identity (`instance`, `job`). | substrate | promrw + loki |

---

## Beyla (eBPF auto-instrumentation)

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/beyla.md`](https://github.com/rknightion/synthkit/blob/main/signals/beyla.md) | Grafana Beyla eBPF auto-instrumentation: RED metrics (`http_server_request_duration_seconds`, `http_server_request_body_size_bytes`, `http_client_*`), network-flow metrics (`beyla_network_flow_bytes_total`), span-metric cross-references, and Beyla internal agent metrics. Two contexts: `ebpf_only` (no SDK) and `coexist_sdk` (Beyla alongside an OTel SDK). Traces delivered via OTLP. Dual-scoped: RED and span-metric families are blueprint-scoped (emitted via the `web_service` Beyla lane); network-flow and internal metrics are substrate-scoped. | blueprint + substrate | promrw + otlp |

---

## Native OTLP metrics

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/otlp-metrics.md`](https://github.com/rknightion/synthkit/blob/main/signals/otlp-metrics.md) | Native OTLP application metrics emitted by the `web_service` workload when `otel: { metrics: true }` is declared. Ships OTel semantic instrument names (`http.server.request.duration`, `http.server.active_requests`) via OTLP/HTTP rather than pre-mangled Prometheus RW2. Documents both the emitted OTLP instruments and the expected post-gateway Prometheus shape (`http_server_request_duration_seconds`, `http_server_active_requests`, `target_info`, `otel_scope_*`) for both naked and `k8s_monitoring` resource modes. Blueprint-scoped. | blueprint | otlp |

---

## Qualification pipeline

| Area file | What it models | Scope | Sink(s) |
|---|---|---|---|
| [`signals/qualification.md`](https://github.com/rknightion/synthkit/blob/main/signals/qualification.md) | GitLab CI qualification and validation pipeline telemetry: the `gitlab_ci_pipeline_*` family from `mvisonneau/gitlab-ci-pipelines-exporter` (exporter-doc verified) plus a coined `qualification_*` suite-signal family for synthkit's own validation pass results. Metric names and label keys confirmed against upstream exporter documentation. Plus a structured Loki log stream. Substrate-scoped; no blueprint label. | substrate | promrw + loki |

---

## Cross-cutting reference

The global rules that apply to every area above — push topology, blueprint selector stamping, scope invariants, environment-label-key variance, the correlation key-set, the cardinality hard limit, the content-strip rule, and the realistic-shape model — live in [`signals/00-canon.md`](https://github.com/rknightion/synthkit/blob/main/signals/00-canon.md). Area files reference these by slug (`[slug: cardinality]`, `[slug: scoping]`, etc.) and never restate them.

For how to read and verify the catalogue, see [Reading the Catalogue](signals.md). For open / unverified items, see [`cantfind.md`](https://github.com/rknightion/synthkit/blob/main/cantfind.md).
