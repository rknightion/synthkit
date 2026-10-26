---
id: SKT-0212
title: Declare a per-resource failed-call hold instead of a shared pre-work constant
status: Parked
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-26T05:56:00Z'
labels:
  - causal
dependencies: []
ordinal: 416000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A shared layout pre-work share currently supplies a failed-call hold without a per-resource basis. The declared healthy reference and failure timing need an explicit resource-owned contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each causal resource can declare a failed-call hold with documented units and validation
- [ ] #2 Engine failure holds and app timelines use the declared value consistently, with red/green proof and generated schema aligned
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Per-resource failed-call hold unimplemented; resume after resend timing seam integration.

Loop60 closeout: No implementation or API freeze this run. Read-only seam decision requires presence-bearing absolute resource-owned failed work in milliseconds, excluding separately modelled children/waits/backoffs, with exact nil-path compatibility. Resume after evaluator/causal admission and context-table review.

Final loop closeout: retained unlanded causal candidateaf34 has native/component review and final CodeRabbit passes, but the exact integrated full gate fails app-race600.139s. No candidate source was pushed or deployed. Preserve task-specific earlier evidence/limits; resume only after the aggregate owner stop and relevant dependency/owner decisions are resolved, with no timeout/assertion/case/isolation reduction or unchanged full-gate retry.
<!-- SECTION:NOTES:END -->
