# synthkit cantfind — open / unverified signal items

The consolidated PENDING register for the synthkit catalog. Anything here is **NOT** asserted as fact
in the [`signals/`](./signals/) catalogue — each item has a representative/synthetic value already emitted (so the
shape is structurally correct) but the exact value is unconfirmed. Resolve before relying on the
affected family in production. **Once an item is confirmed, its contract moves into the matching `signals/<area>.md` family and the
row is removed from here** — this register holds only what is still open.

**Provenance.** Consolidated and de-duplicated from the original signal-extraction dossiers (now
superseded by `signals/`). Each entry carries a stable ID (SK-N), the
affected construct kind, what is unknown, and the resolution path. IDs are stable — append new items,
never renumber, never reuse a retired ID.

**Resolution-path key:** `vendor-doc` = the upstream product/API docs · `ctx7` = current docs via the
`ctx7` CLI · `live-capture` = scrape/inspect a real install (live Alloy/exporter/CW capture) ·
`config-driven` = the value is deployment-specific and must be a synthkit config knob, not a constant.

**Resolved & retired (confirmed against the 2026-06-13/14 live captures, folded into `signals/`):**
SK-1 (S3 request metrics), SK-3 (PrivateLink space-dimension label form: spaces→underscores, CW casing
preserved, cross-region consumers never publish — live-confirmed 2026-06-14), SK-6 (RDS/EBS latency
units), SK-7 (kubelet histogram buckets), SK-8 (KSM pod uid), SK-9 (KSM instance pod-IP), SK-10 (AWS LBC
job=`aws-load-balancer-controller` bare name via annotation autodiscovery — live-confirmed 2026-06-14),
SK-11 (vpc-cni latency summary), SK-12 (EBS-CSI volume_id series), SK-13 (MySQL `mysql_slave_status_*`
live-captured on a multi-node Percona 8.4 replica 2026-06-14: MySQL-8.4 source/replica metric naming,
labels `master_host`/`master_uuid`, ~21 series, no `channel_name` on default channel), SK-14 (MySQL
`query_data_locks` op + field contract sourced from Alloy `collector/locks.go`; `setup_consumers` confirmed
not emitted — resolved 2026-06-14; live emission timing-pathological, see signals/dbo11y.md [slug: dbo11y-mysql-logs]), SK-15 (Postgres has no
`query_data_locks` op / no locks collector — MySQL-only; PG lock waits via `wait_event` — resolved
2026-06-14), SK-16 (Azure FD/Service Bus/SQL elastic-pool — live capture of BOTH paths 2026-06-14: serverless
`dimension_<Name>` underscore vs azure_exporter `dimension<Name>` no-underscore/`dimension` single; no
`elastic_pool_name` label; §2.5 audit deltas job=integrations/azure & timespan=PT1M recorded), SK-17
(GCP exponential buckets), SK-18 (GCP via live Alloy prometheus.exporter.gcp: Bigtable
`method=Bigtable.<RPC>` + `exported_instance`; AlloyDB `cluster_id`/`instance_id` resource labels, no
exported_instance; Cloud SQL `state` enum RUNNABLE/RUNNING/SUSPENDED/PENDING_CREATE/MAINTENANCE/FAILED/
UNKNOWN_STATE — confirmed 2026-06-14), SK-20 (k8s pod-log labels), SK-22 (SM scope), SK-25 (Alloy
build-info), SK-28 (spanmetrics native+classic), SK-30 (kubecost HTTP-response series), and the former k8s-monitoring
conformance build-info item (was a duplicate SK-26; the ID now refers solely to the SM user-labels item
below). See `signals/` for the confirmed contracts.

**Resolved & retired (2026-06-14 — code landed, full gate green):** SK-4 (RDS instance class now a
blueprint `instance_class` knob → `fixture.DB`, default `db.t3.medium`; RDS models no class-derived series
so the field is declarative), SK-5 (ElastiCache `instance_class` knob drives `database_memory_usage_percentage`
via a real AWS node-type → max-memory map; `cache.r6g.large` corrected to its real 13.07 GiB — synth matched
to reality, not the predecessor's frozen 2 GiB), SK-19 (Cloudflare metrics source CONFIRMED = `lablabs/cloudflare-exporter`,
OSS-verified per predecessor `emit/cloudflare.go` + `verified-module-names-2026-06-12.md`; zone/account/colocations
already config-driven knobs with generic defaults), SK-21 (SM provisioner = standalone `cmd/sm-provision`
CLI; offline-probe identity now single-sourced from exported `sm.DefaultProbe*` constants so provisioner↔emitter
can't drift; two-phase startup documented in README), SK-23 (FM `GC_FM_STACK_ID` was ALREADY a distinct wired
credential — `config.go` reads it, `.env.example`/`.env` provision it, `TestEnvSurfaceAligned` enforces it; row
was stale), SK-26 (SM user labels emitted as sorted `label_<k>` on every series / `sm_check_info` / Loki stream),
SK-29 (AlloyDB `node_postgres_uptime` delta corrected to `Interval().Seconds()`=60/tick so the counter tracks
wall-clock 1:1 — no interface change needed; the "requires threading tick-duration" claim was overstated since
the construct already knows its own interval), SK-2 (MWAA — live capture 2026-06-14 confirms TWO
prefixes: `aws_mwaa_*` = AWS/MWAA infra namespace (17 base metrics, worker/scheduler/DB dims) and
`aws_amazonmwaa_*` = AmazonMWAA Airflow-operational namespace (31 base, DAG/Function/Pool dims); the
cantfind `aws_amazonmwaa_*`-only guess was half-right. Documented in signals/cw.md [slug: cw-mwaa]; MWAA construct
itself remains out of v1 scope). See `signals/` for the confirmed contracts.

