---
id: SKT-0091
title: 'Make nettopo_spoke_down take down a subset of spokes, not all of them'
status: To Do
assignee: []
created_date: '2026-10-05 12:29'
labels:
  - network
dependencies: []
references:
  - internal/construct/nettopo/health.go
  - blueprints/netobs-global.yaml
priority: medium
type: bug
ordinal: 213000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
health.go evaluates spokeIsDown as spokeDownActive && unreachableSubset(sp, 1.0). unreachableSubset maps the key to u in [0,1) and returns u < intensity, so at a fixed 1.0 every spoke is down whenever the incident is active. The inline comment ('at any intensity, first spoke is down') and the netobs-global blueprint comment ('only one spoke ... is affected') both say otherwise, and the federation dashboards show all five spokes failing in lockstep, which reads as a hub outage. Use the incident intensity to select a deterministic subset (for example the first max(1, round(intensity*n)) spokes in sorted order) and add a test that pins it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 With intensity 0.3 and five spokes, exactly one deterministic spoke reports federation_spoke_up=0 while the incident is active
- [ ] #2 A test fails on the current code and passes after the fix
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
