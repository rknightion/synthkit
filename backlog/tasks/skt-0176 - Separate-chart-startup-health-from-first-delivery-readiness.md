---
id: SKT-0176
title: Separate chart startup health from first-delivery readiness
status: In Progress
assignee:
  - '@loop54'
created_date: '2026-10-23T21:12:00Z'
updated_date: '2026-10-23T21:12:00Z'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 327000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The pinned runtime uses delivery-aware -healthcheck for chart startup. With an explicitly selected blueprint and DRY_RUN=true, LiveReady and Ready remain false, so a valid nonemitting bootstrap cannot pass startup. Sparse live request-driven lanes also have no guaranteed first-event deadline. Preserve existing delivery readiness and healthcheck semantics; add a distinct backward-compatible boot/startup proof and opt-in chart wiring rather than raising traffic, loosening existing deadlines or declaring live delivery ready.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Real CLI/control-boundary red reproduction demonstrates selected dry-run startup failure before the change; distinct startup proof passes after initialization while existing healthcheck/readiness still refuses unacknowledged live delivery
- [ ] #2 Existing readiness, live_ready, HA fencing, request/observation bounds and sparse-rate semantics are unchanged; startup success makes no delivery or source-admission claim
- [ ] #3 Chart wiring is backward compatible with existing pinned images and uses the new startup command only with an explicitly compatible image; canonical operator guidance and focused public-boundary proof agree
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Runtime-first bounded startup seam: real selected-dry-run CLI reproduction, distinct boot/startup proof preserving all existing readiness/healthcheck/HA semantics. Return frozen compatible CLI/control contract and separate optional chart-wiring hunk; integration owner applies only with compatible-image behavior and independent guarded review. Do not change rates, old deadlines or default old-image probe command.
<!-- SECTION:PLAN:END -->
