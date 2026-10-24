---
id: SKT-0180
title: Do not fail readiness on quiet request-driven delivery lanes
status: To Do
assignee: []
created_date: '2026-10-24T18:18:00Z'
labels: []
dependencies: []
priority: high
ordinal: 353000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Readiness requires every expected delivery lane to have a fresh success, including request-driven lanes (logs, traces) that legitimately emit nothing in low-traffic periods. A realistic weekend-evening blueprint reports delivery_stale/delivery_not_attempted while metrics flow. The delivery manifest already classifies lanes as scheduled vs request-driven; readiness should require freshness only for scheduled lanes and still fail request-driven lanes on current errors.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
