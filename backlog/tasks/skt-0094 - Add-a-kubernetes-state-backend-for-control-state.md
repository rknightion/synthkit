---
id: SKT-0094
title: Add a kubernetes state backend for control state
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-10 18:29'
labels:
  - feature
  - ha
dependencies:
  - SKT-0092
priority: high
ordinal: 216000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Stateless pods need state that follows the lease, since an RWO volume cannot. Add STATE_BACKEND=kubernetes (file stays the default) behind the existing persist seam, using pre-created ConfigMaps named by config.

Scope: control state, boot manifest, git fetch status, fetched git blueprint blobs (one object per source, with a per-source size cap under 1 MiB) so a git host outage at boot is not fatal.

Rules (from review of internal/control/control.go, which reads once at NewStore and overwrites the whole document on every write):
- Leader-only writes; the standby opens the backend read-only.
- Writes use resourceVersion compare-and-swap; a conflict reloads, reapplies and retries.
- On acquiring the lease, re-read and ApplyControl the snapshot before the first tick.
- Never needs create/list/watch: objects are pre-created, so RBAC can be get/update/patch by resourceNames.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 STATE_BACKEND=kubernetes persists control state, boot manifest, fetch status and capped git blobs to named ConfigMaps
- [ ] #2 Concurrent-write test: a stale writer gets a conflict and converges without losing the other write
- [ ] #3 New leader re-reads and applies state before its first tick
- [ ] #4 Needs only get/update/patch on the named objects; file backend unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Frozen HA seam section5; file default preserved, named precreated ConfigMap backend for control/manifest/status/capped blobs; CAS conflict reapply; acquisition re-read beforetick; unknown outcome503/notready; fakeclient publicboundary/failfirst proof then fullgate/security/CodeRabbit preland. No cluster interaction.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 0; frozen order waits for SKT-0092 (lease HA), SKT-0093 (standby readiness), and SKT-0098 (runtime console base path). All unlanded/parked. Frozen reviewed backend API design retained; no backend/ConfigMap source implementation. Resume after accepted prerequisite lands/gates.

loop2: implementation attempts 0; dependency-held by SKT-0092 (lease HA with crash-only fencing), whose final authorized implementation attempt failed compilation and was not landed. No downstream source changes. Resume in frozen order after prerequisite accepted land and green composed gate; owner must first authorize further HA repair.

loop3: implementation attempts0; dependency-held by SKT-0092 (lease HA with crash-only fencing). Attempt5 local gate and review completed, but security rejects retained-runtime-red proof provenance; final Astra rescue confirms incompatible premise, no HA land. Resume frozen order only after corrected proof contract or valid missing evidence, accepted HA land and green composed gate. No downstream source changes.

loop4: implementation attempts0; HA source landed ddee479 but composed gate remains red, owned by SKT-0109 (resolve late Faro POST test-oracle proof defect). No readiness/console/backend/chart code begun. Resume frozen order only after owner-authorized HA proof repair and green composed gate; readiness+console first, then state backend, then chart.

loop5 attempts0; dependency-held: SKT-0109 (late Faro POST oracle) candidate unlanded because full gate exposes independent coordinator-loss assertion/admission discrepancy. No downstream source changes. Resume only after authorized repair and prerequisite accepted land/composed green.

loop7 implementation attempts0; dependency park: H93/B98 must land before S94; R13 park itself is not a blocker per goal. Resume frozen order after prerequisite accepted land/green gate; no candidate/source changes this run.

loop8 attempts0, priorcountsunchanged. Parkneedsdependency: B98mustland first; U115publicationrepairlanded but console/readinesschainblockedR12. ResumefrozenbackendseamafterB98.

loop10: new implementation attempts0, historical implementation count0 unchanged. Dependency park on SKT-0098 (runtime console base path) acceptance and land; no gate or acceptance claimed. Resume frozen serial order after prerequisite green/land, using retained exact candidate where provided; no reset or reconstruction from prose.

This run: Dependency SKT-0098 (runtime console base path) unaccepted/unlanded; performance prerequisite reverted after CI489.278s. Retained input unchanged; zero new implementation attempts. Resume frozen order after prerequisite accepted land and gate.
<!-- SECTION:NOTES:END -->
