---
id: SKT-0127
title: Synchronize the SM lost-response fixture observations
status: Parked
assignee: []
created_date: '2026-10-11 20:14'
updated_date: '2026-10-11 20:15'
labels: []
dependencies: []
references:
  - cmd/sm-provision/kubernetes_test.go
priority: high
type: bug
ordinal: 249000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The exact composed gate of SM provision/activation candidate d850591d71efcdcbca80df2597bd89625d36d08e found two race-detector reports in TestKubernetesProvisionLostSMResponseRetainsPending: the test reads fakeSM.posts at cmd/sm-provision/kubernetes_test.go:429 while fakeSM.ServeHTTP updates it at main_test.go:110. The child process returning is not a Go happens-before edge for the parent HTTP handler. Earlier local race was green but does not invalidate this observed defect. The code was reverted to c4c61fb; exact retained candidate treed0f02189e64c743b78e2241db0bc09af3851c7d2/patch81683d810e4ae34414a1500b40554e775e965b871132dee1fb4d7ba6d10744e2 remains available. This is follow-up repair for SKT-0099 (diskless SM provisioning); its four implementation cycles are exhausted. No new task ID grants fresh attempts: owner must raise that lineage ceiling before implementation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded fail-first race run reproduces the real fixture observation conflict, then synchronized or positively joined real HTTP observations make it race-clean without sleeps or weakened assertions.
- [ ] #2 Lost SM response still records one authoritative mutation, retains pending evidence, rejects activation and blocks the next process without replay.
- [ ] #3 The exact repaired candidate passes full local/composed gate, guarded delta review and exact-SHA CI before engine task acceptance.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop15: zero new implementation attempts; SKT-0099 (diskless SM provisioning) lineage four cycles already spent. Root restored main by exact inverse revert, not a fixture repair. Park defect until owner raises that lineage ceiling; a new tracker ID never supplies additional cycle authority. No test/assertion/gate weakened or rerun unchanged.
<!-- SECTION:NOTES:END -->
