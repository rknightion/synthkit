---
id: SKT-0135
title: Bridge standard operational logs across HA role transitions
status: In Progress
assignee:
  - codex
created_date: '2026-10-15 13:08'
updated_date: '2026-10-15 13:25'
labels: []
dependencies: []
priority: high
ordinal: 258000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lease-mode execution starts independent role-scoped self-observability providers but never connects the standard logger to them. Heartbeats reach stderr while OTLP metrics and traces arrive, leaving a healthy operational logs stream empty. The ordinary single-process path already installs LogWriter. Bind standard logs to the current role provider, restore the prior writer on shutdown and preserve role-transition attribution without retaining a closed provider. This is a generic engine behavior fix; no deployment-specific integration belongs here.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Standard log output from lease-mode standby and leader reaches a real local OTLP receiver with the correct immutable role resource
- [x] #2 Role transition and shutdown remove the old log destination without losing stderr output or attributing current logs to a retired provider
- [x] #3 Disabled self-observability preserves existing logging and public documentation states the operational behavior
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Extend the existing real OTLP receiver integration test to require standard logger records from both immutable roles and writer restoration after stop; observe red first. Bind standard logging in HA composition to the current enabled provider and restore the original writer before shutting it down. Keep no-op logging unchanged, cover standby-to-leader transitions and duplicate teardown, document the generic contract. Focused race then final just check, maintainer CodeRabbit and exact-SHA CI; isolate from unrelated staged GPU work and do not change any live deployment.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Tracked independently from deployment integration. Exact pinned release v1.6.0-rc.179 and current main both omit the standard logger binding from startHAOperational, while ordinary mode calls LogWriter. Existing real HTTP OTLP integration test extended to require actual log.Print records for standby and leader: observed red with empty standard-record map. Candidate binds enabled standard logger to role provider, captures original writer with shared once-only restoration before shutdown; disabled path unchanged. Focused real OTLP + race green, docs/canonical contract/verification skill updated and justgen0. Final justcheck and CodeRabbit active in isolated tree based1bb6cac; preserve unrelated staged GPU work in main.

Final candidate full justcheck passed with Node24.18.0, including full plain and race suites, generated/skills/docs, source hygiene, UI, Compose, Helm, lab scaffolding, signal-fidelity ratchet225/225 and executable troubleshooting rows. Final six candidate file hashes unchanged before/after. Complete CodeRabbit6files0findings. Explicit otlp-native justdump inventory is byte-identical to clean base1bb6cac (156988bytes), with no rendered signal change. Real local OTLP test observes standard logs from both roles, late duplicate old-provider teardown does not detach leader logging, disabled path preserves writer. Exact source CI and release adoption remain separate; no deployed engine changed.
<!-- SECTION:NOTES:END -->
