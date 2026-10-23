---
id: SKT-0166
title: Investigate TestEnvSurfaceAligned failing in gate sweeps of a fresh clone
status: Done
assignee:
  - '@loop53'
created_date: '2026-10-22T05:16:00Z'
updated_date: '2026-10-23T08:01:00Z'
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
- [x] #1 Root cause stated with a reproduction
- [x] #2 Either the test is independent of an ignored local .env or the drift is fixed; no surface check weakened
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Classified without code change: an uncommitted diagnostic non-test source read REPLAN_MARGIN, correctly triggering the environment-surface guard. Controlled remote reproduction fails with that injected read both with and without example-only project environment, and passes after removing the read in both cases. Pristine config build/vet/package test passes on 8545834. No production surface change or test weakening is needed; inherited diagnostics preserved.

No-code diagnosis accepted and evidence recorded before board commit pushed in 13e4258. The existing scanner passes without project environment and still rejects injected undocumented Go reads. No inherited diagnostic artifact removed; no production guard weakened. Full composed just check on 13e4258 also passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fresh checkout was not the defect. Untracked diagnostic instrumentation added an undocumented environment read and correctly failed alignment. Controlled four-case red/green proof distinguishes that cause from ignored environment-file presence. No code change required.
<!-- SECTION:FINAL_SUMMARY:END -->
