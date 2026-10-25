---
id: SKT-0188
title: Reject a resource-axis fault aimed at a resource of the wrong kind
status: In Progress
assignee:
  - '@loop58-root'
created_date: '2026-10-25T07:44:00Z'
updated_date: '2026-10-25T07:49:00Z'
labels: []
dependencies:
  - SKT-0168.09.15
priority: medium
ordinal: 382000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The loader accepts a resource-axis mode whose target is a resource of a kind the mode does not act on (for example dependency_slow on a queue), which silently does nothing. Reject it at load with an error naming the mode, the target and the kinds it accepts.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A wrong-kind resource-axis effect fails at load with a clear error, red then green
- [ ] #2 Every valid mode-kind pairing still loads
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
