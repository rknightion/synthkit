---
id: SKT-0225
title: Restore hosted CI renderer identity proof across supported architectures
status: In Progress
assignee:
  - '@loop59-root'
created_date: '2026-10-25T22:08:00Z'
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
- [ ] #1 Both supported architectures prove candidate unbound output against independently captured pre-change renderer controls with no masking, skipped fields or weakened tolerance
- [ ] #2 The exact repair tree receives its required gate/review and green hosted CI; the Helm installation failure is separately evidenced and appropriately fixed or classified
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Inspect saved failed CI and current tests; capture old renderer controls on arm64 and executed amd64; repair only demonstrably incorrect proof assumptions, stop if actual unbound behavior changed. Diagnose tool installation read-only; no workflow/provider authority changes without root scope. Return candidate before landing.
<!-- SECTION:PLAN:END -->
