# Snowflake (→ Mimir) — ScopeSubstrate

Snowflake account telemetry via Alloy `prometheus.exporter.snowflake` (wraps
`grafana/snowflake-prometheus-exporter`, default `:9975`) scraping `ACCOUNT_USAGE` views → Mimir.
⚠ **NOT a CloudWatch source** — Snowflake has no `AWS/*` namespace. Substrate-scoped; identity is
the scrape target (account/warehouse Config-borne). Global rules: [`00-canon.md`](00-canon.md).

*Provenance: predecessor synthetics/research/snowflake.md (names verified vs the exporter source
`collector/query.go`+`collector.go`, 2026-06-10); no synthkit live capture → `v: assumed`, SK-40.*

⚠ **All 27 metrics are GAUGES** (24-h rolling averages/sums from ACCOUNT_USAGE; latency 90 min – 3 h
→ unsuitable for short SLO windows). Prefix `snowflake_`. No Counters, no Histograms.
`snowflake_up`=1 when all collections succeed.

```yaml signals
family: snowflake
scope: substrate
sink: promrw
labels:
  job: integrations/snowflake
  instance: <host:9975>
  name: <db|warehouse-name>             # database_bytes / warehouse_* / table group "name"
  id: <db|warehouse-id>
  service_type: <service-type>          # used_*_credits
  service: <service>                    # used_*_credits
  client_type: <client-type>            # *_login_rate
  client_version: <client-version>      # *_login_rate
  table_name: <table>                   # auto_clustering_* / table_* (6-label group)
  table_id: <table-id>
  schema_name: <schema>
  schema_id: <schema-id>
  database_name: <database>             # auto_clustering_* / table_* / db_replication_*
  database_id: <database-id>
metrics:
  # account-level storage (no labels) — STORAGE_USAGE
  - {root: snowflake_storage_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_stage_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_failsafe_bytes, type: gauge, unit: bytes, v: assumed}
  # database storage (name,id) — DATABASE_STORAGE_USAGE_HISTORY
  - {root: snowflake_database_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_database_failsafe_bytes, type: gauge, unit: bytes, v: assumed}
  # credit usage (service_type,service) — METERING_HISTORY
  - {root: snowflake_used_compute_credits, type: gauge, unit: credits, v: assumed}
  - {root: snowflake_used_cloud_services_credits, type: gauge, unit: credits, v: assumed}
  # warehouse credits (name,id) — WAREHOUSE_METERING_HISTORY
  - {root: snowflake_warehouse_used_compute_credits, type: gauge, unit: credits, v: assumed}
  - {root: snowflake_warehouse_used_cloud_service_credits, type: gauge, unit: credits, v: assumed}
  # login rates (client_type,client_version) — LOGIN_HISTORY
  - {root: snowflake_login_rate, type: gauge, unit: per_hour, v: assumed}
  - {root: snowflake_successful_login_rate, type: gauge, unit: per_hour, v: assumed}
  - {root: snowflake_failed_login_rate, type: gauge, unit: per_hour, v: assumed}
  # warehouse query load (name,id) — WAREHOUSE_LOAD_HISTORY
  - {root: snowflake_warehouse_executed_queries, type: gauge, unit: load, v: assumed}
  - {root: snowflake_warehouse_overloaded_queue_size, type: gauge, unit: load, v: assumed}
  - {root: snowflake_warehouse_provisioning_queue_size, type: gauge, unit: load, v: assumed}
  - {root: snowflake_warehouse_blocked_queries, type: gauge, unit: load, v: assumed}
  # auto-clustering (6-label group) — AUTOMATIC_CLUSTERING_HISTORY
  - {root: snowflake_auto_clustering_credits, type: gauge, unit: credits, v: assumed}
  - {root: snowflake_auto_clustering_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_auto_clustering_rows, type: gauge, unit: rows, v: assumed}
  # table storage (first four 6-label; deleted_tables no labels) — TABLE_STORAGE_METRICS
  - {root: snowflake_table_active_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_table_time_travel_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_table_failsafe_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_table_clone_bytes, type: gauge, unit: bytes, v: assumed}
  - {root: snowflake_table_deleted_tables, type: gauge, unit: count, v: assumed}
  # database replication (database_name,database_id) — REPLICATION_USAGE_HISTORY
  - {root: snowflake_db_replication_used_credits, type: gauge, unit: credits, v: assumed}
  - {root: snowflake_db_replication_transferred_bytes, type: gauge, unit: bytes, v: assumed}
  # exporter health (no labels)
  - {root: snowflake_up, type: gauge, unit: bool, v: assumed}
```
