---
id: SKT-0072
title: >-
  Replace frozen Datadog receiver system values with grounded deterministic
  mechanics
status: Done
assignee:
  - '@codex'
created_date: '2026-09-09 23:37'
updated_date: '2026-09-10 00:04'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 168000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The shipped Kubernetes receiver emits lifetime-flat idle CPU, zero context switches and unused memory despite modelling a running Agent and collector. Artifact envelopes are correct; captured values are elided and cannot establish plausibility.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every changed value has a stated real-world basis and deterministic bounded mechanics, with ungrounded families explicitly identified
- [x] #2 CPU and memory arithmetic, monotonic accumulation, determinism and negative controls pass with failing-before evidence
- [x] #3 Captured envelopes and standalone host behavior remain unchanged; inventory and final gates pass
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
L1 owns receiver mechanics and focused proof; root reviews grounding, applies documentation proposals, compares inventory and runs final gates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root independently verified current Agent CPU formulas: system includes IRQ+softIRQ and interrupt repeats them; guest is also outside the denominator. The seven named percentages sum to 100 plus interrupt plus guest, not 100. Goal acceptance corrected under delegated authority to source-faithful overlap and disjoint elapsed-time accounting. Grounded zero modes and fixed declared hardware capacity must not be made artificially variable. High materiality; envelopes and frozen seams unchanged.

Landed receiver mechanics and documentation in 33f4cf42bd8ec615a2846fe6e31d888078b69459. Same-producer baseline grounding and corrected CPU overlap arithmetic are retained in codex/scratch/wave-2026-09-18/l1/source-and-baseline.md. Failing-before, passing-after, receiver separation, band and envelope controls passed. CodeRabbit complete with one minor core-set test gap, fixed and package retested (0.507s); no findings left. Root just dump confirmed the entire OTLP metric section unchanged; unrelated time-sampled inventory differences retained separately. Root integrated just check passed, including fidelity with only the existing exemption and producer bound 203. just gen is not applicable: no blueprint field, config struct or skill changed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Completed at source commit 33f4cf42bd8ec615a2846fe6e31d888078b69459. Replaces lifetime-flat busy CPU, context switches, load and memory with deterministic bounded observations; preserves fixed hardware capacity, observed-zero modes and all native envelopes. Host mode remains one family. Evidence: codex/scratch/wave-2026-09-18/l1/, coderabbit-receiver.ndjson, inventory-comparison.txt and check-precommit.log. Final exact-SHA CI is recorded in the campaign report after tracker reconciliation.
<!-- SECTION:FINAL_SUMMARY:END -->
