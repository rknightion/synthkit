---
id: SKT-0115
title: Resolve skcapture Trivy publication failures on both platforms
status: To Do
assignee: []
created_date: '2026-10-09 14:36'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 237000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
At board-only main edf7243dc6742373da1c3feb8b3e72407e5c5df2, publish run37943032397 built both skcapture images then failed Enforce Trivy security gate. The gate reported scanner error or unaccepted HIGH/CRITICAL findings but the inspected reduction did not distinguish them. This is independent of the HA lifecycle CI failure and no application code changed in that push.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Exact failing run scanner/SARIF evidence distinguishes scanner errors from specific unaccepted findings for amd64 and arm64.
- [ ] #2 Safe repair yields both skcapture publication legs green at an exact source SHA without ignored HIGH/CRITICAL findings, lowered severity or weakened security gate.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
