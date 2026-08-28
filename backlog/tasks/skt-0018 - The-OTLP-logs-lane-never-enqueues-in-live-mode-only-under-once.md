---
id: SKT-0018
title: The OTLP-logs lane never pushes on the cluster while five other lanes do
status: To Do
assignee: []
created_date: '2026-08-28 18:34'
updated_date: '2026-08-28 19:36'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 98000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found 2026-08-28 on the lab Kubernetes cluster, the first time synthkit has run continuously outside Compose against a live stack. The OTLP-logs transport does not emit at all in live mode. It works correctly under `-once`.

Observed with six blueprints selected, `DRY_RUN=false`, every credential valid, over four minutes and across three pod lifetimes:

    loki         success        113 pushes, 11776 items
    otlp         success        160 pushes, 22166 items
    otlpmetrics  success          6 pushes,    57 items
    promrw       success        130 pushes, 38773 items
    otlplogs     not_attempted    0 pushes,     0 items   queue depth 0

The queue for `otlplogs` exists and its depth never leaves zero, so nothing is ever enqueued -- this is not a delivery failure, a credential problem or backpressure. The sink is unconditionally constructed at `cmd/synthkit/main.go:302` and wired into `runner.Sinks` at `:303`, so it is not a nil-sink gate either.

The same binary and the same blueprint set emit it fine through `RunOnce`:

    DRY_RUN=true BLUEPRINT_NAMES=k8s-full-stack go run ./cmd/synthkit -once
      8 [dry-run loki]
      1 [dry-run otlp]
      8 [dry-run otlplogs]
      8 [dry-run promrw]

`k8s-full-stack` is the producer: it declares `pod_logs: true` with no method, and `podLogsMethod` defaults to `opentelemetry` since k8s-monitoring 4.x. So the difference is `RunOnce` versus `blueprintLoop`, not configuration.

**Why nothing caught it.** Every existing check drives `RunOnce`: `-once -dump`, `-once -inventory-json`, the `make e2e` receiver correlation, and the signal-fidelity synth side. The live scheduler path has no coverage for this lane at all, so a transport built deliberately under SKT-0006.05 has never actually run in the mode it ships in.

**Operational consequence, which is what makes this urgent rather than cosmetic.** The container's startup probe is delivery-aware and passes only once every configured lane has a current successful push. A lane that never enqueues therefore never goes green, the probe fails to its threshold, and kubelet restarts the pod -- observed twice before the budget was widened in the lab deployment. So this defect crash-loops any Kubernetes deployment whose blueprints declare OTLP pod logs.

Compare `RunOnce` against `blueprintLoop` and `tickBlueprintInstances` for what the live path does not call. Add live-scheduler coverage for the lane as part of the fix; a regression test that runs through `RunOnce` would pass against the broken code.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The cluster deployment's otlplogs lane is reproduced as not_attempted somewhere it can be iterated on, not only observed in the cluster
- [ ] #2 Why the lane resolves differently on the cluster than under a local live scheduler is established with evidence, since the local live scheduler demonstrably does reach it
- [ ] #3 The lane pushes on the cluster, verified through /control/status rather than inferred
- [ ] #4 A regression test covers whichever path was actually broken, and does not pass against the broken code
- [ ] #5 Any other lane reachable through only one entry point is identified in the same pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-28 CORRECTION, measured. The title and description are TOO BROAD and partly wrong. The OTLP-logs lane DOES fire under the live scheduler.

Reproduced locally with the live scheduler (not RunOnce), dry-run, twice:
- BLUEPRINT_NAMES=k8s-full-stack, TICK_DEFAULT=1s: 8 otlplogs pushes by t=25s, 16 by t=75s.
- The EXACT cluster set (k8s-full-stack,k8s-logs-events,aws-cloud-services,dbo11y-mysql,netobs-enterprise,otlp-native,profiling-demo) at TICK_DEFAULT=5s, matching the deployment: 8 otlplogs by t=28s, 16 by t=112s, alongside loki 136, otlp 176, otlpmetrics 8, promrw 138.

So 'the live scheduler never reaches the lane' is DISPROVED. blueprintLoop and tickBlueprintInstances do reach it, on the same blueprint set and the same tick cadence the cluster runs.

What remains true and unexplained: on the cluster the lane sits at not_attempted with queue depth 0 for the whole run while five other lanes push. The differences still in play are (a) DRY_RUN=false, i.e. live delivery through the queue to a real endpoint rather than the printing sink, and (b) something environmental about the deployment. Note otlpmetrics is comparably low-volume and DOES push in the cluster (8 pushes), so 'low-volume lanes are just slow' does not explain it either.

A local live-mode reproduction was attempted and blocked by config validation: GC_PROM_RW must be an HTTPS URL with path /api/prom/push, so pointing the sinks at a dead plain-HTTP endpoint will not start. A reproduction needs HTTPS endpoints or a local TLS receiver — the e2e receiver harness is the obvious candidate since it already terminates what the sinks expect.

Start from the queue and the enqueue path, not the scheduler. Depth 0 with zero pushes means Write was never called on that lane's stamped writer, so the question is whether w.OTLPLogs was nil for the instance in the cluster — which would mean Signals() did not contain core.OTLPLogs there, i.e. podLogsOTLPNative(cl) resolved false. Check what the running pod actually resolved rather than what the blueprint file says.

Also still true and still worth the fourth acceptance criterion: whether any other lane is reachable only through one entry point.
<!-- SECTION:NOTES:END -->
