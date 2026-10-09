---
id: SKT-0093
title: Make readiness standby-aware in HA mode
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
updated_date: '2026-10-09 11:18'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop6: preserve default/leader readiness, standby readiness from configuration/build/preflight only; tests each criterion, local gate, independent review, root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 0; dependency parked on SKT-0092 (lease HA with crash-only fencing), whose third review-repair round remains rejected. Resume after accepted HA lands and composed gate green; no readiness source changes.

loop2: implementation attempts 0; dependency-held by SKT-0092 (lease HA with crash-only fencing), whose final authorized implementation attempt failed compilation and was not landed. No downstream source changes. Resume in frozen order after prerequisite accepted land and green composed gate; owner must first authorize further HA repair.

loop3: implementation attempts0; dependency-held by SKT-0092 (lease HA with crash-only fencing). Attempt5 local gate and review completed, but security rejects retained-runtime-red proof provenance; final Astra rescue confirms incompatible premise, no HA land. Resume frozen order only after corrected proof contract or valid missing evidence, accepted HA land and green composed gate. No downstream source changes.

loop4: implementation attempts0; HA source landed ddee479 but composed gate remains red, owned by SKT-0109 (resolve late Faro POST test-oracle proof defect). No readiness/console/backend/chart code begun. Resume frozen order only after owner-authorized HA proof repair and green composed gate; readiness+console first, then state backend, then chart.

loop5 attempts0; dependency-held: SKT-0109 (late Faro POST oracle) candidate unlanded because full gate exposes independent coordinator-loss assertion/admission discrepancy. No downstream source changes. Resume only after authorized repair and prerequisite accepted land/composed green.

loop6 partial, not landed: lane gate red only on the control-dash race-package timeout (unowned). Candidate retained byte-exact at /Users/rob/repos/synthkit-hosted/codex/retained-2026-10-08-loop6/H93/candidate.patch (base 14719af, sha256 7aa01d96aeb417c8c895b0a40663b943f6f748a6f87d92e3bcf4ed579f754abe); ownership extended to the readiness callback wiring in cmd/synthkit/ha_main.go plus cmd/synthkit/ha_readiness_test.go. Resume from that patch once the race budget is fixed.
<!-- SECTION:NOTES:END -->
