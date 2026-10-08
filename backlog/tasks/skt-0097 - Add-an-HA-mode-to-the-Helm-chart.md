---
id: SKT-0097
title: Add an HA mode to the Helm chart
status: Parked
assignee: []
created_date: '2026-10-07 20:31'
updated_date: '2026-10-08 10:05'
labels:
  - feature
  - ha
  - chart
dependencies:
  - SKT-0092
  - SKT-0093
  - SKT-0094
ordinal: 219000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Allow replicas: 2 only when lease HA and the kubernetes state backend are both enabled (render fails otherwise; the single-replica Recreate default and its rationale stay for non-HA). HA mode: RollingUpdate maxSurge 1 / maxUnavailable 0, no PVC, PDB minAvailable 1 with unhealthyPodEvictionPolicy AlwaysAllow (a NotReady leader must not wedge drains), topology spread across nodes, pre-created Lease and state ConfigMaps, Role scoped by resourceNames with get/update/patch only.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Render tests cover HA on, HA off, and the invalid combinations
- [ ] #2 PDB uses AlwaysAllow; Role has no create/list/watch
- [ ] #3 docs/kubernetes.md documents HA mode and its handoff semantics
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 0; dependency parked on SKT-0094 (named ConfigMap state backend) plus SKT-0092 (lease HA) and SKT-0093 (standby readiness). Resume after accepted prerequisite lands/gates. No chart or cluster changes.

loop2: implementation attempts 0; dependency-held by SKT-0092 (lease HA with crash-only fencing), whose final authorized implementation attempt failed compilation and was not landed. No downstream source changes. Resume in frozen order after prerequisite accepted land and green composed gate; owner must first authorize further HA repair.
<!-- SECTION:NOTES:END -->
