# synthkit blueprint schema (generated)

> **Generated** by `internal/blueprintschema` from the live Go types — do NOT edit by hand.
> Regenerate with `make blueprint-schema`; the `TestSchemaCurrent` gate fails on drift.
> Every key a blueprint may contain is listed below. Strict-decoded: unknown keys fail to load.

## Blueprint document

**Location:** `(top level)`  ·  **group:** blueprint

The blueprint YAML document. Strict-decoded: any key not listed here fails to load.

| key | type | optional | description |
|---|---|---|---|
| `name` | string |  |  |
| `label` | string |  | sink-stamped selector; defaults to Name |
| `metadata` | object |  | optional human-facing annotation (UI only) |
| `metadata.description` | string |  | free-text summary shown in the UI |
| `metadata.tags[]` | string |  | free-form labels for filtering/grouping |
| `metadata.owner` | string |  | owning team/person |
| `metadata.links` | map[string]string |  | named external references (name → url) |
| `metadata.category` | string |  | single classification (e.g. demo/reference/customer) |
| `shape` | string |  | default shape profile |
| `timezone` | string |  | business-hours anchor (default Europe/Zurich) |
| `regions[]` | object |  | follow-the-sun multi-tz composite (mutually exclusive with timezone) |
| `regions[].name` | string |  |  |
| `regions[].timezone` | string |  |  |
| `regions[].weight` | float |  |  |
| `series_budget` | int |  |  |
| `environments[]` | object |  |  |
| `environments[].name` | string |  |  |
| `environments[].weight` | float | yes | default 1.0 |
| `environments[].production` | bool | yes | default: name == "prod" |
| `environments[].metadata` | object |  | optional human-facing annotation (UI only) |
| `environments[].metadata.description` | string |  | free-text summary shown in the UI |
| `environments[].metadata.tags[]` | string |  | free-form labels for filtering/grouping |
| `environments[].metadata.owner` | string |  | owning team/person |
| `environments[].metadata.links` | map[string]string |  | named external references (name → url) |
| `environments[].metadata.category` | string |  | single classification (e.g. demo/reference/customer) |
| `environments[].cloud` | object | yes |  |
| `environments[].cloud.provider` | string |  | "aws" (v1) |
| `environments[].cloud.account_id` | string |  |  |
| `environments[].cloud.region` | string |  |  |
| `environments[].cloud.vpc_id` | string |  |  |
| `environments[].cloud.nat_gateways` | int |  |  |
| `environments[].cloud.cloudwatch` | raw yaml (see per-kind config sections) |  | cw_infra sub-family toggles (raw; decoded via registry) |
| `environments[].cloud.aoss` | raw yaml (see per-kind config sections) |  | OpenSearch Serverless config (collections:); absent ⇒ not emitted |
| `environments[].cloud.mwaa` | raw yaml (see per-kind config sections) |  | Managed Workflows for Apache Airflow (environments:); absent ⇒ not emitted |
| `environments[].cloud.glue` | raw yaml (see per-kind config sections) |  | AWS Glue ETL (jobs:); absent ⇒ not emitted |
| `environments[].cloud.bedrock` | raw yaml (see per-kind config sections) |  | AWS Bedrock CloudWatch (models:/sub_signals:); absent ⇒ not emitted |
| `environments[].cloud.agentcore` | raw yaml (see per-kind config sections) |  | AWS Bedrock-AgentCore CloudWatch (agents:/sub_signals:); absent ⇒ not emitted |
| `environments[].cluster` | object | yes |  |
| `environments[].cluster.type` | string |  | "eks" (v1) |
| `environments[].cluster.name` | string |  |  |
| `environments[].cluster.node_groups[]` | object |  |  |
| `environments[].cluster.node_groups[].name` | string |  |  |
| `environments[].cluster.node_groups[].instance_type` | string |  |  |
| `environments[].cluster.node_groups[].desired` | int |  |  |
| `environments[].cluster.node_groups[].provisioner` | string |  | "managed" (default) \| "karpenter" |
| `environments[].cluster.node_groups[].os` | string |  | ""\|linux\|windows ; "" defaults to linux |
| `environments[].cluster.k8s_monitoring` | object |  |  |
| `environments[].cluster.k8s_monitoring.enabled` | bool |  |  |
| `environments[].cluster.k8s_monitoring.chart_version` | string |  |  |
| `environments[].cluster.k8s_monitoring.alloy` | bool |  |  |
| `environments[].cluster.k8s_monitoring.alloy_version` | string |  | human form ("1.16.3"); canonicalized to "v1.16.3" |
| `environments[].cluster.k8s_monitoring.opencost` | bool |  |  |
| `environments[].cluster.k8s_monitoring.kepler` | bool |  |  |
| `environments[].cluster.k8s_monitoring.features` | map[string]bool |  | Features gates which Alloy collectors a real k8s-monitoring deploy would create. Keys: cluster_metrics, cluster_events, pod_logs, node_logs, profiling, application_observability. Absent/false ⇒ that collector role is not deployed. |
| `environments[].cluster.k8s_monitoring.metrics_replicas` | int |  | MetricsReplicas is the alloy-metrics StatefulSet replica count (does NOT scale with nodes). 0 ⇒ default 1. |
| `environments[].cluster.k8s_monitoring.receiver_as_daemonset` | bool |  | ReceiverAsDaemonset models alloy-receiver as a per-node DaemonSet instead of the synth default (a single Deployment). |
| `environments[].cluster.k8s_monitoring.fleet_management` | bool |  | FleetManagement, when true, registers this cluster's Alloy collectors with the FM API (a fleet_management construct instance is emitted from the cluster path). Requires Enabled. |
| `environments[].cluster.k8s_monitoring.control_plane` | object |  | ControlPlane gates individual control-plane component metric families. |
| `environments[].cluster.k8s_monitoring.control_plane.api_server` | bool |  |  |
| `environments[].cluster.k8s_monitoring.control_plane.kube_proxy` | bool |  |  |
| `environments[].cluster.k8s_monitoring.control_plane.kube_scheduler` | bool |  |  |
| `environments[].cluster.k8s_monitoring.control_plane.kube_controller_manager` | bool |  |  |
| `environments[].cluster.k8s_monitoring.control_plane.kubelet_probes` | bool |  |  |
| `environments[].cluster.k8s_monitoring.pod_logs_method` | string |  | PodLogsMethod selects the pod-log collection mechanism. "" with pod_logs feature enabled defaults to "opentelemetry"; explicit values pass through unchanged; absent pod_logs ⇒ "none". |
| `environments[].cluster.observability` | object | yes | gates the per-node ec2 CloudWatch lane |
| `environments[].cluster.observability.cloudwatch` | bool | yes | emit the CloudWatch lane (default true) |
| `environments[].cluster.addons[]` | object |  |  |
| `environments[].cluster.addons[].name` | string |  | add-on construct kind (registry key). Also accepts the bare-scalar form `- core_dns`. |
| `environments[].cluster.addons[].<kind config…>` | raw yaml |  | every remaining key is the add-on construct's own config — see the matching `… config` section. |
| `environments[].cluster.platform` | object | yes | node OS/runtime/k8s version (defaults applied when omitted) |
| `environments[].cluster.platform.os` | string |  | "al2" \| "al2023" \| "bottlerocket" (default al2023) |
| `environments[].cluster.platform.kubernetes_version` | string |  | e.g. "1.31" (default) |
| `environments[].cluster.platform.kernel_version` | string |  | optional override of the node kernel string |
| `environments[].databases[]` | object |  |  |
| `environments[].databases[].engine` | string |  | "postgres" \| "mysql" |
| `environments[].databases[].version` | string |  |  |
| `environments[].databases[].name` | string |  |  |
| `environments[].databases[].instance_class` | string |  | e.g. "db.t3.medium"; empty → resolver default |
| `environments[].databases[].observability` | object | yes | nil = CloudWatch only (the defaults) |
| `environments[].databases[].observability.cloudwatch` | bool | yes | emit aws_rds_* CloudWatch family (default true) |
| `environments[].databases[].observability.dbo11y` | bool |  | emit database_observability_* lane (default false) |
| `environments[].databases[].observability.digests` | int |  | dbo11y query-catalogue size (default 40); ignored unless dbo11y |
| `environments[].caches[]` | object |  |  |
| `environments[].caches[].engine` | string |  | "redis" |
| `environments[].caches[].version` | string |  |  |
| `environments[].caches[].name` | string |  |  |
| `environments[].caches[].instance_class` | string |  | e.g. "cache.r6g.large"; empty → resolver default |
| `environments[].caches[].observability` | object | yes |  |
| `environments[].caches[].observability.cloudwatch` | bool | yes | emit the CloudWatch lane (default true) |
| `workloads[]` | object |  |  |
| `workloads[].type` | string |  | workload kind — the registry key (e.g. web_service) |
| `workloads[].name` | string |  | unique workload instance name |
| `workloads[].runs_on` | string |  | the cluster/environment this workload runs on |
| `workloads[].replicas` | int |  | pod count driving node derivation (default 2) |
| `workloads[].calls[]` | object |  | downstream DB/cache hops this workload makes |
| `workloads[].calls[].db` | string |  | name of a declared database this workload calls |
| `workloads[].calls[].cache` | string |  | name of a declared cache this workload calls |
| `workloads[].<kind config…>` | raw yaml |  | every remaining key is the workload kind's own config — see the matching `… workload config` section. |
| `features` | map[string]raw yaml (see per-kind config sections) |  | Grafana Cloud products (sm, fleet); `enabled` reserved (default true) |
| `integrations` | map[string]raw yaml (see per-kind config sections) |  | external sources GC ingests (cloudflare, csp_*); same decode + `enabled` key |
| `incidents[]` | object |  |  |
| `incidents[].kind` | string |  |  |
| `incidents[].scenario` | string |  | if set, schedules a whole ScenarioDecl; Kind/Target must be empty |
| `incidents[].target` | string |  | workload/cluster/db/cache instance name ("" = blueprint-wide) |
| `incidents[].at` | string |  | RFC3339 or "2006-01-02T15:04[:05]" (blueprint timezone) |
| `incidents[].every` | string |  | Every, when set, makes the incident INTERVAL-RECURRING: it fires for For out of every Every (a Go duration, e.g. "10m"), repeating continuously and anchored on the Unix epoch. At is ignored and must be empty when Every is set (load rejects setting both). |
| `incidents[].for` | string |  | Go duration ("20m") |
| `incidents[].intensity` | float |  |  |
| `scenarios[]` | object |  |  |
| `scenarios[].name` | string |  |  |
| `scenarios[].title` | string |  | human display; defaults to Name |
| `scenarios[].summary` | string |  | one-line "what it causes" |
| `scenarios[].effects[]` | object |  |  |
| `scenarios[].effects[].mode` | string |  |  |
| `scenarios[].effects[].target` | string |  |  |
| `scenarios[].effects[].intensity` | float |  | [0,1]; default 1.0 when <= 0 |
| `hosts[]` | object |  | traditional non-k8s machines (node/windows/macos exporter + optional docker) |
| `hosts[].name` | string |  | Name is the hostname — the `instance` label and identity. Required, unique. |
| `hosts[].os` | string |  | OS selects the exporter vocabulary: linux \| windows \| macos. Default: linux. |
| `hosts[].ip` | string |  | IP is the optional private address (node_network_info / windows). Omitted when "". |
| `hosts[].cpus` | int |  | CPUs is the logical CPU count. Default: 2. |
| `hosts[].memory_gb` | int |  | MemoryGB is total RAM in GiB. Default: 8. |
| `hosts[].metrics_profile` | string |  | MetricsProfile selects the kept metric set: integration (cost-controlled GC integration allowlist) \| full (broad default-Alloy surface). Default: integration. |
| `hosts[].os_version` | string |  | OSVersion feeds *_os_info (e.g. "22.04" / "Server 2022" / "14.5"). Optional; sensible default per OS. |
| `hosts[].kernel` | string |  | Kernel feeds node_uname_info (linux/macos, e.g. "6.8.0-40-generic"). Optional; sensible default per OS. |
| `hosts[].observability` | object | yes | Observability gates the docker lane and host logs. |
| `hosts[].observability.docker` | bool | yes | Docker emits the Docker cadvisor metric lane + container log streams. Default: false. |
| `hosts[].observability.logs` | bool | yes | Logs emits the host log streams (journal/winevent/file). Default: true. |

