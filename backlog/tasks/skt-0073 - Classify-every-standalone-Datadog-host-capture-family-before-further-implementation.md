---
id: SKT-0073
title: >-
  Classify every standalone Datadog host capture family before further
  implementation
status: Done
assignee:
  - '@codex'
created_date: '2026-09-09 23:37'
updated_date: '2026-09-10 00:04'
labels: []
dependencies: []
priority: medium
type: docs
ordinal: 169000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The standalone host artifact observes 281 families but only the example family is implemented. A per-family evidence verdict is required before honest breadth work.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 All 281 unique families have categories and envelope evidence under the Kubernetes verdict rules
- [x] #2 Shared-name classification differences and least-confident families are explicitly justified
- [x] #3 No host emission, capture shape or comparison policy changes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
L4 proposes the verdict in scratch; root verifies complete coverage and rules, integrates JSON and records the outcome.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root independently verified all 281 unique families and all 300 envelope semantics and placements against the immutable host artifact, with gauge missing temporality represented as empty string per existing verdict. Counts: 1 declaration-backed, 0 implemented fixtures, 155 fixture candidates, 125 Agent descriptors needed. All 31 shared-name differences are Kubernetes-only implementations; the 75 host network candidates still require source-grounded mechanics before implementation. Proposed JSON applied unchanged.

Landed verdict in 0ac777b5fd87c14e836d496402844aa556968eea. Root verified every family and all 300 native shapes against the immutable host artifact. JSON digest 4a0ca98bff12a9663ac5ee7951bb80113b73a414b5aac150fed41c96ab462ffc. Root just check passed with the verdict present; just dump shows unchanged OTLP metric inventory. just gen, unit tests and CodeRabbit were skipped for this JSON documentation-only change; no generated schema input changed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed at 0ac777b5fd87c14e836d496402844aa556968eea: 281 families classified as 1 declaration-backed, 0 implemented fixtures, 155 fixture candidates and 125 descriptor-needed. All 31 shared-name divergences are explicit Kubernetes-only implementations. The 75 host-only system.net candidates need value sourcing before any implementation. No host breadth emission or corpus shape change. Evidence: codex/scratch/wave-2026-09-18/l4/evidence.md and l4-root-validation.txt.
<!-- SECTION:FINAL_SUMMARY:END -->
