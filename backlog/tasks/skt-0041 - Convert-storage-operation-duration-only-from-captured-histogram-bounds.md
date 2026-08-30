---
id: SKT-0041
title: Convert storage operation duration only from captured histogram bounds
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 23:23'
updated_date: '2026-08-30 14:17'
labels:
  - needs-triage
dependencies:
  - SKT-0040
references:
  - internal/construct/k8scluster/kubelet.go
  - signals/k8s.md
priority: high
type: bug
ordinal: 132000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The emitter conversion for storage_operation_duration_seconds remains deliberately blocked because the current k8s corpus elides le values. After SKT-0040 makes observed bucket evidence available, determine the real exported shape and convert the emitter only if the capture proves a classic histogram. Bucket bounds must never be selected by judgement.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A refreshed observed capture proves whether bucket series reach egress for storage_operation_duration_seconds
- [x] #2 If bucket series are observed, the emitter uses exactly the captured finite and +Inf structure and carries the sourced node and activity labels
- [x] #3 If bucket series are not observed, the counter shape is retained and the evidence is recorded without a histogram conversion
- [x] #4 Signals documentation records the chosen shape, provenance, and capture date
- [x] #5 The comparator pairs the base family correctly and reports no false name or instrument mismatch
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
2026-08-31 Deferred behind SKT-0040: after refreshed egress evidence, retain the counter if no buckets are observed or use only captured bounds if they are; return the internal/inventory base-family pairing request to root.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-31 dependency evidence from SKT-0040 selects AC#3: retain the counter. Implement the sourced node label, refresh explicit absent-bucket corpus evidence, and fix base-family pairing without histogram conversion.

2026-08-31 verification selected AC3: the five-minute capture observed only literal storage_operation_duration_seconds_count with real activity labels and no bucket evidence. The counter is retained, the node label and comparator base-family pairing are corrected, and signals/k8s.md records provenance. The conditional histogram branch did not run and no bounds were selected.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-08-31: Retained the observed counter shape, added the sourced node label, corrected base-family pairing, and documented the capture evidence. No histogram conversion or invented bounds were introduced.
<!-- SECTION:FINAL_SUMMARY:END -->
