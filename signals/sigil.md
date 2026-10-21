# Grafana Agent Observability — generation ingest, traces and client metrics

`sigil` is synthkit's historical internal package/sink name. The current vendor namespace is
`agento11y`. The `ai_agent` workload has three correlated lanes: native HTTP protojson generation,
workflow-step and score ingest (A), OTLP spans (B), and SDK client metrics plus optional **legacy
synthetic** evaluation metrics (C). IDs join the lanes; IDs never become metric labels. The workload registration is
substrate-scoped: no synthetic blueprint selector is added to these lanes.

Global rules: [canonical signal rules](00-canon.md). Vocabulary and native seam types:
[`internal/sigil`](../internal/sigil); shared gen_ai vocabulary: [`internal/genai`](../internal/genai).

## Source and producer contract (2026-10-21)

Authoritative current sources, inspected at immutable revisions:

- SDK `grafana/agento11y` **75a1c548**: Go `go/agento11y/client.go`, `exporter.go`,
  `contentcapture/contentcapture.go`; Python `python/agento11y/client.py`, `usage.py`;
  plugins `plugins/agento11y/internal/agents/{claudecode,codex}` and `internal/otel/otel.go`.
- Backend `grafana/sigil` **bc963fb09**: `sigil/proto/sigil/v1/{generation,evaluation}_ingest.proto`,
  `sigil/internal/eval/{worker,ingest}`, `sigil/internal/inference/service.go`,
  `sigil/internal/eval/review_queue_types.go`.
- GET-only tenant baseline on 2026-10-21: historical `gen_ai_client_*` duration/token metrics over
  24 hours carried **`gen_ai_agent_name`**, not `agent_name`; instant query empty. Legacy
  `agento11y_eval_judge_tokens_total` was empty even over seven days. Backend-derived generation
  cost existed for real agents; that is not readback proof for synthetic ingest.

The historical June and September 2026 captures established the three-lane topology and core
metric names. Their older span prefixes, agent labels, eval model interpretation and shared eval
bucket set are **superseded** by the pinned contract below. Captured source data is not rewritten.

| Producer | synthkit policy |
|---|---|
| SDK generations, tools, workflow steps, ratings, experiment writes, OTLP spans, `gen_ai.client.*` | May emit through their actual SDK-side surfaces |
| Backend generation cost, model prices, exported-score `agento11y_eval_score_values_total`, experiment metrics | **Never synthesize** |
| Backend hook metrics, guard workflow steps, online-rule scores and eval metrics | Trigger through real provisioned rules/calls; **never forge** |
| Existing synthetic `scores:export` and other `agento11y_eval_*` | Retained only for blueprints without real rules scoring the same agents; not a second producer alongside real rules |

Exported bool/string scores already increment score-values in backend ingest. Emitting the same
family locally double-counts them, even if local labels differ. Numeric score values are never
enumerated as labels. This retirement does not remove the actual Lane-A scores.

## Bound-app executor subset [slug: sigil-app-pipeline]

`app.agent_pipeline` is a separate, bounded consumer of the vocabulary below, not an
expansion of the generic `ai_agent` emitter. Its initial component requires explicit
`services[].agent_observability`, placed Kubernetes identities and `otel.metrics: true`.
Ordinary CLI boot/live/persisted restore cannot install its required `GenerationStages`
collaborator and fail closed, including hooks disabled/ratings zero. A nonnil collaborator
is a construction dependency only, never destination/current/historical custody admission.
No production hook/rating adapter or backend readback is established by component tests.
Every pipeline graph service must be bound and used by the stage plan. Inline logs,
log-producing catalog profiles, pages and rum_faro declarations are unsupported and reject
at Build; this component does not project ordinary graph log/RUM or unbound-node outputs.
The existing ordinary app behavior is unchanged.

The component emits completed model generations and application workflow steps through
native HTTP; retrieval-only services emit workflow/tool telemetry, **no** model generation
or model metric. Planner identity owns retrieval preflight; rules match that producer,
not a fabricated tool-name predicate. Declared caller/source tool intersection selects
actual invocations. Disabled hooks or explicit hook-local fail-open may retain original
content; denial/redaction must prevent original content reaching dependent exports.
AfterGeneration runs once before publication/delegation; this does not install postflight HTTP.
Ratings use the separate conversation writer, carry no arbitrary comment/metadata in this
component, and do not certify generation delivery. Backend score/guard/cost/experiment
families remain forbidden synthetic producers.

