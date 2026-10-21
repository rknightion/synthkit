---
id: SKT-0157
title: >-
  Preserve native generation part kind when captured content is intentionally
  empty
status: To Do
assignee: []
created_date: '2026-10-21T03:14:00Z'
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
- [ ] #1 The native public domain representation and actual encoded payload retain the sourced part kind when permitted content is empty, including metadata-only thinking, without inventing placeholder content or changing ordinary legacy payloads.
- [ ] #2 A real encoder-boundary regression check is red for the current empty-thinking mismatch and green after correction; metadata-only content remains absent and existing part variants remain correctly encoded.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
