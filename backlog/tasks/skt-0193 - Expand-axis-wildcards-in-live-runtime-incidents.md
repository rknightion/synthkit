---
id: SKT-0193
title: Expand axis wildcards in live runtime incidents
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T10:48:00Z'
updated_date: '2026-10-25T11:37:00Z'
labels: []
dependencies:
  - SKT-0191
priority: high
ordinal: 394000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A live control-plane runtime incident with an <axis>:* target is never expanded: the live closure emits the literal scope (e.g. resource:*) and the shape engine matches scopes exactly, so it moves nothing (found by the SKT-0191 lane with a throwaway test). Scheduled incidents, scenario effects and ad-hoc failures do expand. Expand runtime wildcards with the same helper as load time, on every axis.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A live <axis>:* incident moves every target on that axis (resource:* only the mode's kind), red then green
- [ ] #2 Exact-target live incidents unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
