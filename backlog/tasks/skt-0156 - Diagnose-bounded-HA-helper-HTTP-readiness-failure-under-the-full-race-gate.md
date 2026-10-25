---
id: SKT-0156
title: Diagnose bounded HA helper HTTP readiness failure under the full race gate
status: Parked
assignee: []
created_date: '2026-10-21T03:00:00Z'
updated_date: '2026-10-25T22:18:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 295000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A documentation-only candidate failed TestHACoordinatorRenewalLossSuppressesCleanup/partial at the initial /control/state readiness wait before lease acquisition. The full remote race gate reported the six-second observation deadline, but did not retain child startup output or HTTP diagnostics. All runtime Go inputs are identical to base; one bounded local focused base race test passed, so the remote failure is not yet reproduced or attributed. This blocks trustworthy gate classification independently of the documentation change.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded same-context investigation records exact revisions, helper exit or startup output and readiness response evidence sufficient to distinguish startup failure from scheduling delay; passing focused runs are not called reproduction.
- [ ] #2 Any correction preserves the existing deadline, lease-admission and cleanup assertions, actual HTTP observation and hard-exit proofs; a meaningful red control and the full race gate verify the corrected behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop52 BATCH2: landed e2cea58 (pushed 45ff131). Cause 1 (reserve-and-close port race) fixed in ha_loss_test.go; failures now report helper exit/output or a SIGQUIT dump. Cause 2 unfixed: under gate CPU starvation the helper misses its 100ms startup Lease lookup or the 300ms renew deadline (65/130 under 256-hog load on gate-1); removing it needs a bound or production change. Parked pending owner ruling Q05 (park / isolated race tier / bound change). Evidence: codex/loop52/lha/return.md (machine-local). Sibling port pattern: SKT-0161.

Loop54 authorized bounded remote measurement at source8f5a75e (runtime source unchanged from a0554b6), run20261023T194726Z-synthkit-f2a5755f. One quiet-host race job ran HA then Fleet sequentially: 10 top-level HA iterations/40 scenario subtests and 10 Fleet iterations/20 restart-teardown subtests all passed, zero skipped. The same 40 HA cases cover helper readiness, not an extra independent sample. Bounds/source/assertions/isolation unchanged. No reproduction, repair, cleared full-gate failure, loaded A/B or measured isolation benefit established; retain Parked pending naturally failing same-context request/startup/scheduler evidence.

Loop54 closeout: remains Parked after quiet-host measurements showed no attributable readiness defect or isolation benefit. Resume only on fresh reproducible evidence; no bound/helper policy weakened.

No new timing measurement ran: the documented runner exposes free slots but no guaranteed idle or preemptible gate-host route. Existing quiet results do not clear the park; request/startup/scheduling attribution remains required. No timeout or test was weakened.

Closeout defect park retained: no new helper-readiness measurement ran because the required nonblocking idle/preemptible route was unavailable. Resume on compliant measurement or fresh attributed reproduction; preserve bounds and existing proof.

The same fresh forty HA scenario passes include the helper-readiness phases, not forty additional independent observations. No attributed readiness failure or correction was established; retain Parked, deadlines and assertions unchanged.

loop57 closeout: parked/open, see codex/closeout-2026-10-24-loop57.md.

Owner closeout: No new readiness attribution; unchanged bounds and diagnostic park retained.
<!-- SECTION:NOTES:END -->
