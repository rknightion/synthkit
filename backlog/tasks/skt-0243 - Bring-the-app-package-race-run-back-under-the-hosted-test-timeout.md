---
id: SKT-0243
title: Bring the app package race run back under the hosted test timeout
status: To Do
assignee: []
created_date: '2026-10-26T09:30:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 449000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Hosted CI run 40295213492 on main f111bb3d failed: just race timed out internal/workload/app at 600 s (TestAppDefaultWireParity still running at 2m28s). Base 11a16beb was green; the realism engine tests added to the package pushed the race aggregate over Go's default timeout on hosted runners. Reduce cost without weakening any test, assertion or the 600 s timeout.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
