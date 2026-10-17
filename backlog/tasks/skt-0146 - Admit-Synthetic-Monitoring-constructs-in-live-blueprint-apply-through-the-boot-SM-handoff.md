---
id: SKT-0146
title: >-
  Admit Synthetic Monitoring constructs in live blueprint apply through the boot
  SM handoff
status: Done
assignee: []
created_date: '2026-10-17 17:27'
updated_date: '2026-10-17 23:26'
labels:
  - control
  - blueprints
dependencies: []
priority: low
ordinal: 269000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Live apply (POST /control/blueprints/live) rejects blueprints declaring a synthetic_monitoring construct, because the boot-time fail-closed SM admission (cmd/synthkit/sm_handoff.go prepareSMHandoff: registration injection, RequireRegistration, HA no-SM-emission rule, probe region/coordinate conflicts) is not reachable from the runner's live path. Add a composition-root-injected live admission hook in runner.Options that reuses that filter, so a live blueprint can carry SM only when its checks are provisioned and consistent, and honours startup-disabled kinds. Generic, no vendor or customer names.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A live apply of a blueprint with an SM check is admitted only through the same handoff the boot path uses, and is rejected or stripped exactly where boot would be
- [x] #2 Probe region and coordinate conflicts with running blueprints are rejected naming the probe
- [x] #3 HA lease mode without the kubernetes backend still emits no SM from live blueprints
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed 1d627bf (loop50 BATCH6). Follow-up SKT-0151 covers per-blueprint admission. Main CI green on 1d627bf (rc v1.6.0-rc.202).
<!-- SECTION:NOTES:END -->
