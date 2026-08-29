---
id: SKT-0036
title: Make generated dashboards self-contained and panel-verifiable on a fresh stack
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
updated_date: '2026-08-29 19:22'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: feature
ordinal: 127000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios G2-G5 pushed and snapshot-rendered two generated dashboards only after an undocumented gcx setup. The path produced dashboard PNGs rather than a panel-by-panel rendered/empty inventory, generated resources had no explicit datasource references despite multiple same-type datasources, no native-histogram panel could be identified, and the target folder had to exist before generation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Dashboard generation or push creates its required folder on a bare stack
- [ ] #2 Every generated panel resolves an explicit or deterministic datasource when multiple same-type datasources exist
- [ ] #3 The verification path enumerates every panel as rendered, empty, or errored and reports its query
- [ ] #4 Empty panels are classified as emission gap, query defect, or missing datasource
- [ ] #5 At least one native-histogram panel is generated and its live render is verified
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check </dev/null (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen </dev/null (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump </dev/null — inventory diffed against signals/
<!-- DOD:END -->
