---
id: SKT-0036
title: Make generated dashboards self-contained and panel-verifiable on a fresh stack
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 01:39'
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
- [x] #1 Dashboard generation or push creates its required folder on a bare stack
- [x] #2 Every generated panel resolves an explicit or deterministic datasource when multiple same-type datasources exist
- [x] #3 The verification path enumerates every panel as rendered, empty, or errored and reports its query
- [x] #4 Empty panels are classified as emission gap, query defect, or missing datasource
- [x] #5 At least one native-histogram panel is generated and its live render is verified
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check </dev/null (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen </dev/null (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump </dev/null — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane D makes generated dashboards folder- and datasource-complete with panel inventory; root pushes, snapshots, inspects, and classifies live panels.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: one generated folder and 15 dashboards validated, dry-ran, and pushed with explicit datasource mappings. The verifier produced 1,351 query observations with zero empty ref IDs and zero duplicate keys. A native-histogram quantile returned data and panel 798 rendered p50/p95/p99 lines.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Generated dashboards are folder-complete, datasource-explicit, panel-verifiable, and live-proven for a native histogram.
<!-- SECTION:FINAL_SUMMARY:END -->
