---
id: SKT-0114
title: Diagnose the flaky network probe bound preflight test
status: To Do
assignee: []
created_date: '2026-10-09 11:19'
labels: []
dependencies: []
type: bug
ordinal: 236000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestCheckBoundsEachNetworkProbe (internal/preflight/preflight_test.go) failed in CI 37826211113 at b98798e with a probe reporting State ready where unreachable/timeout was expected, then passed on later runs. Preflight bounds keep a bad network target from stalling startup, so an intermittent pass of the wrong state hides whether the bound holds.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The failure is reproduced or bounded with a repeated race run and its cause is named
- [ ] #2 The fix keeps every probe bound and does not widen any probe or timeout; a repeated run passes
- [ ] #3 just check exits 0 at the landed SHA
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
