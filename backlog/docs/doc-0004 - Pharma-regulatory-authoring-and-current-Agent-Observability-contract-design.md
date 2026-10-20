---
id: doc-0004
title: Pharma regulatory authoring and current Agent Observability contract design
type: specification
created_date: '2026-10-20T22:20:00Z'
updated_date: '2026-10-20T22:20:00Z'
---
Status: approved direction, reviewed (adversarial review FAIL on v1, corrections folded in here).
Owner decisions are in "Decisions". Engineering choices inside that scope are in "Shape".

## Purpose

Model a fictional pharma sponsor's regulated-document authoring estate in a shipped blueprint, end
to end: AWS account, EKS with k8s-monitoring (Alloy, kube-state-metrics, cAdvisor, kubelet, pod
logs), Bedrock application inference profiles per team, and a five-agent authoring pipeline
(orchestrator, source, drafter, reviewer, readability) emitting current Grafana Agent Observability
data: generations, tool executions, workflow steps, guard outcomes, online evaluation scores,
conversation ratings and experiments. Bring synthkit's Agent Observability lanes up to the current
SDK and backend contract while doing it.

The grafana-cloud-agento11y-demo regulatory app is a shape reference only. This is not a clone.
Coding agents, sports-news, comms overlay and any EC2 coding host are out of scope.

## Decisions (owner, 2026-10-20; do not re-open)

- Guards and online evaluations come from the real backend: synthkit provisions zero-cost hook rules
  and heuristic/regex evaluators (and bounded llm_judge evaluators) on the stack and calls
  `POST /api/v1/hooks:evaluate`. The backend emits every `agento11y_hook_*` series, guard workflow
  step and online score. synthkit never forges a backend-only record. In dry-run and e2e the hook
  call goes to the e2e receiver; there is no synthesised guard telemetry.
- Experiments are in scope now, through the real experiment-run endpoints.
- The blueprint uses standard Agent Observability surfaces for outcomes (workflow steps, scores,
  tags, ratings). A generic app custom-metrics mechanism is a separate task, not a dependency.
- Live verification targets the rkaidev stack (1802885). Conversations, ratings, guard steps and
  experiment runs are permanent records there; a live volume budget is set before the first push.

## Sources (pinned)

- SDK `grafana/agento11y` origin/main 75a1c548 (JS `@grafana/agento11y` 0.13.0+ is the shape the
  demo uses; Go and Python agree on wire).
- Backend `grafana/sigil` origin/main bc963fb09.
- Every name below is from those trees; anything else is PENDING in cantfind.md.

## Producer table (binding)

| Class | Examples | synthkit |
|---|---|---|
| SDK-side | generations, tool executions, workflow steps, ratings, experiment-run writes, OTLP spans, `gen_ai.client.*` metrics | emits |
| Backend-derived from synthkit ingest | `agento11y_generation_cost_usd_total`, `agento11y_model_price_*`, `agento11y_eval_score_values_total` from exported scores, `agento11y_experiment_*` | never emits |
| Backend work synthkit triggers | `agento11y_hook_*`, guard workflow steps, online eval scores and `agento11y_eval_*` from evaluation rules | never emits; triggers via real calls and provisioned rules |

The existing synthetic online-score path (Lane A `scores:export` + Lane C `agento11y_eval_*`) stays
for blueprints that do not provision real rules, but never on a tenant where real rules score the
same agents. Its score_values double count is fixed.

## Work packages (serial where marked)

### W0. Live baselines on rkaidev (read-only, main thread)
Query: agent label on `gen_ai_client_*` (`agent_name` vs `gen_ai_agent_name`); whether
`agento11y_generation_cost_usd_total` already exists for synthkit agents; whether
`agento11y_eval_judge_tokens_total` is still pushed; credential scope for the agento11y management
API (evaluators, hook rules, evaluation rules, collections, experiments) and ratings.
Stop if a credential is missing; never mint one silently.

### W1. Contract refresh (one owner of internal/sigil, internal/sink/sigil, signals/sigil.md)
1. Re-vendor `generation_ingest.proto` (now `agento11y.v1`) and `evaluation_ingest.proto` (still
   `sigil.v1`) from bc963fb09; PROVENANCE and `sigil-proto` recipe updated.
2. Full span attribute diff, not just a prefix rename: `sigil.*` → `agento11y.*` keys,
   `gen_ai.agent.name`/`.version`, `agento11y.sdk.name`, `agento11y.user.id`; plugin scopes are fixed
   constants (`agento11y.claude-code`, `agento11y.codex`), `service.name=agento11y`; SDK library
   scopes unchanged and chosen per evidence.
3. Inclusive token semantics: `input_semantics=TOKEN_INPUT_SEMANTICS_INCLUSIVE` +
   `gen_ai.token.semantics="inclusive"` where the real adapter does it; reasoning stays a sub-bucket
   of output; dashboard impact listed.
4. Per-family label and bucket diff at bc963fb09 for every `agento11y_eval_*` family synthkit still
   emits synthetically (eval_ai_request_* is the judge target and absent for non-llm_judge; eval
   duration buckets to 327.68, judge duration to 81.92; `gate` is positional; tag labels need
   `filterable_tag_keys`; drop judge_tokens per W0).
5. Exporter parity: `X-Scope-OrgID` in Basic mode, canonical `agento11y-prod-<region>` host, score
   id limits and reserved `__grafana_` prefix.
6. W1 freezes only the vocabulary and a per-generation stage interface (hook point before the model
   sees a tool result, after a generation completes).