## agentcore config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_bedrock_agentcore_* CloudWatch metrics for Bedrock AgentCore (invocation-class + resource-usage families)

| key | type | optional | description |
|---|---|---|---|
| `agents[]` | string |  | Agents is the list of agent logical names rendered for resource-usage dims. Default: ["planner","retriever"] — generic, no customer/blueprint strings. |
| `sub_signals[]` | string |  | SubSignals selects which families to emit. Valid: runtime, resource_usage, usage_logs, gateway (deferred Spec 3). Empty ⇒ runtime + resource_usage + app_logs (always) emit; usage_logs is opt-in. |

## alloy_health config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

Alloy pipeline meta-health (otelcol_*) + content-strip sentinel

_(no configurable fields)_

## aoss config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_aoss_* CloudWatch metrics for Amazon OpenSearch Serverless (collection-scoped + OCU)

| key | type | optional | description |
|---|---|---|---|
| `collections[]` | string |  | Collections is the list of OpenSearch Serverless collection names to emit. If empty, one synthetic collection derived from fx.Seed is used. |

## argocd config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

Argo CD application-controller / server / repo-server / applicationset metrics

_(no configurable fields)_

## bedrock config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_bedrock_* CloudWatch metrics for Bedrock model invocations, agents, and guardrails

| key | type | optional | description |
|---|---|---|---|
| `models[]` | string |  | Models is the list of model IDs to emit per-model series for. Defaults to defaultModels when empty. |
| `sub_signals[]` | string |  | SubSignals selects which metric families to emit. Valid values: "models", "agents", "guardrails", "invocation_logs". Empty (or omitted) means all four families are emitted. |

