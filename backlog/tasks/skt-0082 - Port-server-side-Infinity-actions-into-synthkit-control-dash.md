---
id: SKT-0082
title: Port server-side Infinity actions into synthkit-control-dash
status: Done
assignee:
  - '@rob'
created_date: '2026-09-27 10:19'
updated_date: '2026-09-27 19:36'
labels: []
dependencies: []
ordinal: 178000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
GitHub issue #172: a contributor runs workshops where the synthkit control plane is ClusterIP only, so browser fetch actions cannot reach it. Grafana infinity-type actions send the POST server-side through the Infinity datasource (optionally over PDC). The contributor click-verified them on Grafana 13.3 with the vizActionsAuth feature toggle; the current dashboard/panels.go comment says infinity actions never fire, which predates that evidence. Source to port: fork commit 4811a47ae8266d046ea7ae664739927cb9eb451c, rebased onto current main, credited with Co-authored-by.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 synthkit-control-dash accepts -action-mode fetch|infinity and -ds-uid; infinity mode without -ds-uid or -write-base-url fails validation with a clear error
- [x] #2 In infinity mode every button is a type infinity action carrying the datasource UID, POST method, absolute URL and JSON content-type header, and no fetch action remains
- [x] #3 Regenerating dashboards/examples/control/synthkit-customer-control.json with default flags produces no diff
- [x] #4 dashboard/panels.go and docs/control-plane.md state the vizActionsAuth requirement and that infinity mode lets anyone who can use the datasource and view the dashboard mutate the control plane
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop42: port Infinity action mode with test-first proof, default JSON parity, offline gate and security review before root landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop42: implementation 1/4 consumed, review-repair 2/3, infrastructure retries 0, grants none. Candidate e87cba15b9a4eeb752cb443979f9280e14dd23d2 passed local gate and CodeRabbit, but mandatory S1 security review could not be dispatched because the collaboration tool failed repeatedly. No root landing, push or CI. Resume by dispatching S1 on that exact SHA and assessing the HTTPS-only URL against ClusterIP PDC before landing.

loop43: resumed; L1r owns HTTP URL repair, gate and CodeRabbit before security review.

loop44: resumed

loop44: implementation 2/4, review-repair 3/3 plus round-4 grant unused, infrastructure retries 0. Candidate c4ee56a passed focused tests, just check, gen-check, spdx-check, docs-check, default JSON cmp and CodeRabbit complete; S1 security review clean. Landed on main 0ea3ff2 with ci run 36344319599 completed success. just dump ran on the landed SHA; no app emission code changed. DoD 2 is conditional and not applicable because no blueprint/config/skill schema changed. Live Grafana import and execution remain unverified by the frozen offline scope.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Ported Infinity control actions with co-author credit, repaired base URL validation and documented datasource authority. Offline checks, clean security review and ci run 36344319599 on 0ea3ff2 passed.
<!-- SECTION:FINAL_SUMMARY:END -->
