---
id: SKT-0040
title: Make k8s corpus histogram bucket evidence consistent
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 23:23'
updated_date: '2026-08-30 14:17'
labels:
  - needs-triage
dependencies: []
references:
  - reality-corpus/k8s/k3d-lab.json
  - reality-corpus/k8s-addons/k3d-lab.json
priority: high
type: bug
ordinal: 131000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The k8s corpus producer records classic-histogram identity while eliding every le value, but the k8s-addons corpus retains real le values for equivalent families. Resolve the producer policy from observed egress so bucket evidence is represented consistently and downstream emitter work never chooses bounds.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The k8s corpus producer records observed le values and bucket bounds for classic histograms, or a documented evidence rule explains and consistently represents why a value cannot be retained
- [x] #2 Instrument type and classic-histogram classification come from observed series structure, never a metric-name suffix
- [x] #3 A regression fixture covers a real finite bucket and +Inf without exposing deployment identity
- [x] #4 The refreshed corpus and comparator report make retained versus absent bucket evidence explicit
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
2026-08-31 Lane F phase 1: establish observed-series histogram classification and explicit retained/absent le evidence with regression fixtures; request the root-scheduled exclusive lab capture before corpus refresh.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-31 exclusive alloy-default capture completed in 441s with a fixed 300s window: 107 metric families and 22,219 requests. storage_operation_duration_seconds had real mount/unmount activity but only the literal _count family; no _bucket, _sum, le, or histogram object reached egress. This satisfies the evidence dependency without choosing bounds.

Campaign label 2026-08-31; the exclusive capture actually executed on 2026-08-30. A fixed 300-second window produced 22,219 RW1 series. Observed-series structure now owns histogram classification, fixtures retain finite and +Inf evidence without identity, and absent bucket evidence remains explicit.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-08-31: Corrected histogram evidence handling to use observed series structure and refreshed the k3d corpus from a five-minute capture. The storage family had real activity but no bucket, sum, histogram block, or le evidence; no bounds were chosen.
<!-- SECTION:FINAL_SUMMARY:END -->
