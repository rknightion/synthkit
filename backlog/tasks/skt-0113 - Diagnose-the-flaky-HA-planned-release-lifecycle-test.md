---
id: SKT-0113
title: Diagnose the flaky HA planned-release lifecycle test
status: Parked
assignee:
  - loop7
created_date: '2026-10-09 11:19'
updated_date: '2026-10-09 13:38'
labels: []
dependencies: []
type: bug
ordinal: 235000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestHAGenericLeaseFileReloadAndPlannedRelease (cmd/synthkit/ha_lifecycle_test.go) failed in CI 37860640279 at c390bd9 with the child process ending "exit status 1" after acquiring the lease, then passed at 95b92ee and bb4c4e2 with no related change. SKT-0092 (lease HA with crash-only fencing) cannot be closed while its own lifecycle test is intermittently red, and a real defect here would sit in planned-release fencing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The failure is reproduced or bounded with a stress run (-race, repeated count) and classified as a test defect or a runtime defect with evidence
- [ ] #2 A test defect is fixed without a new sleep, tolerance or retry and without weakening any assertion; the stress run then passes
- [ ] #3 A runtime defect in election, fencing or release ordering is recorded with the evidence and not changed without owner sign-off
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop7: deterministic forced interleaving plus contended race stress; test-only repair with unchanged timings/assertions or bounded evidence question.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop7: retained implementation attempts0; temporary diagnostic cycle reverted. Bounded unchanged contended race stress8/40 failures; shutdown watchdog with request-header reader, origin unproven; no deterministic reproduction/test-only fix. Park needs=owner per goal explicit stop rule. Resume after scoped deterministic transport investigation decision, runtime/timing changes still prohibited. Evidence retained privately by root; no code changed.
<!-- SECTION:NOTES:END -->
