---
id: SKT-0131
title: Wire named Fleet state into the Kubernetes HA chart
status: Done
assignee:
  - '@codex'
created_date: '2026-10-13 10:31'
updated_date: '2026-10-13 13:55'
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
- [x] #1 HA plus Fleet renders an explicit externally precreated Fleet state ConfigMap mapping and only required named get/update/patch access; no create/list/watch or Secret access and no Helm ownership/reapplication of mutable Fleet data.
- [x] #2 Fail-first render and real local runtime-boundary proof show Fleet startup and handoff consume the rendered mapping while preserving existing state, with negative cases for missing/duplicate names and unchanged HA-off defaults.
- [x] #3 Document the released chart/runtime contract; exact required native/Helm/schema/security review and CI pass, then publish a release with verifiable image/chart provenance. No live Fleet tenant is needed for task acceptance.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop21: isolated named Fleet chart mapping/RBAC with fail-first rendered-runtime proof; preserve mutable state and HA-off defaults; root owns review/gate coordination, landing and automated release provenance.

Continue retained Fleet chart candidate: security correction delta review, CodeRabbit, reconcile main, exact gate and CI, then automated chart/image release provenance. Preserve cumulative loop21 counts.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop21: implementation attempts 1, review-repair rounds 1; no ceiling exhausted. Uncommitted tree568e2aaf46d8ab692af8fe44cca068ef63230750 retained at /Users/rob/repos/skt-loop21-fleet-chart. Fail-first named Fleet render/runtime, state preservation/RBAC and explicit env-override repair observed. Exact full native GOMAXPROCS2 just check on mbp14 passed after Python/Compose prerequisite repair; full-history Gitleaks scanned841commits with859reachable, no leaks. Independent original security review major repaired; delta-security and CodeRabbit not dispatched. Root procedural execution failure left acceptance incomplete, not an evidenced tool outage. Resume same root with actual direct delta-review dispatch, CodeRabbit and exact source landing/CI/release/image provenance. No AC checked.

Named Fleet state source landed at87a2b36a212d1036ecc8f1e461b5396909025138. All14 reviewed source blobs match integrated native-gated treec69a2dbf; source/real rendered-runtime proof, independent security delta and complete CodeRabbit14/14 zero findings passed. Full task acceptance remains pending exact commit CI, automated chart/image provenance and composed gate. No live Fleet tenant or engine production changes.

Accepted named Fleet chart at87a2b36a212d1036ecc8f1e461b5396909025138. Required exact composed native gate passed; CI38403596499 all11 jobs success. Independent correction review passed and CodeRabbit14/14 zero findings. Automated release v1.6.0-rc.181/publication38406485663 verified signature/provenance/binary revision and TestPublishedCompose on immutable image index sha256:67a0b6e75d56ffc77f3589850507a722ba720af442d058e75c1127bd98fd3c8d. Chart is bundled in that public Git release source, not a separately published OCI chart. Released chart render confirms mapping/named RBAC with explicit image digest. Offline just dump passes36 blueprints/3812 inventory rows; producer/blueprint/signals changes empty. just gen conditional not applicable; gen-check passed in full gate. loop22 cumulative implementation1/4 review-repair1/3; source/runtime/chart outcome proven, no live Fleet tenant or HA outcome claimed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Named external Fleet state mapping and scoped RBAC preserve operator-owned data and HA-off behavior. Fail-first rendered/runtime and override proofs, independent security, complete CodeRabbit, exact native composed gate/CI and automated released image/source provenance passed at87a2b36a212d1036ecc8f1e461b5396909025138.
<!-- SECTION:FINAL_SUMMARY:END -->
