---
id: SKT-0217
title: Evaluate retrieval rejection after the store call completes
status: Parked
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-26T03:26:00Z'
labels:
  - causal
dependencies: []
ordinal: 421000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A retrieval rule currently rejects at arrival, making rejected calls finish before the work required to decide the rejection.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Retrieval rule rejection occurs after the declared store call, so rejected requests include the store elapsed time
- [ ] #2 Trace timing, span metrics and model outcomes remain consistent for handled retrieval rejections
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Post-store retrieval rejection held for elapsed-time/app seam.

Loop60 closeout: Actual post-store rule phase remains unimplemented. The invented validator topology was rejected, not accepted as a substitute. Resume only after performance/causal integration and a real post-children timing seam; retain rejection-origin and actual store-elapsed proof.
<!-- SECTION:NOTES:END -->
