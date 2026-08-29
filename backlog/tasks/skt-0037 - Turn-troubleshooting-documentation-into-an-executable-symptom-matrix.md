---
id: SKT-0037
title: Turn troubleshooting documentation into an executable symptom matrix
status: To Do
assignee: []
created_date: '2026-08-29 19:05'
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
- [ ] #1 Every troubleshooting symptom has a reproducible precondition, action, expected diagnostic, remedy, and post-remedy assertion
- [ ] #2 The matrix distinguishes local startup success from live delivery recovery
- [ ] #3 A maintained check exercises all non-destructive remedies from a clean environment
- [ ] #4 Symptoms requiring unavailable external products are explicitly marked with their prerequisite rather than silently skipped
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
