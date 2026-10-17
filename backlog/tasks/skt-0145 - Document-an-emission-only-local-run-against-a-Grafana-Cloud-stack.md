---
id: SKT-0145
title: Document an emission-only local run against a Grafana Cloud stack
status: Done
assignee: []
created_date: '2026-10-17 16:03'
updated_date: '2026-10-17 20:02'
labels:
  - docs
  - control
dependencies:
  - SKT-0143
references:
  - docs/quickstart.md
  - docs/configuration.md
  - docs/fleet-management.md
priority: medium
type: docs
ordinal: 268000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
An operator who wants synthetic data in an existing Grafana Cloud stack, for example to give migrated infrastructure dashboards something to render, needs one documented path to run SynthKit on a workstation in emission-only mode: real delivery (DRY_RUN=false), a metrics, logs and traces write token, no Synthetic Monitoring or Fleet Management side effects, and a bundled blueprint set suited to infrastructure dashboards. docs/quickstart.md covers a first local run but not this mode, and before SKT-0143 there is no way to keep Synthetic Monitoring and Fleet Management construct kinds out of the first tick. This is generic public guidance: no stack names, tenant IDs, hostnames or credential paths. Warm-up applies: there is no backfill, so panels fill as cumulative series accumulate.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A docs page or section gives the emission-only local run on macOS and Linux: DRY_RUN=false, the write token scopes it needs (metrics, logs, traces write), the SKT-0143 startup setting excluding synthetic_monitoring and fleet_management, and an example bundled set of k8s-full-stack, hostfleet, otlp-native, hosts-linux-docker and aws-otlp-native
- [x] #2 It states that no backfill exists and how long warm-up takes before rate and histogram panels populate, citing the tick interval and the cumulative state behaviour
- [x] #3 It tells the reader how to confirm emission (control UI sink readiness and GET /control/inventory) without starting any Grafana write recipe, and contains no stack name, tenant ID, hostname or credential path
- [x] #4 just docs-check passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Landed 3af9667 in loop50 (rc v1.6.0-rc.201) as docs/emission-only.md. DoD #3 just dump not applicable (docs only).
<!-- SECTION:NOTES:END -->
