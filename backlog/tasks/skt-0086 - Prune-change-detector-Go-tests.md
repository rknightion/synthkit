---
id: SKT-0086
title: Prune change-detector Go tests
status: Done
assignee: []
created_date: '2026-09-28 16:32'
updated_date: '2026-09-28 17:59'
labels:
  - testing
dependencies: []
priority: medium
ordinal: 182000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Loop45 read-only sweep of Go tests identified candidates needing contract review before removal: dashboard/metrics_test.go:104 TestMetricsDashboardDefaultUnchanged pins default PromQL/unit spellings; confirm these are not an intentional public dashboard contract. internal/construct/rds/rds_test.go:369 TestRDSOutputUnchangedByInstanceClass pins unchanged series under a currently unused instance-class field; confirm this no-op is still required. Keep genuine wire, security and incident regressions. This task is not admitted for implementation in loop45.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each listed Go candidate is assessed against its actual public or internal contract and retained or pruned with an evidence-backed reason.
- [x] #2 Any removed or consolidated test leaves meaningful contract coverage intact, demonstrated by relevant targeted tests or mutation evidence.
- [x] #3 The repository check gate passes for the completed change.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Assess both tests against their stated contracts; retain ambiguous or sole default guards, prove any deletion with mutation, run focused tests and the appropriate check gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop46: admitted

loop46: attempt SKT-0086-impl-1; no source change. Both tests retained on contract evidence in codex/scratch/loop46/S1/evidence.md. AC2 vacuous (nothing removed). Focused go test ./dashboard/ ./internal/construct/rds/ -count=1 exit 0 at 5ea650f138d37ab0baa789703e5cfdaf0dda636e; packet no-change exception waives full just check. DoD #1 not checked because full just check was not run; DoD #2 just gen and #3 just dump N/A as no config or emission changed. CodeRabbit and source landing CI N/A. No infrastructure retries. Admission commit 0cdd316a584973c8c1959dde9fd0c41abb1b75f0; tracker push SHA/run pending.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Assessed and kept both change-detector tests: the dashboard test guards user-visible defaults not pinned at the rendered boundary elsewhere, and SK-4(d) requires InstanceClass to be inert in RDS v1. No tests removed, so mutation proof not applicable. Focused tests passed on base 5ea650f; no source change, and full just check waived by the S1 packet.
<!-- SECTION:FINAL_SUMMARY:END -->
