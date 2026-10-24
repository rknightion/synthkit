---
id: SKT-0177
title: >-
  Reconcile containerd runtime identity documentation with the closed descriptor
  contract
status: Done
assignee:
  - '@pin64'
created_date: '2026-10-23T23:06:00Z'
updated_date: '2026-10-24T00:57:00Z'
labels: []
dependencies: []
priority: low
type: docs
ordinal: 333000
---

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Deployment guidance distinguishes classic config-digest runtime IDs from the existing index-with-platform-descriptor branch without dropping index, platform, config, binary or cryptographic binding requirements.
- [x] #2 Guide examples and maintainer instructions agree with inspect-running and its negative-control tests; no production verifier, daemon, test assertion or trust predicate is weakened.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Small public documentation correction to match the already-implemented closed runtime identity alternatives and maintainer pointer; no verifier or trust predicate change.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landedclean2a0d861, exact fullcheck/privacy PASS and hostedCI40042942502SUCCESS. Docs match existing closed descriptor alternative; no verifier/bounds/runtime changes. Generation/inventory not changed by this docs-only task; actual same-source catalogue proof retained in the runtime batch. Prior unpublished local privacy failure retained, corrected ancestry verified before normal FF push.
<!-- SECTION:NOTES:END -->
