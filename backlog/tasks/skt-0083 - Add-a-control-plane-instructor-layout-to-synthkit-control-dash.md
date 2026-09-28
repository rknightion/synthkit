---
id: SKT-0083
title: Add a control-plane instructor layout to synthkit-control-dash
status: Done
assignee: []
created_date: '2026-09-27 10:19'
updated_date: '2026-09-28 07:10'
labels: []
dependencies:
  - SKT-0082
ordinal: 179000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
GitHub issue #172: workshop instructors need one console that starts and stops each scenario, shows current state and delivery health, and shows the live impact on affected services. The contributor built it as -layout control-plane in fork commit 7cd3b3bd971540b0dcd6e42b0f25721df92df61a on top of the infinity actions. Its impact charts query http_server_request_duration_seconds by service, which only nodes with the scraped HTTP server profile emit, so on most blueprints they render empty; that commit also edits a workshop blueprint that is not on main.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 synthkit-control-dash -layout control-plane renders a dashboard with status tiles from /control/state and /control/readiness, one Start/Stop card per enumerated scenario on /control/scenarios/activate and /deactivate, and a collapsed fleet-controls row
- [x] #2 -layout control-plane requires -action-mode infinity and the datasource UIDs it queries; missing values fail validation
- [x] #3 Each impact chart queries only a series family the affected node actually emits, derived from the blueprint rather than one hard-coded metric, and a chart generated for every scenario-targeted node of every shipped blueprint references an emitted family (checked against just dump output)
- [x] #4 No blueprints/ file changes and the default customer layout output is byte-identical
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop44: Infinity dependency landed at 0ea3ff2 with ci run 36344319599 completed success; L2 admitted.

loop44: implementation 1/4 consumed, review-repair 0/3, infrastructure retries 0, grants none. Candidate 1e4671a passed focused tests, just check, gen-check, spdx-check, default JSON parity and CodeRabbit complete zero findings. Root decision J3 excluded default-off spanmetrics from chart qualification. Landed at cd052575df6efb89485364a50e8e169a652e899a; ci run 36388020464 completed success. DoD 2 conditional and not applicable; no blueprint/config/skill schema changed. Live Grafana rendering and firing were excluded by the offline scope.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Added the control-plane instructor layout with emitted-family chart selection and co-author credit. Offline gates and ci run 36388020464 on cd052575 passed.
<!-- SECTION:FINAL_SUMMARY:END -->
