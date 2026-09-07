---
id: SKT-0057
title: Expose bounded Fleet collector configuration receipt evidence
status: To Do
assignee: []
created_date: '2026-09-07 11:03'
labels:
  - integration
dependencies: []
references:
  - internal/fleet/client.go
  - internal/fleet/controller.go
  - internal/construct/fleetmgmt/roster.go
  - docs/fleet-management.md
  - docs/control-plane.md
priority: medium
type: feature
ordinal: 153000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Fleet client records a successful GetConfig heartbeat but discards its response, so an external integration cannot distinguish collector liveness from receipt of a particular pipeline revision. Synthetic collectors intentionally do not execute the returned Alloy configuration. Add truthful, sanitized receipt evidence suitable for matching/delivery rehearsal. Existing Kubernetes mirror attributes already cover cluster, namespace, platform, release and workload dimensions; do not add arbitrary attributes or fabricated execution telemetry merely to satisfy a matcher.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Inspect and pin the documented/observed GetConfig response contract, then retain only bounded non-secret receipt metadata sufficient to associate a collector with delivered configuration revision or digest; unknown or unsupported shapes report unavailable evidence.
- [ ] #2 Authenticated control-plane diagnostics expose per-collector registration/heartbeat/receipt distinctions with freshness and stale/no-config/error states, without exposing raw config, credentials or customer attributes.
- [ ] #3 Fixtures cover matching and nonmatching collector configurations, changed/stale revisions and an error response; a successful empty heartbeat cannot count as pipeline receipt.
- [ ] #4 Docs state that receipt never proves configuration parsing or execution, preserve fixed real collector attribute semantics and use only synthetic fixtures; no new agent execution or remote-write self-metric emulation is introduced.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
