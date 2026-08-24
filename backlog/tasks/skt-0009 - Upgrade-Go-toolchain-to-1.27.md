---
id: SKT-0009
title: Upgrade Go toolchain to 1.27
status: Done
assignee: []
created_date: '2026-08-23 19:06'
updated_date: '2026-08-23 21:21'
labels: []
dependencies: []
ordinal: 25000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Adopt Go 1.27 consistently across the application, nested modules, build images, CI configuration, setup automation, and version-specific contributor documentation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All active Go module and toolchain pins require Go 1.27
- [x] #2 Build images, CI jobs, setup automation, and current documentation agree with the Go 1.27 requirement
- [x] #3 The repository green-bar validation passes under Go 1.27
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Inventory every active Go version pin, including nested modules and container or CI toolchains. 2. Update the pins and version-specific documentation to Go 1.27.0 without changing historical records or fixtures. 3. Run the repository-defined validation gate, review the diff, commit to main, push, and confirm hosted CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Local Go 1.27.0 make gate passed in full. The complete-catalog DRY_RUN=true BLUEPRINT_NAMES=* go run ./cmd/synthkit -once -dump inventory completed for all 26 blueprints with 3,144 output lines and no hard errors. No blueprint field or construct/workload config struct changed, so schema regeneration was not required. CodeRabbit was skipped because only declarative module, container, and current documentation pins changed.

Exact-head hosted evidence: GitHub Actions CI run 32662554471 passed at 0437f2dfb3a60386b3578926bbb3ec6a5dd44645, including Go, Docker, UI, hygiene, secret scan, E2E, and ci-success.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Upgraded all active Go toolchain surfaces to Go 1.27, passed the full local gate and complete 26-blueprint dry-run inventory, and passed exact-head hosted CI run 32662554471 at 0437f2dfb3a60386b3578926bbb3ec6a5dd44645.
<!-- SECTION:FINAL_SUMMARY:END -->
