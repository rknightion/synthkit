---
id: SKT-0210
title: Reject the legacy throttle switch on causally bound agent runtimes
status: In Progress
assignee:
  - '@loop59-root'
created_date: '2026-10-25T15:15:00Z'
updated_date: '2026-10-25T19:31:00Z'
labels:
  - causal
dependencies: []
ordinal: 414000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Owner ruling Q10 (loop58): a bound agent runtime silently ignores the region-scoped legacy agentcore_throttle mode, so a story using it does nothing. Make blueprint load reject that switch on bound runtimes (guarded loader hook). The deployment-wide global series stays.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Loading a blueprint that targets a bound runtime with the legacy throttle switch fails with a clear error
- [ ] #2 Unbound runtimes keep the legacy switch; the global series still emits
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add loader rejection for legacy throttle targeting a causally bound agent runtime, retain unbound and deployment-global behavior, and prove via public blueprint load plus scoped gates.
<!-- SECTION:PLAN:END -->
