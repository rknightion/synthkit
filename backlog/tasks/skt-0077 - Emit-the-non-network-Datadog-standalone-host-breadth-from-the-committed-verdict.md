---
id: SKT-0077
title: >-
  Emit the non-network Datadog standalone-host breadth from the committed
  verdict
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-11 19:04'
updated_date: '2026-09-11 20:23'
labels: []
dependencies: []
ordinal: 173000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The committed host verdict records 80 non-network fixture-mechanics candidates absent from host emission.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 All 80 non-network candidate families emit the exact instrument, unit, temporality, resource, datapoint and scope envelope in the committed host classification verdict.
- [x] #2 Each value group names its admissible existing CPU, memory or nodeexp physics basis in a comment; no envelope field is invented.
- [x] #3 The host classification verdict and existing CPU and memory helper files remain byte-unchanged.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Execute the frozen campaign lane contract; root integrates and measures before any claim adjustment, then validates the exact tree and reconciles acceptance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root integration: storage 296d83a91c267c9004ffa79e4cd41825a0b0d4e4 CI 34639635537 success; swap c6e9f9ff9926f7171b76952395dd5aacb2909f71 CI 34641719112 success; profile c500ec61618b13d83218d9b1a4292225e46aeac1 CI34642964976 success. Ordered gen-check, SPDX 736 and full just check passed. just gen regenerated the profile field and earlier storage doc index. Dump comparison proves 36 added host pairs; sample-time Kubernetes termination and topology-conflict changes are separately identified. No cloud, capture, cluster, lab run, second nightly dispatch, release retry or PR mutation occurred.
Implemented 36/80:29 storage and7 disabled-swap gauge envelopes, all checked against the immutable host verdict. The verdict and fixture_cpu.go/fixture_memory_load.go are byte-unchanged. CPU26 cannot receive selected capacity through the frozen host Build/runner seam; memory13 requires selected capacity and eight kernel partitions absent from the permitted mechanics. Storage5 remain missing:system.io.rrqm_s,system.io.wrqm_s,system.fs.file_handles.allocated_unused,system.fs.file_handles.in_use,system.fs.file_handles.used. No absent-source zero was invented for them. Storage uses explicit existing representative100GiB/device defaults, not selected hardware; cumulative time is elapsed-start at a stable initial rate, not changing-rate integration. The blanket swap park was WRONG; goal5.5 permits independently admissible zero output, so seven literal zero helper calls were added after that correction. This supplemental wiring has no branching/state/arithmetic and used the CodeRabbit wiring exception; the storage/profile review completed.
Resume: commission a host hardware declaration transported through the existing Build/runner boundary, grounded kernel memory accounting, and merged-operation/file-nr-unused mechanics. Keep the80-family acceptance set; do not relabel36 as complete.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Partial 36/80 landed and validated; AC1 remains unchecked. Parked on the named pre-wave capacity seam and missing value sources. No broadening of acceptance.
<!-- SECTION:FINAL_SUMMARY:END -->
