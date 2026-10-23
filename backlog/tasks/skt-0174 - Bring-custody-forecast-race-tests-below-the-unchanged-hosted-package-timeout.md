---
id: SKT-0174
title: Bring custody forecast race tests below the unchanged hosted package timeout
status: Done
assignee:
  - '@loop53'
created_date: '2026-10-23T09:29:00Z'
updated_date: '2026-10-23T11:53:00Z'
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
- [x] #1 Measured diagnosis identifies the added dominant work; no timeout increase or weakened correctness coverage
- [x] #2 Exact original maximum-encoded IDs, 700-identity collision, combined generation, canonical checksum, legacy 31600 refusal and TLS pre-request predicates remain covered
- [x] #3 Focused equivalent proof and full unchanged hosted race gate pass on the repaired revision, with exact tree and CI identities recorded
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
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

Complete hosted CI39945598735 passed exact ce0253662945be5229d0457e213b577c262d3894, including e2e and ci-success. Real hosted x64 custody race invocations218.344s/246.123s under unchanged600s bound; all25 configured isolated top-level Tests passed once, including original matrix subcases. Before/after3328 Test names identical, precisely3 profiled tests moved into existing package invocation, runner/cases/flags/deadlines unchanged. Canonical-byte optimization681d26e preserved decoder semantics with current-baseline red/green. Guarded independent reviews PASS; final same-tree remote full check and standalone race passed. No schema/renderer change, generation/dump not applicable. Original two hosted timeouts retained as negative evidence, not waived.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Reduced redundant canonical custody decode work without changing accepted input/output, then partitioned only the three measured dominant forecast Tests through the existing exact-once race tier. All tests/cases, race checks, per-invocation600s limit and caller bounds remain. Exact repaired revision passed full remote check, standalone race and complete hosted x64 CI; no timeout or correctness concession.
<!-- SECTION:FINAL_SUMMARY:END -->