## beyla_agent config

**Location:** `integrations.beyla_agent`  ·  **group:** integration

Grafana Beyla agent self/internal metrics (beyla_internal_* / beyla_bpf_* from /internal/metrics; mode-aware, substrate-scoped)

| key | type | optional | description |
|---|---|---|---|
| `mode` | string |  | Mode is the deployment substrate: "kubernetes" (default) \| "standalone". |
| `instrumented_processes` | int |  | InstrumentedProcesses is the count of eBPF-instrumented processes on this node (drives beyla_instrumented_processes). Default 4. |
| `version` | string |  | Version is the Beyla version stamped in the build-info gauge (default "1.9.0"). |
| `revision` | string |  | Revision is the Beyla git revision stamped in the build-info gauge (default "unknown"). |
| `cluster` | string |  | Identity — kubernetes mode: |
| `node` | string |  |  |
| `host` | string |  | Identity — standalone mode: |

## cert_manager config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

cert-manager certificate/issuer/ACME metrics

| key | type | optional | description |
|---|---|---|---|
| `job_mode` | string |  | JobMode selects the job label value. "" or "autodiscovery" → "cert-manager" (annotation-autodiscovery path, default) "integration" → "integrations/cert-manager" (chart feature-integrations path) |

## cloudflare config

**Location:** `integrations.cloudflare`  ·  **group:** integration

Cloudflare zone + tunnel metrics (lablabs exporter shape)

| key | type | optional | description |
|---|---|---|---|
| `zone` | string |  |  |
| `account` | string |  |  |
| `colocations[]` | string |  |  |
| `tunnels[]` | object |  |  |
| `tunnels[].name` | string |  |  |

## cluster_autoscaler config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

cluster-autoscaler controller metrics

| key | type | optional | description |
|---|---|---|---|
| `min_nodes` | int |  |  |
| `max_nodes` | int |  |  |

## core_dns config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

CoreDNS server/cache/forward metrics

_(no configurable fields)_

## csp_azure config

**Location:** `integrations.csp_azure`  ·  **group:** integration

Grafana CSP Azure integration: azure_* window-gauge metrics + Event Hubs log streams

