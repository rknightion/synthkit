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
