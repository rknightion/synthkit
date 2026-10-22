---
id: SKT-0157
title: >-
  Preserve native generation part kind when captured content is intentionally
  empty
status: Done
assignee: []
created_date: '2026-10-21T03:14:00Z'
updated_date: '2026-10-22T04:24:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 296000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The current native Part representation and mapper infer thinking from nonempty content. After metadata-only stripping, an empty thinking part is encoded as Part_Text instead of the pinned SDK Part_Thinking shape. An actual local HTTP/protojson probe demonstrates the mismatch. The new app pipeline therefore fails closed for selected metadata-only thinking instead of leaking content or silently changing kind. Support should be explicit and backward-compatible, not inferred from placeholder content or unsourced provider strings.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The native public domain representation and actual encoded payload retain the sourced part kind when permitted content is empty, including metadata-only thinking, without inventing placeholder content or changing ordinary legacy payloads.
- [x] #2 A real encoder-boundary regression check is red for the current empty-thinking mismatch and green after correction; metadata-only content remains absent and existing part variants remain correctly encoded.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop52 BATCH3: landed bb31a00. l157 candidate 8dea6340 (repair 1: json omitempty after rv157 M1); rv157b delta PASS (84/84 usage identical); CodeRabbit 5 files 0; gate gaterun 20261022T035227Z-synthkit-3c66e408 exit 0 on ba5d29a. Metadata-only producer follow-up: SKT-0160.
<!-- SECTION:NOTES:END -->
