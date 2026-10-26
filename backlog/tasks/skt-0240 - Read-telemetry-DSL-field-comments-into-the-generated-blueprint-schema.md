---
id: SKT-0240
title: Read telemetry DSL field comments into the generated blueprint schema
status: To Do
assignee: []
created_date: '2026-10-26T08:25:00Z'
labels:
  - realism
dependencies: []
priority: medium
ordinal: 446000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
internal/telemetryspec is not in the schema generator's source directories, so every custom-telemetry DSL field (stock_of, when, dwell_seconds, now, source_read, shape.batch and older fields) has an empty description in BLUEPRINT-SCHEMA.md and fielddocs.json. Add the package and review the regenerated descriptions.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
