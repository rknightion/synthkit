# Grafana AI Observability / sigil (→ Tempo + Mimir) — ScopeBlueprint (workload-emitted)

Sigil is Grafana's AI-Observability backend. The `ai_agent` workload emits **three concurrent
lanes** — all keyed by a shared `conversation_id` + per-turn `trace_id`/`span_id`: native generation
ingest (gRPC, Lane A), OTLP traces to Tempo (Lane B), and gen_ai_client_* + sigil_eval_* metrics via
promrw (Lane C). No Loki lane. Blueprint-scoped; the `blueprint` label is stamped by the scoped
writer as usual. Vocabulary constants live in [`internal/sigil`](../internal/sigil); the shared
`gen_ai.*` keys + `gen_ai_client_*` metric names come from [`internal/genai`](../internal/genai).
Global rules: [`00-canon.md`](00-canon.md) — `[slug: cardinality]`, `[slug: content-strip]`,
`[slug: push-topology]`, `[slug: scoping]`.

*Provenance: sigil backend (`~/repos/sigil`) + sigil-sdk (`~/repos/sigil-sdk`); live capture from
the `m7kni` stack (Grafana internal coding-agent traffic, 64,048 generations: `claude-code`,
`claude-code/<subagent>`, `codex`) + `emea-cloud-demokit-grafana-com` (general / framework agents,
50 agents across orchestration / autonomous / multi-turn / single-shot archetypes); validated
2026-06-30. Lane A proto field names: `generation_ingest.proto` + `evaluation_ingest.proto` in
`~/repos/sigil`. Lane B span attribute names and Lane C metric names: direct live capture
(Tempo trace inspector + Mimir series query). Eval metric families (`sigil_eval_*`): `v: ok` —
LIVE-CAPTURED from `emea-cloud-demokit` (online evals running, namespace `stacks-996949`) via Mimir
series + label-values queries + `gcx aio11y` evaluator/rule inspection, 2026-06-30. Core
`gen_ai_client_*` metrics: `v: ok` (confirmed in m7kni + emea-cloud-demokit live capture,
2026-06-30).*

---

## Lane A — native generation ingest (gRPC) [slug: sigil-lane-a]

`sigil.v1.GenerationIngestService.ExportGenerations` (gRPC). This lane is the **sole source of
aio11y conversation data** — no other ingest path creates conversation/generation records in sigil.
It is the **sanctioned content-bearing exception** to `[slug: content-strip]`: prompt/completion
content rides inside `Generation.input`/`Generation.output` parts (see below). The
`internal/genai.ContentStripList` guard stays default-on for every other construct/workload; only
`internal/workload/ai_agent` opts in explicitly via the sigil sink.

### `Generation` proto fields

| Field | Type | Example / notes |
|---|---|---|
| `id` | string (UUID) | `"f47ac10b-58cc-4372-a567-0e02b2c3d479"` |
| `conversation_id` | string (UUID) | shared across all turns in a session |
| `operation_name` | enum | `generateText` \| `streamText` |
| `mode` | enum | `SYNC` \| `STREAM` |
| `trace_id` | string (hex-32) | links to Lane B root span; e.g. `"4bf92f3577b34da6a3ce929d0e0e4736"` |
| `span_id` | string (hex-16) | links to Lane B root span; e.g. `"00f067aa0ba902b7"` |
| `response_id` | string | provider-assigned response id (optional) |
| `response_model` | string | actual model used by provider (may differ from requested) |
| `model.provider` | string | `anthropic` \| `openai` \| `bedrock` \| `gemini` |
| `model.name` | string | e.g. `claude-sonnet-4-6`, `gpt-4.1-nano` |
| `system_prompt` | string | content-bearing; present on most turns |
| `input[]` | Message[] | user + prior assistant turns (full history for multi-turn) |
| `output[]` | Message[] | assistant response parts |
| `tools[]` | ToolDefinition[] | tool name/description/type/`input_schema_json` |
| `usage.input` | int | fresh prompt tokens |
| `usage.output` | int | completion tokens |
| `usage.total` | int | input + output |
| `usage.cache_read` | int | cache-read tokens (coding agents: dominant — often 10k–200k+) |
| `usage.cache_write` | int | cache-write tokens |
| `usage.reasoning` | int | reasoning / thinking tokens (claude-*-thinking, o3) |
| `stop_reason` | enum | `tool_use` \| `end_turn` \| `tool_calls` \| `stop` \| `completed` |
| `started_at` | timestamp | turn wall-clock start |
| `completed_at` | timestamp | turn wall-clock end; `completed_at − started_at` = actual latency |
| `tags` | map<string,string> | free-form; coding: `cwd`, `git.branch`, `entrypoint`, `codex.*`; general: `region`, `team` |
| `metadata` | Struct | arbitrary JSON; nonces in general-agent prompts etc. |
| `agent_name` | string | `claude-code`, `claude-code/<subagent>`, `codex`, or domain name |
| `agent_version` | string | semver or sha256 hash of the agent binary; absent for codex |
| `parent_generation_ids[]` | string[] | multi-agent call-tree (general agents; fan-out orchestration) |
| `effective_version` | string | `sha256:<64hex>` — deterministic hash of the canonical system prompt (canonical_version=3) |
| `max_tokens` | int | model param |
| `temperature` | float | model param |
| `top_p` | float | model param |
| `tool_choice` | string | e.g. `auto`, `any`, specific tool name |
| `thinking_enabled` | bool | Claude extended-thinking enabled |

