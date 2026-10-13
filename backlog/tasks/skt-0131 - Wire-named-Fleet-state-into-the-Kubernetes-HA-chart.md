---
id: SKT-0131
title: Wire named Fleet state into the Kubernetes HA chart
status: Parked
assignee:
  - '@codex'
created_date: '2026-10-13 10:31'
updated_date: '2026-10-13 11:43'
labels:
  - ha
  - chart
dependencies: []
priority: high
type: feature
ordinal: 254000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The released runtime requires STATE_FLEET_CONFIGMAP when Fleet registration uses the Kubernetes state backend. Chart sources at v1.6.0-rc.179 and v1.6.0-rc.180 expose Fleet credentials but emit neither this environment mapping nor named ConfigMap RBAC. The supported unmodified chart therefore cannot exercise Fleet continuity under HA. Own charts/synthkit/** and relevant chart/runtime-boundary tests and public Kubernetes/Fleet docs only; runtime behavior and shared modules/task surface remain unchanged unless an evidenced prerequisite is separately admitted. Preserve operator-owned mutable state and all existing non-HA behavior.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 HA plus Fleet renders an explicit externally precreated Fleet state ConfigMap mapping and only required named get/update/patch access; no create/list/watch or Secret access and no Helm ownership/reapplication of mutable Fleet data.
- [ ] #2 Fail-first render and real local runtime-boundary proof show Fleet startup and handoff consume the rendered mapping while preserving existing state, with negative cases for missing/duplicate names and unchanged HA-off defaults.
- [ ] #3 Document the released chart/runtime contract; exact required native/Helm/schema/security review and CI pass, then publish a release with verifiable image/chart provenance. No live Fleet tenant is needed for task acceptance.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop21: isolated named Fleet chart mapping/RBAC with fail-first rendered-runtime proof; preserve mutable state and HA-off defaults; root owns review/gate coordination, landing and automated release provenance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop21: implementation attempts 1, review-repair rounds 1; no ceiling exhausted. Uncommitted tree568e2aaf46d8ab692af8fe44cca068ef63230750 retained at /Users/rob/repos/skt-loop21-fleet-chart. Fail-first named Fleet render/runtime, state preservation/RBAC and explicit env-override repair observed. Exact full native GOMAXPROCS2 just check on mbp14 passed after Python/Compose prerequisite repair; full-history Gitleaks scanned841commits with859reachable, no leaks. Independent original security review major repaired; delta-security and CodeRabbit not dispatched. Root procedural execution failure left acceptance incomplete, not an evidenced tool outage. Resume same root with actual direct delta-review dispatch, CodeRabbit and exact source landing/CI/release/image provenance. No AC checked.
<!-- SECTION:NOTES:END -->
