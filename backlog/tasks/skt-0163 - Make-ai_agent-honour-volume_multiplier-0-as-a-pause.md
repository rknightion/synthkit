---
id: SKT-0163
title: Make ai_agent honour volume_multiplier 0 as a pause
status: Done
assignee: []
created_date: '2026-10-22T04:24:00Z'
updated_date: '2026-10-22T05:55:00Z'
labels:
  - control
dependencies: []
priority: medium
ordinal: 306000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0147 (loop52) made volume 0 pause web_service and app via shape.Engine.Paused(). internal/workload/aiagent mints from sessions_per_min and never reads the volume multiplier, so it keeps emitting at 0 (docs/control-plane.md states this). Owner ruling: 0 means pause.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 At volume 0 ai_agent emits no sessions or generations; at any nonzero value output is byte-identical
- [x] #2 docs/control-plane.md no longer lists ai_agent as an exception
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop52 BATCH9: landed 74d8af9. Pause red at base (120/600 conversations at 0), green; volumes 0.5/1/2 byte-identical; CodeRabbit 4 files 0; gate gaterun 20261022T054442Z-synthkit-7ab24aab exit 0 on 445795e.
<!-- SECTION:NOTES:END -->
