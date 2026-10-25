---
id: SKT-0221
title: Support scoped parameterized export of the process operational dashboard
status: To Do
assignee: []
created_date: '2026-10-25T20:22:00Z'
labels: []
dependencies: []
references:
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/dashboards/internal/README.md
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/dashboards/internal/build_selfobs_dashboard.py
  - >-
    https://github.com/rknightion/synthkit/blob/d4a51b34ebb839895c435b46dd14df9c350db7c1/internal/selfobs/helpers.go
priority: medium
type: feature
ordinal: 427000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Independent deployments need reusable operational dashboards without identity collisions or silently selecting another deployment’s telemetry. The process self-observability artifact currently has fixed resource and datasource identities; its process picker does not constrain every signal. This is distinct from synthetic-telemetry dashboard generation and control/action dashboards. Provide a documented supported offline export contract; no dashboard publication, credentials, alerting or access-control changes are requested. Dashboard filtering is not datasource authorization.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A documented supported entrypoint exports operational dashboard resources with explicit resource-identity namespace, folder placement, datasource bindings and deployment scope; identical inputs are deterministic and distinct namespaces cannot collide, preserving compatible defaults
- [ ] #2 Every enabled panel and variable query is constrained to the requested deployment across metrics, logs, traces, profiles and spanmetrics; unsupported dimensions refuse export or explicitly omit affected panels with machine-readable diagnostics, never global fallback
- [ ] #3 The documented identity contract distinguishes stable deployment grouping from process/session selection, explains backend projection and preserves grouping across process replacement without invented labels; malformed or unrepresentable scope refuses without partial output
- [ ] #4 Actual export-entrypoint tests cover two deployments, multiple processes, supported signal families, unsupported dimensions and malformed bindings; complete query inventory and receiver fixtures prove neither deployment selects the other
- [ ] #5 Documentation states coverage, omissions, telemetry/backend prerequisites and filtering versus authorization; export is network-free and requires no tenant credential
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