### `Message` and `Part` shapes

A `Message` has `role` (`user` \| `assistant`) and `parts[]`. Part kinds:

| Kind | Fields |
|---|---|
| `text` | `text: string` |
| `thinking` | `thinking: string` (extended-thinking blocks) |
| `tool_call` | `id: string` (`toulu_*` Anthropic, `call_*` OpenAI), `name: string`, `input_json: bytes` |
| `tool_result` | `tool_call_id: string`, `content: string`, `content_json: bytes`, `is_error: bool` |

### Sibling Lane A RPCs

- **`WorkflowStepIngestService.ExportWorkflowSteps`** — non-LLM agentic nodes (e.g. tool-routing
  decisions, orchestrator state transitions). Present on `general_orchestration` and
  `general_autonomous_loop` archetypes; absent from coding archetypes.
- **`ScoreIngestService.ExportScores`** — eval results written when a configured rule samples a turn.
  Each `Score` has `generation_id`, `evaluator_name`, `score_key`, `value` (number/bool/string),
  `threshold`, `passed: bool`. These feed the `latest_scores` display in the sigil UI and drive the
  `sigil_eval_*` metrics (Lane C).

---

## Lane B — OTLP traces (Tempo) [slug: sigil-lane-b]

Hand-encoded `ResourceSpans` via `internal/sink/otlp`. **No span events** (confirmed in both m7kni
and emea-cloud-demokit captures — the sigil SDK does not emit events). Span name form is `{operation}
{subject}` per gen_ai semconv (e.g. `generateText claude-sonnet-4-6`, `execute_tool Read`).

### Coding archetype (sdk-go) — 2-level tree

Scope name: `sigil.<agent_name>` (e.g. `sigil.claude-code`, `sigil.claude-code/subagent-type`).
Resource `service.name=sigil`, `job=sigil`.

| Level | Span name | SpanKind | Notes |
|---|---|---|---|
| Root | `generateText <model>` | CLIENT | gen_ai identity/usage + `sigil.*` + `user.id`; NO messages |
| Child | `execute_tool <Tool>` | INTERNAL | `gen_ai.tool.*`; under `content_capture_mode=full` ALSO `gen_ai.tool.call.arguments` + `gen_ai.tool.call.result` |

### General archetype (sdk-python) — 3-scope nested tree

Resource attributes: full k8s set (`service.name=chatservice`, `service.namespace`,
`deployment.environment.name`, `k8s.cluster.name`, `k8s.namespace.name`, `k8s.pod.name`,
`k8s.deployment.name`, `cloud.region`).

| Level | Span name | Scope | SpanKind | Notes |
|---|---|---|---|---|
| Envelope | `agent.<name>.chat` | `agents.base` | INTERNAL | framework orchestrator span; `agent.available_tools`, `agent.peer_agents`, `agent.system`, `agent.user_prompt`, `gen_ai.agent.name` |
| LLM call | `generateText <model>` or `streamText <model>` | `sigil.<agent>` | CLIENT | `sigil.generation.parent_generation_ids` for fan-out |
| Tool | `execute_tool <tool>` | `sigil.<agent>` | INTERNAL | `gen_ai.tool.name`, `gen_ai.tool.type`, `gen_ai.tool.call.id` |
| Provider | `chat <model>` | `opentelemetry.instrumentation.openai_v2` | CLIENT | present for openai provider only; `gen_ai.system`, `server.address=api.openai.com` |

