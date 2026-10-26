---
id: SKT-0225
title: Restore hosted CI renderer identity proof across supported architectures
status: Done
assignee: []
created_date: '2026-10-25T22:08:00Z'
updated_date: '2026-10-26T01:04:00Z'
labels:
  - ci
dependencies: []
priority: high
ordinal: 431000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Hosted amd64 coverage fails the newly added complete unbound PostgreSQL/MySQL digest checks while the arm64 composed gate passes. The same run also failed Helm tool installation. Establish pre-change same-platform controls before changing expectations; do not assume production output or tool infrastructure is at fault.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Both supported architectures prove candidate unbound output against independently captured pre-change renderer controls with no masking, skipped fields or weakened tolerance
- [x] #2 The exact repair tree receives its required gate/review and green hosted CI; the Helm installation failure is separately evidenced and appropriately fixed or classified
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Inspect saved failed CI and current tests; capture old renderer controls on arm64 and executed amd64; repair only demonstrably incorrect proof assumptions, stop if actual unbound behavior changed. Diagnose tool installation read-only; no workflow/provider authority changes without root scope. Return candidate before landing.

Loop60: validate retained architecture-control repair, root review/gate and fix-forward first; require exact hosted green before another landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: uncommitted two-test-file patch SHA256 73a021956dce3c268f908bb33ad0a1b894ff06a8bc44b5eb26bcb96af3d64ffc retained. Actual pre-change/candidate full captures byte-identical within arm64 and executed amd64; both full package suites pass. No masking/tolerance/production change. Root CodeRabbit/full gate/native hosted green unrun; no repair push. Helm installed; following kubeconform curl reset before chart validation. Resume exact repair review/gate, explicit fix-forward landing decision, then exact-SHA hosted CI.

Exact repair head bcc5a6fe received complete CodeRabbit review, full composed check and successful hosted CI run 40243147036 with all 11 jobs green, including native architecture and both kubeconform legs. No production/blueprint field changed, so generator condition is not applicable. Selected database CLI inventory finalization remains explicitly pending; no unrun command is claimed.

Published renderer-only repair remains exact-head hosted green, with complete architecture/control review and gate already recorded. Selected real CLI proof now completed on the later composed source while renderer compatibility tests and database adapters remain byte-unchanged: dbo11y-mysql plus acme-ai-platform actual just dump, 1925/1925 sourced names, all 49 distinct PostgreSQL/MySQL names and exact per-family label keys checked, with empty/missing-family/illegal-label negative controls. No sourced name or label difference. The later causal stack race-budget failure does not alter the already published two-test renderer repair or this unchanged renderer inventory. Generator condition remains not applicable; all task criteria and DoD now satisfied.
<!-- SECTION:NOTES:END -->
