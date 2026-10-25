---
id: SKT-0198
title: Scope lambda and SQS causal consumer identities to account and region
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:52:00Z'
updated_date: '2026-10-25T12:36:00Z'
labels:
  - causal
dependencies: []
ordinal: 402000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
lambda and sqs causal_bindings omit account_id and region, and the scoped queries carry no blueprint filter, so a same-named function or queue elsewhere counts as a bound consumer. Landing-10 review minor 3.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Bound-consumer identities include account and region where the emitted series carry them
- [ ] #2 A same-named function or queue in another environment is not counted
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