For `general_orchestration`, each delegation also emits a sub-agent `generateText <model>` CLIENT span
under its OWN scope `sigil.<peer>` (e.g. `sigil.product_agent`), in the SAME trace as the orchestrator
turn, nested under that turn's `agents.base` envelope span, carrying `sigil.generation.parent_generation_ids`
= the orchestrator generation id. The matching sub-agent generation (Lane A) carries the distinct
`agent_name` + the same `parent_generation_ids` — this is what renders the multi-agent call tree.

### Span attributes — coding root span (`generateText <model>`)

```yaml signals
family: sigil_spans_coding
scope: blueprint
sink: otlp
span_attributes:
  gen_ai.operation.name: generateText|streamText
  gen_ai.system: anthropic|openai|bedrock|gemini         # legacy key; current form is gen_ai.provider.name
  gen_ai.provider.name: anthropic|openai|bedrock|gemini
  gen_ai.request.model: claude-sonnet-4-6|claude-opus-4-8|gpt-5.5
  gen_ai.response.model: claude-sonnet-4-6               # actual model returned
  gen_ai.usage.input_tokens: <int>                       # fresh prompt tokens
  gen_ai.usage.output_tokens: <int>                      # completion tokens
  gen_ai.usage.cache_read_input_tokens: <int>            # ⚠ UNDERSCORE form — sigil-sdk-go spelling differs from semconv dotted form
  gen_ai.usage.cache_write_input_tokens: <int>           # ⚠ UNDERSCORE form — sigil-sdk-go spelling differs from semconv dotted form
  gen_ai.usage.reasoning_tokens: <int>                  # reasoning tokens (thinking models); ⚠ UNDERSCORE form — sigil-sdk spelling, not semconv reasoning.output_tokens
  gen_ai.conversation.id: <uuid>
  sigil.generation.id: <uuid>
  sigil.sdk.name: sdk-go|sdk-python
  sigil.conversation.title: <string>                     # optional; present when the agent names the session
  sigil.gen_ai.request.thinking.enabled: true|false
  sigil.tag.<k>: <v>                                     # one attr per tag key; e.g. sigil.tag.cwd, sigil.tag.git.branch
  user.id: <email>                                       # real email for coding; synthetic UUID for general
resource_attributes:
  service.name: sigil
  job: sigil
  # scope name: sigil.<agent_name>
```

### Span attributes — coding child span (`execute_tool <Tool>`)

```yaml signals
family: sigil_spans_coding_tool
scope: blueprint
sink: otlp
span_attributes:
  gen_ai.tool.name: Read|Edit|Bash|Skill|apply_patch|<tool>
  gen_ai.tool.type: function
  gen_ai.tool.call.id: toolu_<hex>|call_<hex>
  gen_ai.tool.call.arguments: <json-bytes>               # present ONLY when content_capture_mode=full
  gen_ai.tool.call.result: <json-bytes>                  # present ONLY when content_capture_mode=full
resource_attributes:
  service.name: sigil
  job: sigil
```

### Span attributes — general envelope span (`agent.<name>.chat`)

```yaml signals
family: sigil_spans_general_envelope
scope: blueprint
sink: otlp
span_attributes:
  gen_ai.agent.name: product_agent|soc-analyst|db-travel-agent|title_generator
  agent.available_tools: <comma-sep tool list>
  agent.peer_agents: <comma-sep agent list>              # multi-agent orchestration only
  agent.system: <system prompt text>                     # content-bearing (general capture mode)
  agent.user_prompt: <user prompt text>                  # content-bearing (general capture mode)
resource_attributes:
  service.name: chatservice
  service.namespace: <k8s-namespace>
  deployment.environment.name: <env>
  k8s.cluster.name: <cluster>
  k8s.namespace.name: <namespace>
  k8s.pod.name: <pod>
  k8s.deployment.name: <deployment>
  cloud.region: <region>
```

### Span attributes — general LLM call span (`generateText`/`streamText <model>`)

