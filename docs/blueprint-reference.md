---
title: Blueprint Schema Reference
description: Complete key-by-key reference for every field a synthkit blueprint may contain, generated from the Go structs.
---

# Blueprint Schema Reference

This reference is generated from the live Go types via `make blueprint-schema` and reflects the schema enforced at load time. Decoding is strict: any key not listed here causes a loud load error. If you hit an unexpected load failure, check that your key spellings match exactly.

!!! info "Regenerating the schema"
    Run `make blueprint-schema` to regenerate [BLUEPRINT-SCHEMA.md](https://github.com/rknightion/synthkit/blob/main/BLUEPRINT-SCHEMA.md) from the source types. The `TestSchemaCurrent` gate fails if the committed file drifts from the live types.

---

## Top-level blueprint document

| Key | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Unique blueprint identifier; also the determinism seed root. |
| `label` | string | yes | Selector value stamped on every blueprint-scoped series. Defaults to `name`. |
| `metadata` | object | no | Human-facing annotation. UI-only; never affects emission. |
| `metadata.description` | string | no | Free-text summary shown in the UI. |
| `metadata.tags[]` | string | no | Free-form labels for filtering/grouping. |
| `metadata.owner` | string | no | Owning team or person. |
| `metadata.links` | map[string]string | no | Named external references (`name → url`). |
| `metadata.category` | string | no | Single classification (e.g. `demo`/`reference`/`customer`). |
| `shape` | string | no | Default shape profile for the whole blueprint. |
| `timezone` | string | no | Business-hours anchor. Default `Europe/Zurich`. Mutually exclusive with `regions`. |
| `regions[]` | object | no | Follow-the-sun multi-timezone composite. Mutually exclusive with `timezone`. |
| `regions[].name` | string | — | Region identifier. |
| `regions[].timezone` | string | — | IANA timezone string. |
| `regions[].weight` | float | — | Relative traffic weight for this region. |
| `series_budget` | int | no | Per-blueprint series cap. |
| `environments[]` | object | yes | One entry per deployment environment. |
| `workloads[]` | object | no | Application workloads (request traffic). |
| `features` | map[string]config | no | Grafana Cloud product declarations (`synthetic_monitoring`, `fleet_management`). |
| `integrations` | map[string]config | no | External-source declarations (`cloudflare`, `csp_azure`, `csp_gcp`, etc.). |
| `incidents[]` | object | no | Scheduled or interval-recurring failure activations. |
| `scenarios[]` | object | no | Named, reusable failure bundles composed of effects. |
| `hosts[]` | object | no | Traditional non-Kubernetes machines. |

---

## environments[]

| Key | Type | Required | Description |
|---|---|---|---|
| `environments[].name` | string | yes | Environment name (e.g. `prod`, `staging`). |
| `environments[].weight` | float | no | Relative traffic volume. Default `1.0`. |
| `environments[].production` | bool | no | Keeps weekend traffic at full level. Default: `true` when `name == "prod"`. |
| `environments[].metadata` | object | no | Human-facing annotation (same sub-keys as top-level `metadata`). |
| `environments[].cloud` | object | no | AWS cloud account and service config. |
| `environments[].cluster` | object | no | Kubernetes cluster config. |
| `environments[].databases[]` | object | no | Database instances. |
| `environments[].caches[]` | object | no | Cache instances. |

### environments[].cloud

| Key | Type | Required | Description |
|---|---|---|---|
| `cloud.provider` | string | yes | `"aws"` (v1). |
| `cloud.account_id` | string | yes | AWS account ID (string to preserve leading zeros). |
| `cloud.region` | string | yes | AWS region (e.g. `us-east-1`). |
| `cloud.vpc_id` | string | yes | VPC identifier. |
| `cloud.nat_gateways` | int | no | Number of NAT Gateway instances to emit `aws_natgateway_*` for. |
| `cloud.cloudwatch` | object | no | `cw_infra` sub-family toggles. See [cw_infra config](#cw_infra-config) below. |
| `cloud.aoss` | object | no | Amazon OpenSearch Serverless config. Absent ⇒ not emitted. See [aoss config](#aoss-config). |
| `cloud.mwaa` | object | no | Amazon Managed Workflows for Apache Airflow config. Absent ⇒ not emitted. See [mwaa config](#mwaa-config). |
| `cloud.glue` | object | no | AWS Glue ETL config. Absent ⇒ not emitted. See [glue config](#glue-config). |
| `cloud.bedrock` | object | no | AWS Bedrock CloudWatch config. Absent ⇒ not emitted. See [bedrock config](#bedrock-config). |
| `cloud.agentcore` | object | no | AWS Bedrock AgentCore CloudWatch config. Absent ⇒ not emitted. See [agentcore config](#agentcore-config). |

### environments[].cluster

| Key | Type | Required | Description |
|---|---|---|---|
| `cluster.type` | string | yes | `"eks"` (v1). |
| `cluster.name` | string | yes | Cluster name. Must be globally unique across all enabled blueprints. |
| `cluster.node_groups[]` | object | no | Node group definitions. |
| `cluster.node_groups[].name` | string | — | Node group name. |
| `cluster.node_groups[].instance_type` | string | — | EC2 instance type (e.g. `m6i.xlarge`). |
| `cluster.node_groups[].desired` | int | — | Desired node count. |
| `cluster.node_groups[].provisioner` | string | — | `"managed"` (default) or `"karpenter"`. |
| `cluster.node_groups[].os` | string | — | `linux` (default) or `windows`. |
| `cluster.k8s_monitoring` | object | no | Grafana k8s-monitoring Helm chart config. |
| `cluster.k8s_monitoring.enabled` | bool | — | Enable the k8s-monitoring substrate. |
| `cluster.k8s_monitoring.chart_version` | string | — | Helm chart version string. |
| `cluster.k8s_monitoring.alloy` | bool | — | Deploy alloy-metrics StatefulSet. |
| `cluster.k8s_monitoring.alloy_version` | string | — | Alloy version (e.g. `"1.16.3"`; canonicalized to `"v1.16.3"`). |
| `cluster.k8s_monitoring.opencost` | bool | — | Enable OpenCost cost-allocation. |
| `cluster.k8s_monitoring.kepler` | bool | — | Enable Kepler energy monitoring. |
| `cluster.k8s_monitoring.features` | map[string]bool | — | Feature gates for Alloy collectors. Valid keys: `cluster_metrics`, `cluster_events`, `pod_logs`, `node_logs`, `profiling`, `application_observability`. Absent/false ⇒ that collector is not deployed. |
| `cluster.k8s_monitoring.metrics_replicas` | int | — | alloy-metrics StatefulSet replica count. `0` ⇒ default 1. |
| `cluster.k8s_monitoring.receiver_as_daemonset` | bool | — | Model alloy-receiver as a per-node DaemonSet instead of a Deployment. |
| `cluster.k8s_monitoring.fleet_management` | bool | — | Register this cluster's Alloy collectors with the Fleet Management API. Requires `enabled: true`. |
| `cluster.k8s_monitoring.control_plane` | object | — | Gate individual control-plane metric families. |
| `cluster.k8s_monitoring.control_plane.api_server` | bool | — | Emit kube-apiserver metrics. |
| `cluster.k8s_monitoring.control_plane.kube_proxy` | bool | — | Emit kube-proxy metrics. |
| `cluster.k8s_monitoring.control_plane.kube_scheduler` | bool | — | Emit kube-scheduler metrics. |
| `cluster.k8s_monitoring.control_plane.kube_controller_manager` | bool | — | Emit kube-controller-manager metrics. |
| `cluster.k8s_monitoring.control_plane.kubelet_probes` | bool | — | Emit kubelet probe metrics. |
| `cluster.k8s_monitoring.pod_logs_method` | string | — | Pod-log collection mechanism. Empty with `pod_logs: true` defaults to `"opentelemetry"`. Absent `pod_logs` ⇒ `"none"`. |
| `cluster.observability` | object | no | Gates the per-node EC2 CloudWatch lane. |
| `cluster.observability.cloudwatch` | bool | no | Emit the `aws_ec2_*` CloudWatch lane for cluster nodes. Default `true`. |
| `cluster.addons[]` | object | no | Cluster add-ons to deploy. May be bare scalar (`- core_dns`) or map form (`- {name: cluster_autoscaler, min_nodes: 3, max_nodes: 10}`). |
| `cluster.addons[].name` | string | — | Add-on construct kind (registry key). |
| `cluster.addons[].<kind config>` | varies | — | Add-on construct's own config fields. See per-kind sections below. |
| `cluster.platform` | object | no | Node OS and Kubernetes version. |
| `cluster.platform.os` | string | — | `"al2"`, `"al2023"` (default), or `"bottlerocket"`. |
| `cluster.platform.kubernetes_version` | string | — | e.g. `"1.31"` (default). |
| `cluster.platform.kernel_version` | string | — | Optional override of the node kernel string. |

### environments[].databases[]

| Key | Type | Required | Description |
|---|---|---|---|
| `databases[].engine` | string | yes | `"postgres"`, `"mysql"`, `"docdb"`, or `"neptune"`. |
| `databases[].version` | string | yes | Engine version string. |
| `databases[].name` | string | yes | Instance name. Must be globally unique across all enabled blueprints. |
| `databases[].instance_class` | string | no | e.g. `"db.t3.medium"`. Empty ⇒ resolver default. |
| `databases[].observability` | object | no | Emission switch. Nil ⇒ CloudWatch only. |
| `databases[].observability.cloudwatch` | bool | no | Emit `aws_rds_*` CloudWatch family. Default `true`. |
| `databases[].observability.dbo11y` | bool | no | Emit `database_observability_*` lane. Default `false`. |
| `databases[].observability.digests` | int | no | dbo11y query-catalogue size. Default `40`. Ignored unless `dbo11y: true`. |

### environments[].caches[]

| Key | Type | Required | Description |
|---|---|---|---|
| `caches[].engine` | string | yes | `"redis"`. |
| `caches[].version` | string | yes | Engine version string. |
| `caches[].name` | string | yes | Instance name. Must be globally unique across all enabled blueprints. |
| `caches[].instance_class` | string | no | e.g. `"cache.r6g.large"`. Empty ⇒ resolver default. |
| `caches[].observability` | object | no | Emission switch. |
| `caches[].observability.cloudwatch` | bool | no | Emit the `aws_elasticache_*` CloudWatch lane. Default `true`. |

---

## workloads[]

| Key | Type | Required | Description |
|---|---|---|---|
| `workloads[].type` | string | yes | Workload kind registry key (e.g. `web_service`, `app`). |
| `workloads[].name` | string | yes | Unique workload instance name. |
| `workloads[].runs_on` | string | yes | Cluster name this workload runs on. Resolved at load or fails. |
| `workloads[].replicas` | int | no | Pod count (default 2). |
| `workloads[].calls[]` | object | no | Downstream DB/cache hops. |
| `workloads[].calls[].db` | string | — | Name of a declared database this workload calls. |
| `workloads[].calls[].cache` | string | — | Name of a declared cache this workload calls. |
| `workloads[].<kind config>` | varies | — | Workload kind's own config. See [web_service config](#web_service-workload-config) and [app config](#app-workload-config) below. |
| `for_each_env` | bool | no | Fan this workload into one instance per environment. |
| `envs[]` | string | no | Subset of environment names to fan into (used with `for_each_env: true`). |

### web_service workload config

| Key | Type | Required | Description |
|---|---|---|---|
| `tracing` | bool | no | Enable the OTLP trace lane. Default `true`. |
| `rum` | bool | no | Enable the Faro/RUM lane (requires `GC_FARO_*` credentials). |
| `traffic` | object | no | Traffic shaping for the metric lane. |
| `traffic.shape` | string | — | Shape profile name (informational). |
| `traffic.off_peak_rps` | float | — | Trough request rate. Default `5`. |
| `traffic.peak_rps` | float | — | Plateau request rate. Default `50`. |
| `endpoints[]` | object | no | Route catalogue; requests are drawn uniformly. |
| `endpoints[].route` | string | — | e.g. `"GET /v1/items"`. |
| `endpoints[].error_rate` | float | — | `[0,1]` base error fraction. |
| `endpoints[].p95_ms` | float | — | p95 latency target in ms. |
| `observability` | object | no | Additive emission switch for the Beyla eBPF observation lane. |
| `observability.beyla` | object | — | Beyla lane config. |
| `observability.beyla.mode` | string | — | `"kubernetes"` (default) or `"standalone"`. |
| `observability.beyla.context` | string | — | `"ebpf_only"` (default) or `"coexist_sdk"`. |
| `observability.beyla.features[]` | string | — | Empty ⇒ Beyla default features for the mode. |
| `pyroscope` | object | no | Continuous profiling lane config. |
| `pyroscope.enabled` | bool | — | Enable profiling emission. |
| `pyroscope.mode` | string | — | Push mode. |
| `pyroscope.runtime` | string | — | Runtime (e.g. `go`, `jvm`). |
| `pyroscope.types[]` | string | — | Profile type names. |
| `pyroscope.span_profiles` | bool | — | Enable span-correlated profiles. |
| `otel` | object | no | Native OTLP application-metrics lane. Absent or `metrics: false` ⇒ no OTLP metrics. |
| `otel.metrics` | bool | — | Enable native OTLP `http.server.*` metrics emission. |
| `otel.mode` | string | — | `"naked"` (default) or `"k8s_monitoring"`. |
| `context` | string | no | §5 resource-attr context (`Platform`, `ContentGen`, `DataGen`). Empty ⇒ omitted. |
| `use_case` | string | no | §5 resource-attr use case. Empty ⇒ omitted. |
| `team` | string | no | §5 resource-attr team. Empty ⇒ omitted. |
| `version` | string | no | Override the default `service.version`. Empty ⇒ default. |

### app workload config

The `app` workload declares a multi-service graph. See [workloads.md](workloads.md) for a conceptual guide.

| Key | Type | Required | Description |
|---|---|---|---|
| `services[]` | object | yes | Graph nodes. |
| `services[].name` | string | yes | Unique graph identity; stamped as the `service` / `service_name` label. |
| `services[].type` | string | no | Span semantics + profile fit (e.g. `frontend`, `web`, `grpc`, `db`, `llm`, `agent`). |
| `services[].runtime` | string | no | `go`, `jvm`, `node`, or `python`. Selects the runtime profile. |
| `services[].entry` | bool | no | Marks the request entry point (mints invocations). |
| `services[].namespace` | string | no | Kubernetes namespace. Propagates to both substrate placement and emitted telemetry. |
| `services[].context` | string | no | §5 resource-attr context. Empty ⇒ omitted. |
| `services[].use_case` | string | no | §5 resource-attr use case. Empty ⇒ omitted. |
| `services[].team` | string | no | §5 resource-attr team. Empty ⇒ omitted. |
| `services[].version` | string | no | Override `service.version`. |
| `services[].routes[]` | string | no | Request routes (e.g. `"GET /v1/items"`). |
| `services[].replicas` | int | no | Pod count for this node (default 2). |
| `services[].profiles[]` | string | no | Catalog profile-template names applied to this node. |
| `services[].calls[]` | string | no | Downstream node names (graph edges). |
| `services[].db_instance` | string | no | Base database name to resolve per-env (e.g. `"orders-pg"` → `"orders-pg-<env>"`). |
| `services[].external` | bool | no | Remote/managed service: appears as a trace hop but is NOT placed as a k8s pod. |
| `services[].pages[]` | object | no | RUM navigation inventory (frontend entry nodes only). |
| `services[].pages[].path` | string | — | SPA route (e.g. `/document-library`). |
| `services[].pages[].name` | string | — | Human view name. |
| `services[].pages[].actions[]` | string | — | User-action intents on this page. |
| `services[].agentic_flow` | object | no | In-process LangGraph orchestration that adds a gen_ai span subtree. Nil ⇒ no agentic flow. |
| `services[].agentic_flow.workflow` | string | — | LangGraph graph name. |
| `services[].agentic_flow.agents[]` | object | — | Pool of agents the workflow can invoke. |
| `services[].agentic_flow.agents[].name` | string | — | Agent name. |
| `services[].agentic_flow.agents[].tools[]` | string | — | Tool names for this agent. |
| `services[].agentic_flow.omit_chat` | bool | — | Drop the `chat <model>` leaf span (set when a connected gateway hop already models the LLM call). |
| `services[].pyroscope` | object | no | Per-node profiling config. Same sub-keys as `web_service.pyroscope`. |
| `services[].resources` | object | no | Container CPU/memory requests/limits and cAdvisor usage base. Affects only the k8s substrate lane. |
| `services[].resources.cpu_request` | float | — | CPU request (cores). |
| `services[].resources.cpu_limit` | float | — | CPU limit (cores). |
| `services[].resources.mem_request` | float | — | Memory request (bytes). |
| `services[].resources.mem_limit` | float | — | Memory limit (bytes). |
| `services[].resources.cpu_usage_base` | float | — | cAdvisor CPU usage base (cores). |
| `services[].controller` | string | no | k8s controller kind: `Deployment` (default) or `StatefulSet`. |
| `services[].hpa` | bool | no | Emit `kube_horizontalpodautoscaler_*` metrics. |
| `services[].volume_claims[]` | string | no | PVC template names for `kube_persistentvolumeclaim_*` and kubelet volume stats. |
| `services[].metrics[]` | object | no | Inline custom metric definitions (DSL). |
| `services[].logs[]` | object | no | Inline custom log stream definitions (DSL). |
| `services[].spans[]` | object | no | Inline custom span attribute definitions (DSL). |
| `traffic` | object | no | Entry node invocation volume shaping. |
| `traffic.shape` | string | — | Shape profile name. |
| `traffic.off_peak_rps` | float | — | Trough rate. Default `5`. |
| `traffic.peak_rps` | float | — | Plateau rate. Default `50`. |
| `traffic.request_latency_p95_ms` | float | — | Base end-to-end latency p95. Default `0` ⇒ `200ms`. LLM/agentic apps should set this to seconds (e.g. `9000`). |
| `models[]` | object | no | Valid `(model, provider)` routing pairs for AI apps. Empty ⇒ non-AI app. |
| `models[].model` | string | — | `gen_ai.request.model` value (e.g. `gpt-4o`, `claude-3.5-sonnet`). |
| `models[].provider` | string | — | `gen_ai.provider.name` value (e.g. `azure-openai`, `bedrock`). |

#### Custom-telemetry DSL (app workload `metrics[]`, `logs[]`, `spans[]`)

The DSL value model is a one-of; exactly one of these keys must be present per value spec:

| Generator key | Type | Description |
|---|---|---|
| `const` | float | Fixed numeric constant. |
| `const_str` | string | Fixed string constant. |
| `enum[]` | `{value, weight}` | Weighted random pick from a domain. **Required for metric and stream labels.** |
| `int_range` | `{min, max, p_zero}` | Uniform integer range with optional zero-probability. |
| `float_range` | `{min, max}` | Uniform float range. |
| `normal` | `{mean, stddev}` | Normal distribution. |
| `bool` | `{p_true}` | Bernoulli with given probability. |
| `shape` | `{base, mode}` | Incident-responsive value driven by the shape engine. |
| `ref` | string | Correlation field reference (high-card). **Metric/stream labels reject this.** |

---

## features

`features` is a map keyed by feature kind. Each entry accepts `enabled: bool` (default `true`) plus kind-specific config.

### synthetic_monitoring config

| Key | Type | Description |
|---|---|---|
| `checks[]` | object | One entry per synthetic check. |
| `checks[].name` | string | Required. Becomes the Prometheus `job` label. |
| `checks[].target` | string | HTTP URL probed. Default: `https://<name>.example.com/health`. |
| `checks[].frequency` | int | Poll interval in milliseconds. Default `60000`. |
| `checks[].probe` | string | Private probe name. Default `"synthkit-private"`. |
| `checks[].region` | string | Probe region. Default `"EMEA"`. |
| `checks[].labels` | map[string]string | User-defined labels emitted as `label_<k>=<v>` on every probe series. |

### fleet_management config

| Key | Type | Description |
|---|---|---|
| `collectors_per_os` | map[string]int | OS name (`linux`/`windows`/`darwin`) → desired fake collector count. Absent OS ⇒ not emitted. |

---

## integrations

`integrations` is a map keyed by integration kind. Each entry accepts `enabled: bool` (default `true`) plus kind-specific config. The `for_each_env` and `envs` keys are also accepted to fan an integration across environments.

### cloudflare config

| Key | Type | Description |
|---|---|---|
| `zone` | string | Cloudflare zone name. |
| `account` | string | Account identifier. |
| `colocations[]` | string | Colocation names. |
| `tunnels[]` | object | Tunnel entries. |
| `tunnels[].name` | string | Tunnel name. |

### csp_azure config

| Key | Type | Description |
|---|---|---|
| `subscriptions` | int | Number of synthetic Azure subscriptions (default `2`). |
| `company` | string | Company slug for subscription names (default `"demo"`). |
| `sub_signals[]` | string | Families to emit. Valid: `compute`, `databases`, `storage`, `networking`, `messaging`, `logs`. **`ai` is opt-in and NOT in the default set** — must be listed explicitly. Empty ⇒ all default families. |
| `ingestion_path` | string | `"serverless"` (default, GC managed scraper) or `"azure_exporter"`. |
| `credential` | string | Managed-scraper credential name (`serverless` path only). Default `"azure"`. |
| `tags` | map[string]string | Resource tags emitted as `tag_<key>` on every series. Opt-in: omit ⇒ no tag labels. |

### csp_gcp config

| Key | Type | Description |
|---|---|---|
| `projects` | int | Number of synthetic GCP projects (default `2`). |
| `company` | string | Company slug for project IDs (default `"demo"`). |
| `sub_signals[]` | string | Families to emit. Valid: `compute`, `databases`, `storage`, `networking`, `loadbalancing`, `pubsub`, `cloudrun`, `bigtable`, `logs`. **`vertex` is opt-in** — must be listed explicitly. Empty ⇒ all default families. |

### portkey_gateway config

| Key | Type | Description |
|---|---|---|
| `models[]` | string | LLM model names to spread across label values (default `["gpt-4o"]`). |
| `providers[]` | string | Provider names (default `["azure-openai"]`). |
| `app` | string | Service name for the `app=` label (default `"ai-gateway"`). |
| `env` | string | Environment name for the `env=` label (default `"prod"`). |
| `sub_signals[]` | string | Valid: `"gateway"` (14 portkey_* metrics), `"runtime"` (node_* subset). Empty ⇒ both. |

### portkey_poller config

| Key | Type | Description |
|---|---|---|
| `workspace` | string | Portkey workspace identifier (default `"ws-demo"`). |
| `use_cases[]` | string | `metadata_use_case` dimension values (default `["assistant","summarization"]`). |
| `models[]` | string | `ai_model` dimension values (default `["gpt-4o","gpt-4o-mini","gpt-4.1-mini"]`). |
| `use_case_weights` | map[string]float | Per-use-case volume multiplier. Missing key ⇒ `1.0`. |

### langsmith_eval config

| Key | Type | Description |
|---|---|---|
| `projects[]` | string | LangSmith project names (default `["assistant-prod"]`). |
| `use_cases[]` | string | Use-case dimension values (default `["assistant","summarization"]`). |
| `evaluators[]` | string | Evaluator keys for `langsmith_eval_score` (default `["faithfulness","completeness","relevance"]`). |
| `use_case_weights` | map[string]float | Per-use-case volume multiplier. Missing key ⇒ `1.0`. |

### langsmith_platform config

| Key | Type | Description |
|---|---|---|
| `services[]` | string | Service names to emit metrics for. Valid: `backend`, `host-backend`, `platform-backend`, `playground`, `clickhouse`, `redis`, `postgres`, `nginx`. Empty ⇒ all eight. |

### snowflake config

| Key | Type | Description |
|---|---|---|
| `account` | string | Snowflake account identifier (default `"demo-acct"`). |
| `warehouses[]` | string | Virtual warehouse names (default `["wh_compute","wh_etl"]`). |
| `databases[]` | string | Database names for per-database metrics (default `["analytics","raw"]`). |

### network_topology config

| Key | Type | Description |
|---|---|---|
| `instance` | string | Exporter scrape endpoint (e.g. `"netobs-dc1:9100"`). Required; must be unique across blueprints. |
| `job` | string | Prometheus `job` label (default `"integrations/network-topology-exporter"`). |
| `role` | string | Federation role: `standalone` (default), `hub`, or `spoke`. |
| `spoke_id` | string | Spoke identity (required when `role: spoke`). |
| `protocols[]` | string | Discovery walker protocols (default `[lldp, bgp]`). |
| `fabric` | object | Optional topology generator config. |
| `fabric.kind` | string | `spine_leaf`, `clos`, `linear`, or `star`. |
| `fabric.spines` | int | Spine count (spine_leaf/clos). |
| `fabric.leaves` | int | Leaf count (spine_leaf/clos). |
| `fabric.hosts_per_leaf` | int | Optional access hosts per leaf. |
| `fabric.vendor_mix[]` | string | Round-robin vendor assignment (default `[arista]`). |
| `fabric.site` | string | Device `site` label. |
| `devices[]` | object | Explicit device declarations (augment/override generated fabric). |
| `devices[].id` | string | Device identifier. |
| `devices[].vendor` | string | Vendor name. |
| `devices[].os_version` | string | OS/firmware version. |
| `devices[].site` | string | Site label. |
| `devices[].uptime` | int | Initial uptime (seconds). |
| `links[]` | object | Explicit link declarations. |
| `links[].src_device` | string | Source device ID. |
| `links[].src_port` | string | Source port. |
| `links[].dst_device` | string | Destination device ID. |
| `links[].dst_port` | string | Destination port. |
| `links[].proto` | string | Protocol. |
| `links[].link_kind` | string | Link kind. |
| `session_pool` | bool | Gate the `snmp_session_pool_*` family. |
| `out_of_scope_neighbours` | int | Steady-state out-of-scope neighbour count. |
| `otlp_output` | bool | Gate the `otlp_push_total` family. |
| `federation` | object | Hub-mode wiring. |
| `federation.spokes[]` | string | Spoke instance names aggregated by this hub. |

### beyla_agent config

| Key | Type | Description |
|---|---|---|
| `mode` | string | `"kubernetes"` (default) or `"standalone"`. |
| `instrumented_processes` | int | eBPF-instrumented process count (default `4`). |
| `version` | string | Beyla version for build-info gauge (default `"1.9.0"`). |
| `revision` | string | Beyla git revision (default `"unknown"`). |
| `cluster` | string | Cluster name (kubernetes mode identity). |
| `node` | string | Node name (kubernetes mode identity). |
| `host` | string | Host name (standalone mode identity). |

### qualification_pipeline config

| Key | Type | Description |
|---|---|---|
| `stages[]` | string | Pipeline stage names. Default: `["verification","build","test","test-tokens-usage","autovalidate","pdf"]`. |
| `jobs[]` | string | CI job names. Default: `["validation-sbom","iac-tests","functional-tests"]`. |
| `suites[]` | string | Test suite names (coined `qualification_*` metrics). Default: `["infra","functional"]`. |
| `clouds[]` | string | Cloud target names. Default: `["aws","azure","gcp","common"]`. |

---

## Cluster add-on configs

Add-ons are listed under `cluster.addons[]`. Most have no configurable fields; only those with config are shown.

### cw_infra config (cloud.cloudwatch)

| Key | Type | Description |
|---|---|---|
| `albs` | int | ALB instance count. Nil/omitted ⇒ default 1. Explicit `0` disables the ALB family. |
| `s3_buckets` | int | S3 bucket count. Nil/omitted ⇒ default 2. Explicit `0` disables the S3 family. |
| `firehose` | bool | Emit `aws_firehose_*` (default `true`). |
| `nlb` | bool | Emit `aws_networkelb_*` (default `true`). |
| `ebs` | bool | Emit `aws_ebs_*` (default `true`). |
| `nat_gateway` | bool | Emit `aws_natgateway_*` (default `true`). |
| `eks` | bool | Emit `aws_eks_*` control-plane (default `true`). |
| `private_link` | bool | Emit `aws_privatelink_*` endpoints and services (default `true`). |

### bedrock config

| Key | Type | Description |
|---|---|---|
| `models[]` | string | Model IDs to emit per-model series for. Empty ⇒ default model list. |
| `sub_signals[]` | string | Families to emit. Valid: `models`, `agents`, `guardrails`, `invocation_logs`. Empty ⇒ all four. |

### agentcore config

| Key | Type | Description |
|---|---|---|
| `agents[]` | string | Agent logical names for resource-usage dimensions. Default: `["planner","retriever"]`. |
| `sub_signals[]` | string | Families to emit. Valid: `runtime`, `resource_usage`, `usage_logs`. Empty ⇒ `runtime` + `resource_usage` + app logs. `usage_logs` is opt-in. |

### aoss config

| Key | Type | Description |
|---|---|---|
| `collections[]` | string | OpenSearch Serverless collection names. Empty ⇒ one synthetic collection. |

### mwaa config

| Key | Type | Description |
|---|---|---|
| `environments[]` | string | MWAA environment names. Empty ⇒ one synthetic environment. |

### glue config

| Key | Type | Description |
|---|---|---|
| `jobs[]` | string | AWS Glue job names. Empty ⇒ one synthetic job. |

### cluster_autoscaler config

| Key | Type | Description |
|---|---|---|
| `min_nodes` | int | Minimum node count for autoscaler metrics. |
| `max_nodes` | int | Maximum node count for autoscaler metrics. |

### cert_manager config

| Key | Type | Description |
|---|---|---|
| `job_mode` | string | `""` or `"autodiscovery"` ⇒ `job="cert-manager"`. `"integration"` ⇒ `job="integrations/cert-manager"`. |

### ksm_ingress config

| Key | Type | Description |
|---|---|---|
| `ingresses[]` | object | Ingress declarations. |
| `ingresses[].name` | string | Ingress name. |
| `ingresses[].namespace` | string | Namespace (default: first workload's namespace). |
| `ingresses[].host` | string | Hostname (default: `<name>.example.com`). |
| `ingresses[].path` | string | Path (default: `"/"`). |
| `ingresses[].service_name` | string | Required. Service name. |
| `ingresses[].service_port` | int | Service port (default `80`). |
| `ingresses[].tls` | bool | TLS enabled (default `false`). |

Add-ons with no configurable fields: `alloy_health`, `argocd`, `core_dns`, `ebs_csi`, `envoy_gateway`, `etcd`, `external_dns`, `karpenter`, `load_balancer_controller`, `vpc_cni`.

---

## incidents[]

| Key | Type | Description |
|---|---|---|
| `incidents[].kind` | string | Failure mode name. Mutually exclusive with `scenario`. |
| `incidents[].scenario` | string | Scenario name to fire (fires the whole bundle). Mutually exclusive with `kind`/`target`. |
| `incidents[].target` | string | Instance name to target. `""` ⇒ blueprint-wide (single-axis modes only). |
| `incidents[].at` | string | RFC3339 or `"2006-01-02T15:04[:05]"` (blueprint timezone). One-shot activation. |
| `incidents[].every` | string | Go duration (e.g. `"10m"`). Makes the incident interval-recurring. Mutually exclusive with `at`. |
| `incidents[].for` | string | Go duration (e.g. `"20m"`). Active window per `every` cycle (or one-shot duration). |
| `incidents[].intensity` | float | `[0,1]` effect intensity. |

---

## scenarios[]

| Key | Type | Description |
|---|---|---|
| `scenarios[].name` | string | Scenario identifier used in `incidents[].scenario` and control-plane API calls. |
| `scenarios[].title` | string | Human display name. Defaults to `name`. |
| `scenarios[].summary` | string | One-line description of what the scenario causes. |
| `scenarios[].effects[]` | object | Effect list. |
| `scenarios[].effects[].mode` | string | Failure mode name. |
| `scenarios[].effects[].target` | string | Instance name, `<axis>:*` wildcard, or empty (single-axis modes only). |
| `scenarios[].effects[].intensity` | float | `[0,1]` effect intensity. Default `1.0`. |

---

## hosts[]

| Key | Type | Description |
|---|---|---|
| `hosts[].name` | string | Hostname. Required, unique. Becomes the `instance` label. |
| `hosts[].os` | string | Exporter vocabulary: `linux` (default), `windows`, or `macos`. |
| `hosts[].ip` | string | Optional private IP address. Omitted when empty. |
| `hosts[].cpus` | int | Logical CPU count. Default `2`. |
| `hosts[].memory_gb` | int | Total RAM in GiB. Default `8`. |
| `hosts[].metrics_profile` | string | `"integration"` (GC integration allowlist, default) or `"full"` (broad default-Alloy surface). |
| `hosts[].os_version` | string | OS version string (e.g. `"22.04"`, `"Server 2022"`). Optional. |
| `hosts[].kernel` | string | Kernel string for `node_uname_info` (linux/macos). Optional. |
| `hosts[].observability` | object | Gates the Docker cadvisor lane and host logs. |
| `hosts[].observability.docker` | bool | Emit Docker cadvisor metrics and container log streams. Default `false`. |
| `hosts[].observability.logs` | bool | Emit host log streams (journal/winevent/file). Default `true`. |

---

## Failure modes

The table below lists every valid `mode` value for `incidents[].kind` or `scenarios[].effects[].mode`.

| Mode | Axis | Description |
|---|---|---|
| `agentcore_throttle` | cloud | AgentCore request throttles + `system_errors` spike (region-scoped capacity constraint). |
| `bedrock_throttle` | cloud | Bedrock invocation throttling climbs. |
| `connection_saturation` | database | Active connections climb toward max. |
| `cpu_hotspot` | service / workload / cluster | Elevated CPU concentrated in a hot frame. |
| `error_burst` | workload | Elevated 5xx error rate. |
| `error_spike` | service | Elevated 5xx error rate on the targeted service node. |
| `eval_quality_degraded` | cloud | LangSmith eval quality regresses — scores drop, retry/HITL rates climb. |
| `fallback_storm` | service | Elevated gateway fallback rate on the targeted service node. |
| `goroutine_leak` | service / workload | Goroutine accumulation. |
| `latency_spike` | workload | Elevated request latency (up to 4× at full intensity). |
| `latency_storm` | service | Elevated request latency on the targeted service node. |
| `lock_contention` | service / workload / database | Elevated mutex/block contention or database lock waits. |
| `memory_leak` | workload / service | Growing heap — raises memory inuse/alloc profile values. |
| `nettopo_auth_failures` | network | SNMP credential trials fail. |
| `nettopo_devices_unreachable` | network | SNMP polling fails for a fraction of devices. |
| `nettopo_discovery_slow` | network | Discovery cycle duration inflates. |
| `nettopo_spoke_down` | network | A federation spoke goes offline. |
| `nettopo_walker_degraded` | network | Walker outcome errors climb; edge count under-reports. |
| `node_not_ready` | cluster | A node flips NotReady; its pods go Pending. |
| `oom_kill` | cluster | Containers OOM-killed; restart count climbs, status reason OOMKilled. |
| `pod_crashloop` | cluster | Pods crash-looping; restarts climb, phase Pending. |
| `portkey_scrape_degraded` | cloud | Portkey Analytics scrape degrades — error rate, latency, and poller lag climb. |
| `replication_lag` | database | Replica falls behind primary. |
| `retry_storm` | service | Elevated gateway retry rate on the targeted service node. |
| `slow_query_storm` | database | Query latency right-tail spikes. |
| `throughput_drop` | service | Reduced throughput on the targeted service node. |
| `web_vitals_degraded` | service | Browser web-vitals degrade (LCP/INP/TTFB/FCP and CLS spike). |
