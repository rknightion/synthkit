# CSP GCP (→ Mimir) — ScopeSubstrate

GCP Cloud Monitoring families exported via Alloy's `prometheus.exporter.gcp` (embeds `stackdriver_exporter`).
Substrate-scoped: disambiguated by `project_id` + per-service resource labels (e.g. `database_id`, `exported_instance`);
**never carries a `blueprint` label**. Global rules: see [`00-canon.md`](00-canon.md) — scoping `[slug: scoping]`,
cardinality `[slug: cardinality]`, shape rules `[slug: shape-rules]`.

---

## CSP GCP overview [slug: cspgcp]

*Provenance: predecessor SIGNALS §13 Lane D + `emit/cloud_gcp.go`+`cloud_shared.go`.*
Feature `cloud_gcp`; sub-signals `compute,databases,storage,networking,loadbalancing,pubsub,cloudrun,
bigtable,logs`. NO blueprint label. `job="integrations/gcp"` on every series.

Base labels: `job`, `project_id`, `unit` (⚠ ALWAYS present, even `"1"`). Name pattern
`stackdriver_<resource_type>_<metric_path_snake>` (dots/slashes → underscores).

> ⚠ **Metric-type mapping (CRITICAL):** GAUGE → `st.Set`; CUMULATIVE/DELTA → `st.Add` (counter);
> DISTRIBUTION → `st.Observe(BASE name, …)` — `state.Collect` appends `_bucket`/`_sum`/`_count`. Passing
> the literal `_bucket` name produces `_bucket_bucket`.

**Inventory (representative roots — full names in the emitter; all `stackdriver_…`).** ✅ **SK-18
resolved (live Monitoring REST capture):** resource-label shapes corrected — GCE `gce_instance` carries
resource labels `{project_id, instance_id (numeric string), zone}` + metric label `instance_name`; GCS
`gcs_bucket` carries `{location, project_id, bucket_name}` (api_request_count + metric labels
`method`/`response_code`); Cloud Run `cloud_run_revision` carries `{service_name, location,
revision_name, configuration_name, project_id}`; Cloud SQL `cloudsql_database` carries `{project_id,
database_id=<project>:<name>, region}`.

> ✅ **SK-18 fully resolved (live Alloy `prometheus.exporter.gcp` capture 2026-06-14, `job="integrations/gcp"`).**
> The authoritative scrape path is Alloy's `prometheus.exporter.gcp` (embeds `stackdriver_exporter`) — NOT the
> raw Monitoring REST API; the Prom forms below are what actually lands in Mimir.
> • **Bigtable** `…server_request_count` full live label set: `{exported_instance=<bt-instance>, cluster=<cluster-id>,
> table=<table>, app_profile="default", zone=<zone>, method, instance=<opaque scrape hash>, project_id, unit="1"}`.
> `method` enum is `Bigtable.<RPC>` — live-confirmed `Bigtable.ReadRows`, `Bigtable.MutateRow` (the fuller
> {MutateRows,CheckAndMutateRow,ReadModifyWriteRow,SampleRowKeys,ExecuteQuery} appear under the matching RPC traffic).
> • **AlloyDB** identity is resource labels `cluster_id=<cluster-name>` + `instance_id=<instance-name>` + `location`
> + `project_id` (e.g. `cluster_id="sk18-capture"`, `instance_id="sk18-primary"`, `location="us-central1"`); `instance`
> is an **opaque target hash**, and there is **NO `exported_instance`** for AlloyDB (that label is Bigtable-specific).
> • **Cloud SQL** `…database_instance_state` full live `state` enum: `{RUNNABLE, RUNNING, SUSPENDED, PENDING_CREATE,
> MAINTENANCE, FAILED, UNKNOWN_STATE}`; connection-state metrics carry `state` ∈ {`active`,`idle`}.

⚠ Enum-coverage traps: emit ALL queried enum values (Cloud SQL RUNNABLE+MAINTENANCE, HEALTHY+UNHEALTHY;
AlloyDB up+down; Cloud Run active+idle); Bigtable `exported_instance`; Pub/Sub `unacked_bytes_by_region`
mandatory; `unit` present even when `"1"`.

DISTRIBUTION buckets (LEBare) — ✅ **SK-17 resolved w/ correction (live Cloud Monitoring REST capture
2026-06-13):** Stackdriver DISTRIBUTIONs use **exponential** buckets `bound_i = scale·growthFactor^i`,
with params **PER-METRIC** (NOT one fixed explicit set). synthkit models three per-metric sets via
`expBuckets(scale,growth,n)`: Cloud Run `*_latencies` → `expBuckets(10, 1.1, 135)` (ms; captured from
`run.googleapis.com/request_latencies`); HTTPS-LB/Pub-Sub/Bigtable latencies → `expBuckets(1, 1.4, 66)`
(ms; captured from `loadbalancing.../https/total_latencies`); non-latency DISTRIBUTIONs (Cloud Run
cpu/memory/concurrency) → a representative `expBuckets(1, 1.4, 20)`. Unit is per-metric (Cloud Run
latency = ms). The former fixed `[0.005…10]` seconds set was wrong. Logs: `{job="integrations/gcp"}`, raw Cloud Logging JSON.

---

## OTLP receiver form — `googlecloudmonitoringreceiver` [slug: cspgcp-otlp-receiver]

*Provenance: OpenTelemetry Collector Contrib source at tag `v0.160.0` (tag object
`97a2cdd7876501c39b3b7cbac82025b1e41e0282`), `receiver/googlecloudmonitoringreceiver/`, read 2026-09-06.
Resolves the Google half of SK-88. The opt-in native lane below mirrors this source-derived
contract; it is not a claim of live receiver capture.*

This is the OTel-native form the verdict record names for `csp_gcp`. It is a **different namespace**
from the `stackdriver_*` scrape form above: the receiver sets the OTLP metric name to the Cloud
Monitoring metric type **verbatim** (`m.SetName(ts.GetMetric().GetType())`, all four converters in
`internal/metrics_conversion.go`), so a series leaves the collector as
`compute.googleapis.com/instance/cpu/utilization` with the domain and slashes intact.

| Field | Receiver behaviour | Source |
|---|---|---|
| name | Cloud Monitoring `metric.type`, byte-for-byte; no prefix, no `/`→`.`/`_` rewrite | `metrics_conversion.go:40,82,121,160` |
| instrument | GAUGE → Gauge; CUMULATIVE → Sum cumulative monotonic; DELTA → Sum delta non-monotonic; DELTA + DISTRIBUTION → Histogram delta with explicit bounds (GCP linear/exponential buckets converted); GAUGE/CUMULATIVE DISTRIBUTION dropped with a warning; no ExponentialHistogram, no Summary | `receiver.go:312-334`, `metrics_conversion.go:84-87,123-124,162-163,366-373` |
| unit | vendor unit string verbatim (`10^2.%`, `By`, `s`, …); no UCUM translation | `receiver.go:310`, `metrics_conversion.go:41,83,122,161` |
| description | `MetricDescriptor.description` verbatim | `receiver.go:309` |
| datapoint attributes | the metric labels, keys unprefixed, values as strings; resource/user/system labels never reach the datapoint | `metrics_conversion.go:291-298` |
| resource attributes | `gcp.resource_type` = MonitoredResource type; every MonitoredResource label, user label and system label under its bare key (`project_id`, `zone`, `instance_id`, …); **no** `cloud.provider`, `cloud.account.id` or other semconv key | `receiver.go:274-287` |
| scope | name and version both empty | `receiver.go:298`; golden `internal/testdata/TestConvertGaugeToMetrics_ValidGaugePoints.yaml:14` |
| value types | INT64/DOUBLE handled; BOOL → 1/0 on Gauge only; STRING/MONEY logged and left valueless | `metrics_conversion.go:63-75,108-114,145-151` |

