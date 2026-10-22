---
id: SKT-0166
title: Investigate TestEnvSurfaceAligned failing in gate sweeps of a fresh clone
status: To Do
assignee: []
created_date: '2026-10-22T05:16:00Z'
labels:
  - test
  - config
dependencies: []
priority: low
ordinal: 310000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Seen by loop52 replan3 lane in gaterun sweeps run from a fresh clone (codex/loop52/REPLAN/rev3/); the composed gates from git worktrees pass. internal/config.TestEnvSurfaceAligned compares the Go-read, .env.example, Compose and project-.env surfaces, so a clone without the ignored .env may change its inputs. Classify: test-environment dependency vs real surface drift.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Root cause stated with a reproduction
- [ ] #2 Either the test is independent of an ignored local .env or the drift is fixed; no surface check weakened
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
