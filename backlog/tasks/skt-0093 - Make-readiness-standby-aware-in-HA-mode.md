---
id: SKT-0093
title: Make readiness standby-aware in HA mode
status: Parked
assignee: []
created_date: '2026-10-07 20:31'
updated_date: '2026-10-08 15:10'
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 0; dependency parked on SKT-0092 (lease HA with crash-only fencing), whose third review-repair round remains rejected. Resume after accepted HA lands and composed gate green; no readiness source changes.

loop2: implementation attempts 0; dependency-held by SKT-0092 (lease HA with crash-only fencing), whose final authorized implementation attempt failed compilation and was not landed. No downstream source changes. Resume in frozen order after prerequisite accepted land and green composed gate; owner must first authorize further HA repair.

loop3: implementation attempts0; dependency-held by SKT-0092 (lease HA with crash-only fencing). Attempt5 local gate and review completed, but security rejects retained-runtime-red proof provenance; final Astra rescue confirms incompatible premise, no HA land. Resume frozen order only after corrected proof contract or valid missing evidence, accepted HA land and green composed gate. No downstream source changes.

loop4: implementation attempts0; HA source landed ddee479 but composed gate remains red, owned by SKT-0109 (resolve late Faro POST test-oracle proof defect). No readiness/console/backend/chart code begun. Resume frozen order only after owner-authorized HA proof repair and green composed gate; readiness+console first, then state backend, then chart.
<!-- SECTION:NOTES:END -->
