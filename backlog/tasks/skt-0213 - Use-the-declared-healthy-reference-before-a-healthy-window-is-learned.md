---
id: SKT-0213
title: Use the declared healthy reference before a healthy window is learned
status: Parked
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-25T22:18:00Z'
labels:
  - causal
dependencies: []
ordinal: 417000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A fault live from process start currently leaves faulted span-metric rows flat until a healthy observation exists, hiding the causal effect during cold start.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Causally bound span-metric rows show a fault from process start using the declared healthy reference
- [ ] #2 A subsequently learned healthy reference replaces the fallback without changing healthy output
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use the accepted restored engine/timestamp candidate as baseline; reproduce a fault live from first tick, derive a declared healthy reference for causal span-metric rows until real healthy observations exist, preserve unaffected rows and learned-reference behavior, and return only the task delta.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Accepted healthy-reference candidate unlanded. Resume causal-stack composition; preserve missing-own-timing limitation and learned-reference precedence.
<!-- SECTION:NOTES:END -->
