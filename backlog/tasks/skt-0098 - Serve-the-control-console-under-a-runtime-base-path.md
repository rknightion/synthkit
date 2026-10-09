---
id: SKT-0098
title: Serve the control console under a runtime base path
status: Parked
assignee: []
created_date: '2026-10-07 20:31'
updated_date: '2026-10-09 16:56'
labels:
  - feature
  - ui
dependencies: []
ordinal: 220000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Running the console behind a reverse proxy under a prefix is impossible today: Vite bakes base /control/ui/ (internal/control/ui/vite.config.ts), the router base is compile-time BASE_URL (src/App.tsx), every API call is an absolute /control/ path (src/api/client.ts) and the server redirect /control/ui -> /control/ui/ is absolute (internal/control/http.go). One image must serve any prefix: build with a relative base, have the server inject <base href> and an API prefix into index.html from CONTROL_BASE_PATH (or X-Forwarded-Prefix from a trusted proxy), and make redirects prefix-aware. Default behaviour unchanged.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Console works end to end under an arbitrary prefix with one build
- [ ] #2 Default (no prefix) unchanged; Infinity GET shapes unchanged
- [ ] #3 UI tests cover prefixed API URL construction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop6: runtime configured base path with one relative UI build, prefix-aware redirects/assets/API; Go serving proof and UI URL tests, unchanged default/Infinity shapes, environment alignment, gate/review/root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 0; frozen order and shared HTTP/config seam depend on SKT-0092 (lease HA with crash-only fencing) landing first. HA candidate rejected at review ceiling. Resume after accepted HA land/gate; no UI/base-path source changes.

loop2: implementation attempts 0; dependency-held by SKT-0092 (lease HA with crash-only fencing), whose final authorized implementation attempt failed compilation and was not landed. No downstream source changes. Resume in frozen order after prerequisite accepted land and green composed gate; owner must first authorize further HA repair.

loop3: implementation attempts0; dependency-held by SKT-0092 (lease HA with crash-only fencing). Attempt5 local gate and review completed, but security rejects retained-runtime-red proof provenance; final Astra rescue confirms incompatible premise, no HA land. Resume frozen order only after corrected proof contract or valid missing evidence, accepted HA land and green composed gate. No downstream source changes.

loop4: implementation attempts0; HA source landed ddee479 but composed gate remains red, owned by SKT-0109 (resolve late Faro POST test-oracle proof defect). No readiness/console/backend/chart code begun. Resume frozen order only after owner-authorized HA proof repair and green composed gate; readiness+console first, then state backend, then chart.

loop5 attempts0; dependency-held: SKT-0109 (late Faro POST oracle) candidate unlanded because full gate exposes independent coordinator-loss assertion/admission discrepancy. No downstream source changes. Resume only after authorized repair and prerequisite accepted land/composed green.

loop6 partial, not landed: adds ControlBasePath (CONTROL_BASE_PATH) and Handler.SetBasePath; main.go and ha_main.go wiring (SetBasePath before serving, mount under base+/control/) was never done. Candidate retained byte-exact at operator-held local evidence (base 14719af, sha256 3c6a123e7375be174b068601fb63f4987e00fed28ded6d48c6f35c36ce96fc9d). Gate was red on the control-dash race timeout and an HA planned-exit assertion.

loop7 implementation attempts0; dependency park: H93 must land before B98; retained patch unchanged and hash verified. Resume frozen order after prerequisite accepted land/green gate; no candidate/source changes this run.

loop8 attempts0, priorcountsunchanged. Parkneedsdependency: H93mustland first, blockedR12. RetainedB98patchverified untouched; resumeafterreadinessland.
<!-- SECTION:NOTES:END -->