```yaml signals
family: sigil_spans_general_llm
scope: blueprint
sink: otlp
span_attributes:
  gen_ai.operation.name: generateText|streamText
  gen_ai.provider.name: openai|anthropic|bedrock
  gen_ai.request.model: gpt-4.1-nano|gpt-4o-mini|global.anthropic.claude-3-5-sonnet|us.anthropic.claude-3-haiku
  gen_ai.usage.input_tokens: <int>
  gen_ai.usage.output_tokens: <int>
  gen_ai.usage.cache_read_input_tokens: <int>            # ⚠ UNDERSCORE form (sdk-python sigil spelling)
  gen_ai.usage.cache_write_input_tokens: <int>           # ⚠ UNDERSCORE form (sdk-python sigil spelling)
  gen_ai.conversation.id: <uuid>
  sigil.generation.id: <uuid>
  sigil.sdk.name: sdk-python
  sigil.generation.parent_generation_ids: <uuid>,<uuid>  # comma-sep list; fan-out multi-agent
```

---

## Lane C — metrics (promrw → Mimir) [slug: sigil-lane-c]

Metric label names follow the OTLP→Prom translation (`gen_ai.operation.name` → `gen_ai_operation_name`,
etc.). Resource-attr labels (`deployment_environment_name`, `service_name`, `k8s_cluster_name`, etc.)
are promoted by GC onto the `gen_ai_client_*` series — see `[slug: env-label-keys]`. (NOTE: those
resource labels are ABSENT on the `sigil_eval_*` series — confirmed live.) `gen_ai_client_*`
histograms use base-2 second buckets (matching `internal/genai` advisory buckets; `v: assumed` for
those bucket boundaries). Core `gen_ai_client_*` metric names + label shapes: `v: ok` (live-confirmed
in m7kni + emea-cloud-demokit captures, 2026-06-30). `sigil_eval_*` families: `v: ok` — names, labels,
AND histogram buckets live-captured from `emea-cloud-demokit` 2026-06-30 (see below).

### gen_ai client metrics [slug: sigil-gen-ai-metrics]

These reuse the `internal/genai` metric names; the sigil-specific extension is the expanded
`gen_ai_token_type` value set (adds `cache_read`, `cache_write`, `reasoning`) and the
`agent_name`/`agent_version` labels. **R-m3: `gen_ai_token_type` values are the token-category
names, NOT the proto field suffixes** — value is `cache_read` (not `cache_read_input_tokens`).

```yaml signals
family: gen_ai_client
scope: blueprint
sink: promrw
labels:
  gen_ai_operation_name: generateText|streamText|execute_tool
  gen_ai_provider_name: anthropic|openai|bedrock|gemini
  gen_ai_request_model: <model>                          # e.g. claude-sonnet-4-6, gpt-4.1-nano
  agent_name: claude-code|claude-code/<type>|codex|product_agent|soc-analyst|db-travel-agent|title_generator
  agent_version: <semver>                                # optional; absent for codex + some general agents
  gen_ai_token_type: input|output|cache_read|cache_write|reasoning   # token_usage ONLY; R-m3: category name, not field suffix
  gen_ai_tool_name: <tool>                               # execute_tool spans only
  error_type: <error.type>                               # operation_duration on failure only
  error_category: <category>                             # operation_duration on failure only (sigil-extended)
  # resource-attr labels promoted by GC: deployment_environment_name, service_name, job, k8s_cluster_name, etc.
metrics:
  - {root: gen_ai_client_token_usage, type: histogram, unit: token, v: ok, note: "live-confirmed m7kni+emea-cloud-demokit 2026-06-30; agent_name/version + expanded token_type set (incl. cache_read/cache_write/reasoning) are sigil-extensions beyond genai.md; buckets v:assumed"}
  - {root: gen_ai_client_operation_duration_seconds, type: histogram, unit: seconds, v: ok, note: "live-confirmed 2026-06-30; gen_ai_tool_name+error_type+error_category present on execute_tool / error paths; buckets v:assumed"}
  - {root: gen_ai_client_tool_calls_per_operation_count, type: histogram, unit: count, v: ok, note: "live-confirmed 2026-06-30; identity labels only (no token_type); buckets v:assumed"}
  - {root: gen_ai_client_time_to_first_token_seconds, type: histogram, unit: seconds, v: ok, note: "live-confirmed on streaming agents (streamText turns) 2026-06-30; absent for SYNC generateText; buckets v:assumed"}
```

### sigil_eval metrics [slug: sigil-eval-metrics]

