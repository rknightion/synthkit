---
id: SKT-0017
title: 'Readiness counts lanes with credentials, not lanes a blueprint feeds'
status: Done
assignee:
  - '@codex'
created_date: '2026-08-28 18:24'
updated_date: '2026-08-28 21:21'
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
- [x] #1 A lane with credentials that no loaded blueprint feeds does not hold readiness red
- [x] #2 A lane a loaded blueprint does feed, which has never pushed successfully, still holds readiness red
- [x] #3 The denominator is recomputed when blueprints are loaded or disabled at runtime, not fixed at startup
- [x] #4 The reason a deployment is not ready is visible without an authenticated control-plane call
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add failing readiness tests proving an observed historical lane outside the current active-blueprint denominator is ignored, while a currently configured not_attempted lane stays red. 2. Add a failing public-probe test for stable sanitized reason codes that expose the readiness gate without lane names, endpoints, paths, or raw errors. 3. Make the smallest internal/control change, retain runner-driven dynamic recomputation, and run focused control/runner/pushstatus tests. 4. Run CodeRabbit before the code commit, then the task gates and exact-SHA CI before finalization.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
TDD evidence: before the fix, TestEvaluateReadinessUsesActiveBlueprintFeedsAsDenominator failed because a credential-configured otlplogs lane outside RequiredLanes remained not_attempted and held readiness red. The public-probe test also failed because no safe reason codes were present. The implementation now passes an explicit RequiredLanes list derived fresh from Runner.DeliveryReadinessLanes() on every readiness request; only those active feeds form the denominator. Required lanes missing from status or not yet successful remain red. Public /control/readiness exposes deduplicated non-sensitive reason_codes, including delivery_not_attempted/error/stale, while still omitting lane names, endpoint errors, paths, and detailed reasons. Existing SetChangeObserver reconfigures pushstatus freshness on runtime control changes, and the per-request RequiredLanes derivation prevents a stale cached denominator. CodeRabbit first pass raised one major ambiguity about relying on Configured as an implicit active-feed signal; addressed with the explicit RequiredLanes seam. Second pass reviewed all five code/test files and raised 0 issues. Focused control/runner/pushstatus/cmd tests passed. make gate passed. The literal DOD dump command selected no blueprints and produced setup-only empty inventory, so it was not counted; rerunning with BLUEPRINT_NAMES=k8s-full-stack passed and exercised 1,137 metric families plus Loki, OTLP logs, traces, and profiles, including eight dry-run otlplogs pushes. make blueprint-schema is not applicable because no blueprint field or construct/workload config struct changed.

Exact-SHA CI run 33211535841 passed all required jobs at commit 0007368994e18c1d3581cf4bfe7b25a29473f8e2, including e2e and the aggregate ci-success job. DOD #2 remains unchecked and is not applicable: no blueprint field or construct/workload config struct changed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Implemented active-feed readiness derivation and sanitized public reason codes. Verified by focused TDD, a zero-finding second CodeRabbit review, make gate, a k8s-full-stack dry-run dump with 1,137 metric families and all declared signal lanes, and exact-SHA CI run 33211535841 including e2e. Credential-only lanes outside the current blueprint feed set no longer hold readiness red; fed not_attempted lanes remain red; the denominator is recomputed per readiness request.
<!-- SECTION:FINAL_SUMMARY:END -->
