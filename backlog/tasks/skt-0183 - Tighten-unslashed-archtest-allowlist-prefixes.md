---
id: SKT-0183
title: Tighten unslashed archtest allowlist prefixes
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T02:43:00Z'
updated_date: '2026-10-25T08:23:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 375000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestCatalogImportIsolation matches allowedPrefixes by string prefix; eight entries lack a trailing slash, so a package path that merely shares a prefix with an allowed entry passes with no allowlist change. Make those entries exact-match or slash-terminated without changing which current packages pass.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A package whose path only shares a prefix with an allowed entry is rejected, proven red then green
- [x] #2 Every current catalog package still passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58: landed fcf4443; CI green 5007f33.
<!-- SECTION:NOTES:END -->
