---
id: SKT-0088
title: Give each Synthetic Monitoring private probe its own location
status: To Do
assignee: []
created_date: '2026-10-05 11:57'
labels:
  - network
  - synthetic-monitoring
dependencies: []
references:
  - signals/sm.md
  - internal/construct/sm/sm.go
  - cmd/sm-provision/main.go
priority: medium
type: feature
ordinal: 210000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every private probe registered by sm-provision gets the construct default coordinates (sm.DefaultProbeLat/Lon, Frankfurt), so sm_check_info carries one geohash for probes declared in EMEA, US and APAC and a probe map shows a single marker. Add an optional per-probe latitude/longitude to the synthetic_monitoring check/probe declaration, thread it through ResolveSpec, the smstate snapshot and sm-provision, and derive the geohash from it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A blueprint can declare a location per private probe; omitted keeps today's default
- [ ] #2 sm_check_info geohash and the registered SM probe lat/lon match the declared location
- [ ] #3 blueprints/synthetic-monitoring.yaml declares plausible locations for its EMEA, US and APAC probes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
