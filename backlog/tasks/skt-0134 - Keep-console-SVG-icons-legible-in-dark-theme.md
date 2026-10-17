---
id: SKT-0134
title: Keep console SVG icons legible in dark theme
status: Done
assignee: []
created_date: '2026-10-15 11:47'
updated_date: '2026-10-17 23:26'
labels: []
dependencies: []
references:
  - internal/control/ui/src
priority: low
type: bug
ordinal: 257000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Browser verification during the approved console token extraction found existing black SVG img icons remain black on dark surfaces. The token and layout work preserves these assets; inspect affected navigation/action icons and correct their theme rendering without changing control behavior.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Affected SVG icons are visibly legible in both light and dark themes using the approved token contract.
- [x] #2 Browser verification covers navigation and action icons at desktop and mobile widths.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed 87f4be5 (loop50 BATCH3). Browser check by root via headless Chrome, light and dark at 1440 and 390 px. Main CI green on 1d627bf (rc v1.6.0-rc.202).
<!-- SECTION:NOTES:END -->
