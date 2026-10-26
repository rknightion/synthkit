---
id: SKT-0231
title: Share per-request draws across every line of one log bundle
status: To Do
assignee: []
created_date: '2026-10-26T07:05:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 437000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A log bundle draws its outcome, route and other request-scoped choices once per logical request, so every line of that request agrees. Each logical request gets its own identifier, reused across its retries.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
