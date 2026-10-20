---
id: SKT-0154
title: >-
  Add a generic app custom-metrics mechanism for blueprint-declared business
  metrics
status: To Do
assignee: []
created_date: '2026-10-20T22:20:00Z'
labels: []
dependencies: []
priority: low
type: feature
ordinal: 287000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Blueprints cannot declare app-defined business metrics (counters, histograms) with names they own. Useful for in-app metrics such as review outcomes or grounding ratios when content capture is off. Names live only in the blueprint and cite their source; the catalog invents none.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A blueprint can declare a custom counter or histogram with labels and a source citation
- [ ] #2 Load rejects a declared name without a source
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
