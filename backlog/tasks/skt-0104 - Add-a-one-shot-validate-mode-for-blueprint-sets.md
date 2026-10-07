---
id: SKT-0104
title: Add a one-shot validate mode for blueprint sets
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-07 22:54'
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
- [x] #1 Valid set: exits 0 with a JSON summary (per-blueprint series counts, diagnostics)
- [x] #2 Collision or schema error: exits non-zero with diagnostics
- [x] #3 No network calls and no state writes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Isolated candidate with frozen ownership; acceptance reproduction, focused checks and just check; independent review before root landing. HA seam design is independently challenged and frozen first.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 1, review-repair rounds 0 (identity objection resolved by native edit replay, no candidate change). Landd61ea18359738eddc93993fdb7be4faa94acdf60; composed gate green exactSHA. Final integrated CI run37696196429 atf3d701a1a4500eb2944a0a354c53a9c641fed371 green; earlierd61 run37693078591 cancelled, not pass. Real CLI shipped set JSON3537series, collision/schema errors nonzero, no network/state writes, independent review ACCEPT. Generation not applicable; no live renderer changes; composed inventory/fidelity green. Routine aggregate review scheduled after last routine land.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Offline -validate loads selected sets and reports JSON diagnostics/per-blueprint single-cycle cardinality including native OTLP metrics. Real CLI and read-only staged-source tests proved behavior. Evidence /tmp/C104-evidence/acceptance-report.json and /tmp/C104-composed-d61ea18/gate.log; contemporaneous native tool chronology binds exact candidate hashes.
<!-- SECTION:FINAL_SUMMARY:END -->
