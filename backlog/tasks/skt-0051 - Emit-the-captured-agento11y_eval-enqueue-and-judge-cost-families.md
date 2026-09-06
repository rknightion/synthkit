---
id: SKT-0051
title: Emit the captured agento11y_eval enqueue and judge-cost families
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-06 20:17'
updated_date: '2026-09-06 21:06'
labels:
  - ai-agent
  - signals
dependencies: []
priority: medium
type: feature
ordinal: 147000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The 2026-09-06 heuristic-evaluator capture (signals/sigil.md, Lane C) observed two Agent Observability evaluation families that the ai_agent workload does not emit: agento11y_eval_enqueue_total {evaluator_kind, rule} (work items enqueued; observed 26 against 26 successful evaluator executions) and agento11y_eval_judge_cost_usd_total {evaluator, evaluator_kind, rule, gen_ai_agent_name, gen_ai_request_model, gen_ai_request_provider, model, provider} (cumulative judge spend; observed two judge requests, 916 input and 84 output tokens, USD 0.001336 on the configured judge model). Both are catalogued with provenance and marked catalogue-only. Emit them from internal/workload/aiagent/evals.go under the same evaluator fixture the other agento11y_eval_* counters use: enqueue counts one per scoring event that a rule samples; judge cost accrues only for evaluator_kind=llm_judge from the ledger's token counts times the judge model's price in signals/genai-models.md, never a fabricated constant. Labels exactly as captured; an absent dimension is omitted.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Both families appear in the explicit safe dump for a judge-declaring blueprint with exactly the captured label keys, and are absent for a heuristic-only declaration except enqueue
- [ ] #2 Judge cost derives from ledger token counts and a priced model in signals/genai-models.md; a test pins the arithmetic against the captured 916/84-token, USD 0.001336 sample
- [ ] #3 signals/sigil.md moves both rows from catalogue-only to emitted with the date
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-09: implement enqueue and judge-cost emission test-first from sampled evaluator events and ledger token pricing, update the signal catalogue, and prove both families with an explicit judge fixture dump.
<!-- SECTION:PLAN:END -->
