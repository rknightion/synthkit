---
id: SKT-0070
title: >-
  Close standalone Docker machine identity gaps from the independent cAdvisor
  capture
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-08 21:15'
updated_date: '2026-09-09 18:42'
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
- [x] #2 Retain failing-before and passing-after proof with negative controls for container siblings and non-Docker paths
- [ ] #3 Inventory and fidelity pass without rewriting evidence, weakening producer comparison, or adding exemptions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-17: L3 sources standalone cAdvisor machine descriptor, proves the observed machine-only identity gap failing first, implements narrowly, and proves sibling/non-Docker rejection. Root applies signal proposals and runs integrated inventory/fidelity/gates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Standalone cAdvisor descriptor and focused failing-before/passing-after controls support machine-only boot_id and machine_id. Integrated just check fails: legacy unpaired k3s machine_memory_bytes capture compares the global synth label union and rejects Docker machine_id. Source patch archived under codex/scratch/wave-2026-09-17/l3/parked.patch and parked-source; owned source restored. No comparator weakening, exemption or capture rewrite. CodeRabbit completed zero findings on the archived implementation.

Acceptance correction: AC1 was checked when the source model was archived; that was too broad because the model is not in current source. AC1 is now unchecked. AC2 records retained failing-before/passing-after and mutation evidence only. Reapply the archived patch only after the legacy unpaired-producer comparison boundary is authorized and resolved.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
AC3 remains open. Resume by authorizing a producer-scoped treatment of the legacy unpaired Kubernetes capture, then reapply the archived narrow Docker patch and pass integrated fidelity. Focused proof does not constitute integrated acceptance.

Current acceptance is AC2 only. AC1 model integration and AC3 fidelity remain open; no Docker identity implementation landed.
<!-- SECTION:FINAL_SUMMARY:END -->