### Capture provenance and admitted shapes

SDK **75a1c548** Go `content_capture.go`, `contentcapture/contentcapture.go` and
`client.go` tool recorder, plus Python `client.py` `_strip_content`,
`_strip_message_content` and tool recorder define the native-export/span split:

| Binding capture mode | Native generations | Shared tool spans |
|---|---|---|
| Omitted / `default` / `no_tool_content` | Full modeled content | No arguments/results; no per-tool IncludeContent opt-in exists here |
| `full` | Full modeled content | Actual completed call arguments and accepted text or JSON result |
| `full_with_metadata_spans` | Full modeled content | No arguments/results |
| `metadata_only` | Strip system/text/tool content and descriptions/schemas, retain roles, part structure, tool names/IDs, usage/timing | No arguments/results |

Native metadata stamps `agento11y.sdk.content_capture_mode` with the resolved mode.
Metadata-only removes `call_error` and `agento11y.conversation.title`; Go also removes
`sigil.conversation.title`, while the pinned Python policy retains an authored legacy-title
key. A nonempty call error becomes its category or `sdk_error`. Arbitrary authored metadata
and tags are **not** scrubbed. Keep them free of private content in every mode.
Generation spans carry identity/usage, not prompts/messages or arbitrary metadata.
Native message `name` and part `metadata.provider_type` retain their pinned proto fields
(`generation_ingest.proto` at backend bc963fb09); neither is an extra metric dimension.

The app span subset is generation CLIENT and execute_tool INTERNAL spans in SDK library
scopes, alongside ordinary app SERVER/CLIENT transport spans in scope `synthkit`.
Generation attributes are `gen_ai.operation.name`, provider/request/response model,
`gen_ai.response.id`, array-valued `gen_ai.response.finish_reasons`, agent name/optional
version, conversation ID, input/output token usage, `agento11y.generation.id`,
`agento11y.sdk.name`, configured client/context tags and positively marked token semantics.
Python adds array-valued `agento11y.generation.parent_generation_ids` when present.
Tool spans carry `gen_ai.operation.name`, `gen_ai.tool.name`, `gen_ai.tool.call.id`,
`gen_ai.agent.name` (source identity), `gen_ai.conversation.id` and `agento11y.sdk.name`,
plus arguments/result only under explicit full. They do not invent source model identity.
Ordinary app spans retain `app.correlation_id`, `request_id` and `session_id` attributes
under the existing trace contract; none becomes a metric label.

Metadata-only thinking is **unsupported/fail-closed**: the current native Part encoder
retypes intentionally empty thinking to text. Selected thinking rejects at Build and
carried thinking rejects at capture; neither placeholders nor private reasoning retention
are a workaround. The corpus materializer emits no media; reduced Python media is refused,
not claimed SDK parity. Supported text/tool shapes are tested through actual native and
OTLP encoders; this is not complete SDK/media parity or whole-CLI admission proof.

### Native metric subset and identity

Only `gen_ai.client.operation.duration` (histogram, unit `s`) and
`gen_ai.client.token.usage` (histogram, unit `{token}`) are emitted, using the duration/token
bounds below and cumulative temporality. Observations count actual completed model executions,
including work whose subsequent callback/publication fails; metric-only ticks add no work.
Tool-count, TTFT, execute-tool duration and error instruments are not part of this initial
subset. Synthetic byte-based token modeling is not a vendor tokenizer measurement; Python
marks inclusive input, Go leaves it unspecified.

Point attributes are `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`,
`gen_ai.agent.name`, optional `gen_ai.agent.version` and configured `agento11y.tag.<k>`.
Token points add `gen_ai.token.type` and positively marked `gen_ai.token.semantics`.
Go client/context tags merge with context winning; Python accepts client tags only.
Generation-only tags override native-generation tags, never span/metric dimensions.
No duplicate SDK promrw instrument is emitted.

