# Database Observability — dbo11y (→ Mimir + Loki) — ScopeSubstrate

Substrate-scoped; never carries a `blueprint` label. Disambiguated by dbo11y instance identity
(`instance` InstanceKey, `server_id`, `db_instance_identifier`). Two independently-gated lanes:
`dbo11y_mysql` and `dbo11y_postgres`. Global scoping and cardinality rules: [`00-canon.md`](00-canon.md)
`[slug: cardinality]`, `[slug: request-correlation]`.

*Provenance: predecessor SIGNALS §13 Lanes A/B + `emit/dbo11y_{mysql,postgres,shared}.go`, `estate/estate.go`+`cloud.go`.
**Live-validated** against the Grafana Cloud Database Observability product stack (Rob) — `v: ok`, not assumed.*
Two independently-gated lanes (`dbo11y_mysql`, `dbo11y_postgres`); both default OFF; both tick 60s;
both `Prom.Write`+`Loki.Write` (no OTLP/traces). **No blueprint label, no scenario label** on any
series or stream.

> **Emission switch (ARCHITECTURE §3.2).** A `databases:` entry fans into the cloud-side RDS
> CloudWatch lane (`aws_rds_*`, §2.2) and/or this dbo11y lane, gated independently by
> `observability: { cloudwatch: <default true>, dbo11y: <default false>, digests: N }`. A DB may emit
> both, either, or neither (a neither-DB is a workload call-target only). The DB `Name` is the join
> key across whichever lanes are on; `ValidateSet` keeps it unique across blueprints whenever EITHER
> lane is enabled. RDS-Postgres vs Aurora-Postgres is an `engine`/type discriminator on the same
> declaration — the dbo11y lane (the Postgres wire protocol) is identical; only the CloudWatch family
> differs.

---

## Shared identity model (the cross-app join key) [slug: dbo11y-identity]

`DBInstance{Engine, Name, ServerID(64-hex sha256), InstanceKey, EngineVersion, Cloud, ProviderName,
ProviderAccount, ProviderRegion, ResourceID, Databases, Queries}` — pure/deterministic
`Build(counts)`. `Name` is THE cross-app join key (dbo11y↔CSP — §2.5). `ServerID` = 64-hex
SHA-256 of `"<seed>:"+name`.

`InstanceKey` forms (Loki stream label `instance`): MySQL/GCP Cloud SQL `tcp(<name>:3306)/appdb`;
Postgres/Azure Flexible `postgresql://<name>.postgres.database.azure.com:5432/appdb`; Postgres/GCP
`postgresql://<name>:5432/appdb`.

Query catalogue: `Query{ID, Text, Tables, Slow}`. **MySQL query ID = 64-hex** (`sha256(seed)`);
**Postgres queryid = decimal int64 string** (`sha256(seed)[:8]>>1`). They are different shapes.

> ⚠ **Cross-signal join rule (T5):** the SAME `ID` string must appear byte-identically as the metric
> label (`digest` MySQL / `queryid` Postgres) AND as the matching field in EVERY log line for that
> query. One character difference breaks the join.

**dbo11y↔CSP hosting map:** MySQL = GCP Cloud SQL only (Azure has no MySQL Flexible Server);
Postgres even-index = Azure Flexible Server (`resourceID` last segment == server name); Postgres
odd-index = GCP Cloud SQL (`database_id`=`project_id:name`). dbo11y `db_instance_identifier` == the
CSP resource name.

**dbo11y↔trace join (app db-leaf):** an `app` db/cache leaf node that declares `db_instance`
resolves to its env's `DBInstance` fixture and decorates the caller's db-CLIENT span with
`server.address` = the RDS endpoint FQDN — whose **first DNS label == `db_instance_identifier`** (the
same `Name`). This links the trace's `Service→RDSInstance` service-graph edge to this lane's RDS
instance metrics. See [`traces.md`](traces.md) `[slug: traces-app-db-instance]`.

## Shared labels [slug: dbo11y-shared-labels]

