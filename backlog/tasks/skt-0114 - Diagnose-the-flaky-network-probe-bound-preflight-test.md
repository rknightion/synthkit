---
id: SKT-0114
title: Diagnose the flaky network probe bound preflight test
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-09 11:19'
updated_date: '2026-10-10 21:37'
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
- [x] #1 The failure is reproduced or bounded with a repeated race run and its cause is named
- [x] #2 The fix keeps every probe bound and does not widen any probe or timeout; a repeated run passes
- [x] #3 just check exits 0 at the landed SHA
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop7: deterministic failing-first test-only probe bound repair without widening probes/timeouts; focused repeated race proof, local gate, root review/land; composed criterion waits for all three CI repairs.

Reapply unchanged retained test fix on current main; race100 bound proof, full gate, review, root land and composed criterion3.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop7 implementation attempts1, focused fail-first proof + race100/package20 passes and independent routine review PASS. Exact one-file candidate retained uncommitted; full gate red only known R12 control-dash600s timeout. No AC3 checked or land: frozen all-three composed prerequisite also needs accepted R13. Resume after blockers resolve; no unchanged failed gate retry.

loop8 attempts0 (priorloop7 implementation1 unchanged). Frozen dependency R12mustland; R12ownerparked shared-path optimization. R14 retainedworktreebase89b5cb1742b53d975b53caad0fdf78688d7f8ff9 onlypreflighttestdirty; workingbinarydiff exactlymatches frozenretainedpatch01779a8ca97dd106664ef2d35dfc60b1ae752686525c440838cbbc3a1e81ad3e. Parkneedsdependency, resumeunchangedrebase thenrace100/fullgateafterR12; finalcomposedallthreecriterionnotchecked.

loop10: new implementation attempts0, historical implementation count1 unchanged. Dependency park on SKT-0112 (control-dash race performance) acceptance and land; no gate or acceptance claimed. Resume frozen serial order after prerequisite green/land, using retained exact candidate where provided; no reset or reconstruction from prose.

This run: Dependency SKT-0112 (control-dash race performance) unaccepted/unlanded; performance prerequisite reverted after CI489.278s. Retained input unchanged; zero new implementation attempts. Resume frozen order after prerequisite accepted land and gate.

This run: one minimal review correction observes forced request-context cancellation. Original-handler overlay deterministically reproduced ready/implicit200 for all3 probes; corrected race100 passed15.466s without widening20ms probe/1s bound. Root landed8c4e4c042f0f5b50d755f491fcdc42e85573b234, CI38071356877 all10 jobs success includingci-success; clean detached composed just check exit0 at that exactSHA. Attempt history preserved: retained prior attempt1 plus one review-repair cycle, infrastructure outages chargednone. Aggregate routine CodeRabbit remains run-level gate. No generation/inventory behavior changed; conditional generation not applicable and external prerequisite rows unexecuted.

Run-level aggregate CodeRabbit now complete against851d879 from6edd50e: all20changedfiles reviewed, outcomecompleted, zero findings. Prior deferred routine review requirement satisfied.

loop12: one new review-repair change/verify cycle after the retained prior attempt, no historical reset. Done after deterministic fail-first, race100, exact-SHA CI/composed proof and aggregate CodeRabbit.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Deterministic cancellation regression now observes the canceled context and retains the unresponsive handler until cleanup. Criteria1-3 proved by fail-first overlay, race100, exact landedSHA CI and composed gate; original probe deadlines/assertions preserved.
<!-- SECTION:FINAL_SUMMARY:END -->
