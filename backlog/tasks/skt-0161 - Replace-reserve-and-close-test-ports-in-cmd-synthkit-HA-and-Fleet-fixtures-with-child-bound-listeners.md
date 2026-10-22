---
id: SKT-0161
title: >-
  Replace reserve-and-close test ports in cmd/synthkit HA and Fleet fixtures
  with child-bound listeners
status: To Do
assignee: []
created_date: '2026-10-22T03:48:00Z'
labels:
  - test
  - ha
dependencies: []
priority: medium
ordinal: 304000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found by loop52 lanes lha (SKT-0158) and l128 (SKT-0128): about ten cmd/synthkit tests (incl. fleet_unregister_test.go:149, ha_backend_test.go) reserve 127.0.0.1:0, close it and let a child bind it; under parallel gate load another process takes the port ('bind: address already in use', foreign 403 probes). ha_loss_test.go was fixed in loop52 by having the helper bind :0 itself and publish its address via WrapHTTPListener; apply the same pattern to the siblings.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 No cmd/synthkit test hands a closed reserved port to a child process
- [ ] #2 A foreign listener on the old reserved port is red at base and green after, for each converted test
- [ ] #3 Every deadline, bound and assertion is unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
