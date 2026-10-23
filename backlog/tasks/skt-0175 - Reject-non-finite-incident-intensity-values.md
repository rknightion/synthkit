---
id: SKT-0175
title: Reject non-finite incident intensity values
status: In Progress
assignee:
  - '@loop54'
created_date: '2026-10-23T19:51:00Z'
updated_date: '2026-10-23T19:51:00Z'
labels: []
dependencies: []
priority: low
type: bug
ordinal: 326000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Source review of scheduled-intensity presence found that existing numeric range comparisons do not independently reject NaN, which is not a member of the documented [0,1] domain. Keep this separate from authored zero-versus-omission semantics. Confirm the public-loader behavior before changing it; no broader Active-only, live activation, timing-bound or emitter change is implied.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Public-loader regression rejects NaN and non-finite incident/effect intensity without accepting it as an omitted default or usable numeric value
- [ ] #2 Omitted, explicit zero and finite fractional values retain their documented scheduling and live semantics; mode/target/scope validation is not bypassed
- [ ] #3 Meaningful base red and candidate green evidence crosses the actual loader, with canonical guidance reconciled if changed
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Confirm non-finite public-loader behavior on unchanged production code first, reject only values outside the existing finite [0,1] contract, preserve scope and zero/omission/live behavior; focused remote proof and guarded review before integration.
<!-- SECTION:PLAN:END -->
