---
id: SKT-0156
title: Diagnose bounded HA helper HTTP readiness failure under the full race gate
status: To Do
assignee: []
created_date: '2026-10-21T03:00:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 295000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A documentation-only candidate failed TestHACoordinatorRenewalLossSuppressesCleanup/partial at the initial /control/state readiness wait before lease acquisition. The full remote race gate reported the six-second observation deadline, but did not retain child startup output or HTTP diagnostics. All runtime Go inputs are identical to base; one bounded local focused base race test passed, so the remote failure is not yet reproduced or attributed. This blocks trustworthy gate classification independently of the documentation change.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bounded same-context investigation records exact revisions, helper exit or startup output and readiness response evidence sufficient to distinguish startup failure from scheduling delay; passing focused runs are not called reproduction.
- [ ] #2 Any correction preserves the existing deadline, lease-admission and cleanup assertions, actual HTTP observation and hard-exit proofs; a meaningful red control and the full race gate verify the corrected behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
