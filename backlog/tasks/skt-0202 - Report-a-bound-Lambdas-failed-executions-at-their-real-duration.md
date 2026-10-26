---
id: SKT-0202
title: Report a bound Lambda's failed executions at their real duration
status: Parked
assignee: []
created_date: '2026-10-25T12:50:00Z'
updated_date: '2026-10-26T03:26:00Z'
labels:
  - causal
dependencies: []
ordinal: 406000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-up to SKT-0195. Failed executions of a causally bound Lambda now occupy the function only until they fail (about 102 ms in the certificate story), but the lambda construct still reports the completing hold (900 ms) as Duration for them. Expose the model's mean occupancy for failed work and use it for Duration. Evidence: /Users/rob/repos/synthkit/codex/loop58/rsd/return.md Q1.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bound function's Duration statistics reflect failed executions at their failure duration and successful ones at their completing duration
- [ ] #2 Unbound functions unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58 closeout (2026-10-25): parked unlanded; see /Users/rob/repos/synthkit/codex/closeout-2026-10-25-loop58.md. Accepted and in landing-13 mbox (codex/loop58/archive/land13-d4a51b34..3c664fc6.mbox); landing blocked by SKT-0211 (amd64 FMA digest drift). Resume: fix SKT-0211, re-gate, land.

Owner closeout: accepted prior timing/join candidate remains in the retained archived causal stack, not in published database-only delivery. Resume from the explicit engine-only split with accepted timestamp/resend fixes, preserving existing assertions and excluding the held reference migration; final composition, generation, review and exact-tree gate remain required.

Loop60 closeout: Accepted task-separated candidate is retained in the repaired fe995479 causal stack, not published. Composed app race still exceeds the unchanged600-second budget, including after exact optimization foundation; plain/component success is not full-gate acceptance. Resume after owner-authorized evaluator resolution, current-main composition, final generation/review/full gate and actual inventory proof. No timeout/assertion/population weakening or unchanged green sampling.
<!-- SECTION:NOTES:END -->
