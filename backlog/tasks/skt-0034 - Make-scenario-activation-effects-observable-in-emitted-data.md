---
id: SKT-0034
title: Make scenario activation effects observable in emitted data
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 125000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios E1-E3 activated all 14 schema-declared scenarios successfully, but the bounded live read did not show the declared effects, ambient intervals, or environment-scoped sibling separation. HTTP 200 control state is not sufficient evidence that a scenario changed telemetry.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every declared scenario has a queryable emitted-data assertion tied to its documented effect
- [ ] #2 Ambient incident effects appear and clear on their declared intervals
- [ ] #3 An environment-scoped failure changes only the targeted environment while a sibling remains at baseline
- [ ] #4 The acceptance harness activates every schema-derived scenario ID and continues after individual failures
- [ ] #5 Deactivation proves active effects clear without misreading cumulative counters
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