Prometheus target labels on EVERY dbo11y metric: `job="integrations/db-o11y"`, `instance`(InstanceKey),
`server_id`, `provider_name` (azure|gcp), `provider_region` (azure: resource group e.g. `rg-databases`,
**NOT geo-region** — T15; gcp: region), `provider_account` (azure subscriptionID | gcp project_id).
Loki stream labels on EVERY dbo11y stream: `{job="integrations/db-o11y", instance, server_id, op}`.
⚠ No structured metadata on dbo11y streams — all fields go in the logfmt line body.

```yaml signals
labels:
  job: "integrations/db-o11y"
  instance: <InstanceKey>          # tcp(<name>:3306)/appdb  OR  postgresql://<name>...:5432/appdb
  server_id: <64-hex-sha256>
  provider_name: azure|gcp
  provider_region: <resource-group (azure) | region (gcp)>   # ⚠ T15: azure = rg name, NOT geo-region
  provider_account: <subscriptionID (azure) | project_id (gcp)>
```

## MySQL metric families (`dbo11y_mysql`) [slug: dbo11ymysql]

- `database_observability_connection_info` (G=1) — ⚠ ALL SIX labels required (`provider_name,
  provider_region, provider_account, db_instance_identifier, engine="mysql", engine_version`) or the
  app instance-list is empty (T1); plus the six target labels.
- `database_observability_setup_consumers_enabled` (G=1; `consumer_name` ∈ {events_statements_cpu,
  events_waits_current, events_waits_history, statements_digest, global_instrumentation}).
- Global variables (G): `mysql_global_variables_performance_schema`(=1), `_max_digest_length`(=1024),
  `_max_sql_text_length`(=1024), `mysql_global_variables_max_digest_length`(=4096),
  `_max_connections`(=512). Global status: `mysql_global_status_threads_connected` (G),
  `_threads_running` (G), `_questions` (C), `_slow_queries` (C), `_bytes_received` (C), `_bytes_sent`
  (C), `_uptime` (C), `_innodb_buffer_pool_read_requests` (G), `_innodb_buffer_pool_reads` (G).
- `mysql_perf_schema_events_statements_*` (6 counter families per schema×digest; labels `schema`,
  `digest`): `_total`, `_seconds_total`, `_rows_sent_total`, `_rows_examined_total`, `_errors_total`,
  `_lock_time_seconds_total`. ⚠ **NO `digest_text` label** (T2 — belongs in `query_association` logs only).
- `database_observability_wait_event_seconds_total` (C; labels `digest`, `schema`) — ⚠ **MySQL-ONLY**
  (T3); its presence signals the app's richer v2 wait-events path. Postgres MUST NOT emit it.
- Replication (G) — ✅ **SK-13 resolved (live multi-node Percona Server 8.4 + `prometheus.exporter.mysql`
  on a replica, 2026-06-14; `job="integrations/mysql"`).** On MySQL 8.4 the `mysql_slave_status_*` family
  uses **source/replica** column naming (from `SHOW REPLICA STATUS`), NOT the old master/slave: e.g.
  `mysql_slave_status_seconds_behind_source`, `_replica_io_running`, `_replica_sql_running`,
  `_source_port`, `_source_retry_count`, `_source_ssl_allowed`, `_relay_log_pos`, `_relay_log_space`,
  `_exec_source_log_pos`, `_skip_counter`, `_last_errno`, `_last_sql_errno`, `_get_source_public_key`
  (~21 series total). **Label set: `master_host`, `master_uuid`** (+ `instance`=exporter DSN, `job`);
  the default channel carries NO `channel_name` label (it appears only for named replication channels).
  > synthkit's `dbo11y_mysql` emits exactly the **13 enumerated names above** (the only ones SIGNALS asserts
  > by name) with seed-derived `master_host`/`master_uuid`; the "~21" is an approximate live count — emitting
  > the unenumerated remainder would require inventing names, so a fresh multi-node capture is deferred (SK-13 note).
  Real exporter: emits 0 series on a single node (no replication source) — requires a real replica.
  (synthkit models a replica and emits the 13 series unconditionally with seed-derived identity.)

