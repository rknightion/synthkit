---
id: SKT-0172
title: >-
  Preserve fractional Solace publish and drain capacity across observation
  windows
status: Done
assignee:
  - '@loop53'
created_date: '2026-10-23T09:00:00Z'
updated_date: '2026-10-23T10:07:00Z'
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
- [x] #1 Sparse positive publishing produces integral messages over elapsed windows rather than permanent zero, with bounded cumulative rounding error
- [x] #2 Fractional consumer capacity drains queued messages over time without creating idle-time catch-up bursts, including zero-volume and zero-consumer behavior
- [x] #3 Meaningful red/green real construct Tick tests cover sparse traffic, queue conservation and rollups; inventory names labels and types remain unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read existing queue step and tests; prove sparse publish/drain failures via Tick; retain bounded fractional work across windows without idle delivery banking; scoped remote gate, inventory comparison and dsreview, then composed root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed task 3d1e3d6 in exact green merged30e154c/tree1290aa3 preserving foreign main. Root recomputed accepted patch hash98a8af49. Actual Tick red/green proves fractional publish/drain and conservation/rollups/no idle bank/pause; selected CLI80/80 names and labels retained. CodeRabbit7/7 composed files zero findings, full remote gate20261023T095319Z-synthkit-94a6d21e exit0 including race, local gzip-aware2003 scan clean. No public schema inputs, generator not applicable. Hosted CI watched separately; existing custody aggregate timeout remains separate repair.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Integral queue traffic now carries bounded sub-message publish and active-backlog service work across windows instead of rounding sparse work away; empty queues expire spare service. Rates and public telemetry surface unchanged.
<!-- SECTION:FINAL_SUMMARY:END -->