Acceptance: `just check`; golden Lane A and Lane B payload tests; `just dump` diff limited to the
intended changes; existing blueprints load.

### W2. Agent Observability provisioner (new control-plane surface)
Blueprint-declared evaluators, hook rules, evaluation rules, collections/rule actions and test
suites, mirroring the provider resources `grafana_agento11y_evaluator`, `_hook_rule`,
`_evaluation_rule`, `_collection`, `_rule_action` exactly (`rule_id`, `phase`, `priority`,
`selector`, `match`, `evaluator_ids`, `action_on_fail`, `short_circuit`, `tool_filter`,
`redact{patterns}`). Applied serially with read-back (the API partially fails under parallel
creates). Destination-bound ownership and teardown in the Fleet/SM provisioner style; declared once
per blueprint; ValidateSet claims rule_id, agent_name and judge model/provider across blueprints.
Design review before code: API surface and auth from the backend source, ownership/lease model,
dry-run behaviour.

### W3. Live hook calls (serial after W1 and W2 design)
Per agent `hooks: {enabled, phases, fail_open, timeout_ms}` (client-side; fail_open emits nothing).
Preflight once per tool result before the model sees it (demo basis); postflight only with a stated
basis. Request shape per `conformance/hooks/request-*.json` with `context.model` set and correlation
(conversation_id, trace_id, span_id, `agento11y.generation_id` tag). Deny aborts the stage and drives
the escalated outcome; `transformed_input` replaces the tool result content (backend writes
`[REDACTED]`). e2e receiver implements `hooks:evaluate` with deterministic allow/deny/redact.

### W4. Multi-service agent pipeline (spike first, one owner with W3)
Spike decides the seam. Preferred: the five agents are `app` service nodes (existing HTTP hops, W3C
propagation, service graph, spanmetrics, pod placement and logs) with a per-node Agent Observability
binding; fallback: `ai_agent` per-agent `service:` mapped by the resolver. The spike lists every
single-owner file it touches (resolver, fixture, runner, ledger).
Behaviour: orchestrator plans, source retrieves (tools only), drafter drafts, reviewer gates with a
bounded revision loop, readability runs for some request kinds; terminal outcome from a blueprint
set; workflow steps per stage; ratings at a low single-digit rate; tag channels per SDK language
(only client/context tags become `agento11y_tag_<k>` metric labels).
Acceptance: one trace spans every agent service; KSM pod names equal OTLP `k8s.pod.name`; outcome
mix matches declared weights over a seeded run; native OTLP metrics (`otel.metrics: true`).

### W5. Experiments (after W2 and W4)
Real experiment runs via the experiment-run endpoints (upsert, trials, finalize), `test_suite_run` /
`eval.trial` spans with `gen_ai.evaluation.result` events, trial scores with one `report_role:
primary_verdict` per trial. Backend emits `agento11y_experiment_*`. Suites: section-prompt variants
and a guard-policy suite. Schedule and daily cap with a stated basis.

### W6. Fictional regulatory corpus (parallel with W4)
Generic catalog key, fictional content only (sponsor Brindlemoor Therapeutics, compound brindanib,
studies XQZ-301/XQZ-204, after a web check for real-world collisions). Includes subject and
investigator identifiers for the redaction rule and source-note injection text for the detector.
Acceptance: every corpus file decompressed and scanned against the local forbidden list and the CI
pattern; generator never sees customer material or the demo design doc.

### W7. Bedrock application inference profiles (sourcing gate)
Profile shapes sourced from a real deployment (read, with owner approval) or kept gated with a
cantfind PENDING. Invocation logs are an app telemetry profile; OTLP CloudWatch mode uses
metric-stream attributes. Generation model stays provider + model name so backend pricing works.

### W8. Blueprint `pharma-regulatory-authoring.yaml` (main thread)
One prod env, eu-west-1, EKS with k8s_monitoring and alloy_health, `cloudwatch_export: otlp`,
Bedrock profiles 3 teams × 2 models, the pipeline, provisioned guards/evaluators/rules/suites.
Rates with basis: about 30 medical writers × 10-20 section requests per working day ≈ 0.6-1.25
requests/min over business hours, 3-5 generations and one hook call per tool result per request;
baseline deny ≈ 0 outside the injection-campaign incident. Incidents only where a mechanic targets
them (per-agent / per-profile targeting and an agent_version rollout are scoped in W4/W7 or dropped);
each incident changes `-dump`.

### W9. Docs, gates, live verification
`just gen`; docs and the create-blueprint skill; cantfind entries; signals fidelity list decision;
`.env`/`.env.example` + `TestEnvSurfaceAligned` for new knobs; selfobs push observers for new
endpoints; e2e routes for hooks, ratings and experiments; forced-emission duplicate-series test;
CodeRabbit once per wave; `just race` and `gaterun -- just check` on the final candidate; live push
to rkaidev inside the volume budget with read-back of conversations, guard steps, scores, hook
metrics and experiments.

## Stopping conditions

- Read-back shows the backend already produces a family synthkit was about to emit.
- No source for a Bedrock profile shape: keep it gated, file PENDING.
- Any forbidden-word hit, including inside decompressed corpus files.
- Mimir duplicate-series rejections.
- Missing credential or scope on rkaidev.

## Standing constraints

- The private customer codename never enters the repository, commits, tasks or docs.
- Catalog vocabulary stays generic; pharma roles, teams, document types, sections and rule ids live
  in the blueprint.
- Test-shaped rates live in `e2e/fixtures/`, never in `blueprints/`.
- Fleet Management and selfobs behaviour unchanged apart from push observers for new endpoints.
