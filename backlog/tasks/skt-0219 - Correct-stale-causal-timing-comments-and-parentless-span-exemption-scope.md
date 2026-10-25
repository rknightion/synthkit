---
id: SKT-0219
title: Correct stale causal timing comments and parentless-span exemption scope
status: Parked
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-25T22:18:00Z'
labels:
  - causal
dependencies: []
ordinal: 423000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The parked landing review identified a stale timing test comment, a root exemption that may cover unrelated parentless spans, and a public blueprint comment containing operational loop identifiers.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The span nesting check exempts only intended roots, not arbitrary parentless spans
- [ ] #2 Stale causal timing comments and public operational loop identifiers are removed or corrected without weakening checks
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Apply the accepted engine-only restored baseline; locate the stale timing comment and parentless-span exemption from retained review, tighten intended-root recognition without weakening nesting checks, and remove the public operational comment only as a separate hunk for the held migration.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Accepted strict intended-root test candidate unlanded. Resume causal stack without loosening bounds; archived reference-blueprint comment remains separate.
<!-- SECTION:NOTES:END -->
