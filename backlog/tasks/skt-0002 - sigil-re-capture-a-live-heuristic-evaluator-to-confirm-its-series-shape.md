---
id: SKT-0002
title: 'sigil: re-capture a live heuristic evaluator to confirm its series shape'
status: To Do
assignee: []
created_date: '2026-08-14 16:09'
updated_date: '2026-08-14 16:11'
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
- [ ] #1 A heuristic (non-llm_judge) evaluator is confirmed active and emitting on a live sigil stack, with the stack and capture date recorded.
- [ ] #2 The exact metric names and label keys its series carry are captured, and the judge-label question is answered definitively: labels omitted, or present with some value.
- [ ] #3 signals/sigil.md is updated with the captured contract, its provenance and date, matching how the 2026-06-30 llm_judge capture is recorded there.
- [ ] #4 Where synth diverges from the capture, the construct/workload is corrected to match reality, not the other way round.
- [ ] #5 signals/sigil.md "Implementation status & next steps" item 2 is resolved or, if no heuristic evaluator can be made live, restated with what specifically blocks it.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
