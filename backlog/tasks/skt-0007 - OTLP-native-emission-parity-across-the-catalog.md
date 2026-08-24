---
id: SKT-0007
title: OTLP-native emission parity across the catalog
status: To Do
assignee: []
created_date: '2026-08-24 12:05'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkit can model an OTel-native estate for exactly one workload. Measured 2026-08-24 across the 45 catalog packages (42 constructs + 3 workloads):

| signal class | packages declaring it |
|---|---|
| `core.Metrics` (promrw) | 44 |
| `core.Logs` | 18 |
| `core.Traces` | 3 |
| `core.RUM` | 2 |
| `core.OTLPMetrics` | **1** |

The single OTLP-native metrics emitter is `web_service` under `otel.metrics: true`, and it carries two families: `http.server.request.duration` (explicit-bound histogram) and `http.server.active_requests` (UpDownCounter). See `signals/otlp-metrics.md`, which is live-validated as far as it goes.

The consequence: "deploy synthkit as if my estate were OTel-native" is not something a user can currently do. A cluster, a database, CloudWatch, the platform addons — none has an OTLP-native representation, so any dashboard or alert a user builds against the OTel-native shape has no synthetic data behind it.

This epic closes that gap. It is deliberately evidence-led rather than a blanket port: some constructs genuinely have an OTel-native form in the real world, others only ever exist as Prometheus scrape targets, and emitting OTLP for the latter would be inventing telemetry that reality never produces — which the repository contract forbids. Establishing which is which is the first subtask, and the later waves are created from its answer rather than guessed at now.

Shares its seam with SKT-0006.05 (the OTLP logs lane): both follow the `core.OTLPMetricWriter` / `World.OTLPMetrics` precedent — an opt-in native-OTLP alternative lane, nil unless declared, wired by the runner from a declared signal class, hand-encoded in `internal/sink/otlp` with no OTel SDK (the architecture contract confines the SDK to `internal/selfobs`). Coordinate so the two do not both restructure that sink.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A recorded, evidence-backed answer to which catalog constructs and workloads have a real OTel-native metric form, and which are Prometheus-scrape-only and must NOT gain an OTLP lane
- [ ] #2 The OTLP metrics lane mechanics support every instrument shape the in-scope catalog needs, not just the two families web_service uses
- [ ] #3 The base blueprint surface can be emitted OTel-native end to end, and a blueprint demonstrates it
- [ ] #4 No construct or workload invents an OTLP representation for telemetry that reality only ever produces as a Prometheus scrape target
- [ ] #5 signals/otlp-metrics.md grows to cover every added family with provenance
- [ ] #6 Constructs and workloads still do not import the OTel SDK, selfobs, or profiling; the architecture isolation test passes
- [ ] #7 Later implementation waves are created as subtasks from the first subtask answer, not pre-guessed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