**Resolved & retired (2026-06-15 — Beyla live capture, folded into `signals/beyla.md`):** SK-41
(k8s label spelling: `k8s_namespace_name` CONFIRMED, not `k8s_namespace`; full live k8s envelope —
all `k8s_*` keys present, absent dims emitted `""`), SK-42 (network-flow chart-curated allow-list
CONFIRMED: `direction, k8s_cluster_name, k8s_src_name, k8s_src_namespace, k8s_src_owner_name,
k8s_src_owner_type, k8s_dst_name, k8s_dst_namespace, k8s_dst_owner_name, k8s_dst_owner_type`;
`direction ∈ {request,response,unknown}`; extra `*_owner_type` + `*_name` keys beyond the original
guess; dst fields often `""`), SK-43 (span-metric/service-graph `source="beyla"` CONFIRMED; also
corrected `telemetry_sdk_name="beyla"` not "opentelemetry", `telemetry_distro_version="unset"`,
`telemetry_sdk_version="v1.43.0"`, service-graph uses `client_k8s_*`/`server_k8s_*` PREFIXED keys,
`traces_host_info{grafana_host_id}`, internal-metric label sets + bpf network packet counters,
`beyla_build_info` /metrics has `target_lang` vs `beyla_internal_build_info` /internal/metrics).
Captured live from a Beyla 3.20.0 DaemonSet (grafana-k8s-monitoring-beyla), OBI
telemetry_sdk_version v1.43.0, 2026-06-15. ⚠ Recorded the ABSENT-DIM TRAP: Beyla emits absent
k8s/cloud dims as `""`, NOT omitted (exception to the synthkit omit-absent invariant). See
`signals/beyla.md`.

---

## AWS CloudWatch (construct kinds: `s3`, `rds`, `elasticache`, `eks`, plus generic CW)

