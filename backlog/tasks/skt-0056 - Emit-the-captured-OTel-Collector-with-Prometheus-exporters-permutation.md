---
id: SKT-0056
title: Emit the captured OTel Collector with Prometheus exporters permutation
status: Done
assignee:
  - '@codex'
created_date: '2026-09-07 11:03'
updated_date: '2026-09-08 15:41'
labels:
  - integration
dependencies: []
references:
  - SKT-0013
  - docs/k8s-monitoring-permutations.md
  - e2e/lab/permutations
  - signals/k8s.md
priority: medium
type: feature
ordinal: 152000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The deployment matrix explicitly says permutation 3 is not emitted even though SKT-0013 completed its capture/documentation scope. Consumers selecting the OTel Collector Prometheus-receiver path cannot safely substitute Alloy or the additive native-OTLP switch. Reuse the captured P3 evidence as the independent contract and implement the missing selectable emission path without reopening completed capture work.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An explicit validated blueprint selection represents P3's observed Prometheus-shaped metrics and OTLP log/event surface, with a documented supported-family scope and no silent fallback to permutation 1 or 4.
- [x] #2 Inventory/fidelity evidence proves positive P3 shape and absence of unintended duplicate/foreign collector lanes; the existing supported permutations keep their contracts.
- [x] #3 Generated schema and the deployment/emission docs show P3 support with an independently deployable synthetic example and exact provenance; no live customer identifiers enter fixtures.
- [x] #4 Supported emitted families reproduce the captured name and label-key projection and OTLP log resource/record attributes, with mixed switches rejected. Document RW2 metric compatibility and RW1 wire reproduction as a deliberate non-goal.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Add fail-first package tests for the P3 selector, exclusive conflicts, 142-family projection, collector labels, histogram renaming, and OTLP event/pod-log shape.
2. Extend k8scluster Config/New/Signals with an explicit OTelCollectorProm mode and reject native OTLP metrics, Operator remote-write, Alloy/default allow-list, and other mixed lanes when selected.
3. Add a dedicated P3 Tick branch/state projection that reuses existing substrate emitters, removes source labels, adds the observed Collector scope labels, restricts the four captured jobs and 142 supported families, and projects histogram names to the Collector contract.
4. Add P3 OTLP event and pod-log builders from the pinned corpus shape, preserving resource and record attributes and transport ownership.
5. Run gofmt and go test ./internal/construct/k8scluster; leave resolver, catalog, schema, docs, and shared stamping to the owning integration lane.

Wave 2026-09-14: implement all seventeen missing captured P3 families in the existing k8scluster envelope. Root adds sourced signal rows, regenerates once, verifies 142/142 families, no foreign metric family and unchanged two log sources, then gates and integrates. RW1 wire reproduction is a deliberate non-goal; the existing RW2 sink already carries classic and native histograms.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
L3 stopped before edits under the frozen construct-boundary rule. P3 uses the existing cluster fixture and resolver registration; its KSM, cAdvisor, node-exporter and kubelet families already belong to k8scluster. A new otelcolprom package would duplicate identity/state or cross-import another construct. No tests or lab run occurred. Resume by authorizing an existing-construct collector-path projection and deciding its explicit conflicts with additive native-OTLP and Alloy-specific monitoring switches; no P3 emission is claimed.

Root completed the interrupted implementation lane. Integration 4dbe491 provides the exclusive collector selector, generated schema, reference blueprint, 125 emitted target families out of 142 observed, no foreign metric family, and both OTLP log sources. The 17 absent target families remain coverage gaps. The metric sink remains RW2; exact RW1 wire reproduction is outside this completed envelope scope. P3 alone compared with its retained metric/log corpus: 265 raw findings, zero unexempted contradictions, zero unmatched producers before fresh reprojection. Source tests preserve string log bodies while encoding structured Event maps. This updates the broader transport criterion to the explicit delivery scope, rather than asserting RW1 compatibility.

Historical 125/142 acceptance remains recorded above. This authorized extension closes every remaining captured family rather than changing or dropping the capture.

Wave complete at 2b964945ddec8b4584091aafd0769dbf23519c75: 17 added families; 142/142 collapsed families, 152 raw components, zero missing/foreign, both log contracts unchanged. Capture-derived le tests failed first and passed after P3-only bare formatting; non-P3 storage behavior preserved. Full exact-source just check passed. Local E2E passed once at preceding edc985d, before formatting correction. RW1 is a deliberate non-goal. P3 comparison: 143 raw, zero unexempted, zero unmatched; not full envelope proof. Evidence in codex/scratch/wave-2026-09-14/: p3-final-proof.txt, p3-exact-le-fail-first.log, p3-exact-le-pass.log, p3-exact-le-sha-check.log.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Envelope implementation integrated in4dbe491; producer-aware fixture compatibility in3ee1ef8; both P3 corpus documents reprojected from retained old and fresh raw candidates and promoted in1f759d8. Supported reference output:125of142captured metric families,17explicit coverage gaps,0foreign metric families,2OTLP log sources. Producer-attributed P3-only comparison:148rawfindings,0unexemptedcontradictions,0unmatched. Each integration tree passed just check and required reviews. The single just e2e run passed at5fede235cd9c37aaf6bdb69ec28194536698439b; chart/published-image opt-ins were skipped. Metrics remainRW2, not the capturedRW1encoding; that explicit compatibility limit is part of the revised supported scope. No source deployment is claimed.

Supported P3 scope: 125 of 142 captured metric families, 17 coverage gaps, zero foreign metric families and two OTLP log sources. Source implementation is 4dbe491, producer-aware test compatibility is 3ee1ef8, and corpus promotion is 1f759d8. The P3-only comparison has 148 raw findings, zero unexempted contradictions and zero unmatched producers. Local gates passed. The single end-to-end run passed at 5fede235cd9c37aaf6bdb69ec28194536698439b, with chart and published-image opt-ins skipped. RW2 metric compatibility is supported; captured RW1 wire reproduction is not implemented.

Wave complete at 2b964945ddec8b4584091aafd0769dbf23519c75: 17 added families; 142/142 collapsed families, 152 raw components, zero missing/foreign, both log contracts unchanged. Capture-derived le tests failed first and passed after P3-only bare formatting; non-P3 storage behavior preserved. Full exact-source just check passed. Local E2E passed once at preceding edc985d, before formatting correction. RW1 is a deliberate non-goal. P3 comparison: 143 raw, zero unexempted, zero unmatched; not full envelope proof. Evidence in codex/scratch/wave-2026-09-14/: p3-final-proof.txt, p3-exact-le-fail-first.log, p3-exact-le-pass.log, p3-exact-le-sha-check.log.
<!-- SECTION:FINAL_SUMMARY:END -->