Traps an emitter lane must carry: system labels are stringified with protobuf text format
(`string_value:"…"`) rather than the bare value (`receiver.go:286`); every TimeSeries produces its
own ResourceMetrics because the dedup map is rebuilt per call (`receiver.go:260-266`); component
stability is `alpha`, so re-read the source at the tag in use before implementing. Per-family kind,
valueType and unit come from the GCP metric list for each `MetricDescriptor`, never from the
`stackdriver_*` name.

### Native family source map

The native catalogue in `internal/construct/cspgcp/native_otlp.go` contains 115
source-confirmed non-Vertex families. The metric type, value type, unit, description,
and monitored-resource type come from the current Cloud Monitoring metric list pages:

| Families | Cloud Monitoring source |
|---|---|
| Compute Engine (`compute.googleapis.com/instance/*`) and Cloud SQL (`cloudsql.googleapis.com/database/*`) | [`metrics_gcp_c`](https://cloud.google.com/monitoring/api/metrics_gcp_c) |
| AlloyDB (`alloydb.googleapis.com/{instance,node,database,cluster}/*`) and Bigtable (`bigtable.googleapis.com/*`) | [`metrics_gcp_a_b`](https://cloud.google.com/monitoring/api/metrics_gcp_a_b) |
| Cloud Load Balancing (`loadbalancing.googleapis.com/https/*`) and Networking (`networking.googleapis.com/*`) | [`metrics_gcp_i_o`](https://cloud.google.com/monitoring/api/metrics_gcp_i_o) |
| Cloud Storage (`storage.googleapis.com/*`), Pub/Sub (`pubsub.googleapis.com/subscription/*`), and Cloud Run (`run.googleapis.com/container/*`) | [`metrics_gcp_p_z`](https://cloud.google.com/monitoring/api/metrics_gcp_p_z) |

The receiver drops GAUGE and CUMULATIVE distributions. Accordingly, the native lane
does not emit `run.googleapis.com/container/cpu/usage` or
`run.googleapis.com/container/memory/usage`; the current descriptors classify both as
GAUGE distributions. Vertex AI stays out of the native catalogue because its existing
names and labels remain `v: assumed` under [slug: cspgcp-vertex].

The native renderer uses the scrape lane's established Cloud Run revision identity for
the Cloud Run resource block. The descriptors below include `cloud_run_revision` in their
supported resource types; several also support jobs and worker pools. Environment-scoped
declarations retain their declared `env` as a bare resource user label and part of the
resource identity, never as a descriptor metric label. Aggregate declarations omit it.
AlloyDB's `node/postgres/uptime` is emitted as a native Gauge per
the descriptor even though the legacy scrape family is cumulative.

The table below is the per-family native contract. The resource type and metric-label keys are
copied from the Cloud Monitoring descriptor; the receiver passes labels through without a
prefix. `∅` means the descriptor has no unit or metric-label keys. The two rows whose native
unit is `us` are scaled from the legacy scrape lane's millisecond values before encoding.

<!-- cspgcp-otlp-contract:begin -->

| Native metric | Native OTLP type | Vendor kind (value type) | Unit | Resource type | Metric-label keys | Official Cloud Monitoring descriptor |
|---|---|---|---|---|---|---|
| `compute.googleapis.com/instance/cpu/utilization` | Gauge | GAUGE (DOUBLE) | `10^2.%` | `gce_instance` | `instance_name` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/cpu/utilization |
| `compute.googleapis.com/instance/cpu/usage_time` | Sum | DELTA (DOUBLE) | `s{CPU}` | `gce_instance` | `instance_name` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/cpu/usage_time |
| `compute.googleapis.com/instance/network/received_bytes_count` | Sum | DELTA (INT64) | `By` | `gce_instance` | `instance_name`, `loadbalanced` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/network/received_bytes_count |
| `compute.googleapis.com/instance/network/sent_bytes_count` | Sum | DELTA (INT64) | `By` | `gce_instance` | `instance_name`, `loadbalanced` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/network/sent_bytes_count |
| `compute.googleapis.com/instance/disk/read_bytes_count` | Sum | DELTA (INT64) | `By` | `gce_instance` | `instance_name`, `device_name`, `storage_type`, `device_type` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/disk/read_bytes_count |
| `compute.googleapis.com/instance/disk/write_bytes_count` | Sum | DELTA (INT64) | `By` | `gce_instance` | `instance_name`, `device_name`, `storage_type`, `device_type` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/disk/write_bytes_count |
| `compute.googleapis.com/instance/disk/read_ops_count` | Sum | DELTA (INT64) | `1` | `gce_instance` | `instance_name`, `device_name`, `storage_type`, `device_type` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/disk/read_ops_count |
| `compute.googleapis.com/instance/disk/write_ops_count` | Sum | DELTA (INT64) | `1` | `gce_instance` | `instance_name`, `device_name`, `storage_type`, `device_type` | https://cloud.google.com/monitoring/api/metrics_gcp_c#compute/instance/disk/write_ops_count |
| `cloudsql.googleapis.com/database/up` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/up |
| `cloudsql.googleapis.com/database/cpu/utilization` | Gauge | GAUGE (DOUBLE) | `10^2.%` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/cpu/utilization |
| `cloudsql.googleapis.com/database/memory/utilization` | Gauge | GAUGE (DOUBLE) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/memory/utilization |
| `cloudsql.googleapis.com/database/disk/utilization` | Gauge | GAUGE (DOUBLE) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/disk/utilization |
| `cloudsql.googleapis.com/database/available_for_failover` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/available_for_failover |
| `cloudsql.googleapis.com/database/cpu/reserved_cores` | Gauge | GAUGE (DOUBLE) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/cpu/reserved_cores |
| `cloudsql.googleapis.com/database/memory/quota` | Gauge | GAUGE (INT64) | `By` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/memory/quota |
| `cloudsql.googleapis.com/database/disk/quota` | Gauge | GAUGE (INT64) | `By` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/disk/quota |
| `cloudsql.googleapis.com/database/disk/read_ops_count` | Sum | DELTA (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/disk/read_ops_count |
| `cloudsql.googleapis.com/database/disk/write_ops_count` | Sum | DELTA (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/disk/write_ops_count |
| `cloudsql.googleapis.com/database/network/connections` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/network/connections |
| `cloudsql.googleapis.com/database/network/received_bytes_count` | Sum | DELTA (INT64) | `By` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/network/received_bytes_count |
| `cloudsql.googleapis.com/database/network/sent_bytes_count` | Sum | DELTA (INT64) | `By` | `cloudsql_database` | `destination` | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/network/sent_bytes_count |
| `cloudsql.googleapis.com/database/instance_state` | Gauge | GAUGE (BOOL) | ∅ | `cloudsql_database` | `state` | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/instance_state |
| `cloudsql.googleapis.com/database/replication/state` | Gauge | GAUGE (BOOL) | ∅ | `cloudsql_database` | `state` | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/replication/state |
| `cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_total` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/mysql/innodb_buffer_pool_pages_total |
| `cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_free` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/mysql/innodb_buffer_pool_pages_free |
| `cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_dirty` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/mysql/innodb_buffer_pool_pages_dirty |
| `cloudsql.googleapis.com/database/postgresql/num_backends` | Gauge | GAUGE (INT64) | `1` | `cloudsql_database` | `database` | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/postgresql/num_backends |
| `cloudsql.googleapis.com/database/postgresql/transaction_count` | Sum | DELTA (INT64) | `1` | `cloudsql_database` | `database`, `transaction_type` | https://cloud.google.com/monitoring/api/metrics_gcp_c#cloudsql/database/postgresql/transaction_count |
| `alloydb.googleapis.com/instance/postgres/instances` | Gauge | GAUGE (INT64) | `1` | `alloydb.googleapis.com/Instance` | `status` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgres/instances |
| `alloydb.googleapis.com/instance/cpu/average_utilization` | Gauge | GAUGE (DOUBLE) | `10^2.%` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/cpu/average_utilization |
| `alloydb.googleapis.com/instance/cpu/maximum_utilization` | Gauge | GAUGE (DOUBLE) | `10^2.%` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/cpu/maximum_utilization |
| `alloydb.googleapis.com/instance/postgresql/deadlock_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/deadlock_count |
| `alloydb.googleapis.com/instance/postgresql/deleted_tuples_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/deleted_tuples_count |
| `alloydb.googleapis.com/instance/postgresql/fetched_tuples_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/fetched_tuples_count |
| `alloydb.googleapis.com/instance/postgresql/inserted_tuples_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/inserted_tuples_count |
| `alloydb.googleapis.com/instance/postgresql/updated_tuples_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/updated_tuples_count |
| `alloydb.googleapis.com/instance/postgresql/written_tuples_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/written_tuples_count |
| `alloydb.googleapis.com/instance/postgresql/returned_tuples_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/returned_tuples_count |
| `alloydb.googleapis.com/instance/postgresql/new_connections_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/new_connections_count |
| `alloydb.googleapis.com/instance/postgres/total_connections` | Gauge | GAUGE (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgres/total_connections |
| `alloydb.googleapis.com/instance/postgres/transaction_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Instance` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgres/transaction_count |
| `alloydb.googleapis.com/database/postgresql/vacuum/oldest_transaction_age` | Gauge | GAUGE (INT64) | `1` | `alloydb.googleapis.com/Instance` | `type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/vacuum/oldest_transaction_age |
| `alloydb.googleapis.com/instance/postgresql/backends_for_top_applications` | Gauge | GAUGE (INT64) | `1` | `alloydb.googleapis.com/Instance` | `application_name` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/instance/postgresql/backends_for_top_applications |
| `alloydb.googleapis.com/node/postgres/wait_time` | Sum | DELTA (DOUBLE) | `us` | `alloydb.googleapis.com/InstanceNode` | `wait_event_type`, `wait_event_name` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/node/postgres/wait_time |
| `alloydb.googleapis.com/node/postgres/wait_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/InstanceNode` | `wait_event_type`, `wait_event_name` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/node/postgres/wait_count |
| `alloydb.googleapis.com/node/postgres/backends_by_state` | Gauge | GAUGE (INT64) | `1` | `alloydb.googleapis.com/InstanceNode` | `state` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/node/postgres/backends_by_state |
| `alloydb.googleapis.com/node/postgres/uptime` | Gauge | GAUGE (DOUBLE) | `1` | `alloydb.googleapis.com/InstanceNode` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/node/postgres/uptime |
| `alloydb.googleapis.com/database/postgresql/tuples` | Gauge | GAUGE (INT64) | `1` | `alloydb.googleapis.com/Database` | `state` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/tuples |
| `alloydb.googleapis.com/database/postgresql/blks_read_for_top_databases` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/blks_read_for_top_databases |
| `alloydb.googleapis.com/database/postgresql/blks_hit_for_top_databases` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/blks_hit_for_top_databases |
| `alloydb.googleapis.com/database/postgresql/temp_bytes_written_for_top_databases` | Sum | DELTA (INT64) | `By` | `alloydb.googleapis.com/Database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/temp_bytes_written_for_top_databases |
| `alloydb.googleapis.com/database/postgresql/temp_files_written_for_top_databases` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/temp_files_written_for_top_databases |
| `alloydb.googleapis.com/database/postgresql/rolledback_transactions_for_top_databases` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Database` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/rolledback_transactions_for_top_databases |
| `alloydb.googleapis.com/database/postgresql/statements_executed_count` | Sum | DELTA (INT64) | `1` | `alloydb.googleapis.com/Database` | `operation_type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/database/postgresql/statements_executed_count |
| `alloydb.googleapis.com/cluster/storage/usage` | Gauge | GAUGE (INT64) | `By` | `alloydb.googleapis.com/Cluster` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#alloydb/cluster/storage/usage |
| `storage.googleapis.com/storage/object_count` | Gauge | GAUGE (INT64) | `1` | `gcs_bucket` | `storage_class` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#storage/storage/object_count |
| `storage.googleapis.com/storage/total_bytes` | Gauge | GAUGE (DOUBLE) | `By` | `gcs_bucket` | `storage_class` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#storage/storage/total_bytes |
| `storage.googleapis.com/network/received_bytes_count` | Sum | DELTA (INT64) | `By` | `gcs_bucket` | `response_code`, `method` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#storage/network/received_bytes_count |
| `storage.googleapis.com/network/sent_bytes_count` | Sum | DELTA (INT64) | `By` | `gcs_bucket` | `response_code`, `method` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#storage/network/sent_bytes_count |
| `storage.googleapis.com/api/request_count` | Sum | DELTA (INT64) | `1` | `gcs_bucket` | `response_code`, `method` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#storage/api/request_count |
| `networking.googleapis.com/google_service/request_bytes_count` | Sum | DELTA (INT64) | `By` | `google_service_gce_client` | `protocol`, `response_code_class`, `service_name`, `service_region`, `local_network`, `local_subnetwork`, `local_network_interface` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#networking/google_service/request_bytes_count |
| `networking.googleapis.com/google_service/response_bytes_count` | Sum | DELTA (INT64) | `By` | `google_service_gce_client` | `protocol`, `response_code_class`, `service_name`, `service_region`, `local_network`, `local_subnetwork`, `local_network_interface` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#networking/google_service/response_bytes_count |
| `networking.googleapis.com/fixed_standard_tier/usage` | Gauge | GAUGE (INT64) | `By` | `networking.googleapis.com/Location` | `bandwidth_policy_id`, `traffic_source` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#networking/fixed_standard_tier/usage |
| `networking.googleapis.com/vpn_tunnel/egress_bytes_count` | Sum | DELTA (INT64) | `By` | `vpn_tunnel` | `local_project_number`, `local_project_id`, `local_region`, `local_zone`, `local_location_type`, `local_resource_type`, `local_network`, `local_subnetwork`, `protocol` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#networking/vpn_tunnel/egress_bytes_count |
| `networking.googleapis.com/vpn_tunnel/ingress_bytes_count` | Sum | DELTA (INT64) | `By` | `vpn_tunnel` | `local_project_number`, `local_project_id`, `local_region`, `local_zone`, `local_location_type`, `local_resource_type`, `local_network`, `local_subnetwork`, `protocol` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#networking/vpn_tunnel/ingress_bytes_count |
| `loadbalancing.googleapis.com/https/request_count` | Sum | DELTA (INT64) | `1` | `https_lb_rule` | `protocol`, `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result`, `client_country` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/request_count |
| `loadbalancing.googleapis.com/https/request_bytes_count` | Sum | DELTA (INT64) | `By` | `https_lb_rule` | `protocol`, `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result`, `client_country` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/request_bytes_count |
| `loadbalancing.googleapis.com/https/response_bytes_count` | Sum | DELTA (INT64) | `By` | `https_lb_rule` | `protocol`, `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result`, `client_country` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/response_bytes_count |
| `loadbalancing.googleapis.com/https/backend_request_bytes_count` | Sum | DELTA (INT64) | `By` | `https_lb_rule` | `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/backend_request_bytes_count |
| `loadbalancing.googleapis.com/https/backend_response_bytes_count` | Sum | DELTA (INT64) | `By` | `https_lb_rule` | `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/backend_response_bytes_count |
| `loadbalancing.googleapis.com/https/total_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `https_lb_rule` | `protocol`, `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result`, `client_country` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/total_latencies |
| `loadbalancing.googleapis.com/https/frontend_tcp_rtt` | Histogram | DELTA (DISTRIBUTION) | `ms` | `https_lb_rule` | `load_balancing_scheme`, `proxy_continent`, `client_country` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/frontend_tcp_rtt |
| `loadbalancing.googleapis.com/https/backend_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `https_lb_rule` | `protocol`, `response_code`, `load_balancing_scheme`, `response_code_class`, `proxy_continent`, `cache_result`, `client_country` | https://cloud.google.com/monitoring/api/metrics_gcp_i_o#loadbalancing/https/backend_latencies |
| `pubsub.googleapis.com/subscription/push_request_count` | Sum | DELTA (INT64) | `1` | `pubsub_subscription` | `response_class`, `response_code`, `delivery_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/push_request_count |
| `pubsub.googleapis.com/subscription/pull_ack_request_count` | Sum | DELTA (INT64) | `1` | `pubsub_subscription` | `response_class`, `response_code` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/pull_ack_request_count |
| `pubsub.googleapis.com/subscription/streaming_pull_response_count` | Sum | DELTA (INT64) | `1` | `pubsub_subscription` | `response_class`, `response_code` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/streaming_pull_response_count |
| `pubsub.googleapis.com/subscription/expired_ack_deadlines_count` | Sum | DELTA (INT64) | `1` | `pubsub_subscription` | `delivery_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/expired_ack_deadlines_count |
| `pubsub.googleapis.com/subscription/num_outstanding_messages` | Gauge | GAUGE (INT64) | `1` | `pubsub_subscription` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/num_outstanding_messages |
| `pubsub.googleapis.com/subscription/num_undelivered_messages` | Gauge | GAUGE (INT64) | `1` | `pubsub_subscription` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/num_undelivered_messages |
| `pubsub.googleapis.com/subscription/oldest_unacked_message_age` | Gauge | GAUGE (INT64) | `s` | `pubsub_subscription` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/oldest_unacked_message_age |
| `pubsub.googleapis.com/subscription/delivery_latency_health_score` | Gauge | GAUGE (BOOL) | `1` | `pubsub_subscription` | `criteria` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/delivery_latency_health_score |
| `pubsub.googleapis.com/subscription/num_unacked_messages_by_region` | Gauge | GAUGE (INT64) | `1` | `pubsub_subscription` | `region` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/num_unacked_messages_by_region |
| `pubsub.googleapis.com/subscription/unacked_bytes_by_region` | Gauge | GAUGE (INT64) | `By` | `pubsub_subscription` | `region` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/unacked_bytes_by_region |
| `pubsub.googleapis.com/subscription/push_request_latencies` | Histogram | DELTA (DISTRIBUTION) | `us` | `pubsub_subscription` | `response_code`, `delivery_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#pubsub/subscription/push_request_latencies |
| `run.googleapis.com/container/containers` | Gauge | GAUGE (INT64) | `1` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | `container_name`, `state` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/containers |
| `run.googleapis.com/container/network/received_bytes_count` | Sum | DELTA (INT64) | `By` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | `kind` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/network/received_bytes_count |
| `run.googleapis.com/container/network/sent_bytes_count` | Sum | DELTA (INT64) | `By` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | `kind` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/network/sent_bytes_count |
| `run.googleapis.com/container/billable_instance_time` | Sum | DELTA (DOUBLE) | `s` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/billable_instance_time |
| `run.googleapis.com/container/network/throttled_inbound_bytes_count` | Sum | DELTA (INT64) | `By` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | `network`, `transport`, `type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/network/throttled_inbound_bytes_count |
| `run.googleapis.com/container/network/throttled_outbound_bytes_count` | Sum | DELTA (INT64) | `By` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | `network`, `transport`, `type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/network/throttled_outbound_bytes_count |
| `run.googleapis.com/container/completed_probe_attempt_count` | Sum | DELTA (INT64) | `1` | `cloud_run_revision cloud_run_worker_pool` | `probe_action`, `is_healthy`, `container_name`, `is_default`, `probe_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/completed_probe_attempt_count |
| `run.googleapis.com/container/completed_probe_count` | Sum | DELTA (INT64) | `1` | `cloud_run_revision cloud_run_worker_pool` | `probe_action`, `is_healthy`, `container_name`, `is_default`, `probe_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/completed_probe_count |
| `run.googleapis.com/container/max_request_concurrencies` | Histogram | DELTA (DISTRIBUTION) | `1` | `cloud_run_revision` | `state` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/max_request_concurrencies |
| `run.googleapis.com/container/startup_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `cloud_run_job cloud_run_revision cloud_run_worker_pool` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/startup_latencies |
| `run.googleapis.com/container/probe_attempt_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `cloud_run_revision cloud_run_worker_pool` | `probe_action`, `is_healthy`, `container_name`, `is_default`, `probe_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/probe_attempt_latencies |
| `run.googleapis.com/container/probe_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `cloud_run_revision cloud_run_worker_pool` | `probe_action`, `is_healthy`, `container_name`, `is_default`, `probe_type` | https://cloud.google.com/monitoring/api/metrics_gcp_p_z#run/container/probe_latencies |
| `bigtable.googleapis.com/cluster/node_count` | Gauge | GAUGE (INT64) | `1` | `bigtable_cluster` | `storage_type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/cluster/node_count |
| `bigtable.googleapis.com/cluster/cpu_load` | Gauge | GAUGE (DOUBLE) | `1` | `bigtable_cluster` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/cluster/cpu_load |
| `bigtable.googleapis.com/cluster/cpu_load_hottest_node` | Gauge | GAUGE (DOUBLE) | `1` | `bigtable_cluster` | ∅ | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/cluster/cpu_load_hottest_node |
| `bigtable.googleapis.com/cluster/storage_utilization` | Gauge | GAUGE (DOUBLE) | `1` | `bigtable_cluster` | `storage_type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/cluster/storage_utilization |
| `bigtable.googleapis.com/disk/bytes_used` | Gauge | GAUGE (INT64) | `By` | `bigtable_cluster` | `storage_type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/disk/bytes_used |
| `bigtable.googleapis.com/disk/storage_capacity` | Gauge | GAUGE (INT64) | `By` | `bigtable_cluster` | `storage_type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/disk/storage_capacity |
| `bigtable.googleapis.com/cluster/cpu_load_by_app_profile_by_method_by_table` | Gauge | GAUGE (DOUBLE) | `1` | `bigtable_cluster` | `app_profile`, `method`, `table` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/cluster/cpu_load_by_app_profile_by_method_by_table |
| `bigtable.googleapis.com/table/bytes_used` | Gauge | GAUGE (INT64) | `By` | `bigtable_table` | `storage_type` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/table/bytes_used |
| `bigtable.googleapis.com/server/data_boost/spu_usage` | Gauge | GAUGE (INT64) | `1` | `bigtable_table` | `app_profile`, `method` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/data_boost/spu_usage |
| `bigtable.googleapis.com/server/returned_rows_count` | Sum | DELTA (INT64) | `1` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/returned_rows_count |
| `bigtable.googleapis.com/server/modified_rows_count` | Sum | DELTA (INT64) | `1` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/modified_rows_count |
| `bigtable.googleapis.com/server/sent_bytes_count` | Sum | DELTA (INT64) | `By` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/sent_bytes_count |
| `bigtable.googleapis.com/server/received_bytes_count` | Sum | DELTA (INT64) | `By` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/received_bytes_count |
| `bigtable.googleapis.com/server/error_count` | Sum | DELTA (INT64) | `1` | `bigtable_table` | `method`, `error_code`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/error_count |
| `bigtable.googleapis.com/server/multi_cluster_failovers_count` | Sum | DELTA (INT64) | `1` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/multi_cluster_failovers_count |
| `bigtable.googleapis.com/server/request_count` | Sum | DELTA (INT64) | `1` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/request_count |
| `bigtable.googleapis.com/server/latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `bigtable_table` | `method`, `app_profile` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/server/latencies |
| `bigtable.googleapis.com/client/operation_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `bigtable_table` | `method`, `app_profile`, `streaming`, `status`, `client_name` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/client/operation_latencies |
| `bigtable.googleapis.com/client/attempt_latencies` | Histogram | DELTA (DISTRIBUTION) | `ms` | `bigtable_table` | `method`, `app_profile`, `streaming`, `status`, `client_name` | https://cloud.google.com/monitoring/api/metrics_gcp_a_b#bigtable/client/attempt_latencies |

<!-- cspgcp-otlp-contract:end -->

---

## Compute — `stackdriver_gce_instance_*` ✅ [slug: cspgcp-compute]

Compute (`gce_instance`; +`instance_id,instance_name,zone`): `…instance_cpu_utilization` (G), `…instance_cpu_usage_time`
(C), `…instance_network_{received,sent}_bytes_count` (C), `…instance_disk_{read,write}_bytes_count` (C),
`…instance_disk_{read,write}_ops_count` (C).

```yaml signals
family: stackdriver_gce_instance
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  instance_id: <numeric-string>
  instance_name: <instance-name>
  zone: <gcp-zone>
  unit: <unit|"1">
metrics:
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_cpu_usage_time, type: counter, unit: seconds, v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_network_received_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_network_sent_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_ops_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_ops_count, type: counter, unit: count, v: ok}
```

---

## Cloud SQL — `stackdriver_cloudsql_database_*` ✅ [slug: cspgcp-cloudsql]

Cloud SQL (`cloudsql_database`; +`instance, database_id="<project>:<instance>", region`): `…database_up` (G anchor), `…database_cpu_utilization`,
`…database_memory_utilization`, `…database_disk_utilization`, `…database_available_for_failover`,
`…database_cpu_reserved_cores`, `…database_memory_quota`, `…database_disk_quota`,
`…database_disk_{read,write}_ops_count` (C), `…database_network_connections` (G),
`…database_network_{received,sent}_bytes_count` (C), `…database_instance_state` (G; ⚠ emit BOTH
`state="RUNNABLE"` AND `state="MAINTENANCE"`), `…database_replication_state` (G; ⚠ BOTH
`HEALTHY`+`UNHEALTHY`). MySQL-only: `…mysql_innodb_buffer_pool_pages_{total,free,dirty}`. Postgres-only:
`…postgresql_num_backends` (G), `…postgresql_transaction_count` (C).

Full live `state` enum for `…database_instance_state`: `{RUNNABLE, RUNNING, SUSPENDED, PENDING_CREATE, MAINTENANCE, FAILED, UNKNOWN_STATE}`.
Connection-state metrics carry `state` ∈ {`active`,`idle`}.

```yaml signals
family: stackdriver_cloudsql_database
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  instance: <instance-name>
  database_id: "<project>:<instance>"
  region: <gcp-region>
  unit: <unit|"1">
  state: RUNNABLE|RUNNING|SUSPENDED|PENDING_CREATE|MAINTENANCE|FAILED|UNKNOWN_STATE  # database_instance_state
metrics:
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up, type: gauge, unit: bool, v: ok, note: anchor}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_available_for_failover, type: gauge, unit: bool, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_reserved_cores, type: gauge, unit: count, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_quota, type: gauge, unit: bytes, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_quota, type: gauge, unit: bytes, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_read_ops_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_write_ops_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_connections, type: gauge, unit: count, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_received_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_sent_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_instance_state, type: gauge, unit: "1", v: ok, note: "⚠ emit BOTH RUNNABLE+MAINTENANCE; full enum: RUNNABLE/RUNNING/SUSPENDED/PENDING_CREATE/MAINTENANCE/FAILED/UNKNOWN_STATE"}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_replication_state, type: gauge, unit: "1", v: ok, note: "⚠ emit BOTH HEALTHY+UNHEALTHY"}
  # MySQL-only
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_total, type: gauge, unit: count, v: ok, note: MySQL-only}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_free, type: gauge, unit: count, v: ok, note: MySQL-only}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_dirty, type: gauge, unit: count, v: ok, note: MySQL-only}
  # Postgres-only
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_postgresql_num_backends, type: gauge, unit: count, v: ok, note: Postgres-only}
  - {root: stackdriver_cloudsql_database_cloudsql_googleapis_com_database_postgresql_transaction_count, type: counter, unit: count, v: ok, note: Postgres-only}
