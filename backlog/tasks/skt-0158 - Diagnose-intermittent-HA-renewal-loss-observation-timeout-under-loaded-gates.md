---
id: SKT-0158
title: Diagnose intermittent HA renewal-loss observation timeout under loaded gates
status: To Do
assignee: []
created_date: '2026-10-21T15:25:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 300000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Go 1.27.3 dependency/security candidate hit TestHACoordinatorRenewalLossSuppressesCleanup/active in cmd/synthkit/ha_loss_test.go:381: HA local behavior was not observed within bound. The same unchanged test passed plain and race in the preceding full gate and passed a focused retry. Preserve the real observation synchronization and current failure bounds; diagnose the occasional missed observation rather than loosening timing or weakening assertions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded reproduction distinguishes scheduling delay from a lost observation or lifecycle defect.
- [ ] #2 Any correction preserves the cleanup suppression and actual HA observation assertions, with red then green evidence.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