_(SK-2 MWAA resolved 2026-06-14 — see retired list above + signals/cw.md [slug: cw-mwaa].)_

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-31 | `docdb` | DocumentDB metric family brought in from predecessor research dossier (`research/aws-infra-cloudwatch.md` §8 + SIGNALS.md §2.9); no synthkit live capture yet. Names `opcounters_getmore`, `opcounters_command`, `documents_inserted/returned/updated/deleted` are predecessor-§2.9 only, absent from AWS docs dossier. | All bases emitted per signals/cw.md [slug: cw-docdb] | live-capture |
| SK-32 | `neptune` | Neptune metric family brought in from predecessor research dossier (`research/aws-infra-cloudwatch.md` §12 + SIGNALS.md §2.9); no synthkit live capture yet. Name `num_tx_opened` is predecessor-§2.9 only, absent from AWS docs dossier. | All bases emitted per signals/cw.md [slug: cw-neptune] | live-capture |
| SK-33 | `aoss` | AOSS metric family brought in from predecessor research dossier (`research/aws-infra-cloudwatch.md` §9 + SIGNALS.md §2.9); no synthkit live capture yet. OCU scope: `search_ocu`/`indexing_ocu` are account-level (`dimension_ClientId` only) OR per-CollectionGroup — NOT per-collection; synthkit emits account-level. Full OCU-group granularity unverified. | All bases emitted per signals/cw.md [slug: cw-aoss] | live-capture |
| SK-34 | `glue` | Glue metric family brought in from predecessor research dossier (`research/aws-infra-cloudwatch.md` §11 + SIGNALS.md §2.9); no synthkit live capture yet. Dotted CW metric names (e.g. `glue.driver.aggregate.bytesRead`) → flattened form (dots→`_`, lowercased) is assumed correct per the cw-naming law; not live-verified for this namespace. | All bases emitted per signals/cw.md [slug: cw-glue] | live-capture |
| SK-75 | *(future construct — no synthkit construct exists yet)* | **Amazon OpenSearch** — vector + keyword RAG retrieval store. Appears in AI platform blueprints as a `retrieval`-type trace node (`opensearch-vectors`) in the app workload backend graph; emits no metrics today (trace-only hop). No synthkit construct or metric family exists for OpenSearch/Elasticsearch. Candidate signals: CloudWatch `AWS/ES` namespace → Prometheus-mangled `aws_es_*` (cluster health, search/index latency, JVM heap, EBS throughput); or the [OpenSearch Prometheus Exporter](https://github.com/aiven/prometheus-exporter-plugin-for-opensearch) which exposes `opensearch_*` gauge/histogram families (indices, JVM, thread pools, circuit-breakers). Dimension set for `AWS/ES`: `ClientId`, `DomainName` (+ optionally `NodeId`). | NONE EMITTED — not in scope until a construct is built | vendor-doc (`AWS/ES` CW metric reference; OpenSearch Prometheus exporter README) / live-capture (enable CloudWatch detailed monitoring on an OpenSearch domain, or deploy the exporter against a real cluster) |

---

## Kubernetes substrate + add-ons (construct kinds: `k8s_monitoring`, add-on kinds)

_(SK-41/SK-42/SK-43 RESOLVED 2026-06-15 — see retired note below + signals/beyla.md.)_

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-44 | `beyla_agent` (standalone) | Standalone-mode resource label keys `host_name` and `host_id`. k8s mode emits k8s_* labels (now live-confirmed); standalone must emit non-k8s host identity. OBI defines these but **only k8s mode was captured** (Beyla 3.20.0 DaemonSet, 2026-06-15) — no standalone instance in the capture fleet. Still PENDING. Note: `db_client_operation_duration_seconds` and `beyla_avoided_services` are likewise source-confirmed only (no DB-observed/avoided service in the capture) — kept `v: assumed` in signals/beyla.md pending a live instance. | emitted as `host_name`, `host_id` per beyla.go ResourceLabelKeys(ModeStandalone) | live-capture |

---

## Database Observability (construct kinds: `dbo11y_mysql`, `dbo11y_postgres`)

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|

---

## CSP Azure / GCP (construct kinds: `cloud_azure`, `cloud_gcp`)

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-45 | `csp_azure` (ai sub-signal) | `ProcessedFineTunedTrainingHours` REST API name spelling: ctx7 docs confirmed the description ("Number of Training Hours Processed on an OpenAI FineTuned Model") and confirmed `ProcessedFineTunedTrainingHours` as the metric name (from the dedicated heading in the docs), but the exact REST API name in the table row was not byte-captured — the `FineTuned` vs `FineTuning` spelling is assumed. If the REST API name is `ProcessedFineTuningTrainingHours` the emitted series name would be wrong. | `azure_microsoft_cognitiveservices_accounts_processed_fine_tuned_training_hours_total_count` | vendor-doc (confirm via live Azure Monitor capture or the raw metrics table JSON) |
| SK-46 | `csp_gcp` (vertex sub-signal) | Prometheus metric names for Vertex AI `aiplatform.googleapis.com/*` metrics as exported by Alloy `prometheus.exporter.gcp` (embeds `stackdriver_exporter`) are UNCONFIRMED. The Stackdriver exporter resource-type string for Vertex AI Endpoints (`aiplatform.googleapis.com/Endpoint`) and Locations (`aiplatform.googleapis.com/Location`) — specifically whether the Prometheus resource prefix is `aiplatform_googleapis_com_endpoint` or a different slug — is extrapolated from the SK-18 naming pattern, not live-captured. Label names (`endpoint_id`, `model_id`, `version_id`, `model_version_id`, `response_code_class`, `error_type`) are from GCP docs prose, not a live scrape. The full metric path for `prediction/online/response/latencies` (nested path level) is particularly uncertain. | All bases emitted per `internal/construct/cspgcp/cspgcp.go` `emitVertex()`; see signals/cspgcp.md [slug: cspgcp-vertex] | live-capture (run Alloy `prometheus.exporter.gcp` against a GCP project with Vertex AI endpoints active, scrape `/metrics`, filter `stackdriver_aiplatform_*`) |
| SK-47 | `csp_gcp` (vertex sub-signal) | GCP custom monitoring metrics for the AI-gateway use case: VPC-Service-Control access-denied, rate-limiting 429/503, and not-found 404 counters emitted by custom Vertex AI proxy implementations. These are NOT built-in `aiplatform.googleapis.com/` metrics — they would be user-defined under `custom.googleapis.com/` or `external.googleapis.com/`. The exact metric names, label schemas, and whether they are emitted by the gateway as custom metrics or through a sidecar exporter are unknown. Not emitted by the vertex sub-signal (no representative value). | NONE EMITTED — not in scope until a live deployment is captured | live-capture / vendor-doc (Vertex AI proxy / Apigee AI Gateway docs; or capture from a running AI-gateway deployment) |
| SK-48 | `qualification_pipeline` | Suite-level qualification signals (`qualification_test_cases_total`, `qualification_test_failures_total`, `qualification_report_generated`) are COINED — no upstream exporter provides them. The names are synthkit convention derived from the qualification use case; no vendor docs, no live capture. The `gitlab_ci_pipeline_*` family is confirmed against `mvisonneau/gitlab-ci-pipelines-exporter` docs/metrics.md (2026-06-15) but has no synthkit live capture. | All three coined gauges emitted per signals/qualification.md [slug: qualification-pipeline] | vendor-doc / live-capture (run gitlab-ci-pipelines-exporter against a GitLab CI instance and scrape `/metrics`) |

---

## Cloudflare (construct kind: `cloudflare`)

_(All Cloudflare items resolved — see SK-19 in the retired list.)_

---

## Application workload — APM / traces / logs / RUM (construct/workload kind: `web_service`)

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-27 | `web_service` (config seams) | Workload config-time seams the v1 build must freeze before emitters agree: generic service-name placeholder constants (`FrontendServiceName`/`BackendServiceName`); `job` label convention (`{service_namespace}/{service_name}` source); `PodName(svc, replica)` helper (`{svc}-0`); DB CLIENT span semconv attrs (`db.system`/`db.name`/`db.operation`/`db.statement`) for declared DB hops; browser `http.host` (configurable frontend hostname); Faro `gf.feo11y.app.id` (stack-specific — must be config, not constant); Faro stream `job` (collector-injected vs emitted in OTLP resource attrs). | predecessor conventions as defaults | config-driven (frozen as workload config keys + the `fixture` vocabulary) |
| SK-56/57/58 ✅ RESOLVED 2026-06-16 (premise WRONG — web-vitals are NOT Prometheus metrics) | `web_service`/`app` (RUM/Faro) | RESOLVED via live capture (`{service_name="frontend"} |= "type=web-vitals"`): Faro web-vitals arrive ONLY as Loki **measurement log events** (`kind=measurement type=web-vitals`) and traces — NEVER as Prometheus gauges. Real body fields (logfmt): `lcp`/`value_lcp`, `cls`/`value_cls`, `inp`/`value_inp`, `ttfb`/`value_ttfb`, `fcp`/`value_fcp` + `value_first_byte_to_fcp`, `delta`/`value_delta`, `context_rating` ∈ {good,needs-improvement,poor}, `context_load_state`, `context_navigation_type`, `context_id`. So the gauges `largest_contentful_paint`/`cumulative_layout_shift`/`interaction_to_next_paint` in `rum_faro.go` are FABRICATED (don't exist) → REALISM FIX: remove those MetricSpecs; emit all 5 vitals as `type=web-vitals` measurement log lines; reconcile WS1 dashboard panels to LogQL over the measurements. | (was 3 fabricated gauges — being removed) | RESOLVED (live capture + Rob) — synth correction tracked as the RUM-realism fix |

---

## Synthetic Monitoring (construct kind: `synthetic_monitoring`)

_(All Synthetic Monitoring items resolved — see SK-21 and SK-26 in the retired list.)_

---

## Fleet Management + Alloy (construct kinds: `fleet_management`, `alloy_health`)

_(SK-23 resolved — see retired list. SK-24 de-scoped — see notes below.)_

---

## Cumulative-discipline / regression items

_(All cumulative-discipline items resolved — see SK-29 in the retired list.)_

---

## AI / LLM catalogue (construct kinds: `bedrock`, `agentcore`, `portkey_gateway`, `portkey_poller`, `langsmith_platform`, `langsmith_eval`, `snowflake`; workload `web_service` gen_ai)

Brought in from the predecessor (Spec 2b ban-lift). Each carries a representative/synthetic value already
emitted (structurally correct); the exact value is unconfirmed against a synthkit live capture.

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-35 | `web_service` (gen_ai) | The two core client metrics (`gen_ai_client_token_usage`, `_operation_duration_seconds`) are live-confirmed (v: ok); `gen_ai_request_model` label confirmed on both (added 2026-06-23); `gen_ai_response_model` is span-only (NOT a metric label). The streaming client (`_time_to_first_chunk_seconds`, `_time_per_output_chunk_seconds`) + server (`gen_ai_server_request_duration_seconds`, `_time_to_first_token_seconds`, `_time_per_output_token_seconds`) families, and ALL histogram bucket boundaries, are ADVISORY in the spec — not mandated, not empirically confirmed. | emitted with advisory buckets per `internal/genai` + signals/genai.md | vendor-doc (`semantic-conventions-genai`) / live-capture |
| SK-36 | `bedrock` | `AWS/Bedrock` (+`/Agents`, `/Guardrails`) metric set is AWS-docs-sourced via predecessor §2.3; the CW mangling is cw-law-verified (`internal/cw`) but the Bedrock namespaces have no synthkit live capture. Mar-2026 metrics (`time_to_first_token`, `estimated_tpmquota_usage` — an approximation) + `dimension_ServiceTier`/`ResolvedServiceTier`/`ContextWindow` especially unverified. | all bases emitted per signals/cw-style block in signals/bedrock.md | live-capture / vendor-doc |
| SK-37 | `agentcore` | `AWS/Bedrock-AgentCore` invocation-class names are PROSE-derived (AWS publishes no CW name table — predecessor §2.4); the resource-usage hyphen→underscore mangling (`CPUUsed-vCPUHours`→`cpu_used_v_cpu_hours`, `MemoryUsed-GBHours`→`memory_used_gb_hours`) is unverified; invocation-class DIMENSIONS are undocumented (synthkit OMITS `dimension_Service/Resource/Name` on them per names-are-law). | bases emitted per signals/agentcore.md | live-capture / vendor-doc |
| SK-71 | `agentcore` (LOG lanes) | The `source=agentcore_app` (APPLICATION_LOGS) + `source=agentcore_usage` (USAGE_LOGS) Loki log SHAPES are `v: assumed` — AWS publishes no normative AgentCore log schema, so all field/label names were derived from AWS docs + CW-metric realism, NEVER validated against a real AgentCore workload: the body fields (`msg`, `agent`, the `event` phase vocab `agent_start\|tool_invoke\|tool_result\|agent_step\|agent_end`; `cpu`/`memory` per-second floats), the stream label `job=cloud/aws/bedrock-agentcore`, and the `session_id`/`trace_id` Meta formats. The `source=agentcore_spans` lane (aws/spans X-Ray record envelope, CW-only → never Tempo) is DEFERRED — record JSON unverified, not in the v1 golden-thread set. | app+usage streams emitted per signals/agentcore.md `[slug: agentcore-logs]` (`v: assumed`); spans deferred | live-capture (a real Bedrock AgentCore runtime vending APPLICATION_LOGS/USAGE_LOGS to CloudWatch → Firehose → Loki) / vendor-doc |
| SK-38 | `portkey_gateway`, `portkey_poller` | Gateway 14 metrics ✅ vs portkey.ai docs, BUT `portkey_request_duration_milliseconds` histogram buckets are undocumented; the `metadata_use_case`/`metadata_context`/`metadata_agent` labels assume a specific gateway metadata-allowlist config; the `poller_*` self-telemetry names are synthkit convention (de-prefixed from a deployment-specific naming convention); the `portkey_api_*` `status_class` enum + `workspace` value are Ⓐ. | emitted per signals/portkey.md | vendor-doc / config-driven |
| SK-39 | `langsmith_eval` | `langsmith_eval_*` metric names are "OUR convention" (LangSmith publishes NO such metric namespace — the values are poll-derived from the feedback API, the names invented in the predecessor). (The `langsmith_platform` self-metrics — `process_*`/`python_*`/`ClickHouse*`/`redis_*`/`pg_*`/`nginx_*` — are standard exporter families, verified, NOT in this register.) | emitted per signals/langsmith.md [slug: langsmith-eval] | config-driven (synthkit naming convention) / vendor-doc |
| SK-74 | `langsmith_platform` | The two HTTP-middleware series on the LangSmith Python services — `http_requests_total` + `http_request_duration_seconds` (marked `v: assumed`, "HTTP-middleware (Ⓘ)" in signals/langsmith.md `[slug: langsmith-platform]`) — are ASSUMED present (standard Python web-framework middleware names) but never confirmed against a real LangSmith deployment. (The OTHER platform self-metrics in that file ARE verified standard-exporter families per the SK-39 note — only these two middleware series are unverified.) | both emitted per signals/langsmith.md `[slug: langsmith-platform]` (`v: assumed`) | live-capture (scrape a real LangSmith platform service `/metrics`) / vendor-doc |
| SK-40 | `snowflake` | 27-gauge family verified against the `grafana/snowflake-prometheus-exporter` source (`collector/query.go`+`collector.go`, 2026-06-10) but no synthkit live capture; the `job`/`instance` label form + default `:9975` are assumed. | all 27 emitted per signals/snowflake.md | live-capture |

---

## k8s-monitoring new constructs (2026-06-15)

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-49 | `k8s_cluster` (etcd sub-family) | etcd histogram bucket boundaries (`etcd_disk_wal_fsync_duration_seconds`, `etcd_disk_backend_commit_duration_seconds`, `etcd_network_peer_round_trip_time_seconds`, `grpc_server_handling_seconds`) and exact steady-state values for leader-election / proposals metrics. Managed EKS does not expose etcd directly — all values doc-sourced. | Synth emits representative healthy-steady-state values with doc-sourced `[0.001..1.024]` bounds (fast NVMe) per `internal/construct/etcd/etcd.go` | live-capture (bare-metal or self-managed etcd, or etcd-compatible managed DB with Prometheus endpoint — scrape `:2381/metrics`; NOT resolvable on managed EKS) |
| SK-50 | `k8s_cluster` (node-logs, non-Bottlerocket) | Exact systemd journal unit names + `level` values for non-Bottlerocket EKS nodes (e.g. Amazon Linux 2023). Bottlerocket units (`host-containers@control.service`, `init.scope`) are live-confirmed 2026-06-15; AL2023 / generic Linux units `kubelet.service` + `containerd.service` (level=INFO) are doc-sourced. Additional units (e.g. `systemd-logind.service`, `cron.service`) may appear. | Synth emits `kubelet.service`/`containerd.service` with `level=INFO` for non-Bottlerocket per `internal/construct/k8scluster/nodelogs.go` | live-capture (add a non-Bottlerocket node group to a reference cluster and scrape journal via Alloy `loki.source.journal`) |
| SK-51 | `k8s_cluster` (kube-proxy histograms) | kube-proxy histogram bucket boundaries for `kubeproxy_sync_proxy_rules_duration_seconds` and sibling histograms. Live-evidenced 2026-06-15 that these families ARE emitted; bucket boundaries used in synth are the Prometheus default `[0.005..10]` — unverified against real kube-proxy `/metrics`. | Synth uses Prometheus default seconds buckets `[.005,.01,.025,.05,.1,.25,.5,1,2.5,5,10]` per `internal/construct/k8scluster/kubeproxy.go` | live-capture (scrape a real kube-proxy metrics endpoint `:10249/metrics` and inspect `_bucket` boundaries) |
| SK-52 ⚠ PARTIAL (label keys RESOLVED 2026-06-15) | `k8s_cluster` (kube-apiserver histogram buckets) | RESOLVED: EKS DOES expose the apiserver `/metrics` endpoint — enabled `clusterMetrics.apiServer` on a live reference EKS cluster, captured 430 families. Label KEYS now live-verified (`apiserver_request_total`: verb/code/component/group/version/resource/scope; `apiserver_current_inflight_requests`: request_kind; `etcd_request_duration_seconds`: operation/resource — synth's `type` was WRONG, fixed). STILL PENDING: histogram bucket boundaries (not extracted). | Label keys v: ok per `controlplane.go`; histogram buckets use Prometheus defaults | live-capture (extract `_bucket` boundaries from the apiserver `/metrics` — endpoint still enabled, or re-enable) |
| SK-53 ✅ RESOLVED 2026-06-15 (confirmed UNREACHABLE) | `k8s_cluster` (kube-scheduler + kube-controller-manager) | CONFIRMED via live capture: `scheduler_schedule_attempts_total` and `{job="kube-controller-manager"}` both return ZERO series on managed EKS — AWS does not expose the scheduler/controller-manager endpoints (unlike apiserver/kube-proxy). Synth values stay doc-sourced (kube-prometheus-stack mixin); they cannot be live-verified without a self-managed/kubeadm cluster. | Doc-sourced values per `controlplane.go` (will never be EKS-verifiable) | deferred — needs self-managed/kubeadm cluster |
| SK-54 ✅ RESOLVED 2026-06-15 | `k8s_cluster` (windows-exporter) | RESOLVED: added a temporary Windows Server 2022 node group to a reference cluster, captured the real windows-exporter `/metrics` (484 lines). Default k8s-monitoring enables collectors cpu/container/logical_disk/memory/net/os ONLY — so windows_cs_* + windows_system_* do NOT exist, and windows_os_* exposes only windows_os_info/windows_os_hostname (NOT windows_os_visible_memory_bytes). `windowsexporter.go` corrected: dropped 5 phantom names, added windows_cpu_logical_processor / windows_memory_physical_total_bytes / windows_os_info / windows_logical_disk_size_bytes / windows_net_packets_received_total; cpu `core` label is "<group>,<core>". Side note: metrics don't reach GC without opening the Windows host firewall for :9182 (scrape up=0). | Names + label keys v: ok per `controlplane.go`-style live capture | — (resolved; Windows node group torn down) |
| SK-72 | `k8s_cluster` (addons — labels/ALPHA names) | Two classes of UNFALSIFIABLE-on-capture-stack assumptions in signals/k8s-addons.md, previously mis-citing the now-RESOLVED SK-10 (which only confirmed the AWS LBC `job` label): (a) the deployment-defined `job` labels `integrations/aws-vpc-cni` `[slug: k8s-vpc-cni]`, `integrations/aws-ebs-csi-driver` `[slug: k8s-ebs-csi]`, `integrations/cluster-autoscaler` `[slug: k8s-cluster-autoscaler]` are `Ⓐ` (the reference cluster runs Karpenter / doesn't expose these addon scrape jobs); (b) the two KSM ALPHA metrics `kube_ingress_annotations` + `kube_ingress_metadata_resource_version` `[slug: k8s-ksm-ingress]` are `v: assumed` (ALPHA — name/presence can change across KSM releases). | emitted per signals/k8s-addons.md (`Ⓐ`/`v: assumed`) | live-capture (deploy the actual addons / enable KSM ingress collector on the reference cluster and scrape `job`/metric names) |
| SK-73 | `k8s_cluster` (kubelet histogram buckets) | Two kubelet histograms carry `v: assumed` bucket boundaries NOT covered by the RESOLVED SK-7 (which confirmed defaults for `cgroup_manager`/`pleg_relist`(_duration)/`pod_worker` only): `kubelet_pod_start_duration_seconds` is a KNOWN DIVERGENCE — synth emits Prometheus default buckets, real kubelet uses a custom ~25-boundary set `[0.5..3600]` (will mis-render pod-startup latency percentiles); `kubelet_pleg_relist_interval_seconds` buckets are assumed (the `_interval` variant was not in SK-7's capture). | synth emits Prometheus default buckets per signals/k8s.md `[slug: k8s-kubelet]` | live-capture (scrape a real kubelet `:10250/metrics` and extract the `_bucket` boundaries for these two histograms) |
| SK-76 | `cluster_autoscaler` | `cluster_autoscaler_*` metric family has NEVER been live-captured on the reference EKS cluster because EKS+Karpenter replaces cluster-autoscaler on this cluster. ALL `cluster_autoscaler_*` metric names, label keys, and values in `signals/k8s-addons.md [slug: k8s-cluster-autoscaler]` are `v: assumed` (doc-sourced only). In particular: `job="integrations/cluster-autoscaler"`, the label `k8s_version`, the metric `cluster_autoscaler_last_activity` type (gauge of epoch seconds) and bucket boundaries of `cluster_autoscaler_function_duration_seconds` are unverified. | All bases emitted per signals/k8s-addons.md [slug: k8s-cluster-autoscaler] (v: assumed) | live-capture (deploy cluster-autoscaler to a real cluster and scrape `:8085/metrics`) |
| SK-77 | `argocd` | ArgoCD `redis_exporter` (`job=argocd-metrics`, port 9121) metric names are UNVERIFIED. The redis_exporter sidecar container is named `metrics` (not `redis_exporter`) per live kube_pod_container_info capture. The metric NAMES it emits (e.g. `redis_connected_clients`, `redis_memory_used_bytes`, `redis_total_commands_processed_total`) are standard redis_exporter family names but have NOT been scraped from an actual argocd redis pod on a live cluster. Synthkit models the sidecar shape (StampPodsContainer container="metrics") but does not emit redis_exporter metrics yet (not in the argocd construct). | Not emitted — sidecar presence modeled but no redis_exporter metric family in signals/k8s-addons.md yet | live-capture (scrape argocd-redis pod port 9121 on a real ArgoCD cluster and capture `/metrics`) |
| SK-78 | `envoy_gateway` | Envoy-Gateway DATA-PLANE proxy deployment naming convention is `envoy-<gw-namespace>-<gw-name>-<hash>` — the exact hash suffix is deployment-specific and cannot be statically modeled. Synthkit emits a representative form `envoy-default-eg-proxy`. Additionally, the shutdown-manager sidecar on data-plane pods (container `shutdown-manager`) emits a subset of envoy_* metrics including `envoy_control_plane_connected_state` and `envoy_tracing_opentelemetry_spans_sent` — these appear TWICE per pod (once per container). The exact metric subset emitted by shutdown-manager vs the primary envoy container is not byte-verified. | Synthkit models single representative proxy deployment `envoy-default-eg-proxy`; shutdown-manager modeled as emitting the same full metric set (v: assumed for sidecar subset) | live-capture (inspect a real Envoy-Gateway data-plane pod with `kubectl exec` or scrape both containers on port 19001 and diff the metric sets) |
| SK-79 | `host` (windows lane) | `windows_os_paging_limit_bytes` — pagefile/paging limit gauge under the `os` collector. Was in the prior windows allowlist; the reference WINSRV capture's OS section (`docs/superpowers/host-capture.md`) shows only `windows_os_hostname` + `windows_os_info`, so this name is UNCONFIRMED on a real host and is NOT emitted. (Synthkit emits the paging surface via `windows_pagefile_limit_bytes{file}` per signals/host.md `[slug: host-windows]`.) Note: `windows_service_status` / `windows_cs_*` / `windows_disk_drive_status` are confirmed-NOT-real (phantoms — resolved by the capture, NOT pending). | NOT emitted — dropped from `windowsIntegrationNames` (`internal/nodeexp/profiles.go`) | live-capture (scrape windows_exporter `/metrics` on the reference WINSRV host with the `os` collector enabled; confirm name + labels) |
| SK-80 | `host` (windows lane) | `windows_os_physical_memory_free_bytes` — free physical memory under the `os` collector. Not in the reference WINSRV capture's OS section; UNCONFIRMED, NOT emitted. Synthkit emits free physical memory via `windows_memory_physical_free_bytes` instead. | NOT emitted — dropped from `windowsIntegrationNames` | live-capture (reference WINSRV windows_exporter `/metrics`, `os` collector) |
| SK-81 | `host` (windows lane) | `windows_os_timezone` — timezone info series under the `os` collector. Not in the reference WINSRV capture's OS section; UNCONFIRMED, NOT emitted. Synthkit emits timezone via `windows_time_timezone{timezone}` (time collector) instead. | NOT emitted — dropped from `windowsIntegrationNames` | live-capture (reference WINSRV windows_exporter `/metrics`, `os` collector) |
| SK-82 | `host` (windows lane) | `windows_system_system_up_time` — system uptime gauge under the `system` collector. Not in the reference WINSRV capture's System section; UNCONFIRMED, NOT emitted. | NOT emitted — dropped from `windowsIntegrationNames` | live-capture (reference WINSRV windows_exporter `/metrics`, `system` collector) |
| SK-83 | `host` (windows lane) | `windows_system_boot_time_timestamp_seconds` — boot-time epoch gauge. The reference WINSRV capture shows only the non-`_seconds` form `windows_system_boot_time_timestamp`; the `_seconds`-suffixed name is UNCONFIRMED and NOT emitted. | NOT emitted — dropped from `windowsIntegrationNames` | live-capture (reference WINSRV windows_exporter `/metrics`; confirm `_seconds` vs non-`_seconds` form) |
| SK-55 ✅ RESOLVED 2026-06-15 | `traces_host_info` label set | RESOLVED via live capture: 5 series carry ONLY `grafana_host_id` (EC2 instance IDs) — NO `k8s_cluster_name` or any other label. `hostinfo.go` corrected to emit `grafana_host_id` only; `[slug: apm-host-info]` updated. | Synth emits `grafana_host_id` ONLY (v: ok, live-confirmed) | — (resolved) |

---

## Notes on items deliberately NOT carried into synthkit

**SK-24 (`fleet_management` — FM-page red "unhealthy" badge) — DE-SCOPED (2026-06-14).** Turning the FM page's
collector badge red requires provisioning a FIRING alert rule carrying `collector_id` (FM `groupAlertsByCollector`).
The predecessor only ever did dashboard-only "unhealthy" (a component-count shift), and the synthkit FM construct already
matches that. The incremental FM-page badge is cosmetic and not worth the alert-rule provisioning machinery; explicitly
dropped rather than deferred. If a future need arises, the resolution path is vendor-doc (FM `groupAlertsByCollector`)
+ an alert rule in the provisioner.

**AI/LLM families are now IN SCOPE (Spec 2b ban-lift).** The predecessor's AI-tied PENDING items have been
re-imported into the AI/LLM section above: gen_ai semconv (streaming/server metrics + advisory
buckets) → SK-35; Bedrock per-use-case attribution is logs-only (no metric dimension — preserved in
signals/bedrock.md `[slug: bedrock-logs]`); AgentCore vended-metric names + dims → SK-37; Portkey
gateway buckets + config-slug labels + poller self-telemetry → SK-38; LangSmith eval extraction →
SK-39; Snowflake exporter names → SK-40. **Still deferred to scenario-driven work** (NOT yet in this
register): compliance-tier qualification metrics and MCP-gateway health paths — these are
deployment/scenario specific and attach with the Spec 3 blueprints, not the generic 2b catalogue. The `agentcore_app` +
`agentcore_usage` log-lane shapes (emitted, but `v: assumed` — never validated against a real workload)
and the optional deferred `agentcore_spans` feed are now tracked together as SK-71
(signals/agentcore.md `[slug: agentcore-logs]`).

---

## Telemetry-spec catalog profiles (Wave 2 — Spec 5) — language runtime + HTTP server scraped metrics

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-59 | `profiles/scraped_http_server` | `http_server_active_requests` — OTel HTTP semconv stable gauge tracking in-flight server requests. NOT found in signals/ during Wave 2 profile authoring (2026-06-15). signals/beyla.md confirms `http_server_request_duration_seconds` (OTel semconv v1.38.0) but does not list the active-requests gauge. | `http_server_active_requests` (gauge, unit=requests, Shape base=5.0) emitted per `internal/telemetryspec/profiles/scraped_http_server.go` | vendor-doc (OTel semconv `http.server.active_requests` stable metric — confirm Prometheus-mangled form; verify label set `http.request.method`+`url.scheme`) |
| SK-60 | `profiles/runtime_jvm` | `jvm_memory_used_bytes` — OTel JVM semconv / micrometer standard heap gauge. NOT found in signals/ (signals/cw.md has Glue JVM names `driver_jvm_heap_used`/`all_jvm_heap_used` which are CW-specific, unrelated). | `jvm_memory_used_bytes` (gauge, unit=bytes, labels: area=heap\|nonheap, Shape base=250MiB) emitted per `internal/telemetryspec/profiles/runtime_jvm.go` | vendor-doc (OTel semconv `jvm.memory.used` → Prometheus-mangled `jvm_memory_used_bytes`; verify label set `jvm.memory.type`→`area` + `jvm.memory.pool.name`→`pool`) / live-capture (scrape a micrometer or OTel-SDK-instrumented JVM service) |
| SK-61 | `profiles/runtime_jvm` | `jvm_gc_pause_seconds` — micrometer/Spring Boot standard JVM GC pause histogram. NOT found in signals/. Micrometer publishes this but OTel semconv uses `jvm.gc.duration` (→ `jvm_gc_duration_seconds`). Correct Prometheus-mangled name and label set (action, cause) unverified. | `jvm_gc_pause_seconds` (histogram, labels: action=end of minor/major GC, cause=G1 Young/Old Generation etc.) emitted per `internal/telemetryspec/profiles/runtime_jvm.go` | vendor-doc (micrometer `JvmGcMetrics` → confirm Prometheus exposition name; or OTel JVM semconv `jvm.gc.duration` → `jvm_gc_duration_seconds_bucket`) / live-capture (Spring Boot actuator /metrics scrape) |
| SK-62 | `profiles/runtime_node` | `nodejs_eventloop_lag_seconds` — prom-client ≥14 event-loop lag gauge. signals/portkey.md uses the older `node_eventloop_lag_seconds` name (prom-client <14); the `nodejs_*` prefix form is NOT confirmed in signals/. Correct current prom-client name unverified. | `nodejs_eventloop_lag_seconds` (gauge, unit=seconds, Normal mean=3ms) emitted per `internal/telemetryspec/profiles/runtime_node.go` | vendor-doc (prom-client changelog for `nodejs_` rename; confirm current default metric names in prom-client v15+) / live-capture (Node.js service with prom-client scrape) |
| SK-63 | `profiles/runtime_node` | `nodejs_heap_size_used_bytes` — prom-client standard V8 heap used gauge. NOT found in signals/ (signals/portkey.md has `node_process_resident_memory_bytes` only). Correct prom-client name (heap_size_used vs heap_used_size) and label set unverified. | `nodejs_heap_size_used_bytes` (gauge, unit=bytes, Shape base=50MiB) emitted per `internal/telemetryspec/profiles/runtime_node.go` | vendor-doc (prom-client default metrics list; confirm exact name) / live-capture |
| SK-64 | `profiles/gateway_native_scrape` | `gc_type` label on `node_gc_duration_seconds` — GC-kind label. NOT in signals/. prom-client's default `nodejs_gc_duration_seconds` uses the label `kind` (minor/major/incremental/weakcb); `gc_type` is an unconfirmed convention. | `gc_type` ∈ {minor,major} emitted per `internal/telemetryspec/profiles/gateway_native_scrape.go` | vendor-doc (prom-client default metrics — confirm the GC label key is `kind`, not `gc_type`) / live-capture |

## Pyroscope continuous profiling (signals/profiles.md)

Captured shapes (Go SDK, Python SDK, Java async-profiler, eBPF) are asserted in `signals/profiles.md`;
the items below are the remaining UNCAPTURED gaps. The synth emits a structurally-correct representative
shape for each (so nothing is invented as fact) — confirm against a real capture, then fold into
`signals/profiles.md` and remove the row.

| ID | Construct kind | What is unknown | Representative value emitted | Resolution path |
|---|---|---|---|---|
| SK-65 | `web_service`/`app` (Python SDK push) | Richer Python profile types beyond `process_cpu`. pyroscope-io 1.0.11 emits `process_cpu` only; newer SDKs or alt profilers (py-spy/austin) may add `memory:*`/lock types. | SDK Python lane emits `process_cpu:cpu:nanoseconds:cpu:nanoseconds` only (`internal/pyroscope` SDKRuntimeTypes("python")) | live-capture (newer pyroscope-python SDK) — do NOT invent |
| SK-66 | `web_service`/`app` (.NET SDK push) | Richer .NET profile types beyond `process_cpu` (heap/alloc/exception). Only `process_cpu` observed on current .NET SDK. | SDK dotnet lane emits `process_cpu` only (SDKRuntimeTypes default) | live-capture (Pyroscope .NET SDK) |
| SK-67 | `k8s_profiling` (standalone/non-k8s) | Standalone (non-k8s) Alloy profiling label sets — e.g. `host_name`/`host_id` in place of `namespace`/`pod`/`node`. Only k8s-mode shapes captured. | construct emits the k8s-mode envelope only | live-capture (standalone Alloy profiling deployment) |
| SK-68 | `k8s_profiling` (java lane) | `jfr_event` values beyond `itimer` (async-profiler `alloc`/`lock`/`wall` may surface as distinct values under sustained alloc/lock load). | java lane emits `jfr_event=itimer` constant | live-capture (sustained-load JVM async-profiler) |
| SK-69 | `k8s_profiling` (pprof-scrape lane) | Rich pprof discovery labels — `app_kubernetes_io_*`, `helm_sh_chart`, `controller_revision_hash`, `apps_kubernetes_io_pod_index`, `statefulset_kubernetes_io_pod_name`, `topology_kubernetes_io_{region,zone}`, `instance` (ip:port). None exist on `fixture.Workload`, so the synth emits only the core k8s envelope + `source` + `profiles_grafana_com_scrape`. | pprof lane emits the core-subset label set (`internal/construct/k8sprofiling`) | config-driven (extend `fixture.Workload` with topology/helm/instance fields via the resolver) + live-capture |
| SK-70 | `web_service`/`app` (JVM SDK push) | JVM SDK-push profile shape (Pyroscope Java SDK / OTel profiling SDK), distinct from the Alloy async-profiler collector shape already captured. Until captured, the SDK lane emits `process_cpu` only for JVM nodes. | SDK jvm lane emits `process_cpu` only (SDKRuntimeTypes("jvm")) | live-capture (JVM app with the Pyroscope Java SDK, not async-profiler) |
</content>
</invoke>
