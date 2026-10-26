---
id: SKT-0241
title: Seed item stocks from the arrival rate over the residence window
status: To Do
assignee: []
created_date: '2026-10-26T08:56:00Z'
labels:
  - realism
dependencies: []
priority: high
ordinal: 447000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A whole-item stock (stock_of with residence) seeds its first-tick population from the first tick's arrival rate times mean residence, so a restart at the daily peak or trough seeds far above or below steady state and takes about 1.5 residence medians to converge. Seed from the arrival curve integrated over the residence window before the first tick instead, so a stock fed by a diurnal curve starts near its steady state at any restart time.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
