---
id: SKT-0192
title: Expose each causal resource's bound consumers in the control schema
status: To Do
assignee: []
created_date: '2026-10-25T09:54:00Z'
labels: []
dependencies:
  - SKT-0189
priority: low
ordinal: 390000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live scenario assertions for resource-axis modes cannot scope to the affected series because no emitted series carries a resource name; they are blueprint- or stack-wide and ambient incidents can confound them (SKT-0189). Expose, per declared resource, the bound consumers (app nodes using it, bound constructs and their identities) in GET /control/schema so assertions can scope to them.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 GET /control/schema lists each resource's bound consumers
- [ ] #2 Resource-mode assertions scope to those consumers
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
