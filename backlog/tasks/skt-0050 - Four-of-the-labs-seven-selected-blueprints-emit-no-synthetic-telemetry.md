---
id: SKT-0050
title: Four of the lab's seven selected blueprints emit no synthetic telemetry
status: To Do
assignee: []
created_date: '2026-09-03 19:36'
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
- [ ] #1 The cause of zero synthetic emission for a loaded, ticking blueprint is identified from evidence, not inferred, and stated per affected blueprint
- [ ] #2 Whichever of the four blueprints share one cause are corrected together, and any that differ are recorded separately with their own evidence
- [ ] #3 The lab shows non-zero distinct synthetic metric families for every blueprint its selection declares, proved by live read-back after one metric interval
- [ ] #4 A regression guard fails when a selected blueprint contributes no synthetic family, so a silent blueprint cannot ship unnoticed
- [ ] #5 profiling-demo emits its type: app workload families on the lab, unblocking the SKT-0008.02 service-to-pod join
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
