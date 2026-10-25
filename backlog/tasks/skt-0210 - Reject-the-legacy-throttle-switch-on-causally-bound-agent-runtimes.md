---
id: SKT-0210
title: Reject the legacy throttle switch on causally bound agent runtimes
status: In Progress
assignee: []
created_date: '2026-10-25T15:15:00Z'
updated_date: '2026-10-25T22:31:00Z'
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
- [x] #1 Loading a blueprint that targets a bound runtime with the legacy throttle switch fails with a clear error
- [x] #2 Unbound runtimes keep the legacy switch; the global series still emits
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add loader rejection for legacy throttle targeting a causally bound agent runtime, retain unbound and deployment-global behavior, and prove via public blueprint load plus scoped gates.

Loop60: complete selected AgentCore bound/unbound public CLI inventory and sourced conformance, with existing source unchanged.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Two failed review rounds opened integration-fixture and authored/effective-scope mismatch classes. First convergence tripwire selected senior escalation and a fail-closed schedule-representability approach; no landing acceptance yet.

Source published at 9d8185727522f30e8eb7b731ff7f3f8c293904db in exact-gated dac0d2b4139b5fc851a49f2b16c77a5dadd15421. Native guarded PASS and loader/CLI rejection/compatibility retained. Required selected AgentCore CLI inventory/conformance was not run: reviewer lacked required wait helper. Resume with that public-entrypoint proof, not unit-test substitution. Hosted CI independently red; owner ended run without repair push.
<!-- SECTION:NOTES:END -->
