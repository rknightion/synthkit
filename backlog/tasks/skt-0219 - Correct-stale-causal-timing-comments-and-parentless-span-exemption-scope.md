---
id: SKT-0219
title: Correct stale causal timing comments and parentless-span exemption scope
status: To Do
assignee: []
created_date: '2026-10-25T19:30:00Z'
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
