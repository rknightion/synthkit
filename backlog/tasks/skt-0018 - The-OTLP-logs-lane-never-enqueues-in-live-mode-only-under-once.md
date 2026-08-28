---
id: SKT-0018
title: 'The OTLP-logs lane never enqueues in live mode, only under -once'
status: To Do
assignee: []
created_date: '2026-08-28 18:34'
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
- [ ] #1 The OTLP-logs lane enqueues and pushes under the live scheduler, observed with a real or stubbed sink rather than through RunOnce
- [ ] #2 The mechanism by which RunOnce reaches the lane and blueprintLoop does not is stated, not just patched around
- [ ] #3 A regression test exercises the live scheduler path, since a RunOnce-based test passes against the broken code
- [ ] #4 Any other lane reachable only through RunOnce is identified in the same pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
