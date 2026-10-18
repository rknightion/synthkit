---
id: SKT-0152
title: >-
  Bring cmd/synthkit-control-dash race runtime back well under the 600 s test
  timeout
status: To Do
assignee: []
created_date: '2026-10-18 00:58'
labels:
  - ci
  - tests
dependencies: []
priority: high
ordinal: 275000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0112 brought the package under the timeout, but main CI just race now reports cmd/synthkit-control-dash at 386 s (run 39082154382, 2ff0839) and a 600.302 s timeout (run 39083496929, c5ddebc, running TestLoadScenariosEnumeratesBlueprints), with 348-353 s on the gate hosts. Small CI runners plus other package load push it over. Cut the package's own test cost (shared fixtures, fewer full-estate derivations, bounded parallelism) without skipping under race, raising the timeout or weakening any assertion.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Main CI just race reports cmd/synthkit-control-dash under 300 s on two consecutive main SHAs, with the unchanged 600 s timeout
- [ ] #2 No test skipped under race, no assertion removed, no timeout raised; the changed tests are named with before and after times
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
