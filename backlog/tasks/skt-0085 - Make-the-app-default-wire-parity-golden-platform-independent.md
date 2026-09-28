---
id: SKT-0085
title: Make the app default-wire parity golden platform-independent
status: In Progress
assignee: []
created_date: '2026-09-28 15:12'
updated_date: '2026-09-28 15:12'
labels:
  - testing
  - bug
dependencies: []
priority: high
type: bug
ordinal: 181000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Loop44 root rescue commit a732ad1 pinned separate metric and log digests for darwin/arm64 and linux/amd64; appParityGoldensForPlatform fatals on every other platform, making the public repository test suite fail. The divergence cause is unverified. Frozen contract: default output must not change; the test-only sort only reorders and internal/state is not edited. First classify three-platform output and follow the frozen D2 decision rule in codex/packets-2026-09-28-loop45/G1.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Evidence records per-case parity outputs on darwin/arm64, linux/arm64 and linux/amd64 from one tree, and classifies every difference as identical, float-only (with the maximum relative difference) or structural.
- [ ] #2 TestAppDefaultWireParity passes on darwin/arm64, linux/arm64 and linux/amd64 with no GOOS or GOARCH switch and no skipped case, and fails on a perturbed metric value, a dropped series and a changed log field.
- [ ] #3 No non-test file changes, and just check passes.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Classify canonical output on three platforms before editing; follow D2 branch, verify mutations and run gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop45: admitted
<!-- SECTION:NOTES:END -->
