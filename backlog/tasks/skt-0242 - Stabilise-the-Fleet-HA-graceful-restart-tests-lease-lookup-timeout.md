---
id: SKT-0242
title: Stabilise the Fleet HA graceful restart test's lease lookup timeout
status: To Do
assignee: []
created_date: '2026-10-26T09:02:00Z'
labels:
  - flaky
dependencies: []
priority: medium
ordinal: 448000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TestFleetHAGracefulRestartKeepsIDsAndExplicitTeardown/restart failed once on the remote gate (gaterun 20261026T085202Z-synthkit-c395bcb7) on a comments-only change: the helper's named lease lookup against the in-process fake API timed out at its 100 ms request timeout (context deadline exceeded), so the lifecycle helper exited 1. The same test passed on the parent commit. Make the helper's lookup tolerate a loaded runner without weakening the assertions or the five-second subprocess bound.
<!-- SECTION:DESCRIPTION:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
