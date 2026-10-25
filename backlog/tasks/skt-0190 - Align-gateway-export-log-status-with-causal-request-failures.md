---
id: SKT-0190
title: Align gateway export log status with causal request failures
status: To Do
assignee: []
created_date: '2026-10-25T08:00:00Z'
labels: []
dependencies: []
priority: medium
ordinal: 384000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
In acme-ai-platform the Portkey export logs still say status 200 on requests that failed with a causal release 500. Derive the export log status from the request outcome where the gateway hop is on the failing path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Gateway export logs carry the failing status when the request failed through them
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