Resources retain actual `service.name`, `service.namespace`, `service.version`,
`deployment.environment.name`, `telemetry.sdk.name`, `telemetry.sdk.language`,
`k8s.cluster.name`, `k8s.namespace.name`, `k8s.pod.name` and the matching
`k8s.deployment.name` or `k8s.statefulset.name`; optional declared context/use-case/team
resource attributes follow the existing app identity contract. Scopes are the pinned Go/Python
library names below. App workload scoping remains blueprint-scoped (unlike `ai_agent`),
with selector stamping owned only by the root scoped writer. Direct component encoder
proof bypasses that writer and cannot certify composed scope or backend promotion.

Local synthkit-derived spanmetrics are explicitly unsupported; root must refresh the flag
before projection for a composed live-toggle fence. Backend graph/spanmetrics derived from
actual traces require separate observed evidence. See the [workload guide](../docs/workloads.md#bound-agent-observability-executor-component-boundary)
for exact selectors, bounds and authoring limitations.

## Lane A — native HTTP protojson [slug: sigil-lane-a]

- `POST /api/v1/generations:export`: `agento11y.v1.ExportGenerationsRequest`.
- `POST /api/v1/workflow-steps:export`: `agento11y.v1.ExportWorkflowStepsRequest`.
- `POST /api/v1/scores:export`: `sigil.v1.ExportScoresRequest` (evaluation namespace unchanged).

Generation ingest remains distinct from OTLP tracing. The backend also aliases legacy generation
gRPC names; synthkit uses HTTP, not gRPC. Vendored message types and hashes are documented in
[proto provenance](../internal/sink/sigil/v1/PROVENANCE.md). `just sigil-proto` generates messages only.

Basic mode sends `Authorization: Basic base64(tenant:token)` **and** `X-Scope-OrgID: <tenant>`.
The canonical hosted endpoint shape is `https://agento11y-prod-<region>.grafana.net`. Endpoint
selection is explicit configuration, never a sink-side rewrite of a caller's URL.

### Generation vocabulary

| Fields | Shape |
|---|---|
| `id`, `conversation_id` | Stable generation and conversation identifiers |
| `operation_name`, `mode` | `generateText` / `streamText`; `GENERATION_MODE_SYNC` / `GENERATION_MODE_STREAM` |
| `trace_id`, `span_id` | Matching Lane-B generation root, hex-32 / hex-16 |
| `model.provider`, `model.name`, `response_model`, `response_id` | Provider/request/response identity; model stays price-resolvable, not an inference-profile alias |
| `system_prompt`, `input[]`, `output[]`, `tools[]` | Content-bearing native ingest surface |
| `started_at`, `completed_at`, `stop_reason`, `call_error` | Completed call timing and outcome |
| `agent_name`, `agent_version` | Declared identity; absent version omitted |
| `tags`, `metadata` | Generation data, not automatically client metric dimensions |
| `max_tokens`, `temperature`, `top_p`, `tool_choice`, `thinking_enabled` | Optional request parameters; preserve scalar presence |
| `parent_generation_ids[]` | Explicit delegation tree |
| `effective_version` | Optional `sha256:<64 lowercase hex>`; synthkit supplies its existing deterministic prompt digest |

When effective_version is absent, the current backend derives its catalog key from agent_version
when present, otherwise the system prompt. That fallback is not the former tools+prompt rule.

Messages support `MESSAGE_ROLE_USER`, `MESSAGE_ROLE_ASSISTANT`, `MESSAGE_ROLE_TOOL`, and additive
`MESSAGE_ROLE_SYSTEM` (also represents an OpenAI developer role). Part payloads are a oneof:
`text`, `thinking`, `tool_call{id,name,input_json}`, `tool_result{tool_call_id,name,content,
content_json,is_error}`, or additive `media{kind,url,mime_type,name}`. Bytes use protojson base64.
Tool definitions carry `name`, `description`, `type`, `input_schema_json`, `deferred`.

### Token usage and dashboard impact

`usage.input_tokens`, `output_tokens`, `total_tokens`, `cache_read_input_tokens`,
`cache_write_input_tokens`, `reasoning_tokens`, and additive `input_semantics` are int64/enum fields.
For `TOKEN_INPUT_SEMANTICS_INCLUSIVE`, input **includes** both cache buckets; output **includes**
reasoning; derived total is **input + output**. Fresh input is input minus cache-read minus
cache-write. Do not add reasoning or cache buckets again in dashboards or cost calculations.

The Claude plugin mapper positively marks inclusive semantics; the pinned Codex mapper leaves
its marker unspecified and reports neither a cache-write bucket nor a thinking-enabled flag. The modeled Python provider adapters normalize to inclusive. Synthkit
mirrors those markers rather than guessing semantics from a provider name. Coding input now
reports the whole prompt; its fresh portion remains the captured tiny 1–6 token shape. Reasoning
is an output sub-bucket, not extra tokens. Existing total/input/output and cost dashboards must
use the semantics marker and avoid summing all five token categories.

Metadata uses `agento11y.sdk.name` and canonical `agento11y.user.id`. **The SDK span user key is
still `user.id`**, not `agento11y.user.id`; metadata and span keys must not be blanket-renamed.

### Workflow steps and scores

Workflow steps carry ID/conversation, step_name/framework, timing, input_state/output_state,
error, tags/metadata, linked_generation_ids/parent_step_ids, agent identity and trace/span links.
Synthetic workflow steps describe application/framework work, never backend guard work.

Scores carry score_id, generation_id or trial_id, evaluator_id/evaluator_version, rule_id,
experiment_id, score_key, typed value, optional passed, explanation, metadata, created_at, source,
conversation/trace/span links, test_case_id and grader correlation. Additive `report_role` is
`primary_verdict` or `diagnostic`; it is **not** online `evaluator_role`. HTTP uses presence of
`passed` directly (including explicit false), not the protobuf's legacy `has_passed` sentinel.
String scores without a pass condition omit passed. Per-item acknowledgements must confirm every
score; duplicate acknowledgements count as idempotent delivery.

`score_id` is 1–128 Unicode characters after trimming. Source kind, rule_id, score_id and score_key
cannot start with reserved `__grafana_`; backend-compatible collation folding also rejects case,
accent, width and invisible-character lookalikes. The sink rejects these locally before any stage
is sent. This is not a substitute for the server's complete score validation.

## Lane B — OTLP spans [slug: sigil-lane-b]

Coding plugin scopes are **fixed**, including subagents: `agento11y.claude-code` and
`agento11y.codex`. They are not `agento11y.<display-agent-name>`. Default plugin resource
`service.name=agento11y`, `job=agento11y`; explicit blueprint resource identity remains authoritative.
SDK library scopes deliberately retain `github.com/grafana/sigil-sdk/go/sigil` and
`github.com/grafana/sigil/sdks/python`. General-agent framework envelope scope `agents.base` and
optional provider scope `opentelemetry.instrumentation.openai_v2` remain unchanged.

Coding has a generation CLIENT root plus execute_tool INTERNAL children. General agents add
`agents.base` INTERNAL envelope parents, and OpenAI provider CLIENT children. Delegation spans
share the parent's trace and carry their own agent identity and generation ID, not a custom scope.
The historical provider child may use `gen_ai.system`; the **current SDK generation root does not**.
No synthetic span events are added by this contract refresh.

### Coding generation spans [slug: sigil-spans-coding]

Generation-root attributes (optional fields omitted when absent):

```yaml signals
family: sigil_spans_coding
scope: substrate
sink: otlp
span_attributes:
  gen_ai.operation.name: generateText|streamText
  gen_ai.provider.name: anthropic|openai|bedrock|gemini
  gen_ai.request.model: <request model>
  gen_ai.response.model: <actual response model>
  gen_ai.response.id: <provider response id>
  gen_ai.response.finish_reasons: <string array>
  gen_ai.request.max_tokens: <int>
  gen_ai.request.temperature: <float>
  gen_ai.request.top_p: <float>
  gen_ai.agent.name: <declared agent>
  gen_ai.agent.version: <optional version>
  gen_ai.conversation.id: <conversation id>
  gen_ai.usage.input_tokens: <inclusive when marked>
  gen_ai.usage.output_tokens: <includes reasoning>
  gen_ai.usage.cache_read_input_tokens: <input subset>
  gen_ai.usage.cache_write_input_tokens: <input subset>
  gen_ai.usage.reasoning_tokens: <output subset>
  gen_ai.token.semantics: inclusive
  agento11y.generation.id: <generation id>
  agento11y.sdk.name: sdk-go|sdk-python
  agento11y.conversation.title: <optional title>
  agento11y.gen_ai.request.tool_choice: <optional choice>
  agento11y.gen_ai.request.thinking.enabled: <bool>
  agento11y.gen_ai.request.thinking.budget_tokens: <optional int>
  agento11y.generation.parent_generation_ids: <string array>
  agento11y.tag.<k>: <configured client/context tag>
  user.id: <synthetic identity>
  error.type: provider_call_error                        # provider failure only
  error.category: server_error|rate_limit|timeout         # modeled cause classification
resource_attributes:
  service.name: <declared service or agento11y plugin default>
  job: <declared job or agento11y plugin default>
```

### Execute-tool spans [slug: sigil-spans-coding-tool]

Execute-tool spans use gen_ai.operation.name=execute_tool, gen_ai.tool.name/type/call.id,
optional gen_ai.tool.description, gen_ai.agent.name/version, conversation/provider/request-model
identity, agento11y.sdk.name and configured client tags. `gen_ai.tool.call.arguments` and
`gen_ai.tool.call.result` require full capture. Generation roots remain metadata-only in the
existing synthetic trace path; native ingest is the sanctioned content-bearing exception.

### General framework envelopes [slug: sigil-spans-general-envelope]

General full-capture envelope attributes remain agent.available_tools, agent.peer_agents,
agent.system, agent.user_prompt. General resources retain declared service/namespace/environment/
k8s/cloud identity.

### General SDK calls [slug: sigil-spans-general-llm]

General SDK generation spans use the same current identity/usage vocabulary above, with
agent-specific library scope selection and array-valued parent-generation links. They do not
receive a fabricated per-agent instrumentation scope.

## Lane C — metrics [slug: sigil-lane-c]

### SDK metrics [slug: sigil-gen-ai-metrics]

Prometheus translation replaces dots with underscores. Current agent labels are
`gen_ai_agent_name` and optional `gen_ai_agent_version`. Shared identity is gen_ai_provider_name,
gen_ai_request_model. Only configured client/context tags become `agento11y_tag_<translated-key>`
labels; arbitrary generation-only tags are never promoted. Values must remain bounded.
Admission rejects client-tag key collisions per declared agent after the SDK's whitespace trim
and actual Prometheus translation, including `team.name` versus `team_name` and whitespace aliases.
Safe distinct dotted native keys remain distinct; lossy keys are never allowed to swap values.

| Histogram | Additional dimensions |
|---|---|
| `gen_ai_client_token_usage` | gen_ai_operation_name, gen_ai_token_type=input/output/cache_read/cache_write/reasoning; gen_ai_token_semantics=inclusive only when usage is positively marked |
| `gen_ai_client_operation_duration_seconds` | gen_ai_operation_name; execute_tool adds gen_ai_tool_name; failure adds error_type/error_category |
| `gen_ai_client_tool_calls_per_operation_count` | Identity + configured tags only; **no operation label** |
| `gen_ai_client_time_to_first_token_seconds` | Identity + configured tags only; streaming observations only |

Provider-call failures use SDK `error.type=provider_call_error` and the modeled cause's SDK
category: `server_error` for 5xx, `rate_limit` for 429, `timeout` for deadline/cancellation.
These are sourced from SDK 75a1c548 Go generationErrorType/classifyErrorCategory (Python agrees).
Provider raw JSON names such as overloaded_error or api_error are not SDK error.type values.
The same classifications reach generation spans, promrw and native OTLP duration metrics.

Duration/TTFT bounds: 0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24,
20.48, 40.96, 81.92. Token bounds are powers of four from 1 to 67,108,864. The retained synthetic
tool-count bounds (0,1,2,4,8,16,32,64) remain **assumed**, not a source-confirmed SDK view.
The optional native OTLP lane carries the token and operation-duration instruments with dotted
names, SDK agent/tag/error dimensions and extended token categories. It uses no OTel SDK.

```yaml signals
family: gen_ai_client
scope: substrate
sink: promrw
labels:
  gen_ai_agent_name: <declared agent>
  gen_ai_agent_version: <optional version>
  gen_ai_operation_name: generateText|streamText|execute_tool
  gen_ai_provider_name: <provider>
  gen_ai_request_model: <model>
  gen_ai_token_type: input|output|cache_read|cache_write|reasoning
  gen_ai_token_semantics: inclusive
  gen_ai_tool_name: <tool>
  error_type: provider_call_error
  error_category: server_error|rate_limit|timeout
  agento11y_tag_<k>: <configured client/context tag>
metrics:
  - {root: gen_ai_client_token_usage, type: histogram, unit: token, v: ok}
  - {root: gen_ai_client_operation_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: gen_ai_client_tool_calls_per_operation_count, type: histogram, unit: count, v: ok}
  - {root: gen_ai_client_time_to_first_token_seconds, type: histogram, unit: seconds, v: ok}
```

## Legacy synthetic evaluation metrics [slug: sigil-eval-metrics]

Allowed only when real rules do not score the same agents. Source shapes below are tenant push
shapes; tenant_id is routing, not an emitted synthetic label. Resource identity and agent_version
are absent from eval families. Empty dimensions are omitted, not sent as empty sentinels.

Define **I** = evaluator, evaluator_kind, rule, gen_ai_agent_name, gen_ai_request_model,
gen_ai_request_provider. For llm_judge only, **I** also includes eval_ai_request_model and
**eval_ai_request_provider**, describing the **configured judge target**, never the scored model.
For non-judge evaluators both eval_ai labels are absent. **T** = explicitly allowlisted
`filterable_tag_keys`, emitted as `agento11y_eval_tag_<sanitized-key>` on score/execution/duration
families only. SDK tag prefixes are stripped, punctuation/underscore runs collapse, trailing
underscores trim; values truncate at 128 Unicode characters. No allowlist means no eval tag labels.
Absent tags are omitted under synthkit's no-empty-dimension invariant.

| Emitted family | Labels | Kind / bounds |
|---|---|---|
| `agento11y_eval_scores_total` | I + T + evaluator_role + score_key + passed | counter; passed=true/false/unknown |
| `agento11y_eval_executions_total` | I + T + status | counter; execution success differs from score pass |
| `agento11y_eval_duration_seconds` | I + T | histogram; 0.01 doubling through **327.68** |
| `agento11y_eval_enqueue_total` | evaluator_kind, rule | counter |
| `agento11y_eval_rule_action_fires_total` | rule, result | counter; no_actions when unconfigured; upstream empty action_type omitted |
| `agento11y_eval_judge_duration_seconds` | model, provider | histogram; 0.01 doubling through **81.92** |
| `agento11y_eval_judge_requests_total` | model, provider, status | counter |
| `agento11y_eval_judge_errors_total` | model, provider, error_type | counter |
| `agento11y_eval_judge_cost_usd_total` | evaluator, evaluator_kind, rule, model, provider, gen_ai_agent_name, gen_ai_request_model, gen_ai_request_provider | synthetic legacy estimate only for a known model price |

```yaml signals
family: agento11y_eval
scope: substrate
sink: promrw
labels:
  evaluator: <evaluator id>
  evaluator_kind: llm_judge|heuristic|json_schema
  rule: <rule id>
  gen_ai_agent_name: <scored agent>
  gen_ai_request_model: <scored model>
  gen_ai_request_provider: <scored provider>
  eval_ai_request_model: <judge target, judge only>
  eval_ai_request_provider: <judge target provider, judge only>
  evaluator_role: gate|outcome
  score_key: <score key>
  passed: true|false|unknown
  status: success|failed
  model: <judge response model>
  provider: <judge response provider>
  error_type: <judge error>
  result: no_actions
  agento11y_eval_tag_<k>: <allowlisted tag>
metrics:
  - {root: agento11y_eval_scores_total, type: counter, unit: count, v: ok}
  - {root: agento11y_eval_executions_total, type: counter, unit: count, v: ok}
  - {root: agento11y_eval_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: agento11y_eval_enqueue_total, type: counter, unit: count, v: ok}
  - {root: agento11y_eval_rule_action_fires_total, type: counter, unit: count, v: ok}
  - {root: agento11y_eval_judge_duration_seconds, type: histogram, unit: seconds, v: ok}
  - {root: agento11y_eval_judge_requests_total, type: counter, unit: count, v: ok}
  - {root: agento11y_eval_judge_errors_total, type: counter, unit: count, v: assumed, note: source-confirmed name and keys; no new live error sample}
  - {root: agento11y_eval_judge_cost_usd_total, type: counter, unit: USD, v: ok}
```

`evaluator_role=gate` is **positional**: non-final evaluators of a sequential rule. The terminal
sequential evaluator and all parallel/ruleless evaluators are `outcome`. Admission rejects a
missing nonterminal evaluator or a gate without a supported explicit verdict: the legacy model
supports its numeric-threshold and bool-detection policies, but no string pass_value/pass_match
condition. Runtime advances only on the emitted score's HasPassed && Passed; absent/false verdicts
stop subsequent evaluations, and a missing gate is never skipped. Omitted execution_mode preserves
the existing parallel behavior. Synthetic sampling remains generation/evaluator-based for parallel
rules and generation/rule-based for sequential rules, not exact backend conversation/rule sampling.
This is legacy synthetic evaluator scheduling, not backend hook/pipeline implementation.

Judge model/provider on request/duration/error rows describes the judge response identity; the
existing JudgeModel/JudgeProvider declaration supplies both target and response identity. A
separate profile-to-response resolution is not modeled. Eval cost remains an explicitly synthetic
estimate from the legacy event's modeled token counts, not an invoice or real judge-token capture.

**Not emitted:** `agento11y_eval_score_values_total` (backend score ingest derives it),
`agento11y_eval_judge_tokens_total` (scrape-only in pinned inference source; absent in W0 tenant
lookbacks), backend-global queue_depth, generation cost/model price, experiment and hook families.
No substitute token metric name is invented. `eval_ai_request_*` are judge-identity **labels**, not
replacement token-count instruments.

## Archetypes, failures and boundaries [slug: sigil-archetypes]

The admitted archetypes are coding_claude_code, coding_codex, general_orchestration,
general_autonomous_loop, general_multiturn, general_single_shot and regulated_document_authoring.
The last key selects the embedded fictional regulated-document corpus; it adds no domain-specific
catalog behavior. Coding uses large cached prompts; general agents have modest input/output.
Orchestration/coding subagents retain parent-generation links; general orchestration/autonomous
loops emit application workflow steps. Tool-result carry-forward is unchanged by the wire refresh.
Generic `ai_agent` corpus sampling does not preserve ordered attacks. The separate bound-app
executor explicitly constructs model-visible tool input at its execution barrier; neither path
infers trusted sequencing from corpus order.

### Failure modes [slug: sigil-failure-modes]

`provider_call_error` makes a deterministic fraction of calls fail across native generation,
root span status and duration labels; failed generations are not scored. `eval_quality_regression`
biases synthetic scores downward. Observe scores_total{passed="false"}, not the retired synthetic
score-values family, for that incident. Real backend rules must own their own outcomes.

### Cardinality [slug: sigil-cardinality]

Conversation/generation/trace/span/tool-call/user identifiers are ingest fields or span attributes,
never Mimir labels or Loki stream labels. Agent/model/evaluator/rule sets and all tag values must
be bounded by declarations. No Loki lane is added.

### Stage boundary

Per-generation stage types live in internal/sigil: BeforeToolResult runs before model-visible
carry-forward and permits explicit allow/deny/content replacement; AfterGeneration receives the
completed generation before publication/delegation. The interfaces alone are **types only**, not installed hooks,
whole-conversation callbacks, or a new pipeline. The separately configured bound-app consumer
above requires injected collaborators; ordinary CLI construction remains closed. Callers own cancellation/fail-open policy. Backend
guard telemetry must come from the actual backend response/work, never these interfaces themselves.
