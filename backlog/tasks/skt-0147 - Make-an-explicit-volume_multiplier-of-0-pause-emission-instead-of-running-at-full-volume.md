---
id: SKT-0147
title: >-
  Make an explicit volume_multiplier of 0 pause emission instead of running at
  full volume
status: To Do
assignee: []
created_date: '2026-10-17 17:43'
updated_date: '2026-10-18 02:52'
labels:
  - control
dependencies: []
priority: medium
ordinal: 270000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
POST /control/load accepts volume_multiplier 0 (validateState range 0-100) and Engine.SetVolumeMultiplier stores math.Float64bits(0), which Engine.VolumeMultiplier (internal/shape/shape.go) reads as unset and returns 1.0. The control store also normalises a persisted 0 back to 1.0 on reload (internal/control/control.go). An operator who sets 0 to pause load gets full volume. Either reject 0 at validation or represent unset separately so 0 means zero volume; document the chosen meaning.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 volume_multiplier 0 either is rejected with a 400 naming the valid range or produces zero-scaled volume, never 1.0x
- [ ] #2 A persisted value round-trips across restart with the same meaning
- [ ] #3 docs/control-plane.md states the meaning of 0
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop50 (2026-10-18): held for an owner decision, not started. Reject 0 with a 400 (root recommendation) or make 0 mean zero volume; see the loop50 closeout.
<!-- SECTION:NOTES:END -->
