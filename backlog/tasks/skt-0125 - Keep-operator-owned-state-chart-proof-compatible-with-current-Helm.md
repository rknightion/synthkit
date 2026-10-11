---
id: SKT-0125
title: Keep operator-owned-state chart proof compatible with current Helm
status: To Do
assignee: []
created_date: '2026-10-11 19:11'
labels: []
dependencies: []
references:
  - 'https://github.com/helm/helm/blob/v3.19.0/cmd/helm/template.go'
priority: medium
type: bug
ordinal: 247000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The unchanged HA chart proof combines --dry-run=server and --validate at charts/synthkit/tests/ha_chart_test.go:395. Helm4.3.0 and3.20.0 reject the flags before any local fixture API call. Official checksum-verified Helm3.19.0 runs all four precreation/install/upgrade cases successfully, preserving actual discovery/read operations and byte-identical operator-owned state. This currently makes the local full gate sensitive to an unpinned Helm version. The temporary compatible toolchain is not a permanent fix. No chart wiring extension is included.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The supported current Helm toolchain runs the unchanged safety contract with actual local fixture discovery and precreation observations in install and upgrade cases.
- [ ] #2 Operator-owned ConfigMaps and Lease remain byte-identical and unaccessed by Helm release rendering; no assertion, fixture or API-call proof is weakened to obtain compatibility.
- [ ] #3 The documented local tool prerequisite and CI behavior agree; just check passes without a private run-local binary.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
