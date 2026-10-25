---
id: SKT-0187
title: Render time.Duration blueprint fields as durations in the generated schema
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T07:09:00Z'
updated_date: '2026-10-25T10:03:00Z'
labels: []
dependencies: []
priority: low
ordinal: 381000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
BLUEPRINT-SCHEMA.md lists solace drain_schedule.period/window/phase (time.Duration fields decoded from strings such as 1h) as type int, which misleads authors. Teach internal/blueprintschema to render time.Duration as a duration string type, then regenerate.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 time.Duration fields render as a duration type in BLUEPRINT-SCHEMA.md and fielddocs.json
- [x] #2 No other schema row changes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58: landed (landings 5-7), gates gate-2 pass, hosted CI green 0cb0310.
<!-- SECTION:NOTES:END -->
