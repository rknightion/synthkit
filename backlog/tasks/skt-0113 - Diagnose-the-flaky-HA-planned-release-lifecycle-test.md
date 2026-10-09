---
id: SKT-0113
title: Diagnose the flaky HA planned-release lifecycle test
status: Done
assignee:
  - '@loop8'
created_date: '2026-10-09 11:19'
updated_date: '2026-10-09 17:23'
labels: []
dependencies: []
type: bug
ordinal: 235000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestHAGenericLeaseFileReloadAndPlannedRelease (cmd/synthkit/ha_lifecycle_test.go) failed in CI 37860640279 at c390bd9 with the child process ending "exit status 1" after acquiring the lease, then passed at 95b92ee and bb4c4e2 with no related change. SKT-0092 (lease HA with crash-only fencing) cannot be closed while its own lifecycle test is intermittently red, and a real defect here would sit in planned-release fencing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The failure is reproduced or bounded with a stress run (-race, repeated count) and classified as a test defect or a runtime defect with evidence
- [x] #2 A test defect is fixed without a new sleep, tolerance or retry and without weakening any assertion; the stress run then passes
- [x] #3 A runtime defect in election, fencing or release ordering is recorded with the evidence and not changed without owner sign-off
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop7: deterministic forced interleaving plus contended race stress; test-only repair with unchanged timings/assertions or bounded evidence question.

loop8 owner widens runtime shutdown/release scope. Deterministic forced interleaving first; classify cause; repair without timing/assertion/fencing weakening; prove40/40 alongside control-dash race; root security review and land. Two attempts this loop.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop7: retained implementation attempts0; temporary diagnostic cycle reverted. Bounded unchanged contended race stress8/40 failures; shutdown watchdog with request-header reader, origin unproven; no deterministic reproduction/test-only fix. Park needs=owner per goal explicit stop rule. Resume after scoped deterministic transport investigation decision, runtime/timing changes still prohibited. Evidence retained privately by root; no code changed.

loop8 attempts1/2; deterministic incomplete HTTP-header reader causes unchanged drainwatchdogexit1 beforefix, fixedclosingNew/IdlewhilejoiningActivehandlers. Original/forced/active tests40/40 each plus concurrentcontrol-dash race0. CompleteCodeRabbit0 and securityPASS; exactbase fullgate1 [plannedreleaseflake,controltimeout], candidate1 [controltimeout] subset. Root reviewed baselineannotation, unchangedtests/timeouts/CI; landedcf8c52df099ea060642add27695c2929f02a8c53. ExactSHA CI/composed pending, noDone claim.

loop8 attempts1/2 implementation, no new review-repair cycles. Deterministicfailbefore/runtimeclassification and40/40 original+40/40 forcedheader+40/40 active-handler/concurrentracepass, unchangedtimings/fencing. Rootlandcf8c52d; absolutecomposedcf8gate0 and integrated2f38gate0, exact2f38CI37962316276 go/e2e/ci-success/allrequiredsuccess. Ownerruntime signoff frozenDecision used; noleaseelection or crash-fence change. CIcf8cancelled supersedednotpass; proof identityfinal2f38dbbba291b794213639a2428b69d3023e58cd.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Closed planned-release HTTP shutdown defect: retire incomplete New/Idle header connections while Shutdown joins active handlers; no assertion/timing/lease/fencing weakening. Deterministic regression and120/120 stress proof, independent reviews/CodeRabbit, composed0 and exact integratedSHA CIgreen.
<!-- SECTION:FINAL_SUMMARY:END -->
