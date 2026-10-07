---
id: SKT-0103
title: Refresh the control console design and extract shared design tokens
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
updated_date: '2026-10-07 21:28'
labels:
  - ui
  - design
dependencies: []
ordinal: 225000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The console is due a design pass and should share design tokens with other frontends that embed or sit beside it, so they converge on one look. Run a design pass, then extract colour, type, spacing and component tokens into a small package the console consumes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Tokens extracted and consumed by the console with no behaviour change
- [ ] #2 Design pass reviewed and approved by the maintainer
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-07: a design pass covering the console and an adjacent frontend was produced and approved by the maintainer; AC #2 is met once the token extraction lands against it.
<!-- SECTION:NOTES:END -->
