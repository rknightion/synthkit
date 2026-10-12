---
id: SKT-0125
title: Keep operator-owned-state chart proof compatible with current Helm
status: Parked
assignee:
  - '@loop-root'
created_date: '2026-10-11 19:11'
updated_date: '2026-10-12 11:19'
labels: []
dependencies: []
references:
  - 'https://github.com/helm/helm/blob/v3.19.0/cmd/helm/template.go'
priority: medium
type: bug
ordinal: 247000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The unchanged HA chart proof combines --dry-run=server and --validate at charts/synthkit/tests/ha_chart_test.go:395. Helm4.3.0 and3.20.0 reject the flags before any local fixture API call. Official checksum-verified Helm3.19.0 runs all four precreation/install/upgrade cases successfully, preserving actual discovery/read operations and byte-identical operator-owned state. This currently makes the local full gate sensitive to an unpinned Helm version. The temporary compatible toolchain is not a permanent fix. No chart wiring extension is included.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The supported current Helm toolchain runs the unchanged safety contract with actual local fixture discovery and precreation observations in install and upgrade cases.
- [ ] #2 Operator-owned ConfigMaps and Lease remain byte-identical and unaccessed by Helm release rendering; no assertion, fixture or API-call proof is weakened to obtain compatibility.
- [ ] #3 The documented local tool prerequisite and CI behavior agree; just check passes without a private run-local binary.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Reproduce installed Helm flag rejection, preserve actual fixture discovery/precreation safety proof under current and prior Helm, update prerequisite documentation, then local gate, review and exact composed-gated CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop16: implementation1 plus bounded review-repairround1 (documentation/source evidence), focused real discovery/precreation/operator-state proofs green under official3.19.0,3.20.0,installed4.3.0. Inherited3.20 rejection invalidated by checksum-verified binary/taggedsource; original4.3 fail-first genuine. R125deltaPASS, full laneG1250. First detachedcompositionef9aa7e failed unchangedFleet100ms initialLease fixture; causeunknown recorded SKT-0128 (classify Fleet fixture initial Lease timeout), no assertion/timing weakening or unchangedrerun. LegitimateSigilbase6751899 composed0. During root providercontinuationfailure, foreignRenovate UIlockfile advancedmain60d3847, preserved; newestunpublished exactcompositionc7782df1deb1b11210aa8d04fb64b42656925f31/baseb93a3bc47830c5e3be1cf4dfa65583f73b3f4880 cannot run requiredgate: gate-runner launcher nopush missing from activeconfig, launchrejected beforework. Parkdefect/runtime, notownerbudget; restore runtime, reconcilecurrentmain and exactcomposedgate before publication/exactCI. No chartcodepublished, allcandidate/review/proofs retained privately; routineCodeRabbit skipped as granted.

loop17: zero new cycles; unchanged reviewed three-file patch matches frozen sha256 c6c8c3cf9494df58368d4bcabf3133fca02032497a26c941d22c5eeb8e591719 on current main. Root commit blocked before execution by auto-mode publication permission boundary; no commit/push/composed gate/CI claimed. Resume after owner clears permission; do not reset reviewed lineage.
<!-- SECTION:NOTES:END -->