Emitted when a configured eval rule samples a turn and scores the `Generation`. **`v: ok`** — all ten
families + their label shapes LIVE-CAPTURED from `emea-cloud-demokit` (online evals running, namespace
`stacks-996949`, 2026-06-30). ⚠ The labels use the **OTLP→Prom translated convention** — NOT the
backend short names a previous (code-derived `v: assumed`) version of this doc guessed: `evaluator`
(not `evaluator_name`), `rule` (not `rule_name`), `gen_ai_agent_name` (not `agent_name`), `model`+
`provider` on judge metrics (not `judge_model`); `agent_version` is ABSENT. This convention is also
DELIBERATELY different from the `gen_ai_client_*` family above (which uses `agent_name` +
`gen_ai_provider_name`) — the two families do not share label spellings.

```yaml signals
family: sigil_eval
scope: blueprint
sink: promrw
labels:
  evaluator: <evaluator>              # name declared in blueprint evaluators: (NOT evaluator_name)
  evaluator_kind: llm_judge|heuristic # heuristic exists in config but only llm_judge seen live (v:ok for llm_judge)
  score_key: <key>                    # e.g. ecommerce_helpfulness_score, prompt_injection_detected, toxicity, topic
  rule: <rule>                        # triggering rule name (NOT rule_name)
  gen_ai_agent_name: <agent>          # the SCORED agent (NOT agent_name); e.g. cart_agent, product_agent
  gen_ai_request_model: <model>       # the scored generation's model
  gen_ai_request_provider: <provider> # the scored generation's provider (NOT gen_ai_provider_name)
  eval_ai_request_model: <judge-model># the judge model (versioned live, e.g. claude-haiku-4-5-20251001) on score/exec/duration
  passed: true|false|unknown          # 3-valued — unknown for string-typed scores with no pass_value; scores/score_values
  score_value: <value-as-string>      # score_values_total ONLY; the bool/string value (e.g. "false", "alerting")
  status: success|failed|queued       # executions/judge_requests (success|failed); queue_depth (queued|failed)
  result: no_actions                  # rule_action_fires_total ONLY
  model: <judge-model>                # judge_* metrics; e.g. claude-haiku-4-5-20251001, eu.anthropic.claude-haiku-4-5-...-v1:0
  provider: <judge-provider>          # judge_* metrics; e.g. bedrock
  direction: input|output             # judge_tokens_total ONLY
  error_type: <type>                  # judge_errors_total ONLY; e.g. unknown
  # NO GC-promoted resource labels on eval series (deployment_environment_name/service_name/etc. confirmed ABSENT live).
metrics:
  - {root: sigil_eval_scores_total, type: counter, unit: count, v: ok, note: "total scoring events; {evaluator,evaluator_kind,score_key,rule,gen_ai_agent_name,gen_ai_request_model,gen_ai_request_provider,eval_ai_request_model,passed}"}
  - {root: sigil_eval_score_values_total, type: counter, unit: count, v: ok, note: "bool/string scores ONLY (numeric NOT enumerated — unbounded cardinality); scores_total labels + score_value"}
  - {root: sigil_eval_executions_total, type: counter, unit: count, v: ok, note: "eval executions; identity labels + status (success|failed) — NOT passed; agent/model labels drop when status=failed"}
  - {root: sigil_eval_rule_action_fires_total, type: counter, unit: count, v: ok, note: "rule outcomes; {rule,result}; result=no_actions when no action rule configured"}
  - {root: sigil_eval_duration_seconds, type: histogram, unit: seconds, v: ok, note: "wall-clock eval duration; identity labels; buckets 0.01..81.92 (base-2 from 0.01s)"}
  - {root: sigil_eval_judge_duration_seconds, type: histogram, unit: seconds, v: ok, note: "LLM judge call duration; {model,provider}; llm_judge only; buckets 0.01..81.92"}
  - {root: sigil_eval_judge_tokens_total, type: counter, unit: tokens, v: ok, note: "judge call tokens; {model,provider,direction}; direction=input|output; llm_judge only"}
  - {root: sigil_eval_judge_requests_total, type: counter, unit: count, v: ok, note: "judge LLM API calls; {model,provider,status}; llm_judge only"}
  - {root: sigil_eval_judge_errors_total, type: counter, unit: count, v: ok, note: "judge call errors; {error_type,model,provider}; llm_judge only; live-captured 2026-06-30 (was undocumented)"}
  - {root: sigil_eval_queue_depth, type: gauge, unit: count, v: ok, note: "pending/failed eval work items; {status} (queued|failed); instantaneous gauge. CAPTURED but synthkit does NOT emit it: it is backend-GLOBAL with zero per-backend identity in the live schema, so emitting it from >1 ai_agent fleet in one push would duplicate-series (Mimir rejects). Documented for completeness."}
```

