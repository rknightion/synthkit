---
id: SKT-0070
title: >-
  Close standalone Docker machine identity gaps from the independent cAdvisor
  capture
status: To Do
assignee: []
created_date: '2026-09-08 21:15'
labels: []
dependencies: []
priority: medium
ordinal: 166000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The standalone Docker cAdvisor capture promoted in reality-corpus/host/docker-standalone-cadvisor.json directly observes boot_id and machine_id on machine_memory_bytes. The existing host Docker emitter lacks both keys. This is an independently proven single-producer envelope gap, distinct from the Kubernetes P3 repair. Raw container-label annotations vary with deployed images and must not be made a global catalogue map. Preserve the capture and source the narrow machine identity model before implementation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Source the standalone cAdvisor machine descriptor and model boot_id and machine_id only where observed
- [ ] #2 Retain failing-before and passing-after proof with negative controls for container siblings and non-Docker paths
- [ ] #3 Inventory and fidelity pass without rewriting evidence, weakening producer comparison, or adding exemptions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
