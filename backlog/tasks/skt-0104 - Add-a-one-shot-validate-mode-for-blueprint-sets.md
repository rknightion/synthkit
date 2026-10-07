---
id: SKT-0104
title: Add a one-shot validate mode for blueprint sets
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
labels:
  - feature
  - cli
dependencies: []
ordinal: 226000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Validation and cardinality preview currently only run inside a live instance (POST /control/blueprints/validate builds a throwaway dry runner in-process, unbounded). Add a one-shot validate mode (flag name to match the existing -preflight style): load the selected set (BLUEPRINT_NAMES plus sources), run ValidateSet and the cardinality projection, print a machine-readable result and exit non-zero on failure. The credentials preflight mode already exists.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Valid set: exits 0 with a JSON summary (per-blueprint series counts, diagnostics)
- [ ] #2 Collision or schema error: exits non-zero with diagnostics
- [ ] #3 No network calls and no state writes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
