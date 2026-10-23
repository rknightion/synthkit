---
id: SKT-0174
title: Bring custody forecast race tests below the unchanged hosted package timeout
status: In Progress
assignee:
  - '@loop53'
created_date: '2026-10-23T09:29:00Z'
updated_date: '2026-10-23T10:53:00Z'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 325000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Hosted CI on a1caa1b failed the Agent Observability race sweep at the unchanged 600-second package bound after canonical teardown forecast coverage landed. The alarm caught TestTeardownForecastDeactivateChecksItself running for 44 seconds, but does not identify the dominant cost. Remote arm64 composed gate passed; hosted x64 aggregate cost must be diagnosed and fixed without hiding predicates or raising timeout.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Measured diagnosis identifies the added dominant work; no timeout increase or weakened correctness coverage
- [ ] #2 Exact original maximum-encoded IDs, 700-identity collision, combined generation, canonical checksum, legacy 31600 refusal and TLS pre-request predicates remain covered
- [ ] #3 Focused equivalent proof and full unchanged hosted race gate pass on the repaired revision, with exact tree and CI identities recorded
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Measure or profile dominant newly added forecast-test work; choose a bounded computationally equivalent fix preserving all safety predicates, independently review it, run focused old/new checks then composed full gate and hosted CI.

First canonical-byte optimization preserved codec semantics and improved measured cost but hosted full race still hit 600s. Named new approach: use existing exact-once race split for only three profiled dominant forecast tests, retaining complete cases, race instrumentation and unchanged per-invocation deadline. Validate selected test union/disjointness and documented admission, independent guarded review, then final full check/race and hosted CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
681d26e hosted run39938949155 still failed at race package600.043s in TestTeardownForecastBoundDominatesBudget; optimization landed correctly but does not fulfill acceptance. No timeout/matrix reduction. Three profiled dominant tests will use the existing isolated race invocation with all names exactly once; codec/forecast and test bodies unchanged. Original failure a1caa1b and second failure681d26e retained as negative evidence.
<!-- SECTION:NOTES:END -->