---

## Coding vs general archetype contract [slug: sigil-archetypes]

Purely conventional — no schema field distinguishes coding from general; the difference is in
`agent_name` patterns, token shapes, tool sets, and span structure. Both emit all three lanes.

| Dimension | Coding | General |
|---|---|---|
| SDK | `sdk-go` | `sdk-python` |
| `agent_name` | `claude-code`, `claude-code/<subagent-type>`, `codex` | Domain names: `product_agent`, `soc-analyst`, `db-travel-agent`, `title_generator` |
| Provider / model | anthropic `claude-opus-4-8` / `claude-sonnet-4-6`; openai `gpt-5.5` (codex) | openai `gpt-4.1-nano` / `gpt-4o-mini`, anthropic, bedrock (`global.anthropic.*`, `us.anthropic.*`) |
| Token shape | Huge `cache_read` (often 10k–200k+), tiny fresh `input` (often 2); `stop_reason=tool_use` ~81% | Modest `input`/`output`, minimal `cache_read`; varies per archetype |
| Tools | FS/shell: `Read`, `Edit`, `Bash`, `Skill`, `apply_patch` | Domain functions: `search_products`, `find_train_connections`, `submit_verdict` |
| `content_capture_mode` | `full` (tool args + results on spans) | `no_tool_content` or `full` |
| Lane A tags | `cwd`, `git.branch`, `entrypoint`, `codex.*` (numeric metadata) | `region`, `team`; nonces in prompts |
| Lane B span structure | 2-level (CLIENT root → INTERNAL tool children); scope `sigil.<agent_name>` | 3-scope nested (`agents.base` envelope → sigil CLIENT → sigil INTERNAL tool → openai CLIENT) |
| Resource identity | `service.name=sigil`, `job=sigil` | `service.name=chatservice`, full k8s resource attrs |
| `user.id` span attr | Real email address | Synthetic UUID (= `conversation_id`) |
| `agent_version` | Semver or sha256 hash of agent binary | Optional; absent for single-shot agents |
| Multi-agent structure | Open-ended turn loop; subagents as `claude-code/<type>` child conversations | Fan-out via `parent_generation_ids` + `agents.base` envelope spans; autonomous loops; single-shot |
| Streaming | `streamText` uncommon (possible) | `streamText` common for multi-turn archetypes; emits `time_to_first_token_seconds` |
| WorkflowSteps (Lane A) | Absent | Present on `general_orchestration` and `general_autonomous_loop` archetypes |
| Eval / scores (Lane A + C) | Optional (blueprint-configured) | Optional; common on autonomous + multi-turn archetypes |

### Archetype roster

| Archetype key | Lane A archetypes | Turn grammar |
|---|---|---|
| `coding_claude_code` | Multi-turn; subagents as child conversations w/ shared content facets | Long sessions; `tool_use` stop ~81%; cache snowball |
| `coding_codex` | Multi-turn; numeric `codex.*` metadata in tags; no `agent_version` | `gpt-5.5`; openai tool_call ids (`call_*`) |
| `general_orchestration` | Orchestrator `streamText` turns (NO parent ids — tree roots) + per-delegation sub-agent generations under distinct `agent_name`s parented to the orchestrator gen (true fan-out TREE) | `agents.base` envelope (`agent.peer_agents`); turn-0 dispatches to every declared peer, later turns delegate stochastically |
| `general_autonomous_loop` | Soc-analyst evidence loop; terminal `submit_verdict` tool w/ enum taxonomy | Loop until terminal tool call |
| `general_multiturn` | db-travel / sales; full conversation history growth | `SYNC` + `STREAM` variants; TTFT on STREAM |
| `general_single_shot` | Title-generator / judge; 0 tools; tiny `max_tokens` | Single turn; no tool calls; fast; `end_turn` stop |

---

## Failure modes [slug: sigil-failure-modes]

