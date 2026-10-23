---
id: SKT-0172
title: >-
  Preserve fractional Solace publish and drain capacity across observation
  windows
status: In Progress
assignee:
  - '@loop53'
created_date: '2026-10-23T09:00:00Z'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 323000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Solace queue model rounds publishRate*load*windowSeconds and consumeRate*windowSeconds independently each tick. At normal 60-second cadence a plausible sparse flow below half a message per window disappears forever, and fractional drain capacity also vanishes or is inflated. Preserve realistic configured rates; no metric names, labels or types change.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Sparse positive publishing produces integral messages over elapsed windows rather than permanent zero, with bounded cumulative rounding error
- [ ] #2 Fractional consumer capacity drains queued messages over time without creating idle-time catch-up bursts, including zero-volume and zero-consumer behavior
- [ ] #3 Meaningful red/green real construct Tick tests cover sparse traffic, queue conservation and rollups; inventory names labels and types remain unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read existing queue step and tests; prove sparse publish/drain failures via Tick; retain bounded fractional work across windows without idle delivery banking; scoped remote gate, inventory comparison and dsreview, then composed root landing.
<!-- SECTION:PLAN:END -->
