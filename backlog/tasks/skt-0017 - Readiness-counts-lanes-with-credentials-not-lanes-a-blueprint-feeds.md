---
id: SKT-0017
title: 'Readiness counts lanes with credentials, not lanes a blueprint feeds'
status: To Do
assignee: []
created_date: '2026-08-28 18:24'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 97000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found 2026-08-28 deploying synthkit to a real Kubernetes cluster for the first time. It holds a Deployment permanently un-Ready in a configuration that is working correctly.

`EvaluateReadiness` (`internal/control/readiness.go`) requires a current success from every **configured** live delivery lane. A lane counts as configured when its credentials are present. But a lane only ever attempts a push when a **loaded blueprint** produces something for it, and those are different conditions.

Observed on the lab cluster with five blueprints selected and every credential correct:

    ready=false live_ready=false
    reasons: ['delivery lane "otlplogs" is not_attempted']
    loki         success   live_ready=true
    otlp         success   live_ready=true
    otlpmetrics  success   live_ready=true
    promrw       success   live_ready=true
    otlplogs     not_attempted  live_ready=false

The OTLP-logs lane only receives anything from a blueprint declaring `pod_logs_method: opentelemetry`, and none of the five selected did. The credentials were valid and the lane was never going to be used, so `not_attempted` was permanent -- and since readiness backs the container's `-healthcheck` probe, the pod stays `0/1 Running` indefinitely. A restart does not clear it. Nothing in the logs says why; the reason is only visible through an authenticated `/control/status`.

**Why this is a defect and not the probe being strict.** The strictness is right: a missing first push SHOULD be red, because a silently unauthenticated lane is exactly what this probe exists to catch. The error is the definition of the denominator. Readiness asks "did every lane with credentials push?" when the answerable question is "did every lane a loaded blueprint actually declares push?". Supplying a credential for a lane you are not currently using is a reasonable thing for an operator to do -- it makes the deployment ready for a blueprint they might select later through the control plane -- and it should not be indistinguishable from a broken lane.

Note the interaction with live blueprint selection: the set of lanes a blueprint feeds changes when an operator loads or disables a blueprint at runtime, so the denominator has to be recomputed then, not fixed at startup.

Do not fix this by dropping `not_attempted` to a pass. A lane that a loaded blueprint DOES declare and has never successfully pushed to must stay red -- that is the original defect this probe was built for.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A lane with credentials that no loaded blueprint feeds does not hold readiness red
- [ ] #2 A lane a loaded blueprint does feed, which has never pushed successfully, still holds readiness red
- [ ] #3 The denominator is recomputed when blueprints are loaded or disabled at runtime, not fixed at startup
- [ ] #4 The reason a deployment is not ready is visible without an authenticated control-plane call
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
