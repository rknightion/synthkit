---
id: SKT-0233
title: Model ledger-driven stocks that accumulate and drain
status: To Do
assignee: []
created_date: '2026-10-26T07:05:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 439000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A stock value source counts whole requests from arrivals and completions, with count and oldest-item age. A stall grows the stock and recovery drains it, instead of flat or resetting stand-ins.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
