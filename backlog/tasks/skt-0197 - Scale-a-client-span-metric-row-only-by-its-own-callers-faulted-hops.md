---
id: SKT-0197
title: Scale a client span-metric row only by its own caller's faulted hops
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:52:00Z'
updated_date: '2026-10-25T14:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 401000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Client factors and the healthy client reference are keyed by callee only (internal/workload/app/causal_project.go), so a caller's row into a target can be scaled by another caller's faulted hop to the same target. Pre-existing, extended by row gating. Landing-10 review minor 2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A caller whose own hops to a target carry no fault keeps that client row unscaled while another caller's hops to the same target are faulted
- [ ] #2 Healthy behaviour unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
