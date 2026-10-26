---
id: SKT-0227
title: >-
  Remove quadratic emitted-log lookup from app proofs without changing their
  coverage
status: In Progress
assignee: []
created_date: '2026-10-26T01:06:00Z'
labels:
  - performance
dependencies: []
priority: high
type: bug
ordinal: 433000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The causal stack passes plain tests but the app race package exceeds its unchanged 600-second budget. Static inspection shows the real-output test helper rescans every service log for every request, and handled-worker checks add another full scan. This is redundant lookup work, not redundant assertions; its material share of the current exact-source failure is not yet proven. Establish a narrow baseline, index the actual captured logs while retaining every match and assertion, and verify the original package budget without reducing populations or weakening the gate.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Indexed lookups return exactly the original full-scan corpus, preserving service/span membership, structured-metadata precedence including explicit empty values, all duplicate matches and emission order; a discriminating negative control detects omitted eligible rows
- [ ] #2 Existing app proofs retain their complete cases, seeds, request counts, emitted surfaces, assertions and 600-second race timeout; the exact repaired app race package passes within that unchanged budget
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
