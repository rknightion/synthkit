---
id: SKT-0007
title: OTLP-native emission parity across the catalog
status: Done
assignee: []
created_date: '2026-08-24 12:05'
updated_date: '2026-09-06 11:25'
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
- [x] #1 A recorded, evidence-backed answer to which catalog constructs and workloads have a real OTel-native metric form, and which are Prometheus-scrape-only and must NOT gain an OTLP lane
- [x] #2 The OTLP metrics lane mechanics support every instrument shape the in-scope catalog needs, not just the two families web_service uses
- [x] #3 The base blueprint surface can be emitted OTel-native end to end, and a blueprint demonstrates it
- [x] #4 No construct or workload invents an OTLP representation for telemetry that reality only ever produces as a Prometheus scrape target
- [x] #5 signals/otlp-metrics.md grows to cover every added family with provenance
- [x] #6 Constructs and workloads still do not import the OTel SDK, selfobs, or profiling; the architecture isolation test passes
- [x] #7 Later implementation waves are created as subtasks from the first subtask answer, not pre-guessed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-05 parent reconciliation: all six earlier Done subtask summaries read. AC2 is supported by the instrument-mechanics work plus this run's Summary tests; AC3 by the native Kubernetes blueprint; AC4 by scrape-only architecture guards and withholding unconfirmed Envoy/CSP/CloudWatch families; AC5 by per-family signal provenance and root pointers; AC6 by the integrated just check architecture pass; AC7 by the evidence-study-created follow-on tasks. AC1 remains unchecked because the Azure/GCP emitted envelope remains unresolved at SK-88. Envoy native datapoints are also withheld at SK-110 through SK-112; this parent is not complete.

2026-09-06 final reconciliation: Envoy native emission is now complete from a richer immutable capture, and CloudWatch Metric Streams lookup coverage expanded to 284 verified pairs with 52 explicit skips. The Azure half of the CSP contract is documented, but the Google receiver emitted name and resource envelope remains unresolved. AC1 therefore remains unchecked; all other parent criteria remain proven.

2026-09-06: SK-88 resolved for both CSP kinds from source and vendor docs (see SKT-0007.09 and signals/otlp-native-verdicts.md), which completes the catalogue-wide evidence-backed verdict record: 20 OTEL-NATIVE, 24 SCRAPE-ONLY, 1 UNRESOLVED (portkey_gateway, outside this epic's scope). AC1 checked. Status stays In Progress until SKT-0007.09 AC2 lands or is explicitly descoped, then run the DoD gate and close.

2026-09-06 closure: all seven ACs are checked. DoD: just check green on main at b1e2534 (CI 34029264851, including race, spdx and forbidden-words legs); no blueprint field or config struct changed in the closing work; the safe explicit dump ran in the 2026-09-07 wave (2,915 metric names, the one absent conditional family documented in signals/nettopo.md). SKT-0007.09 stays open as standalone low-priority work: its AC2 (csp_azure and csp_gcp opt-in lanes) is not a parent criterion; AC7 required later waves to be created from the evidence study, which they were.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-06: Remains In Progress at 6/7. Envoy and CloudWatch advanced without inferred telemetry; the exact resume boundary is the missing Google receiver output needed to finish the catalogue-wide evidence-backed verdict.

2026-09-06: Done at 7/7. Twenty OTEL-NATIVE and twenty-four SCRAPE-ONLY verdicts recorded with provenance, the lane mechanics cover every needed instrument, k8s_cluster, app, ai_agent, host, beyla_agent, envoy_gateway and the CloudWatch group emit OTel-native from captured or documented contracts, and the architecture guard holds. Remaining CSP lanes continue under SKT-0007.09.
<!-- SECTION:FINAL_SUMMARY:END -->