```yaml signals
family: dbo11y_mysql
scope: substrate
sink: promrw
labels:
  job: "integrations/db-o11y"
  instance: <InstanceKey>                    # tcp(<name>:3306)/appdb
  server_id: <64-hex-sha256>
  provider_name: azure|gcp
  provider_region: <resource-group (azure) | region (gcp)>
  provider_account: <subscriptionID (azure) | project_id (gcp)>
  db_instance_identifier: <name>             # connection_info only (+ engine, engine_version)
  schema: <schema>                           # perf_schema_events_statements, wait_event_seconds
  digest: <64-hex>                           # perf_schema_events_statements, wait_event_seconds
  consumer_name: <name>                      # setup_consumers_enabled
  master_host: <host>                        # slave_status_* replication series
  master_uuid: <uuid>                        # slave_status_* replication series
metrics:
  - {root: database_observability_connection_info, type: gauge, unit: bool, v: ok, note: "G=1; ALL SIX labels required (T1)"}
  - {root: database_observability_setup_consumers_enabled, type: gauge, unit: bool, v: ok, note: "G=1; consumer_name label"}
  - {root: mysql_global_variables_performance_schema, type: gauge, unit: bool, v: ok, note: "=1"}
  - {root: mysql_global_variables_max_digest_length, type: gauge, unit: bytes, v: ok, note: "=1024 (short form)"}
  - {root: mysql_global_variables_max_sql_text_length, type: gauge, unit: bytes, v: ok, note: "=1024"}
  - {root: mysql_global_variables_max_digest_length, type: gauge, unit: bytes, v: ok, note: "=4096 (long form)"}
  - {root: mysql_global_variables_max_connections, type: gauge, unit: count, v: ok, note: "=512"}
  - {root: mysql_global_status_threads_connected, type: gauge, unit: count, v: ok}
  - {root: mysql_global_status_threads_running, type: gauge, unit: count, v: ok}
  - {root: mysql_global_status_questions, type: counter, unit: count, v: ok}
  - {root: mysql_global_status_slow_queries, type: counter, unit: count, v: ok}
  - {root: mysql_global_status_bytes_received, type: counter, unit: bytes, v: ok}
  - {root: mysql_global_status_bytes_sent, type: counter, unit: bytes, v: ok}
  - {root: mysql_global_status_uptime, type: counter, unit: seconds, v: ok}
  - {root: mysql_global_status_innodb_buffer_pool_read_requests, type: gauge, unit: count, v: ok}
  - {root: mysql_global_status_innodb_buffer_pool_reads, type: gauge, unit: count, v: ok}
  - {root: mysql_perf_schema_events_statements_total, type: counter, unit: count, v: ok, note: "per schema×digest; NO digest_text (T2)"}
  - {root: mysql_perf_schema_events_statements_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: mysql_perf_schema_events_statements_rows_sent_total, type: counter, unit: count, v: ok}
  - {root: mysql_perf_schema_events_statements_rows_examined_total, type: counter, unit: count, v: ok}
  - {root: mysql_perf_schema_events_statements_errors_total, type: counter, unit: count, v: ok}
  - {root: mysql_perf_schema_events_statements_lock_time_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: database_observability_wait_event_seconds_total, type: counter, unit: seconds, v: ok, note: "MySQL-ONLY (T3); Postgres MUST NOT emit"}
  - {root: mysql_slave_status_seconds_behind_source, type: gauge, unit: seconds, v: ok, note: "replication; label set: master_host, master_uuid"}
  - {root: mysql_slave_status_replica_io_running, type: gauge, unit: bool, v: ok}
  - {root: mysql_slave_status_replica_sql_running, type: gauge, unit: bool, v: ok}
  - {root: mysql_slave_status_source_port, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_source_retry_count, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_source_ssl_allowed, type: gauge, unit: bool, v: ok}
  - {root: mysql_slave_status_relay_log_pos, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_relay_log_space, type: gauge, unit: bytes, v: ok}
  - {root: mysql_slave_status_exec_source_log_pos, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_skip_counter, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_last_errno, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_last_sql_errno, type: gauge, unit: count, v: ok}
  - {root: mysql_slave_status_get_source_public_key, type: gauge, unit: bool, v: ok}
```

