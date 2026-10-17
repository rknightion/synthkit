---
id: SKT-0138
title: Publish a bounded operational manifest of intended delivery lanes and cadences
status: To Do
assignee: []
created_date: '2026-10-16 13:53'
labels: []
dependencies: []
references:
  - >-
    https://github.com/rknightion/synthkit/blob/9f04a7d0b1cfebaccf50a8d40063e547967d99a4/internal/runner/delivery.go
priority: high
type: feature
ordinal: 261000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Push counters describe lanes that have emitted, not every lane the resolved active worlds intend to deliver. An external observer cannot distinguish a quiet configured lane, an absent required lane, disabled emission or missing telemetry by enumerating returned series. The runner already derives delivery-readiness lanes, but there is no supported self-observability expectation manifest bound to runtime configuration.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A documented bounded secret-free manifest derives intended lane identities and relevant cadence or observation bounds from actual resolved active worlds and enabled sinks, not observed push-series enumeration
- [ ] #2 Manifest provenance changes with effective configuration and process/session identity; standby, disabled, unsupported and missing evidence cannot silently remove required lanes from a health denominator
- [ ] #3 Public runtime/export tests cover a missing lane, valid quiet cadence, changed configuration and disabled emission without arbitrary rates or duplicated blueprint-specific logic
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
