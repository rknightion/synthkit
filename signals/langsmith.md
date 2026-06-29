# LangSmith (→ Mimir + Loki) — ScopeSubstrate

Two constructs share this contract: **`langsmith_platform`** (the self-hosted LangSmith services'
`/metrics` scrape — standard process/python/ClickHouse/redis/pg/nginx exporters) and
**`langsmith_eval`** (the LangSmith feedback API polled → `langsmith_eval_*` quality gauges).
Substrate-scoped (`job=langsmith-<svc>` / `project`); never a `blueprint` label. All identity
(project/use-case/evaluator strings) is Config-borne. Global rules: [`00-canon.md`](00-canon.md).

*Provenance: predecessor synthetics/SIGNALS.md §2.7 (platform, ✅ docs.langchain.com 2026-06-10) + §2.6
(eval, names are "OUR convention" → `v: assumed`, SK-39).*

---

## Platform self-metrics — standard exporters [slug: langsmith-platform]

⚠ **LangSmith documents ZERO custom app-metric names** — the services are Python, so the *accurate*
synthetic representation is the standard exporter families with `job=langsmith-<service>`. Do NOT
invent `langsmith_*` app series. Scrape targets: `backend`:1984, `host-backend`:1985,
`platform-backend`:1986, `playground`:1988, ClickHouse:9363, redis:9121, postgres:9187, nginx:9113.
The roots below are a representative emitted subset of standard exporters (`v: ok` — not invented).

```yaml signals
family: langsmith_platform
scope: substrate
sink: promrw
labels:
  job: langsmith-<service>      # backend|host-backend|platform-backend|playground|clickhouse|redis|postgres|nginx
  instance: <host:port>
  env: <name>                   # optional — present only in for_each_env fan-out (Spec 3); omitted in aggregate mode (I13)
metrics:
  # Python services (backend/host-backend/platform-backend/playground) — process + python exporters
  - {root: process_cpu_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: process_resident_memory_bytes, type: gauge, unit: bytes, v: ok}
  - {root: process_virtual_memory_bytes, type: gauge, unit: bytes, v: ok}
  - {root: process_open_fds, type: gauge, unit: count, v: ok}
  - {root: process_max_fds, type: gauge, unit: count, v: ok}
  - {root: process_start_time_seconds, type: gauge, unit: seconds, v: ok}
  - {root: python_gc_objects_collected_total, type: counter, unit: count, v: ok}
  - {root: python_gc_collections_total, type: counter, unit: count, v: ok}
  - {root: python_info, type: gauge, unit: info, v: ok}
  - {root: http_requests_total, type: counter, unit: requests, v: assumed, note: "HTTP-middleware (Ⓘ); unconfirmed vs real LangSmith — SK-74"}
  - {root: http_request_duration_seconds, type: histogram, unit: seconds, v: assumed, note: "HTTP-middleware (Ⓘ); unconfirmed vs real LangSmith — SK-74"}
  # ClickHouse :9363 native Prometheus endpoint
  - {root: ClickHouseMetrics_Query, type: gauge, unit: count, v: ok}
  - {root: ClickHouseMetrics_TCPConnection, type: gauge, unit: count, v: ok}
  - {root: ClickHouseProfileEvents_Query, type: counter, unit: count, v: ok}
  - {root: ClickHouseProfileEvents_SelectQuery, type: counter, unit: count, v: ok}
  - {root: ClickHouseProfileEvents_SelectedRows, type: counter, unit: rows, v: ok, note: "rows read by SELECT statements; real ClickHouse ProfileEvents counter (2026-06-16)"}
  - {root: ClickHouseProfileEvents_InsertedRows, type: counter, unit: rows, v: ok, note: "rows written by INSERT statements; real ClickHouse ProfileEvents counter (2026-06-16)"}
  - {root: ClickHouseAsyncMetrics_Uptime, type: gauge, unit: seconds, v: ok}
  # redis_exporter :9121
  - {root: redis_up, type: gauge, unit: bool, v: ok}
  - {root: redis_connected_clients, type: gauge, unit: count, v: ok}
  - {root: redis_memory_used_bytes, type: gauge, unit: bytes, v: ok}
  - {root: redis_commands_processed_total, type: counter, unit: count, v: ok}
  - {root: redis_keyspace_hits_total, type: counter, unit: count, v: ok}
  - {root: redis_keyspace_misses_total, type: counter, unit: count, v: ok}
  # postgres_exporter :9187
  - {root: pg_up, type: gauge, unit: bool, v: ok}
  - {root: pg_stat_database_numbackends, type: gauge, unit: count, v: ok}
  - {root: pg_stat_activity_count, type: gauge, unit: count, v: ok, note: "per state label (active|idle|idle in transaction); real postgres_exporter gauge (2026-06-16)"}
  - {root: pg_replication_lag, type: gauge, unit: seconds, v: ok, note: "=0 normal; same shape as dbo11y.md [slug: dbo11ypg]; real postgres_exporter gauge (2026-06-16)"}
  - {root: pg_stat_database_xact_commit, type: counter, unit: count, v: ok}
  - {root: pg_stat_database_xact_rollback, type: counter, unit: count, v: ok}
  - {root: pg_stat_database_blks_hit, type: counter, unit: count, v: ok}
  # nginx-prometheus-exporter :9113
  - {root: nginx_http_requests_total, type: counter, unit: requests, v: ok}
  - {root: nginx_connections_active, type: gauge, unit: count, v: ok}
  - {root: nginx_connections_accepted, type: counter, unit: count, v: ok}
  - {root: nginx_connections_handled, type: counter, unit: count, v: ok}
```

