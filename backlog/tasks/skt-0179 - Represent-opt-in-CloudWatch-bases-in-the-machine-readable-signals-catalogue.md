---
id: SKT-0179
title: Represent opt-in CloudWatch bases in the machine-readable signals catalogue
status: Done
assignee: []
created_date: '2026-10-24T17:10:00Z'
updated_date: '2026-10-25T00:26:00Z'
labels: []
dependencies: []
priority: low
ordinal: 350000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Opt-in emitted bases (first case: DynamoDB ReadThrottleEvents/WriteThrottleEvents/ThrottledRequests) are documented in prose in signals/cw.md but not in a yaml signals block, because the default-emission block is a pinned contract. Decide a format for opt-in bases the fidelity checker understands.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop57: landed on main (see codex/loop57/BATCH*.md).
<!-- SECTION:NOTES:END -->
