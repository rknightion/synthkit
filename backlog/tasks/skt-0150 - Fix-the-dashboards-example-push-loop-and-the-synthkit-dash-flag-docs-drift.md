---
id: SKT-0150
title: Fix the dashboards example push loop and the synthkit-dash flag docs drift
status: Done
assignee: []
created_date: '2026-10-17 21:07'
updated_date: '2026-10-17 23:26'
labels:
  - dashboards
  - docs
dependencies: []
priority: low
ordinal: 273000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
dashboards/examples/README.md's push loop skips only *-dashboards-folder.json, so it pushes *-rules.json and the panel inventory as dashboards (already true at base), and with synthkit-dash -namespace a hashed namespaced folder file no longer matches the skip pattern. docs/tools.md documents a -verification-out flag that synthkit-dash does not define.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The documented push loop pushes only dashboard JSON: folder, rules and inventory files are skipped for both legacy and namespaced output
- [x] #2 docs/tools.md lists only flags synthkit-dash defines; just docs-check passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed 6d8304a (loop50 BATCH5). Main CI green on 1d627bf.
<!-- SECTION:NOTES:END -->
