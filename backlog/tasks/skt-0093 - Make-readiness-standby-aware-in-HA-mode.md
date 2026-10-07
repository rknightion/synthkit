---
id: SKT-0093
title: Make readiness standby-aware in HA mode
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
labels:
  - feature
  - ha
dependencies:
  - SKT-0092
priority: high
ordinal: 215000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
In HA_MODE=lease a standby never pushes, so today's delivery-aware readiness (internal/control/readiness.go) would keep it NotReady forever and a surge rollout (maxUnavailable 0) would never complete. In HA mode: a standby is Ready when config is loaded, the runner is built and credentials preflight passes; it does not require writable persisted state. The delivery-aware check keeps applying to the leader. Never widen the probes themselves.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Standby reports Ready after config load, runner build and credential preflight
- [ ] #2 Leader readiness semantics unchanged; a leader whose lane never pushed stays NotReady
- [ ] #3 Non-HA mode readiness unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
