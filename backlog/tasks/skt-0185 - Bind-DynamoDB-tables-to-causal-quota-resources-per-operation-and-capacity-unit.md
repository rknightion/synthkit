---
id: SKT-0185
title: Bind DynamoDB tables to causal quota resources per operation and capacity unit
status: To Do
assignee: []
created_date: '2026-10-25T02:57:00Z'
labels: []
dependencies:
  - SKT-0168.09.15
priority: low
ordinal: 377000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Deferred from the loop58 causal v1 design review: one rps quota per table cannot express read and write capacity units or per-operation throttles. Add an operations[].resource binding with a quota unit (requests, read_units, write_units) so ThrottledRequests and Read/WriteThrottleEvents follow the causal model.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bound operation's throttle families follow a quota resource in its declared unit
- [ ] #2 Unbound output unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
