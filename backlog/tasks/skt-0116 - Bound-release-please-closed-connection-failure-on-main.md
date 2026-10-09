---
id: SKT-0116
title: Bound release-please closed-connection failure on main
status: To Do
assignee: []
created_date: '2026-10-09 14:36'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 238000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Release-please run37943031534 at board-only main edf7243dc6742373da1c3feb8b3e72407e5c5df2 failed with release-please failed: other side closed; dependent publish skipped. This is independent of CI HA lifecycle failure and skcapture Trivy failures. No authentication/root cause was established, so do not rotate credentials or assume a policy defect.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Bounded evidence classifies the closed connection and records exact source/run identity and any targeted retry outcome; skipped dependent publication is not counted as passing.
- [ ] #2 Release-please succeeds at exact main SHA or the remaining prerequisite and safe resume condition are documented without credential rotation or permission weakening.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
