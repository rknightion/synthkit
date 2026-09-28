---
id: SKT-0086
title: Prune change-detector Go tests
status: To Do
assignee: []
created_date: '2026-09-28 16:32'
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
- [ ] #1 Each listed Go candidate is assessed against its actual public or internal contract and retained or pruned with an evidence-backed reason.
- [ ] #2 Any removed or consolidated test leaves meaningful contract coverage intact, demonstrated by relevant targeted tests or mutation evidence.
- [ ] #3 The repository check gate passes for the completed change.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
