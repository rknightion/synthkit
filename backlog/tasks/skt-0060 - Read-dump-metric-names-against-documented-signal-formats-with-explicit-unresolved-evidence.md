---
id: SKT-0060
title: >-
  Read dump metric names against documented signal formats with explicit
  unresolved evidence
status: Done
assignee:
  - '@codex'
created_date: '2026-09-08 00:13'
updated_date: '2026-09-08 01:35'
labels:
  - corpus
dependencies: []
type: feature
ordinal: 156000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The scratch conformance reader leaves 2330 names unresolved because it cannot parse prose declarations, dynamic expansions and native OTLP documentation. That uncertainty must be measured separately from proven synth defects.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A supported command and tested reader handle the documented YAML, prose and expansion formats without changing emitters or catalogue entries.
- [x] #2 An explicit-selection dump measurement reports resolved and unresolved names and every named parse gap, with limits on label and envelope claims.
- [x] #3 Focused tests, the integrated gate and CodeRabbit review complete before source integration.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Promote the reader into a tested package, pin format cases with fail-first tests, measure actual dump output, wire the just entrypoint and review the bounded batch.

1. Parse every fenced `yaml signals` block with YAML v3, including multi-document blocks, recoverable malformed mappings, CloudWatch stats/info expansions, and native OTLP declarations.
2. Parse prose tables and backtick/brace alternatives into transport-specific documented names while recording each named unsupported format as a parse gap.
3. Parse the stable `-dump` sections with sorted label or attribute keys, compare names by Prometheus and OTLP transport, and expose resolved, unresolved, and gap evidence with explicit scope limits.
4. Add a small CLI under `internal/conformance` and a just recipe that reads `signals/` and stdin without mutating emitters or catalogue files.
5. Run focused tests, the explicit-selection dump measurement, and the proportionate repository checks; review the final diff before task finalization.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Final integration selection adds the P3 reference blueprint: 2973 resolved of3347 names,374 unresolved and8parse gaps. The earlier2972/3346 observation remains the measurement before that selection addition.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Integrated in 4dbe491. Reader command: just signals-conformance dump.txt. Explicit-selection measurement resolves2972 of3346 metric names, leaving374 unresolved and8named YAML parse gaps. Name evidence only; dumped label keys are retained but not compared, and unresolved names are not proven emitter defects. Focused tests and isolated integrated just check passed. CodeRabbit minor indentation finding fixed; no remaining finding. No blueprint schema change was required by the reader.
<!-- SECTION:FINAL_SUMMARY:END -->