```

---

## AlloyDB — `stackdriver_alloydb_*` ✅ [slug: cspgcp-alloydb]

AlloyDB (`alloydb.googleapis.com`; +`instance_id, cluster_id, location`): INSTANCES gauge
`…instance_postgres_instances` (⚠ emit BOTH `status="up"` + `status="down"`); `…instance_cpu_{average,
maximum}_utilization` (G); `…instance_postgresql_{deadlock,deleted_tuples,fetched_tuples,
inserted_tuples,updated_tuples,written_tuples,returned_tuples,new_connections}_count` (C);
`…instance_postgres_total_connections` (G); `…instance_postgres_transaction_count` (C);
`…database_postgresql_vacuum_oldest_transaction_age` (G); `…instance_postgresql_backends_for_top_applications`
(G; +`application_name`); node `…node_postgres_wait_{time,count}` (C; +`wait_event_name,wait_event_type`),
`…node_postgres_backends_by_state` (G), `…node_postgres_uptime` (C); Database
`…database_postgresql_tuples` (G; +`database, state` ∈ {live,dead} — anchor),
`…database_postgresql_{blks_read,blks_hit,temp_bytes_written,temp_files_written,
rolledback_transactions}_for_top_databases` (C), `…database_postgresql_statements_executed_count` (C);
Cluster `…cluster_storage_usage` (G).

AlloyDB identity: resource labels `cluster_id=<cluster-name>` + `instance_id=<instance-name>` + `location` + `project_id`
(e.g. `cluster_id="sk18-capture"`, `instance_id="sk18-primary"`, `location="us-central1"`); `instance` is an
**opaque target hash**, and there is **NO `exported_instance`** for AlloyDB (that label is Bigtable-specific).

```yaml signals
family: stackdriver_alloydb
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  cluster_id: <cluster-name>
  instance_id: <instance-name>
  location: <gcp-region>
  unit: <unit|"1">
  status: up|down                         # instance_postgres_instances
  application_name: <app>                 # instance_postgresql_backends_for_top_applications
  wait_event_name: <event>                # node_postgres_wait_*
  wait_event_type: <type>                 # node_postgres_wait_*
  database: <db-name>                     # database_postgresql_tuples
  state: live|dead                        # database_postgresql_tuples
