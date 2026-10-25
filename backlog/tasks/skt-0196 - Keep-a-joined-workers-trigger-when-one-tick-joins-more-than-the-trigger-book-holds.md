---
id: SKT-0196
title: >-
  Keep a joined worker's trigger when one tick joins more than the trigger book
  holds
status: Done
assignee:
  - '@loop58-root'
created_date: '2026-10-25T11:52:00Z'
updated_date: '2026-10-25T12:36:00Z'
labels:
  - causal
dependencies: []
ordinal: 400000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
triggerBook in internal/workload/app/causal_trigger.go evicts at 16384 and get never deletes, so above about 546 rps at a 30 s tick the earliest joined requests keep the producer trace ID but project a CONSUMER root with no parent and no link. Found by the landing-10 review (/Users/rob/repos/synthkit/codex/loop58/rv10/review.md minor 1).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A worker joining more messages per tick than the bound still parents and links every joined root
- [ ] #2 The bound is sized from, or released by, actual use rather than a fixed count
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
