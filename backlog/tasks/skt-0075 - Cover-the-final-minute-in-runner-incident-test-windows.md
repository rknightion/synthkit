---
id: SKT-0075
title: Cover the final minute in runner incident test windows
status: Done
assignee:
  - '@codex'
created_date: '2026-09-11 16:34'
updated_date: '2026-09-11 17:41'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 171000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Three runner tests use a 23h59m window while expecting all-day activity, causing failures during the last UTC minute. Production window arithmetic is correct and remains untouched.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Failing-first arithmetic evidence demonstrates the excluded final minute and deterministic coverage proves the repaired full-day window
- [x] #2 The three tests pass independent of time of day while active/inactive isolation remains intact
- [x] #3 Only runner_incidents_test.go changes and reviewed focused and integrated checks pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
L2 widens only intended open test windows and adds direct boundary coverage; root inspects and reviews before separate commit, then integrates the full gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
L2 focused boundary test failed first at 23:59:30 with the original 23h59m duration, then passed at 23:59:00, 23:59:30 and 23:59:59 after widening test-only open windows. Root read the full diff and confirmed shape evaluates whole-second daily offsets and rejects >=24h. The historical dated inactive isolation window is unchanged. Review and integrated acceptance pending.

Batched CodeRabbit completed all 15 L1/L2 source and fixture files. No finding against this runner test file. Root source review accepted the test-only second-precision boundary repair. Integrated gate still pending.

R5 integrated full check passes and CI34624586338 at27e0288c33a3a3936fd76ccd907676251f827c28, CI34626745006 at89da21b9a85ae87f8f7584b41409bc916d905592 and CI34628037101 atef9862af2f4d02549f2b5e87f0ecac05a29a4c69 all success. Conditional gen not applicable, gen-check passed. Dump compared with no emitter changes, one sampled span difference.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Done: failing-first final-minute boundary proof, repaired all-day open test windows and preserved closed-window isolation. Only runner_incidents_test.go changed, at7fcf159988699b0a9adc489e0c1d2eb35dfa2209. No production clock seam or production arithmetic change.
<!-- SECTION:FINAL_SUMMARY:END -->
