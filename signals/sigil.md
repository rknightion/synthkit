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
(Tempo trace inspector + Mimir series query). Eval metric families (`sigil_eval_*`): sourced from
`~/repos/sigil` backend, not yet captured from a live eval stack → `v: assumed` (advisory). Core
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
are promoted by GC onto every series — see `[slug: env-label-keys]`. Histograms use base-2 second
buckets (matching `internal/genai` advisory buckets; `v: assumed` for bucket boundaries where not
live-confirmed). Core `gen_ai_client_*` metric names and label shapes: `v: ok` (live-confirmed in
m7kni + emea-cloud-demokit captures, 2026-06-30). `sigil_eval_*` families: `v: assumed` (sourced
from sigil backend code; eval stack not yet live-captured).

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

Emitted when a configured eval rule samples a turn and scores the `Generation`. All nine families
share the base identity labels; judge-specific labels appear only on the relevant families.

```yaml signals
family: sigil_eval
scope: blueprint
sink: promrw
labels:
  evaluator_name: <evaluator>         # name declared in blueprint evaluators:
  evaluator_kind: llm_judge|heuristic
  score_key: <key>                    # e.g. helpfulness, accuracy, tool_correctness
  rule_name: <rule>                   # name of the triggering rule (rule_action_fires only)
  agent_name: <agent>
  agent_version: <semver>             # optional
  judge_model: <model-id>             # llm_judge families only; e.g. us.amazon.nova-pro-v1:0
  passed: true|false                  # score_values_total, executions_total
  # resource-attr labels promoted by GC (same as gen_ai_client_* above)
metrics:
  - {root: sigil_eval_scores_total, type: counter, unit: count, v: assumed, note: "total scoring events; {evaluator_name,evaluator_kind,score_key,agent_name,agent_version}"}
  - {root: sigil_eval_score_values_total, type: counter, unit: count, v: assumed, note: "score observations bucketed by passed; {evaluator_name,score_key,passed,agent_name}"}
  - {root: sigil_eval_executions_total, type: counter, unit: count, v: assumed, note: "total eval executions; {evaluator_name,evaluator_kind,passed}"}
  - {root: sigil_eval_rule_action_fires_total, type: counter, unit: count, v: assumed, note: "rule-triggered actions; {rule_name,evaluator_name}"}
  - {root: sigil_eval_duration_seconds, type: histogram, unit: seconds, v: assumed, note: "wall-clock eval duration; {evaluator_name,evaluator_kind}; advisory buckets"}
  - {root: sigil_eval_judge_duration_seconds, type: histogram, unit: seconds, v: assumed, note: "LLM judge call duration; {evaluator_name,judge_model}; llm_judge only; advisory buckets"}
  - {root: sigil_eval_judge_tokens_total, type: counter, unit: tokens, v: assumed, note: "tokens consumed by judge calls; {evaluator_name,judge_model}; llm_judge only"}
  - {root: sigil_eval_judge_requests_total, type: counter, unit: count, v: assumed, note: "judge LLM API calls; {evaluator_name,judge_model}; llm_judge only"}
  - {root: sigil_eval_queue_depth, type: gauge, unit: count, v: assumed, note: "pending eval work items; {evaluator_name}; instantaneous gauge"}
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
| `general_orchestration` | Orchestrator `streamText` + `parent_generation_ids` (⚠ currently a linear turn chain, NOT true sub-agent fan-out — see Implementation status) | `agents.base` envelope; peer agents present |
| `general_autonomous_loop` | Soc-analyst evidence loop; terminal `submit_verdict` tool w/ enum taxonomy | Loop until terminal tool call |
| `general_multiturn` | db-travel / sales; full conversation history growth | `SYNC` + `STREAM` variants; TTFT on STREAM |
| `general_single_shot` | Title-generator / judge; 0 tools; tiny `max_tokens` | Single turn; no tool calls; fast; `end_turn` stop |

---

## Failure modes [slug: sigil-failure-modes]

⚠ **PLANNED — not yet emitted (follow-up).** The `ai_agent` workload does not yet register these
failure modes; the `Generation.CallError` → proto + Lane-B `status=ERROR` plumbing is in place and
ready, but no incident axis drives it today, and the `grafana-ai-o11y` blueprint declares no
incidents/scenarios. The shapes below are the INTENDED contract for when these modes land.

| Mode (planned) | Intended effect |
|---|---|
| `provider_call_error` | Spike in `error_type`/`error_category` labels on `gen_ai_client_operation_duration_seconds`; `call_error` on Lane A generations; `status=ERROR` on Lane B spans |
| `eval_quality_regression` | `sigil_eval_score_values_total{passed=false}` rate rises; `sigil_eval_scores_total` maintained; `sigil_eval_rule_action_fires_total` may rise on threshold-breach rules |

---

## Cardinality notes [slug: sigil-cardinality]

- `generation_id`, `trace_id`, `span_id`, `tool_call_id`, `conversation_id` are Lane A fields and
  Lane B span attributes ONLY — **never Mimir labels** (`[slug: cardinality]`).
- `agent_name` is bounded: coding has 3 values + a small set of subagent suffixes; general has ~4–8
  domain names in the blueprint roster.
- `gen_ai_request_model` is bounded per blueprint (typically 2–5 model IDs).
- `score_key` and `evaluator_name` are bounded by the blueprint `evaluators:` list.
- `judge_model` is a bedrock-style ID from the evaluator config; bounded per blueprint.
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

**PENDING — next steps (in rough priority):**

1. **True orchestration fan-out (`general_orchestration`).** Today `applyOrchestration`
   (`internal/workload/aiagent/archetypes.go`) sets `parent_generation_ids` to the *previous turn*
   in the same conversation — a linear CHAIN under one `agent_name`. Real orchestration is a TREE: an
   orchestrator generation invokes sub-agents (`product_agent`/`cart_agent`) that each emit their own
   generation(s) under their OWN `agent_name`, with `parent_generation_ids` pointing back to the
   orchestrator gen. Implement: emit per-delegation sub-agent generations (distinct `agent_name`, set
   `agent.peer_agents`/`agent.available_tools` on the `agents.base` envelope) parented to the
   orchestrator gen, so the aio11y agent catalog shows the sub-agents and the call tree renders.
   Update `TestOrchestrationArchetype` (it currently asserts the chain shape).
2. **Failure modes** (`provider_call_error`, `eval_quality_regression`) — see the Failure modes
   section. Register them on the workload (`FailureModes` in `Registration()`, like `app`), drive
   `Generation.CallError` + Lane-B `status=ERROR` + `error_type`/`error_category` metric labels +
   eval-score degradation from the shape/incident engine, and add an `incidents:`/scenarios block to
   `blueprints/grafana-ai-o11y.yaml`. The proto + span plumbing is already in place.
3. **e2e receiver** — extend the in-repo `make e2e` receiver to decode the sigil ingest lane and
   assert `expected.Subset(received)` (schema-only), matching the other lanes.
4. **Live-capture the eval stack** to promote the `sigil_eval_*` families from `v: assumed` to `v: ok`
   (they're derived from sigil backend code, not yet captured from a live eval run).
5. **Modeling nits:** carry a turn's tool-results in the *next* turn's input (today they sit in the
   same `AssembledTurn.Input`); per-subagent `agent_name` for `claude-code/<subagent>` child
   conversations.
