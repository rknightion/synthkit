# Portkey LLM gateway (→ Mimir + Loki) — ScopeSubstrate

Two delivery paths, two constructs sharing this contract: **`portkey_gateway`** (the gateway
pods' `/metrics` scrape → `portkey_*` + `node_*`) and **`portkey_poller`** (the Portkey Analytics
API polled → `portkey_api_*` gauges + `poller_*` self-telemetry). Substrate-scoped: disambiguated
by `app`/`env` (gateway) and `workspace` (poller); **never a `blueprint` label**. All identity
(workspace id, model/provider/use-case strings) is Config-borne — zero customer values here.
Global rules: [`00-canon.md`](00-canon.md) — `[slug: cardinality]`, `[slug: shape-rules]`.

*Provenance: verified vs portkey.ai docs 2026-06-10 (gateway) + code-verified spellings (poller).
SK-38.*

**Per-model magnitude weighting:** Portkey gateway and poller emission is weighted per model by
`genai.VolumeWeight(modelId)` and cost metrics by `genai.BlendedCostPerToken(modelId, inputFrac)`.
The full catalogue of model IDs, families, costs, and weights (across all four platforms) lives in
[`signals/genai-models.md`](genai-models.md) `[slug: genai-models]`.

---

## Portkey gateway scrape — `portkey_*` + `node_*` [slug: portkey-gateway]

Universal label schema on all 14 custom metrics: `app` (=service name), `env`, `method`,
`endpoint`, `code`, `provider`, `source`, `stream` (bool), `cacheStatus`. ⚠ **`cacheStatus` ∈
{`hit`,`miss`,`disabled`,`error`}** — there is NO `simple_hit`/`semantic_hit` value (semantic-hit
shows only in log status text). Opt-in labels (the realistic configured state — emitted ON):
`model`, `config_name`, `api_key_name`, and `metadata_*`. ⚠ `use_case`/`context`/`agent` are NOT
native Portkey labels — they ride as `metadata_use_case`/`metadata_context`/`metadata_agent` via
the metadata allowlist (Ⓐ assumes that gateway config; SK-38).

⚠ **Traps:** `llm_token_sum` & `llm_cost_sum` are **GAUGES** (cumulative; reset on gateway
restart) → query `delta()`, never `rate()`/`increase()`. `request_count` is a true Counter.

```yaml signals
family: portkey
scope: substrate
sink: promrw
labels:
  app: <service-name>
  env: <env>
  method: <http-method>
  endpoint: <endpoint>
  code: <status-code>
  provider: <provider>          # bare Portkey label (OTel form is gen_ai_provider_name)
  source: <source>
  stream: "true|false"
  cacheStatus: hit|miss|disabled|error
  model: <model>                # opt-in (PROMETHEUS_INCLUDE_MODEL_LABEL); emitted ON
  config_name: <config>         # opt-in
  api_key_name: <key-name>      # opt-in
  metadata_use_case: <use-case> # metadata allowlist (Ⓐ)
  metadata_context: <context>   # metadata allowlist (Ⓐ)
  metadata_agent: <agent>       # metadata allowlist (Ⓐ)
metrics:
  - {root: request_count, type: counter, unit: requests, v: ok, note: diurnal; ~5-50 rps peak}
  - {root: http_request_duration_seconds, type: histogram, unit: seconds, v: ok, buckets: "[0.1,1,1.5,2,3,5,7,10,15,20,30,45,60,90,120,240,500,1000,3000]"}
  - {root: llm_request_duration_milliseconds, type: histogram, unit: milliseconds, v: ok, note: "LLM provider round-trip", buckets: "[0.1,1,2,5,10,30,50,75,100,150,200,350,500,1000,2500,5000,10000,50000,100000,300000,500000,10000000]"}
  - {root: portkey_request_duration_milliseconds, type: histogram, unit: milliseconds, v: assumed, note: "complete gateway processing; buckets undocumented (SK-38)"}
  - {root: portkey_processing_time_excluding_last_byte_ms, type: histogram, unit: milliseconds, v: ok, note: "gateway overhead excl. streaming final byte"}
  - {root: llm_last_byte_diff_duration_milliseconds, type: histogram, unit: milliseconds, v: ok, note: "first→final byte gap; TTFT proxy"}
  - {root: authentication_duration_milliseconds, type: histogram, unit: milliseconds, v: ok}
  - {root: api_key_rate_limit_check_duration_milliseconds, type: histogram, unit: milliseconds, v: ok}
  - {root: pre_request_processing_duration_milliseconds, type: histogram, unit: milliseconds, v: ok}
  - {root: post_request_processing_duration_milliseconds, type: histogram, unit: milliseconds, v: ok}
  - {root: llm_cache_processing_duration_milliseconds, type: histogram, unit: milliseconds, v: ok}
  - {root: grpc_req_conversion_duration_milliseconds, type: histogram, unit: milliseconds, v: ok, buckets: "[0.01,0.1,0.5,1,2,5,10,20,50,100,200,500,1000,2000,5000]"}
  - {root: llm_token_sum, type: gauge, unit: tokens, v: ok, note: "GAUGE; cumulative; delta() not rate(); input≈3-10× output"}
  - {root: llm_cost_sum, type: gauge, unit: usd, v: ok, note: "GAUGE; cumulative USD; delta() not rate()"}
```

