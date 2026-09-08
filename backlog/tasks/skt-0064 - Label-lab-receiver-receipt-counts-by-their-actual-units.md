---
id: SKT-0064
title: Label lab receiver receipt counts by their actual units
status: Done
assignee: []
created_date: '2026-09-08 00:42'
updated_date: '2026-09-08 01:34'
labels: []
dependencies: []
ordinal: 160000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The matrix calls summed receiver receipt counts decoded requests, but those counters count protocol-dependent datapoints, series, records or spans. Correct report wording without changing acceptance predicates or measurements.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Reports label the count as receipt items and explain protocol-dependent units
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Integrated in 4dbe491. Matrix report labels receipt items with protocol-dependent units; zero decoded items no longer claims that no request reached the receiver. Existing report was regenerated from retained results, with result JSON byte-identical. Targeted checks and final CodeRabbit batch passed; integrated just check exit0.
<!-- SECTION:FINAL_SUMMARY:END -->
