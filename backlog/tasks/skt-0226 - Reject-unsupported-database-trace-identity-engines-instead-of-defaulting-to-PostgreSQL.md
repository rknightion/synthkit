---
id: SKT-0226
title: >-
  Reject unsupported database trace identity engines instead of defaulting to
  PostgreSQL
status: In Progress
assignee: []
created_date: '2026-10-26T00:25:00Z'
updated_date: '2026-10-26T01:09:00Z'
labels:
  - causal
  - fidelity
dependencies: []
priority: medium
type: bug
ordinal: 432000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Source inspection confirms app graph dbSystemName defaults every unknown engine to postgresql, while resolveDBIdentities accepts any resolved database fixture. A DocumentDB db_instance can therefore emit false PostgreSQL span identity. The reference migration omits that optional identity join as a temporary limitation; real causal database bindings are separate and must remain intact. Correct unsupported-engine behavior fail-closed, or add only sourced engine mappings, without inventing vendor identifiers.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Public blueprint loading or build rejects an unsupported db_instance engine rather than emitting PostgreSQL identity; red then green regression crosses the actual app boundary
- [ ] #2 Supported PostgreSQL, MySQL and Redis identities and causal database bindings remain unchanged; any added mapping is sourced in the canonical signal contract
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Admit the independently recorded generic fail-closed bug only: reject unsupported db_instance engines rather than emitting false PostgreSQL identity. No new DocumentDB mapping, call-occurrence API, additive timing, reference migration or owner-park reopening is authorized. Existing supported mappings and actual build boundary must be preserved.
<!-- SECTION:NOTES:END -->
