---
id: SKT-0041
title: Convert storage operation duration only from captured histogram bounds
status: To Do
assignee: []
created_date: '2026-08-29 23:23'
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
- [ ] #1 A refreshed observed capture proves whether bucket series reach egress for storage_operation_duration_seconds
- [ ] #2 If bucket series are observed, the emitter uses exactly the captured finite and +Inf structure and carries the sourced node and activity labels
- [ ] #3 If bucket series are not observed, the counter shape is retained and the evidence is recorded without a histogram conversion
- [ ] #4 Signals documentation records the chosen shape, provenance, and capture date
- [ ] #5 The comparator pairs the base family correctly and reports no false name or instrument mismatch
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
