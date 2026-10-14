---
id: SKT-0088
title: Give each Synthetic Monitoring private probe its own location
status: Done
assignee:
  - '@codex'
created_date: '2026-10-05 11:57'
updated_date: '2026-10-14 20:57'
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
- [x] #1 A blueprint can declare a location per private probe; omitted keeps today's default
- [x] #2 sm_check_info geohash and the registered SM probe lat/lon match the declared location
- [x] #3 blueprints/synthetic-monitoring.yaml declares plausible locations for its EMEA, US and APAC probes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add optional validated per-probe coordinates, preserve defaults, carry locations through registration and emitted geohash, update example with plausible locations; verify fake HTTP registration and emitter output, regenerate schema once, review and composed gate.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Fail-first YAML/default/zero, invalid-coordinate and fake HTTP registration proofs passed. Real handoff snapshot -> built sm-provision -> fake API -> composed runner geohash passes; conflicting shared probe locations preserve existing snapshot. Full gate exposed existing region mutation splitting a shared probe; adjusted that case to change the uniquely named secondary probe, retaining the same stale-registration/no-emission assertion. Focused bound-spec matrix passed. Full baseline/candidate signal-name and label-key dumps byte-identical.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed in6ee431e55612028a697972f23ccbb134e1c201f1. Optional paired coordinates retain defaults and explicit zero; real private snapshot -> built provision CLI -> fake HTTP -> composed emitter verifies location/geohash. Invalid/shared conflicts fail before snapshot publication. Example locations, regenerated schema and docs included. Full local just check passed; CodeRabbit complete0 and independent review clear. Exact CI38632786889 all11required jobs success including e2e. No live stack mutation.
<!-- SECTION:FINAL_SUMMARY:END -->
