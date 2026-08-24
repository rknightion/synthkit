---
id: SKT-0008
title: >-
  Validate self-emitted span metrics against both real producers, and generate
  native histogram panels
status: To Do
assignee: []
created_date: '2026-08-24 12:06'
labels: []
dependencies: []
priority: medium
type: feature
ordinal: 34000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkits own span-derived metric emission is an opt-in path that has never been checked against the real producers it imitates.

By design it is OFF by default: `runner.go` `spanMetricsEnabled` returns false with no control state loaded, commented "default OFF — defer to metrics-generator/beyla", and it is a per-blueprint control-plane opt-in. That default is correct and is not in question. The point of this epic is that when a user DOES opt in to self-emitting, the shape they get must match reality — and today nothing proves it does.

What synthkit emits when opted in (`internal/workload/app/spanmetrics.go`, `internal/workload/webservice/metrics.go`): `traces_spanmetrics_{calls_total,latency_*,size_total}`, `traces_service_graph_request_{total,failed_total,server_seconds*,client_seconds*}`, `traces_target_info`, `traces_host_info` — dual-emitted classic plus native exponential via `state.ObserveDual` / `ObserveDualExemplar`, with exemplars.

There are TWO real producers with different shapes, and which one synthkit matches is unverified:
- **Tempo metrics-generator**, server-side. Live-verified to emit both native and classic (`signals/apm.md`, SK-28).
- **The collector-side spanmetrics connector**, which the reference deployment actually uses: `m7kni/rkps-awsinfra` `applications/k8s-monitoring/values.yaml` sets `applicationObservability.connectors.spanMetrics` with `histogram.type: exponential`, `excludeDimensions: [span.name]`, `aggregationCardinalityLimit: 5000`, and dimensions `http.request.method` / `http.response.status_code` / `rpc.method` — with Tempos generator deliberately turned off to avoid double-counting.

Second deliverable, independent of the validation: generated dashboard panels for these families are still classic-only. `internal/dashgen/classify.go` tags any family carrying a `_bucket` series as `dashboard.HistogramClassic`, so it emits classic quantile queries even though both synthkit and the metrics-generator publish native series. Robs standing direction (2026-06-15) is that latency and service-graph panels should query native histograms. `dashgen` already has a `dashboard.HistogramNative` kind to route to.

The SKT-0006 reality corpus supplies the evidence for the validation half, so sequence after it where practical.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The real shape of span-derived metrics is captured and recorded for BOTH producers, with provenance, including where they disagree
- [ ] #2 Divergences between synthkit self-emission and each real producer are enumerated, and each is either corrected in the emitter or recorded as a deliberate, reasoned difference
- [ ] #3 Dimension sets, excluded dimensions, and cardinality-limit behaviour are addressed explicitly, not just metric names
- [ ] #4 The default-off behaviour is preserved and covered by a test, so deferring to the metrics-generator remains the out-of-the-box path
- [ ] #5 Generated dashboard panels for the latency and service-graph histogram families query native histograms via dashboard.HistogramNative
- [ ] #6 A family that exists only as a classic histogram still generates a working classic panel
- [ ] #7 signals/apm.md records both producer shapes and which one synthkit models
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
