---
id: SKT-0057
title: Expose bounded Fleet collector configuration receipt evidence
status: Done
assignee: []
created_date: '2026-09-07 11:03'
updated_date: '2026-09-07 13:52'
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
- [x] #1 Inspect and pin the documented/observed GetConfig response contract, then retain only bounded non-secret receipt metadata sufficient to associate a collector with delivered configuration revision or digest; unknown or unsupported shapes report unavailable evidence.
- [x] #2 Authenticated control-plane diagnostics expose per-collector registration/heartbeat/receipt distinctions with freshness and stale/no-config/error states, without exposing raw config, credentials or customer attributes.
- [x] #3 Fixtures cover matching and nonmatching collector configurations, changed/stale revisions and an error response; a successful empty heartbeat cannot count as pipeline receipt.
- [x] #4 Docs state that receipt never proves configuration parsing or execution, preserve fixed real collector attribute semantics and use only synthetic fixtures; no new agent execution or remote-write self-metric emulation is introduced.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Pin the GetConfig response schema, reduce non-empty content to bounded SHA-256 receipt metadata, distinguish heartbeat from receipt and stale/error/unavailable outcomes, wire authenticated control status, validate fixtures and review with the residency batch.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Pinned Alloy Remote Config v0.0.12 response fields; content is reduced to SHA-256, opaque server hash discarded, response bounded to 1 MiB and one complete JSON value. Successful empty responses remain unavailable, not receipt. Per-collector lifecycle and receipt status are wired into authenticated control status. Test-first fixtures, integrated fleet/status/runner/control/command tests, and just check passed. CodeRabbit L1/L2 batch completed with no Fleet findings. No live Fleet call occurred; receipt never proves parsing or execution.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Bounded configuration receipt diagnostics are implemented and fixture-proven. Live receipt from a real Fleet response remains unverified.
<!-- SECTION:FINAL_SUMMARY:END -->
