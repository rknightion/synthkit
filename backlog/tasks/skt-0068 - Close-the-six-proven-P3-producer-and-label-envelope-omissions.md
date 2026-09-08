---
id: SKT-0068
title: Close the six proven P3 producer and label envelope omissions
status: To Do
assignee: []
created_date: '2026-09-08 17:16'
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
- [ ] #1 P3 process metrics carry both observed kubelet and node-exporter producers with their own label maps
- [ ] #2 P3 kube_node_info and machine_memory_bytes carry the independently sourced pod_cidr and boot_id keys
- [ ] #3 Only the two observed filesystem status families carry device_error with an explicitly sourced coherent status model
- [ ] #4 Failing-before and passing-after evidence proves the changes and preservation of non-P3 output, sibling filesystem/container labels and scope-free target_info
- [ ] #5 Catalogue, exact inventory comparison and final gates agree without weaker producer matching or new exemptions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