**Emitted.** The `ai_agent` workload registers both modes on **AxisWorkload** (targeted in a blueprint
by the workload INSTANCE name — e.g. `target: sigil-general-agents`). `ProjectBatch` reads the active
intensity via `shape.Eval(now, mode, workloadName)` (UNION of scheduled incidents + live control-plane
state) and applies it deterministically per generation (keyed on the generation id — never wall-clock —
so two `-dump` runs agree). `grafana-ai-o11y.yaml` declares one-shot daily `incidents:` for both.

| Mode | Effect |
|---|---|
| `provider_call_error` | A ~intensity fraction of generations error: `Generation.CallError` set (Lane A), Lane-B root span `status=ERROR`, and the `gen_ai_client_operation_duration_seconds` observation carries `error_type`/`error_category` (a DISTINCT series — absent on success, I13). Failed generations are not scored. error_type/category VALUES are real provider error vocabulary (`overloaded_error`/`rate_limit_error`/`api_error`/`timeout`) — synthetic fault injection; the label NAMES are signals-sourced. |
| `eval_quality_regression` | Eval scores bias downward (number-rubric centre drops, bool/string flag bar rises) so `sigil_eval_score_values_total{passed=false}` rate climbs; `sigil_eval_scores_total`/`executions_total` maintained; `rule_action_fires_total` unaffected (fires per scoring event). |

---

## Cardinality notes [slug: sigil-cardinality]

- `generation_id`, `trace_id`, `span_id`, `tool_call_id`, `conversation_id` are Lane A fields and
  Lane B span attributes ONLY — **never Mimir labels** (`[slug: cardinality]`).
- `agent_name` is bounded: coding has 3 values + a small set of subagent suffixes; general has ~4–8
  domain names in the blueprint roster.
- `gen_ai_request_model` is bounded per blueprint (typically 2–5 model IDs).
- `score_key` and `evaluator` (the eval-metric label; NOT `evaluator_name`) are bounded by the
  blueprint `evaluators:` list. `score_value` (on `score_values_total`) is bounded too: bool ⇒
  true|false, string ⇒ the evaluator's classification taxonomy; numeric scores never enter it.
- `model`/`provider` (judge metrics) and `eval_ai_request_model` are bedrock-style IDs from the
  evaluator config; bounded per blueprint.
- The `sigil.tag.<k>` span attributes are per-key (one attr per tag key, not one attr with a
  map value) → cardinality of tag keys is bounded by the archetype definition (not open-ended at
  runtime).

---

## Implementation status & next steps [slug: sigil-status]

What the `ai_agent` workload + `grafana-ai-o11y` blueprint emit **today** (v1, live-verified against a
real sigil stack 2026-06-30), versus what is **PENDING** (the resume list — pick up here).

**Emitted + verified:** Lane A generations (coding + general), Lane B span trees (coding 2-level;
general `agents.base` → `generateText`/`streamText` → `execute_tool` → openai `chat`), Lane C
`gen_ai_client_*` metrics, the 6 archetypes, `sigil_eval_*` + `latest_scores` for the bound
evaluators/rules, `WorkflowStep` ingest for orchestration, `effective_version`, the full vocabulary.
**True orchestration fan-out** (`general_orchestration` — sub-agent generations under distinct
`agent_name`s parented to the orchestrator gen, with their own `sigil.<peer>` spans; see
`internal/workload/aiagent/orchestration.go`). **Failure modes** (`provider_call_error`,
`eval_quality_regression` — AxisWorkload, see the Failure modes section + `failures.go`).

Plus the **e2e sigil receiver** (the `make e2e` Docker harness decodes the three sigil ingest
endpoints + correlates ingest kinds; see `e2e/receiver`) and the **live-captured eval families**
(`sigil_eval_*` promoted to `v: ok` with corrected label shapes from `emea-cloud-demokit`).

**PENDING — next steps (in rough priority):**

1. **Modeling nits:** carry a turn's tool-results in the *next* turn's input (today they sit in the
   same `AssembledTurn.Input`); per-subagent `agent_name` for `claude-code/<subagent>` child
   conversations.
2. **Heuristic evaluators live:** only `llm_judge` produced eval series in the 2026-06-30 capture
   (`evaluator_kind=heuristic` exists in config but wasn't observed) — re-capture once a heuristic
   evaluator is active to confirm its (judge-label-free) series shape.
