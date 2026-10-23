---
id: SKT-0177
title: >-
  Reconcile containerd runtime identity documentation with the closed descriptor
  contract
status: To Do
assignee: []
created_date: '2026-10-23T23:06:00Z'
labels: []
dependencies: []
priority: low
type: docs
ordinal: 333000
---

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Deployment guidance distinguishes classic config-digest runtime IDs from the existing index-with-platform-descriptor branch without dropping index, platform, config, binary or cryptographic binding requirements.
- [ ] #2 Guide examples and maintainer instructions agree with inspect-running and its negative-control tests; no production verifier, daemon, test assertion or trust predicate is weakened.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
