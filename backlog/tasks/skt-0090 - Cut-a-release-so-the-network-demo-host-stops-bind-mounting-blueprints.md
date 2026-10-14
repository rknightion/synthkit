---
id: SKT-0090
title: Cut a release so the network demo host stops bind-mounting blueprints
status: Done
assignee: []
created_date: '2026-10-05 11:57'
updated_date: '2026-10-14 20:23'
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-14 owner decision: close as obsolete; no deployment remains to upgrade. Read-only checks found no running/stopped synthkit or netobs Docker containers locally or on the previously suggested host, no matching system/system-user service units there, and no synthkit process on either machine. No deployment, provisioning or resource mutation performed. Original release/upgrade acceptance criteria are not claimed passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Closed as obsolete by explicit owner decision on 2026-10-14 after read-only deployment inventory found no remaining target. No upgrade performed; original deployment acceptance criteria remain unchecked intentionally.
<!-- SECTION:FINAL_SUMMARY:END -->