## Eval-derived quality metrics — `langsmith_eval_*` [slug: langsmith-eval]

5b extraction: poll the feedback API → `score = feedback_stats["<key>"]["avg"]`, evaluator key →
`evaluator` label. ⚠ Names are "OUR convention" (`v: assumed`, SK-39). ⚠ All GAUGES (`state.Set`)
EXCEPT `langsmith_eval_token_spend_total` which is a Counter (`state.Add`).

```yaml signals
family: langsmith_eval
scope: substrate
sink: promrw
labels:
  project: <project>
  use_case: <use-case>
  agent: <agent>
  env: <env>
  k: <k>                        # recall/precision/mrr/ndcg @k
  evaluator: <evaluator>        # langsmith_eval_score only
  run_outcome: success|error|pending   # langsmith_eval_score only (← run status)
metrics:
  - {root: langsmith_eval_completeness_ratio, type: gauge, unit: ratio, v: assumed, note: "gate ≥0.995; {project,use_case,agent,env}"}
  - {root: langsmith_eval_faithfulness_ratio, type: gauge, unit: ratio, v: assumed, note: "gate ≥0.85; {project,use_case,agent,env}"}
  - {root: langsmith_eval_env_consistency_ratio, type: gauge, unit: ratio, v: assumed, note: "gate =1.0; {project,env}"}
  - {root: langsmith_eval_schema_validity_ratio, type: gauge, unit: ratio, v: assumed, note: "<0.995 blocks; {project,env}"}
  - {root: langsmith_eval_passthrough_exactness_ratio, type: gauge, unit: ratio, v: assumed, note: "<0.999 blocks; {project,env}"}
  - {root: langsmith_eval_recall_at_k, type: gauge, unit: ratio, v: assumed, note: "{project,k,use_case}"}
  - {root: langsmith_eval_precision_at_k, type: gauge, unit: ratio, v: assumed, note: "{project,k,use_case}"}
  - {root: langsmith_eval_mrr, type: gauge, unit: ratio, v: assumed, note: "{project,k,use_case}"}
  - {root: langsmith_eval_ndcg, type: gauge, unit: ratio, v: assumed, note: "{project,k,use_case}"}
  - {root: langsmith_eval_latency_seconds, type: gauge, unit: seconds, v: assumed, note: "{project,use_case}"}
  - {root: langsmith_eval_token_spend_total, type: counter, unit: tokens, v: assumed, note: "COUNTER (state.Add); {project,use_case}"}
  - {root: langsmith_eval_retry_rate, type: gauge, unit: ratio, v: assumed, note: "{project,use_case}"}
  - {root: langsmith_eval_fallback_rate, type: gauge, unit: ratio, v: assumed, note: "{project,use_case}"}
  - {root: langsmith_eval_hitl_rate, type: gauge, unit: ratio, v: assumed, note: "{project} derived ratio"}
  - {root: langsmith_eval_score, type: gauge, unit: score, v: assumed, note: "generic LLM-as-judge; {project,evaluator,run_outcome}"}
```

## LangSmith run-index log — `source=langsmith-runs` [slug: langsmith-runs]

The 13-field run-tag schema (every LangSmith run carries these — all body fields / structured
metadata, content stripped): `aws_env`, `user_id`, `session_id`/`thread_id`, `request_id`,
`use_case`, `agent_name` (⚠ LangSmith key is `agent_name`; Portkey/k8s use `agent`),
`agent_version`, `model_provider`, `model_name`, `model_version`, `prompt_id`, `prompt_version`,
`portkey_trace_id`. Plus the correlation keys `run_id`, `correlation_id`. ⚠ All high-card
(`run_id`,`portkey_trace_id`,`correlation_id`,`request_id`,`session_id`,`user_id`) ride in Loki
**structured metadata**, NEVER stream labels (the sink asserts; `[slug: cardinality]`). `inputs`/
`outputs`/`messages` are NEVER emitted (content-stripped).