Plus a plausible subset of Node.js runtime metrics on the gateway pods: `node_process_cpu_user_seconds_total`
(counter), `node_process_resident_memory_bytes` (gauge), `node_eventloop_lag_seconds` (gauge),
`node_gc_duration_seconds` (histogram). **No metric exists for retries/fallbacks/guardrail verdicts**
→ they are log-derived (see `portkey-logs` + the `derived` rules below).

## Portkey Analytics poller — `portkey_api_*` + `poller_*` [slug: portkey-poller]

⚠ **ALL `portkey_api_*` are GAUGES — including the `_total`-suffixed ones** (Analytics returns
windowed aggregates + pre-computed percentiles, not live counters): emit via `state.Set`, NEVER
`state.Add`. The `portkey_api_` prefix distinguishes this poll family from the `portkey_*` scrape
family. 5-min-aligned boundary, ~5-15 min analytic lag.

```yaml signals
family: portkey_api
scope: substrate
sink: promrw
labels:
  workspace: <workspace-id>
  ai_model: <model>
  metadata_use_case: <use-case>
  status_class: 2xx|4xx|5xx     # requests_total only (Ⓐ value set, SK-38)
  quantile: "0.5|0.9|0.99"      # latency_seconds only (pre-computed percentiles, NOT buckets)
  env: <env-name>               # OPTIONAL — present only when env-scoped (for_each_env); OMITTED on aggregate (I13)
metrics:
  - {root: portkey_api_requests_total, type: gauge, unit: requests, v: ok, note: "GAUGE despite _total (state.Set)"}
  - {root: portkey_api_cost_usd, type: gauge, unit: usd, v: ok}
  - {root: portkey_api_tokens_total, type: gauge, unit: tokens, v: ok, note: "GAUGE despite _total"}
  - {root: portkey_api_latency_seconds, type: gauge, unit: seconds, v: ok, note: "pre-computed p50/p90/p99 (no histogram_quantile)"}
  - {root: portkey_api_error_rate, type: gauge, unit: ratio, v: ok, note: "0-1; no ai_model (workspace/use-case rollup)"}
  - {root: portkey_api_cache_hit_rate, type: gauge, unit: ratio, v: ok, note: "0-1"}
  - {root: portkey_api_rescued_requests_total, type: gauge, unit: requests, v: ok, note: "GAUGE; retry/fallback visibility"}
```

```yaml signals
family: poller
scope: substrate
sink: promrw
labels:
  api: <api-type>               # which Analytics API the poll targets
metrics:   # synthkit poller self-telemetry (synthkit convention; de-prefixed from a deployment-specific naming convention)
  - {root: poller_last_success_timestamp_seconds, type: gauge, unit: seconds, v: assumed, note: "= now.Unix() at successful boundary"}
  - {root: poller_window_lag_seconds, type: gauge, unit: seconds, v: assumed, note: "jittered analytic lag per api type"}
  - {root: poller_api_errors_total, type: counter, unit: errors, v: assumed, note: "~0 baseline (state.Add)"}
```

## Portkey export log — `source=portkey` [slug: portkey-logs]

Loki stream labels low-card only (`env`,`context`,`service_name`,`level`,`source`); high-card
(`trace_id`,`correlation_id`,`model`,`run_id`) → structured metadata (the sink asserts). Export-schema
body fields (✅): `trace_id`, `created_at`, `is_success`, **`ai_model`** (⚠ not `model`),
**`ai_org`** (⚠ the provider; differs from the Prometheus `provider` label), `req_units`,
`res_units`, `total_units`, `cost`, `cost_currency`, `response_time`, `response_status_code`,
`config`, `prompt_slug` (+ `prompt_version` Ⓐ), `mode`, `metadata{correlation_id, use_case}`;
retry/fallback keys Ⓐ `retry_count`, `fallback` (export keys undocumented, SK-38). Content-free.

## Derived (recording-rule, Spec 4 — NOT emitted by synthkit) [slug: portkey-derived]

`llm_retries_total` and `llm_fallbacks_total` have **no source metric** — they are derived from the
`source=portkey` export logs via Grafana-managed Loki recording rules (the emitted-vs-Grafana-derived
principle). Shipped as generated artifacts in Spec 4; marked `derived` here, never emitted.

**Follow-up (why portkey/exec panels read empty):** two gates remain before these series
exist — (1) the export-log JSON keys `retry_count`/`fallback` are still `Ⓐ assumed` (SK-38;
never verified against a live Portkey export — emitting them now would invent field names), and (2) the
`Rules()` recording rules generated in the dashboard builder are not yet
provisioned into the Grafana Loki ruler by the deploy path. Resolve SK-38 (confirm the export keys), then
wire ruler provisioning. The `portkey_api_rescued_requests_total` gauge already gives aggregate
retry/fallback visibility as a proxy in the meantime.
