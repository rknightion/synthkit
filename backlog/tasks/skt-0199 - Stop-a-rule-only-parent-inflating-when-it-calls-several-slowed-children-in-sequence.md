---
id: SKT-0199
title: >-
  Stop a rule-only parent inflating when it calls several slowed children in
  sequence
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:53:00Z'
updated_date: '2026-10-25T14:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 403000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found by the SKT-0168.09.36 lane: the live-assistant case slows x4.38 because a rule-only legacy parent lays its children out in sequence, so each child's slowdown adds to the parent; it is not a held pool. Evidence: /Users/rob/repos/synthkit/codex/loop58/dbl/return.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A parent whose children are slowed by one fault slows by the fault's declared factor, not the sum over sequential children, where the children overlap in the real system
- [ ] #2 Healthy output unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Closed by SKT-0200: the x4.38 came from healthy requests using the legacy layout while faulted ones used the engine timeline. On 3c0f4ea6 live-assistant slows x2.13 and its added time (1260 ms) equals the store's (1262 ms). Evidence: /Users/rob/repos/synthkit/codex/loop58/seqc/return.md
<!-- SECTION:NOTES:END -->