metrics:
  # Instance-level
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_instances, type: gauge, unit: count, v: ok, note: "⚠ emit BOTH status=up + status=down"}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_cpu_average_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_cpu_maximum_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_deadlock_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_deleted_tuples_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_fetched_tuples_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_inserted_tuples_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_updated_tuples_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_written_tuples_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_returned_tuples_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_new_connections_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_total_connections, type: gauge, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_transaction_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_backends_for_top_applications, type: gauge, unit: count, v: ok, note: +application_name}
  # Node-level
  - {root: stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_time, type: counter, unit: seconds, v: ok, note: +wait_event_name,wait_event_type}
  - {root: stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_count, type: counter, unit: count, v: ok, note: +wait_event_name,wait_event_type}
  - {root: stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_backends_by_state, type: gauge, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_uptime, type: counter, unit: seconds, v: ok}
  # Database-level (instance resource type, database metric)
  - {root: stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_database_postgresql_vacuum_oldest_transaction_age, type: gauge, unit: count, v: ok}
  # Database-level (database resource type)
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_tuples, type: gauge, unit: count, v: ok, note: "anchor; +database, state∈{live,dead}"}
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_blks_read_for_top_databases, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_blks_hit_for_top_databases, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_temp_bytes_written_for_top_databases, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_temp_files_written_for_top_databases, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_rolledback_transactions_for_top_databases, type: counter, unit: count, v: ok}
  - {root: stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_statements_executed_count, type: counter, unit: count, v: ok}
  # Cluster-level
  - {root: stackdriver_alloydb_googleapis_com_cluster_alloydb_googleapis_com_cluster_storage_usage, type: gauge, unit: bytes, v: ok}
