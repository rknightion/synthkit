---
id: SKT-0174
title: Bring custody forecast race tests below the unchanged hosted package timeout
status: In Progress
assignee:
  - '@loop53'
created_date: '2026-10-23T09:29:00Z'
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
<!-- SECTION:PLAN:END -->
