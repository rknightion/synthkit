# gen_ai semantic-conventions (→ Mimir + Tempo) — ScopeBlueprint (workload-emitted)

The `gen_ai.*` trace-span attributes + `gen_ai_client_*`/`gen_ai_server_*` metrics are
**workload-emitted** (the `web_service` workload's AI hops), NOT a construct. The vocabulary
lives in [`internal/genai`](../internal/genai/genai.go) (a mechanic lib, peer to `internal/cw`);
the workload builds spans via the hand-encoded OTLP seam and pushes metrics via promrw with the
final pre-mangled names — the OTel metrics SDK stays banned (ARCHITECTURE §6.1). Global rules:
[`00-canon.md`](00-canon.md) — `[slug: cardinality]`, `[slug: shape-rules]`, `[slug: scoping]`.

*Provenance: gen_ai semconv (validated 2026-06-15 vs `open-telemetry/semantic-conventions`; the
gen_ai families have "Moved" to `semantic-conventions-genai`, stability development) + live capture
from a Grafana Cloud stack (empirical for the two core client metrics, 2026-06-15).*

---

## gen_ai trace-span attributes [slug: genai-spans]

Carried on the AI hop CLIENT spans (and the connected gateway SERVER span). Keys + operation
values are semconv-verbatim (see `internal/genai`). Span name = `{operation} {subject}` (e.g.
`chat gpt-4o`, `invoke_agent planner`, `execute_tool search`, `invoke_workflow rag-pipeline`).

- Operation values (`gen_ai.operation.name`): `chat`, `text_completion`, `embeddings`,
  `generate_content`, `create_agent`, `invoke_agent`, `invoke_workflow`, `execute_tool`,
  `retrieval`.
- Identity/usage attrs: `gen_ai.provider.name` (⚠ CURRENT spelling — replaced `gen_ai.system`
  pre-v1.37; old emitters still emit the `gen_ai_system` label), `gen_ai.request.model`,
  `gen_ai.response.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`,
  `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.cache_creation.input_tokens`,
  `gen_ai.usage.reasoning.output_tokens` (reasoning/chain-of-thought output tokens — e.g.
  o1/o3/claude-reasoning models; 2026-06-23),
  `gen_ai.conversation.id`, `gen_ai.agent.name`, `gen_ai.tool.name`, `gen_ai.tool.call.id`,
  `gen_ai.tool.type`, `gen_ai.data_source.id`, `gen_ai.output.type`, `error.type`.
- ⚠ **Content is NEVER emitted** — the strip-list (`internal/genai.ContentStripList`,
  `[slug: cardinality]`) bans `gen_ai.prompt`/`completion`/`input.messages`/`output.messages`/
  `system_instructions`/`tool.call.arguments`/`tool.call.result`/`tool.definitions`/
  `retrieval.documents`/`retrieval.query.text`/`input.value`/`output.value`.

## gen_ai metrics [slug: genai-metrics]

OTLP→Prom translation: `.`→`_`; unit `s`→`_seconds`; annotation units (`{token}`) dropped (no
suffix); histogram → `_bucket`/`_sum`/`_count` + `le`. Histogram buckets are ADVISORY in the spec
(not mandated) → the two streaming/server families + bucket arrays are `v: assumed` (SK-35); the
two core client metrics are live-confirmed (`v: ok`).

```yaml signals
family: gen_ai_client
scope: blueprint
sink: promrw
labels:
  gen_ai_operation_name: chat|text_completion|embeddings|invoke_agent|invoke_workflow|execute_tool|retrieval
  gen_ai_provider_name: <provider>          # e.g. azure-openai, gcp-vertex, bedrock (current; NOT gen_ai_system)
  gen_ai_request_model: <model>             # ✅ NOW on token_usage + operation_duration (2026-06-23)
  # gen_ai_response_model is SPAN-ONLY — NOT a metric label (cardinality; kept off to bound series)
  gen_ai_token_type: input|output           # token_usage ONLY (no "cache" value — cache tokens are span attrs)
  error_type: <error.type>                  # operation_duration on failure ONLY
  server_address: <host>                    # token_usage
  server_port: <port>                       # token_usage
metrics:
  - {root: gen_ai_client_token_usage, type: histogram, unit: token, v: ok, note: "empirically confirmed (live capture) + gen_ai_request_model label added 2026-06-23; buckets advisory→assumed (SK-35)"}
  - {root: gen_ai_client_operation_duration_seconds, type: histogram, unit: seconds, v: ok, note: "empirically confirmed (live capture) + gen_ai_request_model label added 2026-06-23; buckets advisory→assumed (SK-35)"}
  - {root: gen_ai_client_operation_time_to_first_chunk_seconds, type: histogram, unit: seconds, v: assumed, note: streaming-only (semconv v1.40.0); SK-35}
  - {root: gen_ai_client_operation_time_per_output_chunk_seconds, type: histogram, unit: seconds, v: assumed, note: streaming-only (semconv v1.40.0); SK-35}
```

```yaml signals
family: gen_ai_server
scope: blueprint
sink: promrw
labels:
  gen_ai_operation_name: chat|...
  gen_ai_provider_name: <provider>
  gen_ai_request_model: <model>
metrics:   # server-side (gateway-as-server) — optional; advisory buckets; SK-35
  - {root: gen_ai_server_request_duration_seconds, type: histogram, unit: seconds, v: assumed}
  - {root: gen_ai_server_time_to_first_token_seconds, type: histogram, unit: seconds, v: assumed}
  - {root: gen_ai_server_time_per_output_token_seconds, type: histogram, unit: seconds, v: assumed}
```

Resource-attr labels (GC promotes onto every OTLP metric ✅ live-confirmed): `service_name`,
`service_namespace`, `service_version`, `deployment_environment_name` (synthkit-native `_name` form;
gateway may also promote `deployment_environment` on live stacks — see `[slug: env-label-keys]`),
`k8s_cluster_name`, `k8s_namespace_name`, `k8s_pod_name`, `k8s_deployment_name`; `job` =
`{service.namespace}/{service.name}`; remaining resource attrs → `target_info`.

*Provenance update 2026-06-23: `gen_ai_request_model` confirmed on token_usage + operation_duration
(was absent); `gen_ai_response_model` is span-only (NOT a metric label); `gen_ai.usage.reasoning.output_tokens`
added to span attrs; `deployment_environment_name` replaces legacy form on native emit.*

---

## gen_ai in-process agentic span family [slug: genai-agentic-spans]

*Provenance: gen_ai semantic conventions (`open-telemetry/semantic-conventions-genai`) + live
capture of an app-emitted OTel LangGraph backend; validated 2026-06-16. Scoped to the `app`
workload with a LangGraph-style service graph.*

Four nested spans emitted in-process by the `app` workload for a LangGraph backend.
All spans live under the backend HTTP SERVER span (same `service.name = <backend-svc>`) — they are
NOT separate services. Span name form is `{operation} {subject}` per semconv.

| Span name (example)              | SpanKind        | `gen_ai.operation.name` value |
|----------------------------------|-----------------|-------------------------------|
| `invoke_workflow <workflow>`     | INTERNAL        | `invoke_workflow`             |
| `invoke_agent <agent>`           | INTERNAL        | `invoke_agent`                |
| `execute_tool <tool>`            | INTERNAL        | `execute_tool`                |
| `chat <model>`                   | CLIENT          | `chat`                        |

**Nesting order** (innermost last): SERVER → invoke_workflow → invoke_agent → execute_tool →
chat. The `chat` CLIENT leaf is the LLM call; it deduplicates the flat `chat` span from the
`gen_ai_client` profile — when the agentic flow is active, only the nested leaf is emitted.

### Span attributes

Stamped on **every** span in the family:

- `gen_ai.operation.name` — one of the four values above (enum extended from chat-only)
- `app.correlation_id`, `app.request_id`, `app.session_id` — the three correlation keys

Stamped on the **workflow span** only:

- `gen_ai.workflow.name` — LangGraph graph name (⚠ `assumed`: the semconv is thin on workflow
  attributes; this key is marked `assumed` in `internal/genai/genai.go`)

Stamped on the **agent span** only:

- `gen_ai.agent.name` — the agent identifier (e.g. `planner`, `researcher`, `writer`)

Stamped on the **tool span** only:

- `gen_ai.tool.name` — tool identifier (e.g. `search`, `retrieve_docs`, `code_exec`)

Stamped on the **chat leaf** only:

- `gen_ai.request.model` — model name (e.g. `gpt-4o`, `claude-3-5-sonnet`)
- `gen_ai.provider.name` — provider (e.g. `azure-openai`, `bedrock`)
- `gen_ai.usage.input_tokens` — prompt token count
- `gen_ai.usage.output_tokens` — completion token count

### Spanmetrics derivation (Grafana Cloud metrics-generator)

The `app` workload emits no spanmetrics itself. The GC metrics-generator produces
`traces_spanmetrics_*{span_name=<gen_ai name>, span_kind=SPAN_KIND_INTERNAL}` from these spans.
Operator configuration required:

- Add `SPAN_KIND_INTERNAL` to the generator's `span_kinds` list (default excludes INTERNAL).
- Optionally toggle `gen_ai.agent.name`, `gen_ai.tool.name`, `gen_ai.workflow.name` as extra
  dimension LABELS on the generated spanmetrics (cardinality tradeoff — see below).

### Cardinality bounds

Span name set is bounded per environment:
- 1 workflow × 1 agent-subset per request (3 agents total across the catalog)
- N tools per request (6 tools in the catalog; each request uses a subset)
- N model variants (typically 1–3 per env)

High-card keys (`app.correlation_id`, `app.request_id`) are span attributes ONLY — they never
become Mimir labels or Loki stream labels (per global rule `[slug: cardinality]`).

### Deliberate omissions / caveats

- **AgentCore is NOT modelled as a span.** AgentCore is a runtime/control-plane, not an in-process
  LangGraph child. Modelling it as a span would invent a non-semconv span — omission is intentional.
- **`gen_ai.workflow.name` is `assumed`** (marked in `internal/genai/genai.go`) — the semconv
  currently has weak coverage of workflow-level attributes; treat as best-effort until the spec
  stabilises.
- **`app` workload only.** Simpler workloads model the agent-runtime and portkey gateway as
  connected graph NODES rather than in-process children; there is no in-process agentic flow in
  those workloads.
- **`gen_ai.tool.call.id`** (listed in `[slug: genai-spans]`) is omitted from the agentic
  family — it is a per-call ephemeral ID, high-cardinality, and not needed for the span
  hierarchy. It remains documented in the general span-attributes entry for completeness.
