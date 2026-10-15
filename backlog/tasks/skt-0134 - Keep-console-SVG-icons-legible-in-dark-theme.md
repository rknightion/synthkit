---
id: SKT-0134
title: Keep console SVG icons legible in dark theme
status: To Do
assignee: []
created_date: '2026-10-15 11:47'
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
- [ ] #1 Affected SVG icons are visibly legible in both light and dark themes using the approved token contract.
- [ ] #2 Browser verification covers navigation and action icons at desktop and mobile widths.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
