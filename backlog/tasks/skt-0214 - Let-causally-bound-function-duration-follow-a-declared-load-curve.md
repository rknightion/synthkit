---
id: SKT-0214
title: Let causally bound function duration follow a declared load curve
status: Parked
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-26T03:26:00Z'
labels:
  - causal
dependencies: []
ordinal: 418000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Bound functions currently retain flat duration overnight. The causal model needs a declared duration curve rather than an emitter-only adjustment.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A causally bound function can vary healthy duration with the declared load curve while invocation scheduling remains independent
- [ ] #2 Engine occupancy, emitted duration and caller timing remain consistent under the curve
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Bound-function load-curve implementation held for causal/Lambda seam.

Loop60 closeout: No implementation or API freeze this run. Read-only seam decision uses declared environment Factor for duration independently of arrivals; occupancy, emission and caller timing require joint proof. Resume after evaluator/causal admission.
<!-- SECTION:NOTES:END -->