```

---

## Storage — `stackdriver_gcs_bucket_*` ✅ [slug: cspgcp-storage]

Storage (`gcs_bucket`; +`bucket_name`): `…storage_object_count` (G anchor), `…storage_total_bytes` (G),
`…network_{received,sent}_bytes_count` (C), `…api_request_count` (C).

```yaml signals
family: stackdriver_gcs_bucket
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  location: <gcp-region>
  bucket_name: <bucket>
  unit: <unit|"1">
  method: <http-method>                   # api_request_count
  response_code: <code>                   # api_request_count
metrics:
  - {root: stackdriver_gcs_bucket_storage_googleapis_com_storage_object_count, type: gauge, unit: count, v: ok, note: anchor}
  - {root: stackdriver_gcs_bucket_storage_googleapis_com_storage_total_bytes, type: gauge, unit: bytes, v: ok}
  - {root: stackdriver_gcs_bucket_storage_googleapis_com_network_received_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_gcs_bucket_storage_googleapis_com_network_sent_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_gcs_bucket_storage_googleapis_com_api_request_count, type: counter, unit: count, v: ok, note: +method/response_code}
```

---

## Networking — `stackdriver_gce_*` / `stackdriver_vpn_*` ✅ [slug: cspgcp-networking]

Networking (three resource types):
`…google_service_response_bytes_count`/`_request_bytes_count` (C; `local_resource_type="vm"`),
`…location_…fixed_standard_tier_usage` (C), `…vpn_tunnel_{egress,ingress}_bytes_count` (C;
`local_resource_type="vpn_tunnel"`).

```yaml signals
family: stackdriver_gce_networking
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  unit: <unit|"1">
  local_resource_type: vm|vpn_tunnel
