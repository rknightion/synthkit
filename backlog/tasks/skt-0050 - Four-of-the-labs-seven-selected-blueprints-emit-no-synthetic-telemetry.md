---
id: SKT-0050
title: Four of the lab's seven selected blueprints emit no synthetic telemetry
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-03 19:36'
updated_date: '2026-09-03 21:04'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 142000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found 2026-09-03 by live read-back while verifying SKT-0008.02's park reason, which recorded that no app-workload samples existed on the lab. The cause is broader than that task: four of the seven blueprints the lab selects are loaded and ticking but produce zero synthetic metric families.

The lab's desired selection is seven blueprints. Instant queries against the lab metrics instance, resolved at runtime, counted distinct non-selfobs metric names per blueprint label at 2026-09-03T19:35Z, repeated and stable:

    k8s-full-stack         0
    k8s-logs-events      275
    aws-cloud-services     0
    dbo11y-mysql         349
    netobs-enterprise      0
    profiling-demo         0
    otlp-native          336

The four zeroes are not absent from the stack. Each carries the synthkit self-observability families and nothing else - synthkit_tick_total, synthkit_tick_duration_seconds_{bucket,count,sum}, synthkit_cardinality_series, and for netobs-enterprise also synthkit_cycle_duration_seconds_*. So the blueprint is loaded, its ticks are running, cardinality is being reported, and no construct or workload lane delivers anything.

This is distinct from SKT-0017, which is Done and concerned readiness counting credentialed lanes rather than fed lanes. Here the lanes are fed by nothing at all. It is also the live case SKT-0031 needs: SKT-0031 is parked on eight named lane failures behind a local deployment, and four silent blueprints are visible on the running lab without one.

It is the direct blocker for SKT-0008.02 AC#3: profiling-demo is the only selected blueprint declaring a type: app workload, so with profiling-demo silent there is no app-workload target_info to join, and the wave's finding of zero app samples was a symptom of this, not of a missing app lane.

Query identity: use the read credential named in SKT-0049 with the per-signal instance id as the Basic-auth username, never the stack id.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The cause of zero synthetic emission for a loaded, ticking blueprint is identified from evidence, not inferred, and stated per affected blueprint
- [ ] #2 Whichever of the four blueprints share one cause are corrected together, and any that differ are recorded separately with their own evidence
- [ ] #3 The lab shows non-zero distinct synthetic metric families for every blueprint its selection declares, proved by live read-back after one metric interval
- [x] #4 A regression guard fails when a selected blueprint contributes no synthetic family, so a silent blueprint cannot ship unnoticed
- [ ] #5 profiling-demo emits its type: app workload families on the lab, unblocking the SKT-0008.02 service-to-pod join
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
2026-09-05 wave plan: Lane A first establishes the per-blueprint zero-emission cause from local control-plane inventory and runtime evidence, corrects shared causes together and divergent causes separately within owned construct/workload/control/identity-verifier files, adds the silent-blueprint regression guard, then proves every selected lab blueprint emits non-selfobs families after a metric interval.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-05 lab diagnosis: authenticated control read-back reports loaded=7, active=4, diagnostics=[], and persisted disabled_blueprints=[aws-cloud-services, profiling-demo, k8s-full-stack]. Those three are loaded but intentionally do not tick constructs. Their zero inventories are therefore control-state outcomes, not a shared construct-emitter defect. The same published image emitted all three locally when enabled.

2026-09-05 scope correction: netobs-enterprise is active with 251 series / 45 families. Its network-topology families deliberately have no blueprint label; a live inventory-sourced family returned 33 current series and zero blueprint labels. The original blueprint-label count was a false zero. Current control inventory for the other selected identities is 692/385, 2634/587 and 2343/649 series/families.

2026-09-05 boundary: no lab control mutation was authorised. Resume when the deployment owner enables the three persisted-disabled blueprints through the control API, waits two 60s metric intervals plus the 5s delivery deadline, and reruns status/health/diagnostics/inventory plus scope-aware live queries. Source guard TestEvaluateRejectsInventoryWithNoMetricFamilies, just check, the explicit 27-non-agent safe dump, and just e2e passed. No fidelity exemption was added.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-05: Parked after evidence split the original four-zero report into two causes. Three declared blueprints are explicitly disabled by persisted operator control state; netobs-enterprise was a false zero from an invalid blueprint-label selector and is currently nonzero. The source regression guard and local all-identity verification pass. Resume with authorised control-plane enablement, the 125s observation window, and scope-aware live read-back.
<!-- SECTION:FINAL_SUMMARY:END -->
