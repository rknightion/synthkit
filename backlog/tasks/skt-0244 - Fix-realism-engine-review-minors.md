---
id: SKT-0244
title: Fix realism engine review minors
status: To Do
assignee: []
created_date: '2026-10-26T11:40:00Z'
labels:
  - realism
dependencies: []
priority: medium
ordinal: 450000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
From the composed realism review: (1) batch release applies current live fault and volume state to every past tick of the window (internal/workload/app/loadprofile.go ~233-238), contradicting docs/workloads.md; (2) item-stock backing slice is not bounded by MaxStockItems when a long-tail oldest item pins the head (derived.go ~321,365); (3) the load-time count_of bound ignores the batch multiplier (graph.go ~263-268); (4) docs/blueprint-reference.md lacks count_of and count_of_service rows.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
