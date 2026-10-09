---
id: SKT-0114
title: Diagnose the flaky network probe bound preflight test
status: Parked
assignee:
  - loop7
created_date: '2026-10-09 11:19'
updated_date: '2026-10-09 14:16'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop7: deterministic failing-first test-only probe bound repair without widening probes/timeouts; focused repeated race proof, local gate, root review/land; composed criterion waits for all three CI repairs.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop7 implementation attempts1, focused fail-first proof + race100/package20 passes and independent routine review PASS. Exact one-file candidate retained uncommitted; full gate red only known R12 control-dash600s timeout. No AC3 checked or land: frozen all-three composed prerequisite also needs accepted R13. Resume after blockers resolve; no unchanged failed gate retry.
<!-- SECTION:NOTES:END -->
