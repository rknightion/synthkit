---
id: SKT-0074
title: Repair stale lab acceptance predicates after producer projection
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-11 16:34'
updated_date: '2026-09-11 16:47'
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
- [ ] #1 Both predicates verify captured producer identity with isolated positive and negative controls
- [ ] #2 Static lab validation and source review pass without changes outside the two owned directories
- [ ] #3 A subsequent scheduled lab matrix proves both permutations captured successfully
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Apply the frozen wave-21 predicate contracts through L1; inspect evidence and batched CodeRabbit before commit; root checks the later nightly separately.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root independently downloaded nightly artifact 10191579002 from run 34578122516. Candidate SHA c7348daed140d5dadda7046ec12d7efb4938ed951b5bc8973d7c054aa2511b15 matches L1: count:up0 carries promrw/lab-catalog, service and external labels, with no job key. Both repaired predicates pass every check against the retained raw inventories. This replay proves the representation repair, not a later live nightly. Positive and three isolated negative controls per predicate are retained beside the source. Batched review pending.

CodeRabbit completed review of both predicates, all fixtures and the runner test file: one minor finding only. Rejected its request to regenerate the failing-before operator artifact from the current predicate because the frozen acceptance contract requires retaining the old failure. No code change after review.
<!-- SECTION:NOTES:END -->
