---
id: SKT-0170
title: Allow a zero-baseline custom metric to rise under a scoped incident
status: Done
assignee:
  - '@loop53'
created_date: '2026-10-23T07:41:00Z'
updated_date: '2026-10-23T08:35:00Z'
labels:
  - telemetryspec
  - demo-prerequisite
dependencies: []
priority: high
ordinal: 321000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The numeric custom-telemetry shape model only multiplies its base. A gauge representing an empty error backlog must remain zero normally but rise during a scoped failure; base zero currently remains zero under every multiplier. Making the normal value positive to trigger an alert fabricates ordinary state. Provide a generic opt-in value contract for this case without changing legacy shape output or inventing catalog metric names.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An authored custom gauge can emit exactly zero normally, a declared nonzero value under its scoped incident, and zero again after recovery through the public loader and real workload Tick path
- [x] #2 Absent configuration preserves legacy emitted values; intensity, scope and zero-volume behavior are explicit and tested, with bounded dimensions and no special handling for a blueprint name
- [x] #3 A reproduction fails on the existing model, the candidate passes, and canonical DSL documentation and authoring guidance reflect the additive contract
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add an opt-in generic incident-aware numeric value contract without changing legacy multiplicative shape semantics. Prove zero-baseline onset, scoped intensity and recovery through public loader and real app emission; reconcile canonical DSL guidance and return generated-file requirements for root composition.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed 691dc9a, pushed integrated 93a7f34. Optional metric-only incident_value preserves legacy behavior, handles scoped intensity/pause/recovery and native parity. Independent guarded review and dsreview PASS; composed CodeRabbit complete 41/41 zero findings. Combined generation, real selected CLI inventory/value proof and exact remote just check passed. Existing scheduled zero-intensity issue separately tracked.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Custom gauges can opt into an explicit incident endpoint while retaining an honest zero baseline and returning immediately on recovery. No default shape semantics changed.
<!-- SECTION:FINAL_SUMMARY:END -->