## MySQL log ops [slug: dbo11ymysql-logs]

All MySQL dbo11y streams are logfmt; stream labels `{job="integrations/db-o11y", instance, server_id, op}`.
⚠ No structured metadata — all fields in logfmt body.

`op="query_sample"` (~4 lines/query/tick; ⚠ line ts = `now − elapsed_time`, the query START, not tick
time — T7; fields incl. `digest`, `elapsed_time` & `elapsed_time_ms` **identical** — T13; duration
`"%.6fms"`); `op="wait_event"` (v1) and `op="wait_event_v2"` (adds `wait_event_type` classifier ∈
{IO Wait, Lock Wait, Engine Wait, Network Wait} — T17; `wait_event_name` is the bare InnoDB name e.g.
`wait/io/file/innodb/innodb_data_file` — T9); `op="query_association"` (`digest`, `digest_text`);
`op="query_parsed_table_name"`; `op="table_detection"` (tables: users,orders,inventory,events,audit_log);
`op="create_statement"` (`create_statement=<base64 DDL>`, `table_spec=<base64 JSON>` — T14);
`op="explain_plan_output"` (⚠ uses `digest=` — T6); `op="query_data_locks"` (ONLY under lock-contention);
`op="health_status"` (3 checks: AlloyVersion `value="v1.16.0"` — ⚠ MySQL gate ≥1.16, T16;
RequiredGrantsPresent; PerformanceSchemaHasRows).
> ✅ **SK-14 LARGELY RESOLVED (live Percona Server 8.4.8 capture 2026-06-13, `instance=mysql-app1`):**
> `create_statement` = raw `SHOW CREATE TABLE` DDL (base64), NOT PG structured JSON; `explain_plan_output`
> leaf carries `accessType` ∈{const,index,…}, `keyUsed`, `alias`, **`condition`** (structural predicate),
> `estimatedRows`/`estimatedCost`; `query_sample` memory byte-suffix `"b"` (`max_total_memory="30659b"`),
> `elapsed_time` µs-precision `"0.104893ms"` (= `elapsed_time_ms`, T13); `wait_event_name` = perf_schema
> instrument paths (`wait/lock/table/sql/handler`, `wait/io/…`).
> ✅ **SK-14 fully resolved (2026-06-14).** `setup_consumers`: confirmed NOT emitted as an op (the collector
> auto-enables `performance_schema` consumers implicitly; no log line). `op="query_data_locks"` contract —
> sourced from the authoritative Alloy collector source (`internal/component/database_observability/mysql/
> collector/locks.go`): the `locks` collector is **disabled by default** (`enable_collectors=["locks"]`),
> `collect_interval` default `30s`, `threshold` default `1s`; it queries `performance_schema.data_lock_waits`
> joined to `data_locks` + `events_statements_current` (waiting + blocking), and logs fields
> **`waiting_digest`, `waiting_digest_text`, `blocking_digest`, `blocking_digest_text`, `waiting_timer_wait`
> (ms), `waiting_lock_time` (ms), `blocking_timer_wait` (ms), `blocking_lock_time` (ms)`**. ⚠ Emission is
> timing-pathological for live row-lock contention: it filters in Go on the *waiting* statement's
> `events_statements_current.LOCK_TIME > threshold`, but that LOCK_TIME only finalizes when the statement
> COMPLETES (and a completed statement is no longer in `data_lock_waits`) — so heavy sustained row-lock
> contention did NOT trigger it live (collector confirmed enabled + working: `query_sample`,
> `query_parsed_table_name`, `schema_detection`, `table_detection`, `create_statement` all flowed). Model
> the op + field set from the source contract above; do NOT fabricate values beyond these field names.

```yaml signals
sink: loki
stream_labels:
  job: "integrations/db-o11y"
  instance: <InstanceKey>
  server_id: <64-hex-sha256>
  op: query_sample|wait_event|wait_event_v2|query_association|query_parsed_table_name|table_detection|create_statement|explain_plan_output|query_data_locks|health_status
