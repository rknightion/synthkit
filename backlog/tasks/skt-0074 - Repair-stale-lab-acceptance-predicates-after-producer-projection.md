---
id: SKT-0074
title: Repair stale lab acceptance predicates after producer projection
status: Done
assignee:
  - '@codex'
created_date: '2026-09-11 16:34'
updated_date: '2026-09-11 20:23'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 170000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The scheduled lab matrix fails the collector scrape-job and operator ServiceMonitor checks after receiver job consumption. Repair only the two commissioned predicates, preserving captured evidence and all other acceptance checks.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Both predicates verify captured producer identity with isolated positive and negative controls
- [x] #2 Static lab validation and source review pass without changes outside the two owned directories
- [x] #3 A subsequent scheduled lab matrix proves both permutations captured successfully
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Apply the frozen wave-21 predicate contracts through L1; inspect evidence and batched CodeRabbit before commit; root checks the later nightly separately.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root independently downloaded nightly artifact 10191579002 from run 34578122516. Candidate SHA c7348daed140d5dadda7046ec12d7efb4938ed951b5bc8973d7c054aa2511b15 matches L1: count:up0 carries promrw/lab-catalog, service and external labels, with no job key. Both repaired predicates pass every check against the retained raw inventories. This replay proves the representation repair, not a later live nightly. Positive and three isolated negative controls per predicate are retained beside the source. Batched review pending.

CodeRabbit completed review of both predicates, all fixtures and the runner test file: one minor finding only. Rejected its request to regenerate the failing-before operator artifact from the current predicate because the frozen acceptance contract requires retaining the old failure. No code change after review.

R5 source review, isolated controls, original-nightly inventory replay, static lab-check and integrated full checks pass. Exact-SHA CI34624586338,34626745006,34628037101 all success. Conditional just gen not applicable: no schema/config/skill change; gen-check passed. Dump comparison unchanged except one sampled span. Latest scheduled nightly remains34578122516 failure atcd3ea10ff5d618312459fc54a21d35ee47468640; no later run exists at reconciliation.

The wave-22 run contract section 2.1 explicitly accepts the already-dispatched manual run for AC3. Run 34632048458 is workflow_dispatch at f2fe48a9654f1300b795fe0e918c8cd0191c8251 and completed success. Re-read before closeout: all five rows captured; otel-collector-prom captured/complete/proven,153 metric families,2 logs,20689 metric points; prom-operator-rw captured/complete/proven,843 metric families,566347 metric points. This supplies the subsequent live outcome missing from the old notes. No second run was dispatched.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Predicate source repair complete at4aba3abcb80d6362e5b5ac9a9de8ea853f510faa, but end-to-end purpose NOT PROVEN. AC3 remains open. Resume by inspecting the next scheduled signal-fidelity-k3d run containing this commit, confirming both otel-collector-prom and prom-operator-rw captured with all acceptance checks. Local replay and lab-check do not substitute. No additional capture authorized by this task closeout.

AC3 is now proven under the explicit manual-run exception:34632048458 success, both required permutations captured. Done3/3; the previous open boundary is superseded.
<!-- SECTION:FINAL_SUMMARY:END -->
