---
id: SKT-0008
title: >-
  Validate self-emitted span metrics against both real producers, and generate
  native histogram panels
status: In Progress
assignee: []
created_date: '2026-08-24 12:06'
updated_date: '2026-08-27 07:32'
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
VALIDATION DELIVERED 2026-08-27 (lanes L9a dashboards, L9b producer validation).

## The central answer: synthkit models the Tempo metrics-generator, not the collector connector

Provable from the family names alone, not a judgement call:
- traces_spanmetrics_{calls_total,latency,size_total} are defined in Tempo. The connector publishes traces.span.metrics.calls / .duration and has NO size family.
- source="tempo" is stamped on every row. The chart connector pipeline stamps source="spanmetrics"; Beyla stamps source="beyla".
- synthkit emits the four traces_service_graph_request_* families. The k8s-monitoring chart ships NO service-graph connector — those exist only from Tempo service-graphs processor.
- service (the Tempo intrinsic) sits alongside service_name; the connector emits service.name only.
- Dual native+classic matches SK-28 live capture of a real generator; the reference connector is exponential-only.

COROLLARY WORTH KEEPING: on a deployment configured like the reference one, synthkit self-emission is not a substitute for the real telemetry — it produces a family set that stack does not contain (service graph) and omits the one it does (traces_span_metrics_*). The default-off posture is therefore not merely conservative, it is the only correct out-of-the-box behaviour.

## Five real divergences CORRECTED in internal/workload/app/spanmetrics.go

- D1 span_name on SERVER rows was the NODE name while the trace lane names those spans from declared ROUTES. A generator fed synthkit own traces would emit span_name="POST /v1/assist"; synthkit self-emitted span_name="api". The two shapes could never overlay. Now one row per declared route, with tick volume split so the node total is unchanged.
- D2 the entry node row was always SPAN_KIND_SERVER. The entry root span kind is CLIENT for a browser entry, CONSUMER for worker/stream, INTERNAL for job. A browser entry has no server span, so that was a series that cannot exist.
- D3 latency was observed ONCE PER TICK while calls_total added hundreds. One sample per minute per series makes histogram_quantile a step function of a single random draw — the p99 panels were meaningless. Now observed once per counted call, bounded by the same 200-obs budget web_service uses.
- D4 no latency series for STATUS_CODE_ERROR at all, though calls_total had the error row. Any latency panel filtered to errors returned no data.
- D5 CLIENT edge rows carried only STATUS_CODE_OK though project.go stamps StatusError on a failed hop.

## AC #3 — excludeDimensions and the cardinality limit, explicitly

excludeDimensions: [span.name] is a CONNECTOR-ONLY knob; Tempo has no exclude mechanism (its intrinsic dimensions are opt-IN toggles, span_name default on). In the reference deployment the connector output therefore carries NO span-name dimension, so any panel doing by (span_name) SILENTLY MATCHES NOTHING there. synthkit correctly keeps span_name because it models the generator — this is not a defect to fix. The consequence now recorded in signals/apm.md: a dashboard built against synthkit self-emission will not port unchanged to a span.name-excluding stack.

aggregation_cardinality_limit behaviour is specific and is NOT a drop: past the limit the connector folds further combinations into ONE entry labelled otel.metric.overflow="true", so an aggregate series can look like a real one. Tempo equivalent (max_active_series) drops with no marker. synthkit emits neither, correctly.

## AC #4 default-off

Preserved and untouched. New workload-level test ticks against a bare zero-value core.World (the same value spanMetricsEnabled yields with no control state), asserts all TWELVE wire forms of the six gated families are absent, and — the part that matters — asserts the node own declared metric STILL emits, so a future refactor cannot "pass" by breaking emission entirely.

## AC #5/#6 dashboards — the task premise was stale

internal/dashgen/classify.go:59 already routes to dashboard.HistogramNative off the observed natives map, and dashboard/query.go:66 already emits the no-le native form. Verified against the source by the wiring pass, not taken on trust. The lane added a test pinning all three families by name rather than manufacturing a fix. L9b independently corroborated why it matters: the reference connector publishes ONLY an exponential histogram, so a classic-only panel there returns no data rather than merely being suboptimal.

## Six deliberate differences recorded, not defects

Bucket bounds (empirical GC capture outranks the two different Tempo OSS defaults — SK-96); latency_count as a bounded sample of calls_total; size_total as a flat calls x 256; connection_type="" as a present empty dimension (the generator real behaviour, a deliberate exception to omit-absent); connection_info and messaging_system latency absent (both opt-in subprocessors, so matching the default IS the correct shape); no otel_metric_overflow series.

## Follow-ups created

SKT-0008.01 status_code derivation (root decision: derive from the trace lane, never hard-code either constant), SKT-0008.02 app-workload target_info (breaks the entity-graph service-to-pod join), SKT-0008.03 ledger-driven error fraction (an active incident currently moves trace errors but NOT span-metric errors).

cantfind SK-96 and SK-97 filed. signals/apm.md gained [slug: apm-producers] plus a corrected connection_type enum and real bucket provenance.
<!-- SECTION:NOTES:END -->