structured_metadata: []   # ⚠ NONE — all fields in logfmt body
body_fields:
  # query_sample
  - digest              # 64-hex MySQL query ID (cross-signal join key T5)
  - elapsed_time        # "%.6fms" e.g. "0.104893ms" (= elapsed_time_ms, T13); ts = now−elapsed_time (T7)
  - elapsed_time_ms     # identical to elapsed_time (T13)
  - max_total_memory    # e.g. "30659b" (byte-suffix "b")
  # wait_event / wait_event_v2
  - wait_event_name     # perf_schema instrument path e.g. "wait/io/file/innodb/innodb_data_file" (T9)
  - wait_event_type     # v2 only: IO Wait|Lock Wait|Engine Wait|Network Wait (T17)
  # query_association
  - digest_text         # human-readable SQL template (NOT a metric label — T2)
  # query_data_locks (ONLY under lock-contention; locks collector disabled by default)
  - waiting_digest
  - waiting_digest_text
  - blocking_digest
  - blocking_digest_text
  - waiting_timer_wait  # ms
  - waiting_lock_time   # ms
  - blocking_timer_wait # ms
  - blocking_lock_time  # ms
  # create_statement
  - create_statement    # base64 DDL (raw SHOW CREATE TABLE output)
  - table_spec          # base64 JSON
  # explain_plan_output (⚠ uses digest= not queryid= — T6)
  - digest
  - accessType          # e.g. const, index
  - keyUsed
  - alias
  - condition           # structural predicate
  - estimatedRows
  - estimatedCost
  # health_status (3 checks)
  - check               # AlloyVersion|RequiredGrantsPresent|PerformanceSchemaHasRows
  - result
  - value               # AlloyVersion: "v1.16.0" (⚠ MySQL gate ≥1.16, T16)
```

## Postgres metric families (`dbo11y_postgres`) [slug: dbo11ypg]

- `database_observability_connection_info` (same six-label rule; `engine="postgres"`).
- `pg_up` (G=1), `pg_stat_database_numbackends` (G; `datname`), `pg_replication_is_replica`(=0),
  `pg_replication_lag` (=0 normal), `pg_locks_count` (G; `mode="ExclusiveLock", datname`),
  `pg_stat_bgwriter_buffers_clean` (C), `_buffers_alloc` (C), `pg_postmaster_start_time_seconds` (G).
- `database_observability_pg_errors_total` (C; `severity, sqlstate, sqlstate_class, datname, user`;
  codes 42P01/`42`, 23505/`23`, FATAL 53300/`53`), `database_observability_pg_error_log_parse_failures_total`
  (C; =0).
- `pg_stat_statements_*` (3 counter families; labels `datname`, **`queryid`** = decimal int64 string):
  `_calls_total`, `_seconds_total`, `_rows_total`. ⚠ NO `digest_text`; ⚠ NO
  `database_observability_wait_event_seconds_total` (MySQL-only path).

```yaml signals
family: dbo11y_postgres
scope: substrate
sink: promrw
labels:
  job: "integrations/db-o11y"
  instance: <InstanceKey>                    # postgresql://<name>...:5432/appdb
  server_id: <64-hex-sha256>
  provider_name: azure|gcp
  provider_region: <resource-group (azure) | region (gcp)>
  provider_account: <subscriptionID (azure) | project_id (gcp)>
  db_instance_identifier: <name>             # connection_info only (+ engine="postgres", engine_version)
  datname: <database>                        # pg_stat_database, pg_locks_count, pg_errors, pg_stat_statements
  queryid: <decimal-int64-string>            # pg_stat_statements (⚠ decimal int64, NOT 64-hex)
  mode: ExclusiveLock                        # pg_locks_count
  severity: <severity>                       # pg_errors_total
  sqlstate: <5-char>                         # pg_errors_total e.g. 42P01
  sqlstate_class: <2-char>                   # pg_errors_total e.g. 42
  user: <user>                               # pg_errors_total
