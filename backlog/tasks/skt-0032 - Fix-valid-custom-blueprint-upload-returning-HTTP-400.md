---
id: SKT-0032
title: Fix valid custom blueprint upload returning HTTP 400
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 123000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenario D3 confirmed invalid uploads return a diagnostic, but posting a documented namespaced copy of a shipped valid blueprint returned HTTP 400. A first-time operator cannot distinguish a bad request shape from a valid blueprint rejected by the upload path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A documented valid custom blueprint upload succeeds and applies
- [ ] #2 An invalid custom blueprint still returns a diagnostic naming the schema or semantic problem
- [ ] #3 Focused tests reproduce the valid HTTP 400 before the fix and cover both valid and invalid requests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
