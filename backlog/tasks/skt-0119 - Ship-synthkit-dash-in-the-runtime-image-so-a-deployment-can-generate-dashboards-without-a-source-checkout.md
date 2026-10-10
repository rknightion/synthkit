---
id: SKT-0119
title: >-
  Ship synthkit-dash in the runtime image so a deployment can generate
  dashboards without a source checkout
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-10 09:18'
updated_date: '2026-10-10 10:43'
labels: []
dependencies: []
ordinal: 241000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Runtime deployments need the dashboard generator without a source checkout. Keep the existing entrypoint unchanged.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The published image contains the binary at /app/synthkit-dash
- [x] #2 just check passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Build dashboard generator in existing build stage; retain entrypoint; prove local image usage and full gate, then root review and exact-SHA publish CI.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Completed: one implementation cycle, one proof-only reviewer follow-up with no gate rerun. Land051728b61dd22cecd66e7a44287bd07e59937235; local image help Usage of /app/synthkit-dash and entrypoint version smoke0; lane full gate0; independent reviewPASS; aggregate CodeRabbit findings0 Dockerfile; composed gate650.799s exit0 at landed SHA; CI38044458057 success includingci-success; publish38044457989 successful amd64/arm64 builds, binary build/copy confirmed. Release-only jobs skipped, not passes. No generator source/config changes, so regeneration and inventory-diff DoD not applicable.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Published image includes /app/synthkit-dash with existing entrypoint unchanged, proven by local image smoke, full composed gate and exact-revision CI/publish.
<!-- SECTION:FINAL_SUMMARY:END -->
