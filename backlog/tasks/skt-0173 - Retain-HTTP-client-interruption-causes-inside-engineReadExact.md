---
id: SKT-0173
title: Retain HTTP-client interruption causes inside engineReadExact
status: In Progress
assignee:
  - '@loop54'
created_date: '2026-10-23T09:29:00Z'
updated_date: '2026-10-23T19:15:00Z'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 324000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Five exact-read callers now preserve errors returned by engineReadExact, but engineReadExact itself still collapses non-404 management failures to bare ErrConflict when the outer caller context is active. An HTTP-client-only timeout can therefore still be mistaken for the behavior under test by lifecycle helpers. Preserve interruption evidence without changing refusal decisions or exposing unsanitized request data.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Client-only DeadlineExceeded or cancellation remains detectable with errors.Is while the outer caller context is active
- [ ] #2 All existing presence inventory hash and refusal predicates remain unchanged; safe not-found absence stays intact
- [ ] #3 Real client/engine regression is red on unchanged code and green after fix; lifecycle assertion rejects the interruption
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop54: real HTTP-client-only interruption red proof with active outer context; preserve exact-read conflict and safe absence predicates, sanitized causes, remote focused proof and guarded review.
<!-- SECTION:PLAN:END -->
