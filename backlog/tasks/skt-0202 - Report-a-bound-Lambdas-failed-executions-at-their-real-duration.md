---
id: SKT-0202
title: Report a bound Lambda's failed executions at their real duration
status: To Do
assignee:
  - '@loop58-root'
created_date: '2026-10-25T12:50:00Z'
updated_date: '2026-10-25T15:16:00Z'
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
<!-- SECTION:NOTES:END -->
