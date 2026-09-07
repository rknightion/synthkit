---
id: SKT-0059
title: >-
  Scope signal identity by reviewed producer, and fail loud when no producer is
  comparable
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-07 12:29'
updated_date: '2026-09-07 13:24'
labels:
  - corpus
dependencies: []
references:
  - docs/reality-corpus.md
  - internal/inventory/capture_v2_routing.go
  - codex/scratch/wave-2026-09-10/
priority: high
type: feature
ordinal: 155000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every corpus promotion is blocked behind the same 27 unexempted contradictions, and reading the rows shows they are not one defect. The comparator identifies a signal by metric family name alone, so it folds unrelated producers that happen to share a name into one comparison. `http_request_duration_seconds` is emitted by synthkits LLM gateway with api_key_name/model/provider/stream and by a Grafana component in the AWS capture with handler/method; the union difference is reported as a contradiction, but neither side is wrong and neither can be corrected into the other. The same category error produces the target_info, http_server_active_requests, http_server_request_duration_seconds, up and scrape_duration_seconds rows. A second group, the workqueue and scheduler families, is a real job-shape difference: synthkit models a pod-scoped scrape carrying container/controller/endpoint/namespace/pod while the capture is node-scoped carrying node/source. A third group looks like genuine synth defects: the pg_stat_* families stamp env and grafana_kubernetes_monitoring_build_info stamps source where no observed producer carries them. Scoping by producer must therefore not become a general escape: a synth signal whose producer matches no reality producer has to stay visible, or the third group is silently hidden and the fidelity gate stops meaning anything. The reviewed producer identity already exists on every capture-v2 family route, so the evidence needed is present and no new capture is required. Preserved candidates for the four blocked promotions are retained under codex/scratch/wave-2026-09-10/.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Signal identity for comparison is the pair of metric family and reviewed producer, not the family name alone, and the contract is written into docs/reality-corpus.md as an evidence rule
- [x] #2 A synth and reality claim under the same reviewed producer compares exactly as it does today, so no existing contradiction class is weakened
- [x] #3 A synth and reality claim under different producers is one named coverage gap that states both producers, never a contradiction and never silent absence
- [x] #4 A synth signal whose producer matches no reality producer in scope produces a distinct visible finding rather than passing, and that finding kind is exercised by a test using the pg_stat_* and grafana_kubernetes_monitoring_build_info shapes
- [ ] #5 The four preserved promotion candidates and the preserved control-plane read-back candidate reach zero unexempted contradictions under the new contract, with no exemption added and no route altered
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Test the frozen same-producer, different-producer, and no-comparable outcomes; preserve legacy evidence and all existing contradiction classes. Add a strict versioned count ratchet and separate report section. Measure the unchanged producer sets of every blocking family and compare each preserved or freshly projected candidate in scratch before corpus edits. Write the contract once, review, and integrate with exact residue if frozen identity cannot resolve a real contradiction.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Producer-scoped contract implemented with same-producer comparisons unchanged, named producer_mismatch gaps, separate no_comparable_producer report and strict versioned count ratchet initially 6. Test-first fixtures and just check passed. CodeRabbit completed with two major findings; regression tests reproduced duplicate-name counting and duplicate JSON-key overwrite, both fixed and targeted tests green. AC5 remains unproven: unexempted contradictions before/after Rancher 25/25, AWS 27/27, Azure 64/64, GCP 21/21, control-plane 10/10. Candidate evaluations use actual projection, CanonicalMerge and comparator in scratch; no corpus evidence file changed. Recorded identity is promrw for both modeled and observed collision families; it is not a job identity. Frozen routes cannot distinguish those jobs. Resume with reviewed producer/shape attribution authority or evidence-backed same-producer model corrections; do not suppress findings or add exemptions.
<!-- SECTION:NOTES:END -->
