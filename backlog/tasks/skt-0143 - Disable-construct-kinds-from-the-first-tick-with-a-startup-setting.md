---
id: SKT-0143
title: Disable construct kinds from the first tick with a startup setting
status: To Do
assignee: []
created_date: '2026-10-17 15:12'
labels:
  - control
dependencies: []
references:
  - internal/control/http.go
  - internal/runner/runner.go
  - docs/fleet-management.md
  - docs/configuration.md
priority: medium
type: feature
ordinal: 266000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
disabled_kinds (control.State.DisabledKinds, internal/control/control.go:54) can only be set through POST /control/kinds (internal/control/http.go:742-770), which needs a running process and a control token. An operator that wants an emission-only deployment, with Synthetic Monitoring (kind synthetic_monitoring) and Fleet Management (kind fleet_management) constructs excluded, has to start the process, wait for the first tick to emit those series and register or provision, then call the API: the first tick has already gone out. Leaving the Fleet credentials empty does not help, because Fleet metrics still emit alloy_* series without them (docs/fleet-management.md:125-131, "Fleet metrics" mode needs no FM API credentials). The kind filter is applied in Runner.constructEnabled (internal/runner/runner.go:1568-1577) reading the control snapshot, so a startup value only has to seed that state before the first tick.

The new startup setting follows the env surface rule in AGENTS.md: a literal get("LIT") read in internal/config, declared in .env.example and Compose, covered by TestEnvSurfaceAligned. Its name is chosen at implementation. It must be generic (any registered kind), not specific to SM or Fleet.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A startup setting accepts a list of construct kinds and seeds the disabled kinds before the first tick; a runner test with synthetic_monitoring and fleet_management listed shows the first tick pushes no series of those kinds, including no alloy_* series, with no POST /control/kinds call made
- [ ] #2 With those kinds listed at startup, a test using fake SM and Fleet Management endpoints records zero registration, provisioning or heartbeat requests from the first tick onward
- [ ] #3 An unknown kind is rejected at startup with an error naming it, using the same validation as POST /control/kinds (validateKinds); a config test covers both a valid and an unknown kind
- [ ] #4 The precedence between the startup list and a persisted CONFIG_SNAPSHOT_PATH disabled_kinds list is documented and tested across a restart, including that a later POST /control/kinds replacement still works and GET /control/state reports the effective list
- [ ] #5 TestEnvSurfaceAligned passes with the new variable present in the Go read, .env.example and Compose
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
- [ ] #4 docs/configuration.md, docs/control-plane.md and docs/fleet-management.md reconciled, and just docs-check passes
<!-- DOD:END -->
