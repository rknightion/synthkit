---
id: SKT-0158
title: Diagnose intermittent HA renewal-loss observation timeout under loaded gates
status: Parked
assignee: []
created_date: '2026-10-21T15:25:00Z'
updated_date: '2026-10-24T09:14:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 300000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Go 1.27.3 dependency/security candidate hit TestHACoordinatorRenewalLossSuppressesCleanup/active in cmd/synthkit/ha_loss_test.go:381: HA local behavior was not observed within bound. The same unchanged test passed plain and race in the preceding full gate and passed a focused retry. Preserve the real observation synchronization and current failure bounds; diagnose the occasional missed observation rather than loosening timing or weakening assertions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded reproduction distinguishes scheduling delay from a lost observation or lifecycle defect.
- [ ] #2 Any correction preserves the cleanup suppression and actual HA observation assertions, with red then green evidence.
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

Owner ruling 2026-10-22 (loop52 Q05): isolated race invocation with bounds unchanged; tracked in SKT-0162.

Loop54 authorized bounded remote measurement at source8f5a75e (runtime source unchanged from a0554b6), run20261023T194726Z-synthkit-f2a5755f. One quiet-host race job ran HA then Fleet sequentially: 10 top-level HA iterations/40 scenario subtests and 10 Fleet iterations/20 restart-teardown subtests all passed, zero skipped. The same 40 HA cases cover helper readiness, not an extra independent sample. Bounds/source/assertions/isolation unchanged. No reproduction, repair, cleared full-gate failure, loaded A/B or measured isolation benefit established; retain Parked pending naturally failing same-context request/startup/scheduler evidence.

Loop54 closeout: remains Parked. Read-only quiet-host HA measurements passed40HA/20Fleet scenarios without reproducing the failure or proving isolation benefit. Resume on fresh attributable reproduction; caller deadlines, test bounds and isolation unchanged.

This loop performed only route discovery for the permitted quiet-host measurement. The installed runner has slot admission but no documented idle-only or preemptible measurement path guaranteeing required gates are not blocked. No remote job or test started; this is an unavailable measurement, not a green result or proof the host is busy. Retain the park until a compliant route or attributable reproduction is available.

Closeout defect park retained: no documented idle/preemptible nonblocking measurement route was available, so no new measurement ran. Resume on a compliant route or attributable failing reproduction; a free slot is not idle admission and full-gate success does not close the timing defect.

Fresh bounded remote race measurement ran ten iterations of the unchanged HA loss test: forty scenario passes, zero failures/skips/race warnings. Passing does not reproduce or repair the original scheduling/observation fault or establish isolation benefit; retain Parked and preserve every bound.
<!-- SECTION:NOTES:END -->