metrics:
  - {root: database_observability_connection_info, type: gauge, unit: bool, v: ok, note: "G=1; ALL SIX labels required (T1); engine=postgres"}
  - {root: pg_up, type: gauge, unit: bool, v: ok, note: "G=1"}
  - {root: pg_stat_database_numbackends, type: gauge, unit: count, v: ok, note: "per datname"}
  - {root: pg_replication_is_replica, type: gauge, unit: bool, v: ok, note: "=0"}
  - {root: pg_replication_lag, type: gauge, unit: seconds, v: ok, note: "=0 normal"}
  - {root: pg_locks_count, type: gauge, unit: count, v: ok, note: "mode=ExclusiveLock, datname"}
  - {root: pg_stat_bgwriter_buffers_clean, type: counter, unit: count, v: ok}
  - {root: pg_stat_bgwriter_buffers_alloc, type: counter, unit: count, v: ok}
  - {root: pg_postmaster_start_time_seconds, type: gauge, unit: seconds, v: ok}
  - {root: database_observability_pg_errors_total, type: counter, unit: count, v: ok, note: "codes 42P01/42, 23505/23, FATAL 53300/53"}
  - {root: database_observability_pg_error_log_parse_failures_total, type: counter, unit: count, v: ok, note: "=0"}
  - {root: pg_stat_statements_calls_total, type: counter, unit: count, v: ok, note: "queryid=decimal int64; NO digest_text"}
  - {root: pg_stat_statements_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: pg_stat_statements_rows_total, type: counter, unit: count, v: ok}
```

## Postgres log ops [slug: dbo11ypg-logs]

All Postgres dbo11y streams are logfmt; stream labels `{job="integrations/db-o11y", instance, server_id, op}`.
⚠ No structured metadata — all fields in logfmt body. ⚠ **Duration = Go `time.Duration.String()` —
`"30s"`, `"2m0s"`, `"150ms"` — T8** (NOT `"%.6fms"` like MySQL).

`op="query_sample"` (ts = `now − query_time`; `datname` not `schema`; `leader_pid=""` emitted as empty,
not omitted; `queryid` join key); `op="wait_event"` (v1; `wait_event_name="<type>:<event>"` colon-joined
— T9; `blocked_by_pids="[103 104]"` bracket-space — T10); `op="wait_event_v2"` (pre-classified
`wait_event_type` bucket); `op="query_association"` (⚠ `queryid=`/`querytext=`, NOT `digest=`);
`op="query_parsed_table_name"` (`queryid`,`datname`,`validated="true"`); `op="table_detection"`
(`datname`+`schema="public"`); `op="create_statement"` (⚠ `table_spec=` only, NO `create_statement=`
DDL field — T14); `op="explain_plan_output"` (⚠ uses `schema=<datname>` + `digest=<queryid>`, NOT
`datname`/`queryid` — T6); `op="health_status"` (6 checks; AlloyVersion `value="v1.16.3"` — running
Alloy version, T16 (the earlier `v1.17.0` was a stale pre-release placeholder; v1.16.3 is the latest
real Alloy release — corrected 2026-06-15); PgStatStatementsEnabled; TrackActivityQuerySize `"4096"`; ComputeQueryIdEnabled
`"auto"`; MonitoringUserPrivileges; PgStatStatementsHasRows). Open items → cantfind **SK-15**.
> ✅ **Live-verified (staff stack `integrations/db-o11y`, Postgres 18.3, 2026-06-13 — SK-15).** Confirmed for
> the ops that flow: `create_statement` `table_spec`=base64 JSON {columns[{name,type,not_null,
> primary_key,auto_increment}],indexes,foreign_keys} (type∈{text,bigint,integer,numeric,character
> varying}); `explain_plan_output`=base64 JSON with `metadata.databaseEngine="PostgreSQL"` and PG
> node-name `operation` taxonomy {Append,Function Scan,Hash,Hash Join,Limit,Result,Seq Scan,Simple
> Aggregate,Sort,Subquery Scan}, `processingResult`∈{success,skipped}; `query_association`
> `queryid`(signed int64 string)+`querytext`; `query_parsed_table_name` `validated`∈{"true","false"};
> `health_status` as `check`/`result`/`value` (MonitoringUserPrivileges result="true" value="";
> TrackActivityQuerySize value="1024" deployment-specific). ✅ **ACTIVITY LANE NOW VERIFIED (SK-15
> Phase-2, lock-contention + long-query load):** `query_sample` carries
> `datname,pid,leader_pid,user,app,client,backend_type,state,xid,xmin,xact_time,query_time,queryid,query`
> (+`cpu_time` when CPU-bound) — note the **`query`** field (parameterized SQL); `wait_event` carries the
> same identity block + `wait_time,wait_event_type,wait_event,wait_event_name,blocked_by_pids,queryid`;
> `wait_event_name` = `<Type>:<Event>` e.g. `Lock:tuple`, `Timeout:PgSleep`; `blocked_by_pids` =
> space-separated bracket text `[416793 416794 …]` (lock waits) or `[]` (none); `backend_type="client
> backend"`. ✅ **SK-15 resolved (2026-06-14):** Postgres database-observability has **no `query_data_locks`
> op and no locks collector** — the `locks` collector / `query_data_locks` op are MySQL-only (Alloy
> `database_observability.postgres` collectors = explain_plans, logs, query_details, query_samples,
> schema_details; no locks). PG lock waits surface via `wait_event` (`wait_event_name=Lock:tuple`,
> `blocked_by_pids`, above) — NOT a separate op. `compute_query_id` is ON (queryid is computed, confirmed
> by the queryids flowing). Do NOT emit a Postgres `query_data_locks` op.