metrics:
  - {root: stackdriver_google_service_gce_client_networking_googleapis_com_google_service_response_bytes_count, type: counter, unit: bytes, v: ok, note: "local_resource_type=vm"}
  - {root: stackdriver_google_service_gce_client_networking_googleapis_com_google_service_request_bytes_count, type: counter, unit: bytes, v: ok, note: "local_resource_type=vm"}
  - {root: stackdriver_networking_googleapis_com_location_networking_googleapis_com_fixed_standard_tier_usage, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_egress_bytes_count, type: counter, unit: bytes, v: ok, note: "local_resource_type=vpn_tunnel"}
  - {root: stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_ingress_bytes_count, type: counter, unit: bytes, v: ok, note: "local_resource_type=vpn_tunnel"}
```

---

## Load Balancing — `stackdriver_https_lb_rule_*` ✅ [slug: cspgcp-loadbalancing]

Load Balancing (`https_lb_rule`; +`client_country,
backend_target_name`): `…https_request_count` (C anchor), `…https_{request,response,backend_request,
backend_response}_bytes_count` (C), `…https_total_latencies`/`_frontend_tcp_rtt`/`_backend_latencies`
(DISTRIBUTION, ms).

DISTRIBUTION bucket scheme: HTTPS-LB latencies → `expBuckets(1, 1.4, 66)` (ms).

```yaml signals
family: stackdriver_https_lb_rule
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  client_country: <country>
  backend_target_name: <target>
  unit: <unit|"1">
