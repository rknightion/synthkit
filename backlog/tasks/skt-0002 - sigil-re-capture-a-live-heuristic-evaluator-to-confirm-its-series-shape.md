---
id: SKT-0002
title: 'sigil: re-capture a live heuristic evaluator to confirm its series shape'
status: Done
assignee: []
created_date: '2026-08-14 16:09'
updated_date: '2026-09-06 19:03'
labels: []
dependencies: []
references:
  - signals/sigil.md
  - internal/workload/aiagent/evals.go
ordinal: 2000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
signals/sigil.md marks the `sigil_eval_*` families `v: ok` from a live capture against a reference sigil stack (2026-06-30) — but ONLY `evaluator_kind=llm_judge` actually produced eval series in that capture. `evaluator_kind=heuristic` exists in the config surface and was never observed emitting, so its series shape is inferred rather than captured.

The specific unknown: a heuristic evaluator has no judge model, so its series should be judge-label-FREE. Whether that means the judge labels are omitted entirely (the correct shape per the absent-dimension-is-omitted rule) or carry some placeholder is unverified.

This is a live-capture task, not a code task. Per the Wave operating model doc, a live-capture lane is READ-ONLY against the stack and returns FINDINGS; the resulting signals/sigil.md edit is applied by the main thread, because signals/ files are serialized single-owner. Ask which Grafana Cloud stack to use — never assume one.

