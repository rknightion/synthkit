---
id: SKT-0068
title: Close the six proven P3 producer and label envelope omissions
status: Done
assignee:
  - '@codex'
created_date: '2026-09-08 17:16'
updated_date: '2026-09-08 22:11'
labels: []
dependencies: []
priority: medium
ordinal: 164000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The independent P3 envelope review established six genuine emitter divergences, not family-union artifacts: kubelet membership is absent on process_cpu_seconds_total and process_resident_memory_bytes; kube_node_info lacks pod_cidr; machine_memory_bytes lacks boot_id; node_filesystem_device_error and node_filesystem_readonly lack device_error. The four label gaps each have a single captured producer. Family unions do not establish a kubelet namespace. Original values were deliberately elided; a later retained capture records permission denied for filesystem errors, without establishing a closed enum. Evidence: codex/scratch/wave-2026-09-15/l3/brief.md and evidence-key-output.txt. This task records the proven defects without broadening the completed investigation lane into implementation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 P3 process metrics carry both observed kubelet and node-exporter producers with their own label maps
- [x] #2 P3 kube_node_info and machine_memory_bytes carry the independently sourced pod_cidr and boot_id keys
- [x] #3 Only the two observed filesystem status families carry device_error with an explicitly sourced coherent status model
- [x] #4 Failing-before and passing-after evidence proves the changes and preservation of non-P3 output, sibling filesystem/container labels and scope-free target_info
- [x] #5 Catalogue, exact inventory comparison and final gates agree without weaker producer matching or new exemptions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-16: L2 implements the six scoped P3 omissions with failing-before, mutation-proven preservation controls and targeted tests. Root integrates sourced catalogue notes, compares inventory and runs review and final gates without weaker producer matching.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Wave closure: all six scoped omissions are implemented. Retained lane failing-before and passing-after mutation logs prove filesystem/container siblings, kubelet namespace separation, non-P3 preservation and scope-free target_info. Root inventory retains exactly 142 captured families with no missing family; source descriptors and catalogue notes agree. Full just check passed after updating the new blueprint runtime-name registry entry; no producer matching or exemption change. Evidence: codex/scratch/wave-2026-09-16/l2, p3-inventory-proof.txt, integration-precommit-check-after.txt. CodeRabbit reviewed the changed emitter/tests with no P3 finding.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
All five acceptance criteria proven: missing kubelet producer memberships and four P3-only keys repaired with sourced coherent values and mutation-proven boundaries. Full gate passed; integration SHA and CI will be recorded in the wave report.

Source integration commit bcf1687689fc9199e8f232b3cef23370b56f5aba is published on main; CI run 34279467976 succeeded at that exact SHA. The six P3 repairs and their negative controls are in that commit. Later corpus-only promotions preserve all 142 P3 families and do not change these emitters.
<!-- SECTION:FINAL_SUMMARY:END -->
