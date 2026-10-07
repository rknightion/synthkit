---
id: SKT-0099
title: Make Synthetic Monitoring provisioning work without a shared volume
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
labels:
  - feature
  - ha
  - sm
dependencies:
  - SKT-0094
ordinal: 221000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SM provisioning is a separate two-boot handoff: cmd/sm-provision takes an O_EXCL file lock and writes a registration the emitter reads at boot, and the registration binding includes the release version (internal/smstate/state.go), so every image bump silently disables SM emission until the provisioner re-runs and the emitter restarts. Under stateless HA: run sm-provision against the kubernetes state backend with a Lease-based lock, and stop a release-version bump with unchanged SM specs from invalidating the registration.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sm-provision runs against STATE_BACKEND=kubernetes with a Lease lock
- [ ] #2 An image bump with unchanged SM specs keeps SM emission enabled
- [ ] #3 Changed SM specs still fail closed until re-provisioned
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
