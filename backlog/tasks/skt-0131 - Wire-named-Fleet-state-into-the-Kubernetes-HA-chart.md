---
id: SKT-0131
title: Wire named Fleet state into the Kubernetes HA chart
status: To Do
assignee: []
created_date: '2026-10-13 10:31'
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
