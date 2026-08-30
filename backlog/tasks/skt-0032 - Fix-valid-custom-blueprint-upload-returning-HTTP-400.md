---
id: SKT-0032
title: Fix valid custom blueprint upload returning HTTP 400
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 01:39'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: high
type: bug
ordinal: 123000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenario D3 confirmed invalid uploads return a diagnostic, but posting a documented namespaced copy of a shipped valid blueprint returned HTTP 400. A first-time operator cannot distinguish a bad request shape from a valid blueprint rejected by the upload path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A documented valid custom blueprint upload succeeds and applies
- [x] #2 An invalid custom blueprint still returns a diagnostic naming the schema or semantic problem
- [x] #3 Focused tests reproduce the valid HTTP 400 before the fix and cover both valid and invalid requests
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane B first reproduces the valid-upload HTTP 400 with a focused failing test, then fixes valid and invalid request handling.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: the focused test reproduced the valid-upload failure before the manager fix; invalid input still returned HTTP 400 diagnostics. A valid namespace=acceptance, name=copied request returned staged acceptance/copied, and the local wave image exposed acceptance/copied in the live schema after restart.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Valid namespaced custom blueprints now stage, survive restart, and apply; invalid blueprints retain actionable diagnostics.
<!-- SECTION:FINAL_SUMMARY:END -->
