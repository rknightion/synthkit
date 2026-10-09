---
id: SKT-0112
title: Keep the control-dash race package inside the test timeout
status: To Do
assignee: []
created_date: '2026-10-09 11:19'
labels: []
dependencies: []
type: bug
ordinal: 234000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Under `just race`, cmd/synthkit-control-dash took 363s (CI 37810460433), 437s (37884775156) and 551s (37889782301), and exceeded the default 10m test timeout in CI 37855685768 at 14719af with TestShippedScenarioImpactQueriesUseDerivedFamilies still running at 9m54s. The same timeout made local `just check` red for unrelated lanes. The test dry-runs every shipped scenario serially under the race detector, so main CI and every composed gate will go red as the catalogue grows.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 go test -race ./cmd/synthkit-control-dash -count=1 completes in under 5 minutes on the CI go job, measured at the landed SHA
- [ ] #2 TestShippedScenarioImpactQueriesUseDerivedFamilies still covers every shipped scenario and keeps every assertion; no test is skipped under race and no -timeout is raised
- [ ] #3 just check exits 0 at the landed SHA
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
