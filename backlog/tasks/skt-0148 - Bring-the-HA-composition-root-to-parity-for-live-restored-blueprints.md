---
id: SKT-0148
title: Bring the HA composition root to parity for live-restored blueprints
status: To Do
assignee: []
created_date: '2026-10-17 18:33'
labels:
  - control
  - blueprints
dependencies: []
priority: low
ordinal: 271000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live-applied blueprints (POST /control/blueprints/live) restored on the first ApplyControl get shape warnings, restore-skip diagnostics and optional-lane dispositions on the single-process path (cmd/synthkit/main.go), but the HA path does not: cmd/synthkit/ha_main.go builds optional-lane facts from the boot accepted set, and HA restore skips and shape warnings reach logs only, not GET /control/diagnostics.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 In HA leader mode a restored live blueprint's restore-skip and shape warnings appear in GET /control/diagnostics, with a test
- [ ] #2 HA optional-lane dispositions include lanes of restored live blueprints, with a test
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
