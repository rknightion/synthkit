---
id: SKT-0173
title: Retain HTTP-client interruption causes inside engineReadExact
status: Done
assignee:
  - '@loop54'
created_date: '2026-10-23T09:29:00Z'
updated_date: '2026-10-23T19:49:00Z'
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
- [x] #1 Client-only DeadlineExceeded or cancellation remains detectable with errors.Is while the outer caller context is active
- [x] #2 All existing presence inventory hash and refusal predicates remain unchanged; safe not-found absence stays intact
- [x] #3 Real client/engine regression is red on unchanged code and green after fix; lifecycle assertion rejects the interruption
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop54: real HTTP-client-only interruption red proof with active outer context; preserve exact-read conflict and safe absence predicates, sanitized causes, remote focused proof and guarded review.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Loop54 landed 63fccfea90d76092e4008fb2f27ecfe6450bc7e4. Exact one-line engineReadExact cause wrap preserves conflict plus sanitized client interruption; all presence/inventory/hash/refusal and safe404 decisions unchanged. Real TLS five-caller active-outer-context regression red at unchanged source, green complete scoped package; lifecycle helper rejects interruption. Guarded independent review and dsreview PASS; CodeRabbit complete2/2 zero findings; composed remote full just check passed clean exact tree6831e8256f9d9a2dee507b3a95d522bbbde5a343 run20261023T193031Z-synthkit-81f0e82a, local privacy guard clean2003files. Hosted exact-SHA CI pending. Generation/dump inapplicable: no schema/skill/emitted signal changes. No private-cancellation/body-read normalization or runtime admission claim.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Preserved sanitized HTTP-client-only interruption causes under ErrConflict at the exact-read helper. Required refusal semantics unchanged; real TLS red/green, guarded review, complete CodeRabbit and exact composed gate passed before publication.
<!-- SECTION:FINAL_SUMMARY:END -->