metrics:
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_count, type: counter, unit: count, v: ok, note: anchor}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_response_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_request_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_response_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_total_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_frontend_tcp_rtt, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
  - {root: stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
```

---

## Pub/Sub — `stackdriver_pubsub_subscription_*` ✅ [slug: cspgcp-pubsub]

Pub/Sub (`pubsub_subscription`; +`subscription_id`): `…subscription_push_request_count`
(C), `…pull_ack_request_count` (C), `…streaming_pull_response_count` (C), `…expired_ack_deadlines_count`
(C), `…num_outstanding_messages` (G), `…num_undelivered_messages` (G), `…oldest_unacked_message_age` (G),
`…delivery_latency_health_score` (G), `…num_unacked_messages_by_region` (G anchor),
`…unacked_bytes_by_region` (G; ⚠ MUST be emitted — variable anchor), `…push_request_latencies`
(DISTRIBUTION, ms).

DISTRIBUTION bucket scheme: Pub/Sub latencies → `expBuckets(1, 1.4, 66)` (ms).

```yaml signals
family: stackdriver_pubsub_subscription
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  subscription_id: <subscription>
  unit: <unit|"1">
metrics:
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_pull_ack_request_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_streaming_pull_response_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_expired_ack_deadlines_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_outstanding_messages, type: gauge, unit: count, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_undelivered_messages, type: gauge, unit: count, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_oldest_unacked_message_age, type: gauge, unit: seconds, v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_delivery_latency_health_score, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_unacked_messages_by_region, type: gauge, unit: count, v: ok, note: anchor}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_unacked_bytes_by_region, type: gauge, unit: bytes, v: ok, note: "⚠ MUST be emitted — variable anchor"}
  - {root: stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
```

---

## Cloud Run — `stackdriver_cloud_run_revision_*` ✅ [slug: cspgcp-cloudrun]

Cloud Run (`cloud_run_revision`; +`service_name,revision_name,container_name`):
`…container_containers` (G; ⚠ emit BOTH `state="active"` + `state="idle"`),
`…container_network_{received,sent}_bytes_count` (C), `…container_billable_instance_time` (C),
`…container_network_throttled_{inbound,outbound}_bytes_count` (C), `…container_completed_probe_{attempt_,}count`
(C), `…container_cpu_usage`/`_memory_usage`/`_max_request_concurrencies`/`_startup_latencies`/
`_probe_attempt_latencies` (+`probe_type,probe_action,is_healthy`)/`_probe_latencies` (DISTRIBUTION).

DISTRIBUTION bucket schemes: Cloud Run `*_latencies` → `expBuckets(10, 1.1, 135)` (ms);
non-latency DISTRIBUTIONs (cpu/memory/concurrency) → `expBuckets(1, 1.4, 20)`.

```yaml signals
family: stackdriver_cloud_run_revision
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  service_name: <service>
  location: <gcp-region>
  revision_name: <revision>
  configuration_name: <config>
  container_name: <container>
  unit: <unit|"1">
  state: active|idle                      # container_containers
  probe_type: <type>                      # probe_attempt_latencies
  probe_action: <action>                  # probe_attempt_latencies
  is_healthy: <bool>                      # probe_attempt_latencies
metrics:
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_containers, type: gauge, unit: count, v: ok, note: "⚠ emit BOTH state=active + state=idle"}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_network_received_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_network_sent_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_billable_instance_time, type: counter, unit: seconds, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_inbound_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_outbound_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_attempt_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_cpu_usage, type: histogram, unit: "1", v: ok, note: "exponential buckets expBuckets(1,1.4,20)"}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_memory_usage, type: histogram, unit: bytes, v: ok, note: "exponential buckets expBuckets(1,1.4,20)"}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_max_request_concurrencies, type: histogram, unit: count, v: ok, note: "exponential buckets expBuckets(1,1.4,20)"}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_startup_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(10,1.1,135)"}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_probe_attempt_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(10,1.1,135); +probe_type,probe_action,is_healthy"}
  - {root: stackdriver_cloud_run_revision_run_googleapis_com_container_probe_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(10,1.1,135)"}
```

---

## Bigtable — `stackdriver_bigtable_cluster_*` / `stackdriver_bigtable_table_*` ✅ [slug: cspgcp-bigtable]

Bigtable (`bigtable_cluster`+`bigtable_table`; ⚠ uses **`exported_instance`** NOT `instance`):
cluster `…cluster_node_count` (G anchor), `…cluster_cpu_load`, `…cluster_cpu_load_hottest_node`,
`…cluster_storage_utilization`, `…disk_bytes_used`, `…disk_storage_capacity`,
`…cluster_cpu_load_by_app_profile_by_method_by_table` (+`app_profile,method,table`); table
`…table_bytes_used` (G), `…server_data_boost_spu_usage` (G), `…server_{returned,modified}_rows_count`
(C), `…server_{sent,received}_bytes_count` (C), `…server_error_count` (C),
`…server_multi_cluster_failovers_count` (C), `…server_request_count` (C; per `method`),
`…server_latencies`/`…client_operation_latencies`/`…client_attempt_latencies` (DISTRIBUTION). Bigtable
`method` enum: `Bigtable.{ReadRows,MutateRow,MutateRows,CheckAndMutateRow,ReadModifyWriteRow,
SampleRowKeys,ExecuteQuery}`.

Full live label set for `…server_request_count`: `{exported_instance=<bt-instance>, cluster=<cluster-id>,
table=<table>, app_profile="default", zone=<zone>, method, instance=<opaque scrape hash>, project_id, unit="1"}`.

DISTRIBUTION bucket scheme: Bigtable latencies → `expBuckets(1, 1.4, 66)` (ms).

```yaml signals
family: stackdriver_bigtable
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  exported_instance: <bt-instance>        # ⚠ NOT `instance` — Bigtable-specific
  cluster: <cluster-id>
  zone: <gcp-zone>
  instance: <opaque-scrape-hash>          # opaque target hash from Alloy scrape
  unit: <unit|"1">
  # table-level additional labels:
  table: <table>
  app_profile: default|<profile>
  method: "Bigtable.ReadRows|Bigtable.MutateRow|Bigtable.MutateRows|Bigtable.CheckAndMutateRow|Bigtable.ReadModifyWriteRow|Bigtable.SampleRowKeys|Bigtable.ExecuteQuery"
metrics:
  # Cluster-level
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_node_count, type: gauge, unit: count, v: ok, note: anchor}
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_hottest_node, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_storage_utilization, type: gauge, unit: "1", v: ok}
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_bytes_used, type: gauge, unit: bytes, v: ok}
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_storage_capacity, type: gauge, unit: bytes, v: ok}
  - {root: stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_by_app_profile_by_method_by_table, type: gauge, unit: "1", v: ok, note: +app_profile,method,table}
  # Table-level
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_table_bytes_used, type: gauge, unit: bytes, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_data_boost_spu_usage, type: gauge, unit: count, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_returned_rows_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_modified_rows_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_sent_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_received_bytes_count, type: counter, unit: bytes, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_error_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_multi_cluster_failovers_count, type: counter, unit: count, v: ok}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_request_count, type: counter, unit: count, v: ok, note: "+method (Bigtable.<RPC>); live label set includes exported_instance,cluster,table,app_profile,zone,instance,project_id,unit"}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_server_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_client_operation_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
  - {root: stackdriver_bigtable_table_bigtable_googleapis_com_client_attempt_latencies, type: histogram, unit: ms, v: ok, note: "exponential buckets expBuckets(1,1.4,66)"}
