---
id: SKT-0228
title: >-
  Derive only the deterministic correlation SpanID when the app discards the
  other fields
status: In Progress
assignee: []
created_date: '2026-10-26T01:40:00Z'
updated_date: '2026-10-26T02:18:00Z'
labels:
  - performance
dependencies: []
priority: medium
type: enhancement
ordinal: 434000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
App mintRequest creates fresh correlation fields, then calls ledger.NewCorrelationFromSeed solely to overwrite SpanID. The pure full deterministic call derives eight fields and discards seven. This predates the causal stack; it is not a newly introduced regression or proven cause of the app package timeout. Remove that demonstrated redundant derivation without changing the exact existing SpanID seed/domain, any fresh correlation field or shape RNG consumption. Existing SpanIDFromSeed uses different framing and must not be substituted blindly. Capture an unchanged baseline before optimizing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A narrow ledger-owned SpanID derivation exactly matches the unchanged full deterministic correlation reference across discriminating seeds and pre-change vectors; a wrong-domain negative control fails
- [ ] #2 The public app minter preserves exact deterministic root SpanID binding, fresh other join keys and engine draws; focused real-path tests and before/after CPU/allocation evidence are retained, with no unmeasured whole-package budget claim
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root freezes a narrow ledger-owned CorrelationSpanIDFromSeed(seed string) string seam with exactly the old full-correlation SpanID framing seed plus NUL span, length eight bytes. Keep NewCorrelationFromSeed unchanged as an independent reference, keep all fresh join keys and shape draws unchanged, and change only the app call that currently discards seven deterministic fields. Capture before/after cost and real minter compatibility; no package-budget completion inferred.
<!-- SECTION:NOTES:END -->
