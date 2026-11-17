---
title: Blueprint Schema Reference
description: Complete key-by-key reference for every field a synthkit blueprint may contain, generated from the Go structs.
---

# Blueprint Schema Reference

This reference is generated from the live Go types via `just gen` and reflects the schema enforced at load time. Decoding is strict: any key not listed here causes a loud load error. If you hit an unexpected load failure, check that your key spellings match exactly.

!!! info "Regenerating the schema"
    Run `just gen` to regenerate [BLUEPRINT-SCHEMA.md](https://github.com/rknightion/synthkit/blob/main/BLUEPRINT-SCHEMA.md) from the source types. The `TestSchemaCurrent` gate fails if the committed file drifts from the live types.

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
| `agent_observability` | object | no | Manual Agent Observability management declarations for `agento11y-provision`. Never enables runtime emission. See [agent_observability](#agent_observability). |


---

## agent_observability

Optional top-level block read only by the source-built
[agent-observability provisioning command](agent-observability-provisioning.md). The emitter ignores
it: loading a blueprint with this block does not install runtime admission or change emission.

| Key | Type | Required | Description |
|---|---|---|---|
| `closures[]` | object | yes | One closure per real agent owner. |
| `closures[].key` | string | yes | Blueprint-unique local key. |
| `closures[].agent.instance` | string | yes | Post-fanout workload instance that declares the agent. |
| `closures[].agent.agent_name` | string | yes | Declared agent name; it must have a real producer. |
| `closures[].evaluators[]`, `hooks[]`, `rules[]`, `collections[]`, `suites[]` | object | no | `{key, spec}` entries; `spec` uses the exact management API request fields. |
| `closures[].actions[]` | object | no | `{key, parent_rule, spec}`; `parent_rule` is a rule key in the same closure. |
| `closures[].suites[].draft` | object | yes, per suite | `{key, spec, cases[], bindings: {key, bindings[]}, publication: {key, spec}}`. |

Decoding is stricter than the rest of the blueprint: anchors, aliases, merge keys, duplicate or
case-variant keys, lossy numbers and trailing documents refuse. Scalars must resolve to plain
string, integer, float, boolean or null (explicit `!!str`/`!!int`/`!!float`/`!!bool`/`!!null` tags
are accepted but must match the field type), and `null` is kept only inside open case maps. Evaluator
`config` and hook/rule `match` also accept a JSON-object string; a hook's `blocked_tools` and list
form of `redact` are aliases for `tool_filter.blocked_names` and `redact.patterns`. The full field
list is in [BLUEPRINT-SCHEMA.md](https://github.com/rknightion/synthkit/blob/main/BLUEPRINT-SCHEMA.md).

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
| `cloud.cloudwatch` | object | no | `cw_infra` sub-family toggles. See [cw_infra config](#cw_infra-config-cloudcloudwatch) below. |
| `cloud.aoss` | object | no | Amazon OpenSearch Serverless config. Absent ⇒ not emitted. See [aoss config](#aoss-config). |
| `cloud.mwaa` | object | no | Amazon Managed Workflows for Apache Airflow config. Absent ⇒ not emitted. See [mwaa config](#mwaa-config). |
| `cloud.glue` | object | no | AWS Glue ETL config. Absent ⇒ not emitted. See [glue config](#glue-config). |
| `cloud.bedrock` | object | no | AWS Bedrock CloudWatch config. Absent ⇒ not emitted. See [bedrock config](#bedrock-config). |
| `cloud.agentcore` | object | no | AWS Bedrock AgentCore CloudWatch config. Absent ⇒ not emitted. See [agentcore config](#agentcore-config). |
| `cloud.lambda` | object | no | Function list and account concurrency; [captured config](#captured-cloudwatch-config), remote-write only. |
| `cloud.sqs` | object | no | Ordinary queue list; [captured config](#captured-cloudwatch-config), remote-write only. |
| `cloud.dynamodb` | object | no | Provisioned table list and account quotas; [captured config](#captured-cloudwatch-config), remote-write only. |
| `cloud.redshift` | object | no | Provisioned cluster snapshots, named queues and optional I/O latency; [captured config](#captured-cloudwatch-config), remote-write only. |
| `cloud.cloudwatch_logs` | object | no | Log-group uncompressed intake and optional independent Lambda delivery observations; [captured config](#captured-cloudwatch-config), remote-write only. |
| `cloud.scheduler` | object | no | EventBridge Scheduler fixed-rate schedules with an opt-in target-error mode; [captured config](#captured-cloudwatch-config), remote-write only. |

### Captured CloudWatch config

`cloud.lambda`, `cloud.sqs`, `cloud.dynamodb`, `cloud.redshift` and `cloud.cloudwatch_logs` are
optional strict-decoded objects; absent means no instance. They require `cloudwatch_export: remote_write` (the default) and explicit cloud identity.
Only one declaration per blueprint/account/region/kind is admitted, even across environments with
disjoint resources. See [authoring and value assumptions](blueprints.md#captured-cloudwatch-consumers)
and the [canonical emitted subset](https://github.com/rknightion/synthkit/blob/main/signals/cw.md#initial-captured-service-emitters-slug-cw-captured-emitted).
These engineering bounds are not AWS service quotas.

| Object | Accepted fields / constraints |
|---|---|
| `cloud.lambda` | `functions` (1..1000); `reserved_concurrency` (whole 0..1,000,000). |
| `functions[]` | `name` (unique unqualified 1..64 letters/digits/underscore/hyphen); `invocations_per_min`, `throttles_per_min`, `async_received_per_min`, `stream_batches_per_min` (0..1e9); `duration_ms` (0..60000); `async_age_ms`, `iterator_age_ms` (0..604800000); `error_fraction`, `async_dropped_fraction` (0..1); optional `duration_ms_min`/`duration_ms_max` (0..60000, declared together, bracketing `duration_ms`, which stays the mean) widen Duration Minimum/Maximum; optional `load: business` scales the four arrival rates by the business curve (default `constant`); with `load: business`, optional `duration_ms_idle` (positive, at most `duration_ms`) makes the mean Duration follow the same curve from that idle value up to `duration_ms` at the plateau, and any `duration_ms_min`/`duration_ms_max` bounds scale with it (omitted keeps constant duration); `duration_load: business` (requires `duration_ms_idle`) applies that duration curve without `load: business`, so a scheduled function keeps its constant invocations while its batch duration follows the curve. Total executions cover admitted async plus stream rates. Optional `zero_valued_samples: true` makes `Throttles` and `Errors` observe every request (executions plus throttled requests) with 0 or 1, as a lab capture reported, instead of only when an event occurs; default output is unchanged. |
| `cloud.sqs` | `queues` (1..64). |
| `queues[]` | Unique `name`, unique non-global `collector_name`; `arrivals_per_second`, `receives_per_second` (0..1e6); whole `message_bytes` (0..1048576, at least 1 for positive arrivals); `delay_seconds` (integer 0..900), `processing_seconds` (integer 0..43200); optional `load: business` scales arrivals by the business curve, with `receives_per_second` as the consumer throughput ceiling (default `constant`); optional `burst: {period, duty, phase}` (unbound queues only; `period` a Go duration 1s..168h, `duty` in (0,1], optional `phase` in [0,period), all epoch-aligned on the tick clock) delivers the same mean `arrivals_per_second` inside the first `duty` of each period, offset by `phase`, at `arrivals_per_second / duty` (at most 1e6) and none in the rest, so a backlog forms while that exceeds `receives_per_second` and drains after (default absent: arrivals evenly spread; needs positive arrivals); optional `enrichment`. |
| `queues[].enrichment` | Optional explicit `asserts_env`, `asserts_site`, `service`, `environment`, `owner`, `purpose`, `terraform`; the last four project `tag_*` labels, not dimensions. |
| `cloud.dynamodb` | `account`, `tables` (empty permits account-only snapshots). All numeric inputs finite/nonnegative and at most 1e9 unless constrained further. |
| `account` | Positive `max_reads`, `max_writes`, `max_table_reads`, `max_table_writes`; table maxima cannot exceed account maxima. |
| `tables[]` | Unique `name`, unique non-global `collector_name`; positive `provisioned_reads`, `provisioned_writes` within table/account quotas; optional `write_source`, `replication`, `conditional_failures_per_second`, `operations`, `stream`. |
| `operations[]` | Unique `name` (GetItem, PutItem, UpdateItem, Query, Scan, SELECT-only ExecuteStatement); `requests_per_second`, `read_units`, `write_units`, positive `latency_ms`, whole `returned_items`. Read operations require read units only; PutItem and UpdateItem write units only (UpdateItem emits only `SuccessfulRequestLatency` on TableName+Operation, takes no `throttled_requests_per_second` and is excluded from GlobalTable customer writes); returned items only for selection operations. Optional `load: business` (also on the table, where an operation's own `load` overrides it) makes `requests_per_second` the peak rate and scales it, and a GlobalTable's `replication.writes_per_second`, by the blueprint business curve (default `constant`); the Build capacity guard still applies to the peak. Optional `latency_min_ms` and `latency_max_ms` (both or neither, `latency_min_ms <= latency_ms <= latency_max_ms`) spread Minimum/Maximum around a mean that drifts per window; omitted keeps constant latency. Optional `throttled_requests_per_second` (GetItem or PutItem only, 0..1e9, follows the operation's load) adds throttled requests that consume no capacity and emit `ThrottledRequests` (TableName+Operation) plus table-level `ReadThrottleEvents` (GetItem) or `WriteThrottleEvents` (PutItem), one sample of 1 per throttled request and no zero samples; omitted emits none of the three. Optional `resource` (GetItem or PutItem only) binds the operation to a declared `quota` resource and requires `quota_unit` (`requests`, `read_units` for GetItem or `write_units` for PutItem; only with `resource`): the quota's admitted volume becomes the successful requests and its rejected volume the throttled requests, divided by the operation's `read_units` or `write_units` when the quota counts capacity units (one RCU is one strongly consistent or two eventually consistent reads of an item up to 4 KB, one WCU one write of an item up to 1 KB, per AWS). A bound operation omits `requests_per_second`, `throttled_requests_per_second` and `load`; one quota binds one operation; its `limit_rps`, in units, counts toward the provisioned-capacity guard. |
| `replication` | GlobalTable-only required `writes_per_second`, positive `units_per_write`; GlobalTable cannot also declare customer write operations. Demand must fit provisioned capacity. |
| `stream` | Nonblank `label`; `requests_per_second`, whole `records_per_request` (0..1000), whole `bytes_per_record`; positive `latency_ms`; response size at most 1 MiB. GetRecords operation is selected by this block, not authored. |
| `cloud.redshift` | `clusters` (1..100), no inferred inventory. |
| `clusters[]` (Redshift) | Unique unpadded `name` and unique non-global `collector_name`; required `snapshot`; optional `read_io`, `write_io`, `wlm_queues` (at most 100). |
| `snapshot` | All fields required: finite `cpu_percent`, `disk_used_percent` (0..100); whole `database_connections`, `total_tables` (0..1e9); boolean `healthy`, `maintenance`. Explicit zero/false is valid. |
| `read_io`, `write_io` | Finite `operations_per_second` (0..1e6, omitted=0); required finite `latency_seconds` ((0,3600]). Independent sparse completed-I/O assumptions, not throughput. |
| `wlm_queues[]` | Unique unpadded `name` within cluster; required whole `waiting`, `running` (0..1e9). |
| `cloud.cloudwatch_logs` | `log_groups` (1..100), no inferred inventory. |
| `log_groups[]` | Unique unpadded `name`; required finite `events_per_second` (0..1e6, explicit zero valid); required finite whole `uncompressed_bytes_per_event` (1..65536); optional `subscription_filters`. |
| `subscription_filters[]` | At most 100 total across log groups; unique unpadded `name` within group; required `destination_type: Lambda`; required `period_observation`. No target identity or joins. |
| `period_observation` | All four required, whole finite nonnegative: `delivery_errors`, `delivery_throttling`, `forwarded_log_events` (0..60000001), `forwarded_bytes` (0..3932160065536, already compressed). No defaults; explicit zero valid. One observation per active minute, four value stats=x, sample_count=1. |
| `cloud.scheduler` | `schedules` (1..1000), no inferred inventory. Same one-declaration-per-blueprint/account/region rule as the kinds above. |
| `schedules[]` | `name` (1..64 letters/digits/`.`/`_`/`-`, unique within its group); optional `group` (same character rule, default `default`); required `rate` as `rate(<whole value> minute(s)\|hour(s)\|day(s))`, period at most 366 days, no `cron()`/`at()`; optional `target_error_fraction` (0..1, default 0) makes that fraction of runs target errors, each also counted as dropped (no retries modelled). A function-side failure belongs on the target's `cloud.lambda` entry instead. |

Unspecified traffic is zero only where the Config permits omission; required Redshift/Logs
assumptions reject absence. No ESM/fair/info/native switches or cross-service joins exist in these
configs. Logs other delivery selectors/profiles and by-data-source, and Redshift
user-query/NodeID/managed-storage expansions remain withheld. The [generated schema](https://github.com/rknightion/synthkit/blob/main/BLUEPRINT-SCHEMA.md)
reflects the registered Config types; the loader and Build enforce semantic constraints.

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
| `databases[].resource` | string | no | Name of a declared `dependency` or `pool` resource (`environments[].resources`) that drives the database's CloudWatch lane: read and write latency scale with the resource's latency factor and, for a pool, `DatabaseConnections` follows its in-use connections. A bound database takes no legacy `database`-axis mode and still needs the CloudWatch lane. PostgreSQL/MySQL may also enable `observability.dbo11y`: both lanes agree on same-minute pool connections, and dbo11y statement-seconds increments plus existing synthetic query/wait durations follow mean latency without server Lock/IO or SQL-error attribution. DocumentDB/Neptune dbo11y remains unsupported. See [database bindings](blueprints.md#the-emission-switch). Supported for `postgres`, `mysql` and `docdb`; a pool-bound `docdb` cluster reports the pool's in-use connections on its `WRITER` row only. |
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
| `workloads[].type` | string | yes | Workload kind registry key (`web_service`, `app`, `ai_agent`). |
| `workloads[].name` | string | yes | Unique workload instance name. |
| `workloads[].runs_on` | string | yes | Cluster name this workload runs on. Resolved at load or fails. |
| `workloads[].replicas` | int | no | Pod count (default 2). |
| `workloads[].calls[]` | object | no | Downstream DB/cache hops. |
| `workloads[].calls[].db` | string | — | Name of a declared database this workload calls. |
| `workloads[].calls[].cache` | string | — | Name of a declared cache this workload calls. |
| `workloads[].<kind config>` | varies | — | Workload kind's own config. See [web_service config](#web_service-workload-config) and [app config](#app-workload-config) below; `ai_agent` fields are in the [generated schema](https://github.com/rknightion/synthkit/blob/main/BLUEPRINT-SCHEMA.md). |
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
| `services[].signals` | object | no | Per-node telemetry emission. Omitted block or keys leave all signals enabled. |
| `services[].signals.traces` | bool | no | Emit this node's own spans; false still passes W3C trace context to downstream nodes and does not remove a traced caller's CLIENT span. Must be true for an entry with `rum_faro` or a node with `agentic_flow`. |
| `services[].signals.logs` | bool | no | Emit this node's profile and inline app log streams. |
| `services[].signals.metrics` | bool | no | Emit this node's profile and inline app metrics and `target_info`; false also removes this node's native OTLP inline metrics when `otel.metrics: true`, but not trace-derived APM or its pods' infrastructure metrics. |
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
| `services[].logs[].when` | object | no | Writes the line only for requests whose per-request draw is listed. See [gated and dwelled log lines](workloads.md#gated-and-dwelled-log-lines). |
| `services[].logs[].when.request_draw` | object | — | An `enum` `request_draw` (`key`, `enum[]`; no `format`), with the same weights in the same order as every other `request_draw` of that `key` in the workload's logs. |
| `services[].logs[].when.in[]` | string | — | One or more of the enum's values, no repeats. |
| `services[].logs[].dwell_seconds` | value model | no | Seconds the request spent before this line; a bundle's earlier lines are backdated from its completion by their successors' dwells. Numeric models only (`const`, `int_range`, `float_range`, `normal`, `lognormal`); a draw is clamped to `[0, 86400]` s. Keep a bundle's summed dwell well under 1 h (Loki's out-of-order window); the sum is not checked at load. |
| `services[].spans[]` | object | no | Inline custom span attribute definitions (DSL). |
| `traffic` | object | no | Entry node invocation volume shaping. |
| `traffic.shape` | string | — | Shape profile name. |
| `traffic.off_peak_rps` | float | — | Trough rate. Default `5`. |
| `traffic.peak_rps` | float | — | Plateau rate. Default `50`. |
| `traffic.request_latency_p95_ms` | float | — | Base end-to-end latency p95. Default `0` ⇒ `200ms`. LLM/agentic apps should set this to seconds (e.g. `9000`). |
| `traffic.client_retry` | object | no | Entry-client re-send of a failed request as a new request of the same logical request (the logical request's per-request draws repeat). Graph apps only: rejected with `automation`, `agent_pipeline` and `traffic.from_resource`. Omitted re-sends nothing. See [entry-client retry](workloads.md#entry-client-retry). |
| `traffic.client_retry.retries` | int | — | Re-sends after the first attempt, `1` to `3`. |
| `traffic.client_retry.backoff_seconds` | float | — | Delay before the first re-send, in `(0, 600]`; doubled for each further re-send. |
| `traffic.client_retry.retry_on[]` | string | — | One or more of `5xx` (any 5xx status) and `throttle` (429); no repeats. |
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
| `shape.batch` | `{period, phase}` | Counter metric values only: releases the accumulated arrivals once per `period` (`(0, 24h]`), boundaries offset by `phase` (`[0, period)`), zero between releases. Not valid with `stock_of`, `count_of` or `incident_value` on the same metric. See [batch arrivals](workloads.md#batch-arrivals). |
| `now` | `{stall_mode, arrivals_only, period, phase}` | Gauge metric values only: the tick time in Unix seconds. `stall_mode` holds it while that incident mode is active; `arrivals_only` advances it only on ticks where the node's source had arrivals; `period` (`(0, 24h]`) and `phase` (`[0, period)`) read the last schedule boundary, held under a stall. An enum label value may add its own `stall_mode` (one label per metric). See [collector self-telemetry](workloads.md#collector-self-telemetry). |
| `source_read` | `{measure, per_tick, stall_mode}` | Counter metric values only. `measure` is `records_inspected`, `checkpoint` (both stop while stalled) or `read_failures` (only while stalled, needs a `stall_mode`); `per_tick` is finite and above 0 and is added per metric tick. See [collector self-telemetry](workloads.md#collector-self-telemetry). |
| `ref` | string | Correlation field reference (high-card). **Metric/stream labels reject this.** |

Two further metric keys derive a metric from a counter (see
[stock gauges](workloads.md#stock-gauges)): `stock_of` derives a gauge and replaces its `value`;
`count_of` sets how many values a histogram observes and keeps the histogram's `value`:

| Key | Type | Description |
|---|---|---|
| `stock_of.metric` | string | The counter to stock (not the gauge itself). Resolves every same-named counter spec on the node. |
| `stock_of.dwell_seconds` | float | Fluid stock: finite and above 0. Set exactly one of this and `stock_of.residence`. |
| `stock_of.residence` | `{median, p95}` | Item stock of whole items with a lognormal residence: `median > 0`, `p95 >= median`, finite. At most 100,000 items per series; arrivals above that are dropped. |
| `stock_of.stall_mode` | string | Item stock only: while that incident mode is active at intensity `i`, residences advance at `1 - i` of real time. |
| `stock_of.field` | string | Item stock only: `count` (default) or `oldest_age_seconds`. |
| `labels` (on a `stock_of` gauge) | map | Optional projection: each label must be a counter label with values the counter takes, and every combination must match a counter series. `value_scale` is rejected. Omitted inherits the counter's series. |
| `count_of` | string | Histogram only: the name of a counter on the same service node (or the node named by `count_of_service`); each tick the histogram observes as many values (drawn from its `value`) as that counter incremented, so `_count` tracks the counter. Labels must be a subset of the counter's, each value one it takes, and every combination must match a counter series. At most 10,000 observations per series per tick; a batched counter's whole-window release counts (rejected at load). See [histogram counts from a counter](workloads.md#stock-gauges). |
| `count_of_service` | string | With `count_of` only: the service node whose counter `count_of` reads. It must be declared before the histogram's service; unknown or later services are rejected at load. A source that is paused records nothing for the tick. |

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
| `runtimes[]` | object | Binds an agent's runtime to a causal resource: `{agent, resource}`. `agent` names an entry of `agents`; `resource` names a `concurrency` resource under `environments[].resources` (one runtime per resource). The runtime then emits its own `invocations`, `throttles`, `system_errors`, `total_errors` and `latency` series, named by the runtime ARN, from the resource model. Needs the `runtime` family. Absent ⇒ unchanged output. |

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
