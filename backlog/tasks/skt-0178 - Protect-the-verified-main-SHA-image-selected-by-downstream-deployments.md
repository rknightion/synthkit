---
id: SKT-0178
title: Protect the verified main-SHA image selected by downstream deployments
status: In Progress
assignee:
  - '@pin64'
created_date: '2026-10-23T23:34:00Z'
updated_date: '2026-10-23T23:34:00Z'
labels: []
dependencies: []
priority: high
type: chore
ordinal: 334000
---

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Image cleanup protects main-7573924 in addition to all existing stable/chart protections; ordinary unprotected edge-image expiry is unchanged.
- [ ] #2 Only the protected-tag input is changed; validation and review confirm no new publishing, deletion, credential, runner or permission behavior.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the verified main-SHA tag to the existing cleanup protection input only; preserve stable/current-chart tags and ordinary seven-day edge cleanup.
<!-- SECTION:PLAN:END -->