```yaml signals
sink: loki
stream_labels:
  job: "integrations/db-o11y"
  instance: <InstanceKey>
  server_id: <64-hex-sha256>
  op: query_sample|wait_event|wait_event_v2|query_association|query_parsed_table_name|table_detection|create_statement|explain_plan_output|health_status
structured_metadata: []   # ⚠ NONE — all fields in logfmt body
body_fields:
  # query_sample (ts = now−query_time)
  - datname
  - pid
  - leader_pid            # emitted as "" when absent (NOT omitted)
  - user
  - app
  - client
  - backend_type          # "client backend"
  - state
  - xid
  - xmin
  - xact_time             # Go time.Duration.String() e.g. "30s" (T8)
  - query_time            # Go time.Duration.String() (T8)
  - queryid               # decimal int64 string (cross-signal join key T5)
  - query                 # parameterized SQL
  - cpu_time              # present only when CPU-bound
  # wait_event / wait_event_v2
  - wait_time             # Go time.Duration.String() (T8)
  - wait_event_type       # e.g. Lock, Timeout, IO
  - wait_event            # bare event name
  - wait_event_name       # "<Type>:<Event>" colon-joined (T9) e.g. "Lock:tuple"
  - blocked_by_pids       # "[416793 416794 …]" bracket-space (T10); "[]" when none
  # query_association (⚠ queryid= and querytext=, NOT digest= — T6)
  - queryid
  - querytext
  # query_parsed_table_name
  - queryid
  - datname
  - validated             # "true" or "false"
  # table_detection
  - datname
  - schema                # "public"
  # create_statement (⚠ table_spec= ONLY, NO create_statement= DDL — T14)
  - table_spec            # base64 JSON {columns,indexes,foreign_keys}
  # explain_plan_output (⚠ schema=<datname>, digest=<queryid> — T6)
  - schema                # = datname value
  - digest                # = queryid value
  - operation             # PG node-name: Append|Function Scan|Hash|Hash Join|Limit|Result|Seq Scan|Simple Aggregate|Sort|Subquery Scan
  - processingResult      # success|skipped
  # health_status (6 checks)
  - check                 # AlloyVersion|PgStatStatementsEnabled|TrackActivityQuerySize|ComputeQueryIdEnabled|MonitoringUserPrivileges|PgStatStatementsHasRows
  - result                # "true"/"false"
  - value                 # AlloyVersion: "v1.16.3" (running Alloy version, T16 — latest real release); TrackActivityQuerySize: "1024" deployment-specific
```
