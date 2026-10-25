---
id: SKT-0189
title: Assert causal resource modes in the live scenario check
status: In Progress
assignee:
  - '@loop58-root'
created_date: '2026-10-25T08:00:00Z'
updated_date: '2026-10-25T08:57:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 383000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The live scenario check has no assertion for dependency_slow or rule_regression, so migrated scenarios (acme-ai-platform backend_latency_storm, assist_outage) report no covering assertion, regressing the coverage SKT-0034 delivered. Add assertions for the resource-axis modes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each resource-axis mode used by a shipped scenario has a live-check assertion
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
