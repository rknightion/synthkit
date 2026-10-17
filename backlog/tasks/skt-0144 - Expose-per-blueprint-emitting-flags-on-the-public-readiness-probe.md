---
id: SKT-0144
title: Expose per-blueprint emitting flags on the public readiness probe
status: Done
assignee: []
created_date: '2026-10-17 15:12'
updated_date: '2026-10-17 20:02'
labels:
  - control
dependencies: []
references:
  - internal/control/http.go
  - internal/control/readiness.go
priority: low
type: feature
ordinal: 267000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Optional and lower priority. An operator that holds no control token cannot tell whether a given blueprint is emitting: GET /control/readiness is deliberately public so Docker can call it, and its probe form omits topology, errors, filesystem paths and operational reasons (internal/control/http.go:344-357, ReadinessProbe and Probe() in internal/control/readiness.go:108). It carries only aggregate blueprint counts (loaded, skipped, active, readiness.go:14-18). Everything per blueprint sits behind CONTROL_TOKEN on GET /control/status and GET /control/inventory.

Trade-off to settle in this task, not assume. Per-blueprint emitting flags make the public probe name blueprints, and blueprint names are topology and can carry operator meaning, so adding them to the default probe would weaken the sanitization on purpose. Options the implementer should weigh: an opt-in setting that is off by default and leaves the probe byte-identical when off, a flag set keyed by an opaque or hashed identifier, or leaving this to authenticated routes and closing the task. If the cost to the sanitization contract outweighs the value, close the task as not needed with the reasoning recorded. Related: the live apply and remove operation, which makes a per-blueprint flag more useful.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 With the new setting off (the default), the GET /control/readiness JSON is byte-identical to today for the same state; a golden test asserts it
- [x] #2 With the setting on, the probe lists a per-blueprint emitting boolean and nothing else per blueprint: no reasons, errors, paths, lane detail or secrets; a test asserts the exact field set
- [x] #3 The decision on how blueprints are named in the probe (plain name, opaque id, or not exposed) and its trade-off against the sanitized-probe contract is recorded in docs/control-plane.md and covered by a test for the chosen naming
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
- [x] #4 docs/control-plane.md reconciled, and just docs-check passes
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner ruling 2026-10-17: build this as the opt-in setting, off by default with the probe byte-identical when off. A downstream operator that holds no control token will enable it and read only the public probe for per-blueprint emission, so do not close it as not needed. The naming choice (plain name or opaque id) is still this task's call, recorded per AC #3.

Landed a4ce297 in loop50 (rc v1.6.0-rc.201). Naming: plain blueprint name, recorded in docs/control-plane.md. DoD #3 just dump not run: no renderer change.
<!-- SECTION:NOTES:END -->