```

---

## Vertex AI — `stackdriver_aiplatform_googleapis_com_*` [slug: cspgcp-vertex]

**OPT-IN ONLY** — not in `defaultSubSignals`. Blueprint must declare `sub_signals: [vertex]` explicitly.

GCP Vertex AI Cloud Monitoring metrics. Two resource types:

1. **`aiplatform.googleapis.com/Endpoint`** → Stackdriver Prometheus resource `aiplatform_googleapis_com_endpoint`.
   Per-deployed-endpoint metrics. Metric labels: `endpoint_id`, `model_id`, `version_id`, `response_code`, `response_code_class`.

2. **`aiplatform.googleapis.com/Location`** → Stackdriver Prometheus resource `aiplatform_googleapis_com_location`.
   Managed Model Garden / Foundation Model API invocations. Metric labels: `model_id`, `model_version_id`, `error_type`.

**Env-awareness (unique to vertex):** When `fx.Env` is set, all vertex series carry `env=<name>` and volume is
scaled via `Shape.Factor(now, env.Weight, env.NonProd)`. When `fx.Env` is nil (aggregate path), `env` label is
OMITTED (I13) and volume uses the aggregate `BusinessFactor`. Other cspgcp sub-signals are not env-scoped.

Synthetic models emitted (current-generation, version `"default"`):
`gemini-2.5-flash`, `gemini-2.5-flash-lite`, `gemini-2.5-pro`,
`claude-sonnet-4-5@20250929`, `claude-haiku-4-5@20251001`, `text-embedding-005`.
Volume is differentiated per model via `genai.VolumeWeight(id) × Shape.Wander(id, now, 0.15)`;
`vf` already includes `Noise(0.1)` so no second Noise is applied to the scalar counters.
Full costs and weights for these IDs: [`signals/genai-models.md`](genai-models.md) `[slug: genai-models-vertex]`.

*Provenance: GCP Cloud Monitoring Vertex AI metrics documentation (`cloud.google.com/monitoring/api/metrics_gcp`;
ctx7 `/websites/cloud_google_monitoring` query 2026-06-15; cross-checked with
`/googlecloudplatform/monitoring-dashboard-samples` google-vertex-ai dashboard README).
Prometheus metric names extrapolated from the live SK-18 `stackdriver_<resource>_<path_snake>` naming convention
(confirmed for compute/CloudSQL/AlloyDB/Bigtable/CloudRun via Alloy `prometheus.exporter.gcp` 2026-06-14).
No live Alloy capture of the aiplatform families yet → all entries flagged `v: assumed`.
Resolve by running `prometheus.exporter.gcp` against a real Vertex AI project and capturing the
`stackdriver_aiplatform_*` Prometheus names — verify resource type string and label names.*

```yaml signals
family: stackdriver_aiplatform_googleapis_com_endpoint
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  location: <gcp-region>
  endpoint_id: <numeric-string>
  model_id: <model-id>            # e.g. "gemini-1-5-flash"
  version_id: <version>           # e.g. "001"
  response_code: <http-code>      # prediction_count / error_count
  response_code_class: <class>    # "2xx"|"5xx"
  error_type: <type>              # error_count only; e.g. "INTERNAL"
  env: <env-name>                 # ONLY when fx.Env set; omitted otherwise (I13)
  unit: <unit|"1">
metrics:
  - {root: stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count, type: counter, unit: "1", v: assumed, note: "DELTA→cumulative; labels: response_code, response_code_class"}
  - {root: stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_error_count, type: counter, unit: "1", v: assumed, note: "labels: response_code, response_code_class, error_type"}
  - {root: stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_response_latencies, type: histogram, unit: ms, v: assumed, note: "exponential buckets expBuckets(1,1.4,66)"}
```

```yaml signals
family: stackdriver_aiplatform_googleapis_com_location
scope: substrate
sink: promrw
labels:
  job: integrations/gcp
  project_id: <project-id>
  location: <gcp-region>
  model_id: <model-id>            # e.g. "gemini-1-5-pro"
  model_version_id: <version>     # e.g. "001"
  error_type: <type>              # failures only; e.g. "INTERNAL"
  env: <env-name>                 # ONLY when fx.Env set; omitted otherwise (I13)
  unit: <unit|"1">
metrics:
  - {root: stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_invocations, type: counter, unit: "1", v: assumed, note: "DELTA→cumulative; managed Model Garden invocations"}
  - {root: stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_input_token_count, type: counter, unit: "1", v: assumed, note: "prompt / input tokens"}
  - {root: stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_output_token_count, type: counter, unit: "1", v: assumed, note: "completion / output tokens"}
  - {root: stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_failures, type: counter, unit: "1", v: assumed, note: "labels: error_type"}
  - {root: stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_latencies, type: histogram, unit: ms, v: assumed, note: "end-to-end model invocation latency; exponential buckets expBuckets(1,1.4,66)"}
```

> ⚠ **GCP custom monitoring metrics (AI-gateway use case):** VPC-Service-Control access-denied,
> rate-limiting 429/503, and not-found 404 counters emitted by custom Vertex AI proxy implementations
> do NOT appear in the `aiplatform.googleapis.com/` built-in metric family — they would be user-defined
> custom metrics under `custom.googleapis.com/` or `external.googleapis.com/` namespaces. These
> are not sourceable from the GCP docs without a specific custom monitoring setup and are recorded as
> PENDING in cantfind.md (SK-32).

> ✏ **Resolution path:** Run `prometheus.exporter.gcp` against a real GCP project with Vertex AI endpoints
> active, scrape the `/metrics` endpoint, and filter for `stackdriver_aiplatform_*`. Capture the exact
> resource type string (first segment after `stackdriver_`), all label names, and any additional
> metric paths. Update this section and flip `v: assumed` → `v: ok` entries. Remove SK-32 from cantfind.
