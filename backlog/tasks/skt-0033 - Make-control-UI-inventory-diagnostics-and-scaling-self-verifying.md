---
id: SKT-0033
title: 'Make control UI, inventory, diagnostics, and scaling self-verifying'
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 01:39'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: medium
type: feature
ordinal: 124000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios C5, D5, D6, and D8 could reach the control surfaces, but the UI path yielded only a redirect in the clean-container check, inventory exposed no per-blueprint entries, diagnostics did not establish shell-free sufficiency, and the full catalogue exposed zero scalable targets. The round could not verify every UI view, identity separation, or a real scaling mutation from the supplied control data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The admin UI has an executable check that enumerates every shipped view and confirms it reflects live state
- [x] #2 Control inventory exposes per-blueprint and substrate identity needed for label-separation diagnosis
- [x] #3 Diagnostics identify a failing blueprint and lane without container shell access
- [x] #4 At least one shipped acceptance configuration exposes a valid scalable target, or the API clearly reports why none exist
- [ ] #5 Reset and scaling acceptance checks assert an observable emitted-data change
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane B makes UI, inventory, diagnostics, reset, and scaling self-verifying; root performs emitted-data mutation checks live.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: UI enumeration, per-blueprint/substrate inventory, lane diagnostics, and a scalable acceptance target landed. Live scale 2 to 4 and reset were accepted and read back, but emitted rate ratios were 0.977853 while scaled and 0.995540 after reset.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at AC#5. Resume in the runner scaling path and require the selected target live rate to move with the configured multiplier before accepting reset/scaling as self-verifying.
<!-- SECTION:FINAL_SUMMARY:END -->
