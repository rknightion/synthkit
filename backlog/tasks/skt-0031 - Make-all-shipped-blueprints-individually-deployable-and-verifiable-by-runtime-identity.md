---
id: SKT-0031
title: >-
  Make all shipped blueprints individually deployable and verifiable by runtime
  identity
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 122000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios C1, C4, and C5 selected every shipped blueprint individually. Only 18 of 26 became healthy within the bounded wait; some timed out after logging that synthkit was up, filenames did not always match selectable runtime names, immediate live read-back returned no frames, and the control inventory did not expose per-blueprint identity evidence needed to compare dump families with landed data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The canonical selectable runtime name for every shipped blueprint is discoverable without inferring it from the filename
- [ ] #2 Every shipped blueprint reaches a truthful healthy or setup state individually within a documented bound, or reports the lane preventing readiness
- [ ] #3 The verification path waits for two tick intervals and proves each declared signal class arrived
- [ ] #4 Dump families can be compared to live queryability per blueprint without a hand-built mapping
- [ ] #5 Representative substrate-scoped and blueprint-scoped families expose enough evidence to verify label separation
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
