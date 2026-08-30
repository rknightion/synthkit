---
id: SKT-0037
title: Turn troubleshooting documentation into an executable symptom matrix
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 01:39'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: medium
type: docs
ordinal: 128000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenario H2 reproduced the missing-token and unsafe-exposure remedies, but the remaining documented symptoms could not be exercised end to end from the clean environment. New users have prose remedies without a maintained way to prove each symptom and recovery path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every troubleshooting symptom has a reproducible precondition, action, expected diagnostic, remedy, and post-remedy assertion
- [x] #2 The matrix distinguishes local startup success from live delivery recovery
- [x] #3 A maintained check exercises all non-destructive remedies from a clean environment
- [x] #4 Symptoms requiring unavailable external products are explicitly marked with their prerequisite rather than silently skipped
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check </dev/null (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen </dev/null (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump </dev/null — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane E converts every troubleshooting symptom to a five-part executable row and validates all non-destructive remedies.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: troubleshooting-check executed all ten local symptom rows successfully and emitted explicit BLOCKED_EXTERNAL dispositions for all eight product-dependent lanes; final just check exited 0.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Troubleshooting is now a maintained executable matrix with local-versus-live proof boundaries and explicit external prerequisites.
<!-- SECTION:FINAL_SUMMARY:END -->
