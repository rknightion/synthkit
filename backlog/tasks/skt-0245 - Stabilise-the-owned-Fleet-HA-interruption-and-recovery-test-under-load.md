---
id: SKT-0245
title: Stabilise the owned Fleet HA interruption and recovery test under load
status: To Do
assignee: []
created_date: '2026-10-26T16:43:00Z'
labels:
  - flaky
dependencies: []
priority: medium
ordinal: 451000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestFleetUnregisterOwnedHAAuthorityInterruptionAndRecovery (cmd/synthkit/fleet_owned_test.go:579) failed once on the remote gate (gaterun 20261026T163726Z-synthkit-15cb7d7f) on a change that does not touch cmd/synthkit: recovery exit status 1 with teardown receipt status remaining (1 of 2 retired). The same code passed the gate an hour earlier. The test uses a deliberate paced 400 ms / 1 s client setting for the client-go rate limiter. Find why recovery can leave a collector unretired on a loaded runner, without weakening assertions or bounds.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