| key | type | optional | description |
|---|---|---|---|
| `subscriptions` | int |  | Subscriptions is the number of synthetic Azure subscriptions to emit (default 2). |
| `company` | string |  | Company is the slug used to build subscription names (default "demo"). |
| `sub_signals[]` | string |  | SubSignals is the set of sub-signals to emit. When empty, all are enabled. Valid values: compute, databases, storage, networking, messaging, logs, ai. NOTE: "ai" is OPT-IN and is NOT included in the default set — it must be named explicitly (e.g. sub_signals: [ai]) to emit Cognitive Services / Azure OpenAI metrics. |
| `ingestion_path` | string |  | IngestionPath selects the Azure→Mimir ingestion path the estate emulates (signals/cspazure.md [slug: cspazure], SK-16): "serverless" (the GC cloud/azure managed scraper — the PREFERRED default) or "azure_exporter" (prometheus.exporter.azure). The two label the same metrics differently (job, resourceID casing, instance, interval/timespan, dimension key form, HttpStatusGroup casing). Default "serverless". |
| `credential` | string |  | Credential is the managed-scraper credential name surfaced as the `credential` label on EVERY serverless-path series (e.g. "ps_azure"). Deployment-specific (like an AWS account_id). Ignored on the azure_exporter path (which has no credential label). Defaults to "azure" on the serverless path when omitted. |
| `tags` | map[string]string |  | Tags are resource tags surfaced as `tag_<key>` labels on EVERY series, on both paths (serverless via the managed scraper's `tags` setting; azure_exporter via `included_resource_tags`). OPT-IN: when omitted, NO tag labels are emitted — matching a default managed scraper (live-confirmed: the default scraper surfaces no tags). Use lowercase CAF keys (e.g. app, env, owner, costcenter) for cross-cloud consistency. |

## csp_gcp config

**Location:** `integrations.csp_gcp`  ·  **group:** integration

GCP Cloud Monitoring (stackdriver_*) metrics + logs

| key | type | optional | description |
|---|---|---|---|
| `projects` | int |  | Projects is the number of synthetic GCP projects to emit (default 2). |
| `company` | string |  | Company is the company slug for project IDs: "<company>-NN" (default "demo"). |
| `sub_signals[]` | string |  | SubSignals is the per-service-family emission switch. When empty, all families are enabled. Set to a non-empty list to emit only those families and suppress the rest. Valid values: compute, databases, storage, networking, loadbalancing, pubsub, cloudrun, bigtable, logs. OPT-IN ONLY (not in default set): vertex — Vertex AI Endpoint + Model Invocation metrics. Blueprint must list it explicitly: sub_signals: [vertex] |

## cw_infra config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

AWS CloudWatch infrastructure metric-stream families (ALB/NLB/EBS/NAT/S3/EKS/Firehose)

| key | type | optional | description |
|---|---|---|---|
| `albs` | int | yes | ALBs is the number of Application Load Balancer instances to emit: nil/omitted → default 1; explicit 0 disables the ALB family entirely; N emits N. See albCount. |
| `s3_buckets` | int | yes | S3Buckets is the number of S3 buckets to emit: nil/omitted → default 2; explicit 0 disables the S3 family entirely; N emits N. See s3Count. |
| `firehose` | bool | yes | emit Firehose pipeline-health metrics (default true) |
| `nlb` | bool | yes | emit AWS/NetworkELB family (default true) |
| `ebs` | bool | yes | emit AWS/EBS family (default true) |
| `nat_gateway` | bool | yes | emit AWS/NATGateway family (default true) |
| `eks` | bool | yes | emit AWS/EKS control-plane family (default true) |
| `private_link` | bool | yes | emit AWS/PrivateLink endpoints+services families (default true) |

## dbo11y_mysql config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

Database Observability MySQL lane (metrics + log ops)

_(no configurable fields)_

## dbo11y_postgres config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

Database Observability Postgres lane (metrics + log ops)

_(no configurable fields)_

## docdb config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_docdb_* CloudWatch metrics for one DocumentDB cluster (WRITER/READER role split)

_(no configurable fields)_

## ebs_csi config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

AWS EBS CSI Driver controller metrics (aws_ebs_csi_api_request_duration_seconds, aws_ebs_csi_ec2_collector_duration_seconds, aws_ebs_csi_ec2_collector_scrapes_total); no per-volume series (SK-12)

_(no configurable fields)_

## ec2 config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_ec2_* CloudWatch metrics correlated to the cluster nodes

_(no configurable fields)_

## elasticache config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_elasticache_* CloudWatch metrics for one cache cluster

_(no configurable fields)_

## envoy_gateway config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

Envoy Gateway control-plane (xds_*/watchable_*/controller_runtime_*) and data-plane (envoy_*) metrics

_(no configurable fields)_

## etcd config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

etcd control-plane metrics (integrations/etcd)

_(no configurable fields)_

## external_dns config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

ExternalDNS — external_dns_controller_* / registry_* / source_* / build_info + JSON logs

_(no configurable fields)_

## fleet_management config

**Location:** `features.fleet_management`  ·  **group:** feature

fake Fleet Management collector roster (Alloy self-metrics)

| key | type | optional | description |
|---|---|---|---|
| `collectors_per_os` | map[string]int |  | CollectorsPerOS maps OS name (linux/windows/darwin) → desired fake collector count. Absent OS types emit no collectors (not an error — I13: absent ⇒ omitted). |

## glue config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_glue_* CloudWatch metrics for AWS Glue ETL jobs (namespace: Glue, no AWS/ prefix)

| key | type | optional | description |
|---|---|---|---|
| `jobs[]` | string |  | Jobs is the list of AWS Glue job names to emit. If empty, one synthetic job derived from fx.Seed is used. |

## host config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

traditional non-k8s host (node/windows/macos exporter + optional docker cadvisor), metrics + logs

_(no configurable fields)_

## k8s_cluster config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

k8s-monitoring substrate (KSM/cAdvisor/kubelet/node-exporter + conformance + events)

_(no configurable fields)_

## k8s_profiling config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

k8s-monitoring continuous-profiling (Alloy eBPF process_cpu per pod)

_(no configurable fields)_

## karpenter config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

Karpenter node autoscaler metrics (karpenter_* + go_* + process_* + controller_runtime_*)

_(no configurable fields)_

## ksm_ingress config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

KSM kube_ingress_* metrics with mandatory cluster label injection (I16)

| key | type | optional | description |
|---|---|---|---|
| `ingresses[]` | object |  |  |
| `ingresses[].name` | string |  |  |
| `ingresses[].namespace` | string |  | default: first workload's namespace |
| `ingresses[].host` | string |  | default: "<name>.example.com" |
| `ingresses[].path` | string |  | default: "/" |
| `ingresses[].service_name` | string |  | required |
| `ingresses[].service_port` | int |  | default: 80 |
| `ingresses[].tls` | bool |  | default: false |

## langsmith_eval config

**Location:** `integrations.langsmith_eval`  ·  **group:** integration

LangSmith feedback API poll → langsmith_eval_* quality gauges (+ token counter)

| key | type | optional | description |
|---|---|---|---|
| `projects[]` | string |  | Projects is the list of LangSmith project names to emit (default ["assistant-prod"]). |
| `use_cases[]` | string |  | UseCases is the list of use-case dimension values (default ["assistant","summarization"]). |
| `evaluators[]` | string |  | Evaluators is the list of evaluator keys for langsmith_eval_score (default ["faithfulness","completeness","relevance"]). |
| `use_case_weights` | map[string]float |  | UseCaseWeights is an optional per-use-case relative traffic weight. Keys must match values in UseCases. A missing or zero entry defaults to 1.0 (neutral). Used to scale the langsmith_eval_token_spend_total increment: high-volume use cases produce proportionally more token spend than low-volume ones. |

## langsmith_platform config

**Location:** `integrations.langsmith_platform`  ·  **group:** integration

LangSmith platform /metrics scrape — standard process/python/ClickHouse/redis/pg/nginx exporters

| key | type | optional | description |
|---|---|---|---|
| `services[]` | string |  | Services is the list of LangSmith service names to emit metrics for. When empty, all eight default services are emitted. Valid values: backend, host-backend, platform-backend, playground, clickhouse, redis, postgres, nginx. |

## load_balancer_controller config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

AWS Load Balancer Controller — awslbc_* / aws_api_* / controller_runtime_* / workqueue_* / rest_client_*

_(no configurable fields)_

## mwaa config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_mwaa_* + aws_amazonmwaa_* CloudWatch metrics for Amazon MWAA (Managed Workflows for Apache Airflow)

| key | type | optional | description |
|---|---|---|---|
| `environments[]` | string |  | Environments is the list of MWAA environment names to emit. If empty, one synthetic environment derived from fx.Seed is used. |

## neptune config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_neptune_* CloudWatch metrics for one Neptune cluster (WRITER/READER role split)

_(no configurable fields)_

## network_topology config

**Location:** `integrations.network_topology`  ·  **group:** integration

fake network-topology-exporter (SNMP topology discovery: devices/edges + discovery health, metrics + logs)

| key | type | optional | description |
|---|---|---|---|
| `instance` | string |  | Instance is the exporter scrape endpoint (e.g. "netobs-dc1:9100"). It is the substrate disambiguator (with Job) and MUST be unique across blueprints. Required. |
| `job` | string |  | Job is the Prometheus `job` label. Default: "integrations/network-topology-exporter". |
| `role` | string |  | Role selects federation behaviour: standalone (default) \| hub \| spoke. |
| `spoke_id` | string |  | SpokeID is this spoke's identity (role=spoke); also service.instance.id. Required when role=spoke. |
| `protocols[]` | string |  | Protocols lists which discovery walkers "run". Edges are only produced for listed protocols; walker-outcome families are emitted per listed walker. Default: [lldp, bgp]. |
| `fabric` | object | yes | Fabric is the optional topology generator. |
| `fabric.kind` | string |  | spine_leaf \| clos \| linear \| star |
| `fabric.spines` | int |  | spine_leaf/clos |
| `fabric.leaves` | int |  | spine_leaf/clos |
| `fabric.hosts_per_leaf` | int |  | optional access hosts (FDB edges) |
| `fabric.vendor_mix[]` | string |  | round-robin vendor assignment; default [arista] |
| `fabric.site` | string |  | device `site` label |
| `devices[]` | object |  | Devices are explicit device declarations that augment/override the generated fabric (matched by id). |
| `devices[].id` | string |  |  |
| `devices[].vendor` | string |  |  |
| `devices[].model` | string |  | Model is accepted for back-compat parsing of existing blueprints but NOT emitted — the real exporter has no model source (no ENTITY-MIB walk). Setting this field has no effect on emitted metrics or labels. |
| `devices[].os_version` | string |  |  |
| `devices[].site` | string |  |  |
| `devices[].uptime` | int |  |  |
| `links[]` | object |  | Links are explicit link declarations that augment/override generated links. |
| `links[].src_device` | string |  |  |
| `links[].src_port` | string |  |  |
| `links[].dst_device` | string |  |  |
| `links[].dst_port` | string |  |  |
| `links[].proto` | string |  |  |
| `links[].link_kind` | string |  |  |
| `session_pool` | bool |  | SessionPool gates the snmp_session_pool_* family. |
| `out_of_scope_neighbours` | int |  | OutOfScopeNeighbours sets the steady-state out-of-scope neighbour count (gates the OOS gauge, and the hub boundary-observation series when role=hub). |
| `otlp_output` | bool |  | OTLPOutput gates the otlp_push_total family. |
| `federation` | object | yes | Federation carries hub-mode wiring (the spokes this hub aggregates). |
| `federation.spokes[]` | string |  |  |

## portkey_gateway config

**Location:** `integrations.portkey_gateway`  ·  **group:** integration

Portkey LLM gateway /metrics scrape (portkey_* custom metrics + node_* runtime subset)

| key | type | optional | description |
|---|---|---|---|
| `models[]` | string |  | Models is the list of LLM model names to spread across label values (default ["gpt-4o"]). |
| `providers[]` | string |  | Providers is the list of provider names for label spread (default ["azure-openai"]). |
| `app` | string |  | App is the service name stamped in the app= label (default "ai-gateway"). |
| `env` | string |  | Env is the environment name stamped in the env= label (default "prod"). |
| `sub_signals[]` | string |  | SubSignals gates metric families. Valid: "gateway", "runtime". Empty ⇒ both families. "gateway" = 14 custom portkey_* metrics. "runtime" = node_* runtime subset. |

## portkey_poller config

**Location:** `integrations.portkey_poller`  ·  **group:** integration

Portkey Analytics API poll → portkey_api_* windowed-aggregate gauges + poller_* self-telemetry

| key | type | optional | description |
|---|---|---|---|
| `workspace` | string |  | Workspace is the Portkey workspace identifier (default "ws-demo"). |
| `use_cases[]` | string |  | UseCases is the set of metadata_use_case values to spread over (default ["assistant","summarization"]). |
| `models[]` | string |  | Models is the set of ai_model values to spread over (default ["gpt-4o","gpt-4o-mini","gpt-4.1-mini"]). |
| `use_case_weights` | map[string]float |  | UseCaseWeights optionally sets a per-use-case volume multiplier (default 1.0 for any absent key). Higher weight ⇒ proportionally more requests/tokens/cost for that use case. |

## qualification_pipeline config

**Location:** `integrations.qualification_pipeline`  ·  **group:** integration

GitLab CI qualification/validation pipeline — gitlab_ci_pipeline_* exporter families + coined qualification_* suite signals

| key | type | optional | description |
|---|---|---|---|
| `stages[]` | string |  | Stages is the list of CI pipeline stage names to spread across label values. Default: ["verification","build","test","test-tokens-usage","autovalidate","pdf"]. |
| `jobs[]` | string |  | Jobs is the list of CI job names to spread across label values. Default: ["validation-sbom","iac-tests","functional-tests"]. |
| `suites[]` | string |  | Suites is the list of test suite names (coined qualification_* metrics only). Default: ["infra","functional"]. |
| `clouds[]` | string |  | Clouds is the list of cloud target names to fan across label spread. Default: ["aws","azure","gcp","common"]. |

## rds config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

aws_rds_* CloudWatch metrics for one database instance

_(no configurable fields)_

## snowflake config

**Location:** `integrations.snowflake`  ·  **group:** integration

Snowflake account telemetry (prometheus.exporter.snowflake → 27 gauges from ACCOUNT_USAGE)

| key | type | optional | description |
|---|---|---|---|
| `account` | string |  | Account is the Snowflake account identifier (default "demo-acct"). |
| `warehouses[]` | string |  | Warehouses is the list of virtual warehouse names to emit per-warehouse metrics for. Default: ["wh_compute","wh_etl"]. |
| `databases[]` | string |  | Databases is the list of database names to emit per-database metrics for. Default: ["analytics","raw"]. |

## synthetic_monitoring config

**Location:** `features.synthetic_monitoring`  ·  **group:** feature

fake Synthetic Monitoring checks (metrics + logs)

| key | type | optional | description |
|---|---|---|---|
| `checks[]` | object |  |  |
| `checks[].name` | string |  | Name is required. It becomes the Prometheus `job` label on all emitted series. |
| `checks[].target` | string |  | Target is the HTTP URL probed. Defaults to "https://<name>.example.com/health" if empty. |
| `checks[].frequency` | int |  | FrequencyMs is the check poll interval in milliseconds. Defaults to 60000 (60s). |
| `checks[].probe` | string |  | Probe is the private probe name. Defaults to "synthkit-private". |
| `checks[].region` | string |  | Region is the probe region. Defaults to defaultProbeRegion ("EMEA"). |
| `checks[].labels` | map[string]string |  | Labels is an optional map of user-defined labels. Each entry is emitted as label_<k>=<v> on every probe_* metric series, on sm_check_info, and on the Loki stream labels. Keys are sorted for deterministic output. An absent or empty map produces no label_ keys (byte-identical to a check with no labels). |

## vpc_cni config

**Location:** `(config-gated by env/cloud/cluster/database declarations)`  ·  **group:** topology

AWS VPC CNI (awscni_*) metrics

_(no configurable fields)_

## ai_agent workload config

**Location:** `workloads[].config (type: ai_agent)`  ·  **group:** workload

ai_agent — agent CONVERSATIONS (coding + general archetypes); emits native sigil generation/workflow-step/score ingest + gen_ai OTLP spans + gen_ai_client_*/sigil_eval_* metrics

| key | type | optional | description |
|---|---|---|---|
| `resource` | object |  |  |
| `resource.service_name` | string |  |  |
| `resource.service_namespace` | string |  |  |
| `resource.service_version` | string |  |  |
| `resource.deployment_environment` | string |  |  |
| `resource.k8s_cluster` | string |  |  |
| `resource.k8s_namespace` | string |  |  |
| `resource.k8s_deployment` | string |  |  |
| `resource.cloud_region` | string |  |  |
| `resource.job` | string |  |  |
| `agents[]` | object |  |  |
| `agents[].name` | string |  |  |
| `agents[].archetype` | string |  |  |
| `agents[].sdk` | string |  | sdk-go \| sdk-python |
| `agents[].provider` | string |  | anthropic \| openai \| bedrock \| gemini |
| `agents[].models[]` | string |  |  |
| `agents[].tools[]` | string |  |  |
| `agents[].capture_mode` | string |  | full \| no_tool_content \| metadata_only \| full_with_metadata_spans |
| `agents[].version` | string |  | declared agent version (empty ⇒ omitted, e.g. codex) |
| `agents[].streaming` | bool |  | streamText + time_to_first_token |
| `agents[].subagents[]` | string |  | coding: spawns claude-code/<type> child conversations |
| `agents[].tags` | map[string]string |  | sigil.tag.<k> + generation tags (cwd/git.branch/region/team…) |
| `agents[].activity` | object |  |  |
| `agents[].activity.sessions_per_min` | float |  |  |
| `agents[].activity.turns_p50` | int |  |  |
| `agents[].activity.turns_p95` | int |  |  |
| `evaluators[]` | object |  |  |
| `evaluators[].name` | string |  |  |
| `evaluators[].kind` | string |  | llm_judge \| heuristic |
| `evaluators[].score_key` | string |  |  |
| `evaluators[].value_type` | string |  | number \| bool \| string |
| `evaluators[].threshold` | float |  |  |
| `evaluators[].judge_model` | string |  |  |
| `evaluators[].judge_provider` | string |  |  |
| `rules[]` | object |  |  |
| `rules[].name` | string |  |  |
| `rules[].selector` | string |  | e.g. all_assistant_generations \| user_visible_turn |
| `rules[].sample_rate` | float |  |  |
| `rules[].match_agent[]` | string |  |  |
| `rules[].evaluators[]` | string |  |  |

## app workload config

**Location:** `workloads[].config (type: app)`  ·  **group:** workload

app — blueprint-declared service GRAPH; each node emits custom (DSL) metrics/logs/spans, one correlated trace across the graph

| key | type | optional | description |
|---|---|---|---|
| `services[]` | object |  | Services are the graph's nodes (typed services + their call edges). |
| `services[].name` | string |  | unique graph identity (service / service_name label) |
| `services[].type` | string |  | span semantics + profile fit (registry + default fallback) |
| `services[].runtime` | string |  | go\|jvm\|node\|python (informational; selects runtime profile) |
| `services[].entry` | bool |  | the request entry point (mints invocations) |
| `services[].namespace` | string |  | Namespace is the k8s namespace for this service. Default (empty) = node name (one namespace per service, the historic default). When set, the namespace propagates to BOTH the k8s substrate placement (fixture.Workload.Namespace → kube_pod_* kube_deployment_* namespace label) AND the app workload's own emitted telemetry (namespace / k8s_namespace_name metric labels, service.namespace / k8s.namespace.name OTLP resource attrs, Loki namespace stream label). This keeps the two signal families consistent — a real OTel-instrumented pod in namespace "datagen" emits k8s.namespace.name=datagen in both its kube-state-metrics AND its OTel resource block. |
| `services[].context` | string |  | Context / UseCase / Team are the §5 resource-attr canon (optional; AI blueprints only). Empty ⇒ OMITTED (I13). Context ∈ {Platform, ContentGen, DataGen}; Team is set per blueprint (blueprint-only identity). Emitted as bounded metric labels (context/use_case) + resource attrs (context/use_case/team). |
| `services[].use_case` | string |  |  |
| `services[].team` | string |  |  |
| `services[].version` | string |  | Version overrides the default service.version (the released image-tag intent, §5). Empty ⇒ the serviceVersion default. Stamped on service.version (resource attr) + service_version (spanmetrics). |
| `services[].routes[]` | string |  | request routes "{METHOD} {path}"; on the entry → drawn per request into r.Route (default "GET /"), on a callee → names its SERVER span (else the node name) |
| `services[].replicas` | int |  | pods for the node cascade (default 2); per-node scaling §6.6 |
| `services[].profiles[]` | string |  | catalog profile-template names (resolved at load) |
| `services[].metrics[]` | object |  | inline custom metrics (the DSL escape hatch) |
| `services[].metrics[].name` | string |  |  |
| `services[].metrics[].instrument` | string |  |  |
| `services[].metrics[].unit` | string |  |  |
| `services[].metrics[].scope` | string |  |  |
| `services[].metrics[].labels` | map[string]ValueModel |  |  |
| `services[].metrics[].value` | object |  |  |
| `services[].metrics[].value.const` | float | yes |  |
| `services[].metrics[].value.const_str` | string | yes |  |
| `services[].metrics[].value.enum[]` | object |  |  |
| `services[].metrics[].value.enum[].value` | string |  |  |
| `services[].metrics[].value.enum[].weight` | float |  |  |
| `services[].metrics[].value.int_range` | object | yes |  |
| `services[].metrics[].value.int_range.min` | int |  |  |
| `services[].metrics[].value.int_range.max` | int |  |  |
| `services[].metrics[].value.int_range.p_zero` | float |  |  |
| `services[].metrics[].value.float_range` | object | yes |  |
| `services[].metrics[].value.float_range.min` | float |  |  |
| `services[].metrics[].value.float_range.max` | float |  |  |
| `services[].metrics[].value.normal` | object | yes |  |
| `services[].metrics[].value.normal.mean` | float |  |  |
| `services[].metrics[].value.normal.stddev` | float |  |  |
| `services[].metrics[].value.bool` | object | yes |  |
| `services[].metrics[].value.bool.p_true` | float |  |  |
| `services[].metrics[].value.shape` | object | yes |  |
| `services[].metrics[].value.shape.base` | float |  |  |
| `services[].metrics[].value.shape.mode` | string |  |  |
| `services[].metrics[].value.ref` | string |  |  |
| `services[].metrics[].buckets[]` | float |  |  |
| `services[].metrics[].le_style` | string |  |  |
| `services[].logs[]` | object |  | inline custom log streams |
| `services[].logs[].source` | string |  |  |
| `services[].logs[].stream_labels` | map[string]ValueModel |  |  |
| `services[].logs[].body` | map[string]ValueModel |  |  |
| `services[].spans[]` | object |  | inline custom spans (extra attrs on this node's span) |
| `services[].spans[].name_template` | string |  |  |
| `services[].spans[].kind` | string |  |  |
| `services[].spans[].attributes` | map[string]ValueModel |  |  |
| `services[].calls[]` | string |  | downstream node names (the graph edges / propagation) |
| `services[].db_instance` | string |  | DBInstance (db/cache leaf nodes only) names the BASE blueprint database this node represents (e.g. "orders-pg"); the workload resolves it per-env against the binding's Databases to the concrete RDS instance "<db_instance>-<lower(env)>" (case-insensitive env suffix; exact name is the primary match). The resolved RDS identity decorates this node's db-CLIENT span (db.system.name / db.namespace / server.address) so the trace links to the env's RDS instance — faithful to real OTel/Beyla DB instrumentation. Empty ⇒ the leaf carries no DB identity (back-compatible: the edge stays the bare node name). |
| `services[].pages[]` | object |  | RUM navigation inventory (frontend entry only; empty ⇒ single-page behavior). RUM-only page-views, never backend-traced. |
| `services[].pages[].path` | string |  | SPA route → page_id / view_name / page_url (e.g. /document-library) |
| `services[].pages[].name` | string |  | human view name (e.g. "Document Library") |
| `services[].pages[].actions[]` | string |  | slash-free user-action intents on this page (e.g. ["search documents","open document"]) |
| `services[].external` | bool |  | remote/managed service in another team's estate (e.g. a SaaS gateway): still appears as a connected span/hop in the trace, but is NOT placed as a k8s deployment on the caller's cluster |
| `services[].agentic_flow` | object | yes | in-process LangGraph orchestration → nested gen_ai span subtree (invoke_workflow→invoke_agent→execute_tool*→chat) on this node's SERVER span; nil = no agentic flow |
| `services[].agentic_flow.workflow` | string |  | Workflow is the LangGraph graph name → span "invoke_workflow <Workflow>" (gen_ai.workflow.name). |
| `services[].agentic_flow.agents[]` | object |  | Agents is the pool of agents the workflow can invoke; one fires per request (deterministic draw). |
| `services[].agentic_flow.agents[].name` | string |  |  |
| `services[].agentic_flow.agents[].tools[]` | string |  |  |
| `services[].agentic_flow.omit_chat` | bool |  | OmitChat drops the chat <model> leaf — set where a CONNECTED gateway/llm hop already models the LLM call (WS2/WS3 Path-B gateway span) so the LLM hop is not double-counted. WS1 (API-poll, no connected gateway) leaves it false: the in-process chat span IS the observed LLM call. |
| `services[].pyroscope` | object | yes | per-node Pyroscope SDK-push profiling (nil = disabled) |
| `services[].pyroscope.enabled` | bool |  |  |
| `services[].pyroscope.mode` | string |  |  |
| `services[].pyroscope.runtime` | string |  |  |
| `services[].pyroscope.types[]` | string |  |  |
| `services[].pyroscope.span_profiles` | bool |  |  |
| `services[].resources` | object | yes | Resources optionally pins this service's container CPU/memory requests/limits + cAdvisor usage base on the cluster (kube_pod_container_resource_*). nil ⇒ the k8scluster construct's deterministic per-workload size-class defaults apply (back-compatible). It is a placement fact: the blueprint resolver copies it onto fixture.Workload.Resources, so the value only affects the k8s substrate lane, never this workload's own emitted telemetry. |
| `services[].resources.cpu_request` | float |  |  |
| `services[].resources.cpu_limit` | float |  |  |
| `services[].resources.mem_request` | float |  |  |
| `services[].resources.mem_limit` | float |  |  |
| `services[].resources.cpu_usage_base` | float |  |  |
| `services[].controller` | string |  | Controller/HasHPA/VolumeClaims are placement facts (like Resources): the blueprint resolver copies them onto fixture.Workload so this node is modelled as the right k8s controller kind (Deployment default \| StatefulSet) with optional HPA/PVC. They affect ONLY the k8s substrate lane (KSM family, pod naming, PVC/HPA), never this workload's own emitted telemetry. `controller: daemonset` is rejected on an app service node at resolve time (DaemonSets don't emit app traces — spec Q4); declare a DaemonSet via a `runs_in_cluster` integration instead. |
| `services[].hpa` | bool |  | opt-in kube_horizontalpodautoscaler_* (Deployment/StatefulSet) |
| `services[].volume_claims[]` | string |  | PVC template names → kube_persistentvolumeclaim_*/kubelet_volume_stats_*/pv cost |
| `traffic` | object |  | Traffic shapes the entry node's invocation volume (the correlated narrative sample). |
| `traffic.shape` | string |  | shape profile name (informational) |
| `traffic.off_peak_rps` | float |  | trough rate (default 5) |
| `traffic.peak_rps` | float |  | plateau rate (default 50) |
| `traffic.request_latency_p95_ms` | float |  | RequestLatencyP95Ms is the entry request's base end-to-end latency p95 (lognormal; the per-hop budget + agentflow span windows derive from it). Default 0 ⇒ 200ms (plain HTTP). LLM/agentic apps should set this to seconds (e.g. 9000) so http_server_request_duration + the in-process invoke_workflow/agent/chat span latencies reflect real LLM-wait time, not HTTP speed. |
| `models[]` | object |  | Models is the set of valid (model, provider) routings this app's requests draw from. The minter picks ONE pair per request and stamps it into the correlation, so the gen_ai spans + gateway export logs of every gen_ai-composed node carry the REAL model AND provider (the names-are-law value behind the gen_ai_client / gateway_export_log profiles). Pairing them (vs two independent lists) prevents impossible combinations like a Claude model on the Azure-OpenAI provider. Empty ⇒ a non-AI app. These feed body/attr FIELDS only (never labels), so the per-request draw does not affect the -dump inventory (I32). Values are blueprint-declared (customer model lists stay out of the catalog). |
| `models[].model` | string |  | gen_ai.request.model (e.g. gpt-4o, claude-3.5-sonnet) |
| `models[].provider` | string |  | gen_ai.provider.name (e.g. azure-openai, bedrock) |

## web_service workload config

**Location:** `workloads[].config (type: web_service)`  ·  **group:** workload

web_service — browser→backend→DB request-correlation workload (APM span-metrics, traces, app logs, optional Faro/RUM)

| key | type | optional | description |
|---|---|---|---|
| `tracing` | bool |  | Tracing enables the OTLP trace lane. Default true (see NewConfig). |
| `rum` | bool |  | RUM enables the Faro/RUM lane (also requires the binding to carry a Faro sink). |
| `traffic` | object |  | Traffic shapes the metric-lane request VOLUME (not the ledger sample). |
| `traffic.shape` | string |  | shape profile name (informational; engine carries the curve) |
| `traffic.off_peak_rps` | float |  | trough request rate (default 5) |
| `traffic.peak_rps` | float |  | plateau request rate (default 50) |
| `endpoints[]` | object |  | Endpoints is the route catalogue; requests are drawn uniformly across it. |
| `endpoints[].route` | string |  | e.g. "GET /v1/items" |
| `endpoints[].error_rate` | float |  | [0,1] base error fraction |
| `endpoints[].p95_ms` | float |  | p95 latency target in ms (drives the lognormal draw) |
| `observability` | object | yes | Observability is the additive emission-switch for the Beyla observation lane. Absent (nil) ⇒ the workload keeps its existing source="tempo" behavior unchanged. When .Beyla is set, the Beyla lane renders this workload's ledger traffic in Beyla's surface (mode + context discriminated). Strict yaml decoding rejects unknown keys. |
| `observability.beyla` | object | yes |  |
| `observability.beyla.mode` | string |  | "kubernetes" \| "standalone" (default kubernetes) |
| `observability.beyla.context` | string |  | "ebpf_only" \| "coexist_sdk" (default ebpf_only) |
| `observability.beyla.features[]` | string |  | empty ⇒ beyla.DefaultFeatures(mode) |
| `pyroscope` | object | yes | Pyroscope enables the SDK-push continuous-profiling lane. Absent (nil) or Enabled=false ⇒ no profiling emission. Mode="scraped" ⇒ Alloy scrapes the service; we do not push profiles ourselves (Signals() omits PyroscopeProfiles so the runner never wires the sink). |
| `pyroscope.enabled` | bool |  |  |
| `pyroscope.mode` | string |  |  |
| `pyroscope.runtime` | string |  |  |
| `pyroscope.types[]` | string |  |  |
| `pyroscope.span_profiles` | bool |  |  |
| `otel` | object | yes | OTel enables the native OTLP application-metrics lane (http.server.* via /v1/metrics). Absent (nil) or Metrics=false ⇒ no OTLP-metrics emission (the workload keeps promrw span-metrics). |
| `otel.metrics` | bool |  | Metrics enables native OTLP application-metrics emission (http.server.* via /v1/metrics); false (default) ⇒ no OTLP-metrics emission. |
| `otel.mode` | string |  | "naked" (default) \| "k8s_monitoring" |
| `context` | string |  | Context / UseCase / Team are the §5 resource-attr canon (optional; AI blueprints only). Empty ⇒ OMITTED (I13). Context ∈ {Platform, ContentGen, DataGen}; Team is set per blueprint (blueprint-only identity). ⚠ This is the TOP-LEVEL context (the §5 canon) — distinct from BeylaObs.Context (ebpf_only\|coexist_sdk). |
| `use_case` | string |  |  |
| `team` | string |  |  |
| `version` | string |  | Version overrides the default service.version (released image-tag intent, §5). Empty ⇒ serviceVersion default. |

## Failure modes

**Location:** `incidents[].mode / scenarios[].effects[].mode`  ·  **group:** failure_modes

The valid `mode:` values an incident or scenario effect may reference (union across all registered kinds).

| key | type | optional | description |
|---|---|---|---|
| `agentcore_throttle` | axis: cloud |  | AgentCore request throttles + system_errors spike (region-scoped capacity constraint) |
| `bedrock_throttle` | axis: cloud |  | Bedrock invocation throttling climbs |
| `connection_saturation` | axis: database |  | active connections climb toward max |
| `cpu_hotspot` | axis: service |  | elevated CPU concentrated in a hot frame on the targeted service node |
| `cpu_hotspot` | axis: workload |  | elevated CPU concentrated in a hot frame (visible in process_cpu flamegraph) |
| `cpu_hotspot` | axis: cluster |  | elevated CPU concentrated in a hot frame on the cluster's profiled pods |
| `error_burst` | axis: workload |  | elevated 5xx error rate |
| `error_spike` | axis: service |  | elevated 5xx error rate on the targeted service node |
| `eval_quality_degraded` | axis: cloud |  | LangSmith eval quality regresses — faithfulness/completeness/relevance and retrieval scores drop while retry/fallback/HITL rates and error/pending run-outcomes climb |
| `eval_quality_regression` | axis: workload |  | online-eval quality regresses on the targeted ai_agent fleet — sigil_eval_score_values_total{passed=false} rate rises |
| `fallback_storm` | axis: service |  | elevated gateway fallback rate on the targeted service node |
| `goroutine_leak` | axis: service |  | goroutine accumulation on the targeted service node |
| `goroutine_leak` | axis: workload |  | goroutine accumulation — raises goroutines/goroutine profile sample values |
| `latency_spike` | axis: workload |  | elevated request latency (up to 4× at full intensity) |
| `latency_storm` | axis: service |  | elevated request latency on the targeted service node |
| `lock_contention` | axis: service |  | elevated mutex/block contention on the targeted service node |
| `lock_contention` | axis: workload |  | elevated mutex/block contention — raises mutex and block profile sample values |
| `lock_contention` | axis: database |  | lock waits climb |
| `memory_leak` | axis: workload |  | growing heap — raises memory inuse/alloc profile sample values |
| `memory_leak` | axis: service |  | growing heap on the targeted service node — raises memory inuse/alloc profile values |
| `nettopo_auth_failures` | axis: network |  | SNMP credential trials fail (credential_trials_total error rate rises) |
| `nettopo_devices_unreachable` | axis: network |  | SNMP polling fails for a fraction of devices (walk errors spike, device discovery drops) |
| `nettopo_discovery_slow` | axis: network |  | discovery cycle duration inflates (cycle_duration_seconds and module walk times rise) |
| `nettopo_spoke_down` | axis: network |  | a federation spoke goes offline (spoke_status=down, hub/spoke session metrics degrade) |
| `nettopo_walker_degraded` | axis: network |  | walker outcome errors climb; edge count under-reports (partial topology visibility) |
| `node_not_ready` | axis: cluster |  | a node flips NotReady; its pods go Pending |
| `oom_kill` | axis: cluster |  | containers OOM-killed; intensity selects fraction of pods affected (low intensity ⇒ a few pods); restart count climbs, status reason OOMKilled |
| `pod_crashloop` | axis: cluster |  | pods crash-looping; intensity selects fraction of pods affected (low intensity ⇒ a few pods); restarts climb, phase Pending not Running |
| `portkey_scrape_degraded` | axis: cloud |  | Portkey Analytics scrape degrades — API error_rate and 4xx/5xx share climb, latency rises, and the poller falls behind (poller errors + window lag grow) |
| `provider_call_error` | axis: workload |  | elevated provider/LLM call-error rate on the targeted ai_agent fleet — call_error generations, ERROR spans, error_type/error_category on operation_duration |
| `replication_lag` | axis: database |  | replica falls behind primary |
| `retry_storm` | axis: service |  | elevated gateway retry rate on the targeted service node |
| `slow_query_storm` | axis: database |  | query latency right-tail spikes |
| `throughput_drop` | axis: service |  | reduced throughput on the targeted service node |
| `web_vitals_degraded` | axis: service |  | browser web-vitals degrade on the targeted frontend node — LCP/INP/TTFB/FCP and CLS spike |