If the capture shows synth diverges from reality, the SYNTH is corrected, never the captured data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A heuristic (non-llm_judge) evaluator is confirmed active and emitting on a live sigil stack, with the stack and capture date recorded.
- [x] #2 The exact metric names and label keys its series carry are captured, and the judge-label question is answered definitively: labels omitted, or present with some value.
- [x] #3 signals/sigil.md is updated with the captured contract, its provenance and date, matching how the 2026-06-30 llm_judge capture is recorded there.
- [x] #4 Where synth diverges from the capture, the construct/workload is corrected to match reality, not the other way round.
- [x] #5 signals/sigil.md "Implementation status & next steps" item 2 is resolved or, if no heuristic evaluator can be made live, restated with what specifically blocks it.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
The repaired reference agent now has persisted online heuristic, JSON-schema and numeric/string judge scores. RKSY-0030 AC3 is checked from authenticated results. Query the authorized reference metrics tenant for the documented eval and judge families; record exact observed family names and label keys. Correct an emitter only if returned metric data establishes a divergence.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PARKED 2026-08-19 by explicit user decision (offered: run the capture against one of the project's stacks, or park with the blocker recorded — park was chosen).

Why it cannot proceed now: the task requires a heuristic (non-llm_judge) evaluator ACTIVE and emitting on a live sigil stack. None of the Grafana Cloud contexts configured on this machine is the stack that produced the 2026-06-30 llm_judge capture, and no reachable stack was found with a heuristic evaluator configured. Making one emit is a tenant WRITE (configure an evaluator of kind heuristic + an eval rule that samples turns), which is the stack owner's decision, not something a read-only capture lane can arrange. So the blocker is provisioning, not access or tooling.

What was established without a capture (read-only, code-side):
- The unknown is narrow. internal/workload/aiagent/evals.go:208-209 omits eval_ai_request_model when JudgeModel is empty, and evals.go:243-260 emits the whole sigil_eval_judge_* family ONLY for Kind == llm_judge. So synthkit already emits the judge-label-FREE shape for heuristic, i.e. it assumes the absent-dimension-is-omitted rule holds. The capture would either confirm that or show a placeholder value, in which case the SYNTH gets corrected.
- No code change was made, and nothing in the emit path was 'fixed' on an assumption.

Durable record: signals/sigil.md 'Implementation status & next steps' item 2 has been rewritten from 'pending a re-capture' to BLOCKED, carrying the specific blocker, the narrowed question (which of eval_ai_request_model / model / provider are present), and the resume boundary — so the doc no longer reads as merely deferred work.

Resume boundary for the next session: (1) get a stack NAMED, with a heuristic evaluator confirmed active; (2) query sigil_eval_scores_total{evaluator_kind="heuristic"} and the sigil_eval_judge_* families for that evaluator; (3) record which label keys are present, with provenance + date, in the same form as the 2026-06-30 capture in signals/sigil.md; (4) if reality diverges, correct the emit in evals.go, never the capture. ACs 1-5 stay unchecked: none is provable without the capture.

Doc pointer correction (2026-08-19): after the shipped SKT-0001 entry was folded into signals/sigil.md's 'Emitted + verified' paragraph, the heuristic-evaluator item became item 1 of 'PENDING — next steps', not item 2. AC #5 and the notes above refer to it by its old number.

2026-09-06 resume plan agreed with Rob: the blocker (no stack with a heuristic evaluator active) is removed by synthkit-terraform task RKSY-0030, which builds the cheapest AWS environment running a real agent instrumented with the Grafana Agent Observability SDK sending to the shared rksy stack, then creates one heuristic evaluator and one online evaluation rule through gcx. When RKSY-0030 records scores, this task resumes exactly at its recorded resume boundary as a read-only capture against that stack. Status stays Parked until then; do not start the capture before RKSY-0030 AC3 is pasted.

2026-09-06: the terraform reference stack has one configured heuristic evaluator and one online rule. Authenticated read-back at 15:59:40Z returned 40 generations, version 2026.09.06, and zero nonempty persisted-score responses. The direct model invocation requires an inference profile; failed generations carry no output. A direct evaluator test false does not satisfy the persisted-score prerequisite. No Mimir eval-family query was run; judge labels omitted versus placeholder remains unverified. signals/sigil.md now records this precise boundary, satisfying AC5 only. No emitter correction is justified. Resume after an authorized inference-scope decision and a persisted heuristic score, then capture family and label keys read-only. just check and the safe explicit-selection dump passed this wave; conditional generation requirement is not applicable to this prose-only step.

Final score prerequisite read, 2026-09-06T17:45:34Z: generations=62, agent_version=2026.09.06, conversations=62, nonempty_score_responses=0. The recorded AC5 boundary remains unchanged; no metric-family query or emitter correction was justified.

2026-09-06 capture completed after persisted online scores on the authorized terraform reference stack. Window 18:42:00-18:46:11 UTC: old-prefix queries returned HTTP 200/status success but zero series; current agento11y_eval_* queries returned heuristic=45, judge=22, all_eval=136 series. Heuristic eval_ai_request_model is PRESENT and equals the scored generation model; model/provider are absent. Score counters carry evaluator_role=outcome, execution/duration do not. Exact family/key sets and provenance are in signals/sigil.md. Corrected metric constants, scored-model labels and score-role labels; regression failed before the fix and passed afterwards.
Validation: targeted aiagent/sigil tests, just gen, just check (including safe signal-fidelity inventory comparison), explicit safe-selection just dump, and SYNTHKIT_E2E_INCLUDE_AGENT=false just e2e passed. No agent-declaring blueprint or fixture was selected. CodeRabbit completed with one minor documentation finding: clarified that JudgeModel supplies synthetic judge response-model labels, retaining the unmodified live profile/response distinction. No extra test was added for that prose clarification. Docker context now excludes ignored codex/ and runtime/; declarative exclusion validated without a new test. Local scratch has an ignored nested Go module to prevent raw evidence Go files entering package discovery; no root dependency change.
Coverage limits: enqueue and judge-cost families are catalogue-only; categorical pass_match configuration is not modeled; current judge-error and queue names are source-confirmed but unobserved in this successful live window. These do not leave the original heuristic label-key question unresolved. Stack identity is retained only in protected runtime evidence, per the run privacy contract.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Captured live heuristic series and corrected SynthKit to current agento11y_eval_* names, scored-model identity and score-only evaluator_role. All five acceptance criteria proven; targeted regression, full local check, safe dump and agent-excluded Docker e2e passed. Broader catalogue gaps remain explicitly documented.
<!-- SECTION:FINAL_SUMMARY:END -->
