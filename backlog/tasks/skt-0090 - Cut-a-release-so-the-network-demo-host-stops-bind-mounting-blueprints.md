---
id: SKT-0090
title: Cut a release so the network demo host stops bind-mounting blueprints
status: To Do
assignee: []
created_date: '2026-10-05 11:57'
labels:
  - network
  - deploy
dependencies: []
priority: medium
type: chore
ordinal: 212000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The network-observability demo deployment runs the 1.3.1 image with four blueprints bind-mounted over the image copies: the three netobs blueprints (background fault incidents) and synthetic-monitoring.yaml (distinct private probe names; 1.3.1 reuses one name across regions, which SM rejects, leaving sm-provision with a pending journal). Cut a release containing them, move the deployment to it, and drop the overrides.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A published release contains the netobs incident schedules and the distinct SM probe names
- [ ] #2 The demo deployment runs that release with no blueprint bind mounts and still registers and emits the SM checks
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
