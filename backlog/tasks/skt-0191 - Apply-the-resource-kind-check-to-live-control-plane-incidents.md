---
id: SKT-0191
title: Apply the resource-kind check to live control-plane incidents
status: To Do
assignee: []
created_date: '2026-10-25T09:10:00Z'
labels: []
dependencies:
  - SKT-0188
priority: low
ordinal: 389000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0188 rejects a resource-axis fault aimed at a wrong-kind resource at load, but the runner's ValidateRuntimeIncident (internal/runner/runner.go ~:1455-1522) has no kind check, so a live control-plane incident such as dependency_slow on a pool, or pool_exhausted on resource:* with no pools, is accepted and moves nothing. Call the shared kind check (causal.ModeKind) from live validation; live resource:* should select only the mode's kind as load does.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A live wrong-kind resource incident is rejected with the same error as at load
- [ ] #2 Valid live incidents unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
