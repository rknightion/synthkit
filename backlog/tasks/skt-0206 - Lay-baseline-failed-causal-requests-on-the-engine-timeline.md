---
id: SKT-0206
title: Lay baseline-failed causal requests on the engine timeline
status: Parked
assignee: []
created_date: '2026-10-25T13:38:00Z'
updated_date: '2026-10-25T22:19:00Z'
labels:
  - causal
dependencies: []
ordinal: 410000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Landing-12 review M1 (codex/loop58/rv12/review.md): after SKT-0200, requests failed only by the entry's baseline error draw keep the legacy span nesting (D85), so error traces in healthy windows show untouched services about 4x slower than success traces, and learnHealthySpans adds their hop durations to the CLIENT/SERVER healthy references, biasing faulted-row factors low by about error_rate x 3. Must be fixed before a shipped blueprint opts in.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Baseline-failed causal requests use the engine timeline while keeping their drawn status and error code
- [ ] #2 Healthy span references exclude any request not laid out on the engine timeline, and faulted-row factors match the engine factor within sampling noise at 1% and 5% error rates
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58 closeout (2026-10-25): parked unlanded; see /Users/rob/repos/synthkit/codex/closeout-2026-10-25-loop58.md. Accepted and in landing-13 mbox (codex/loop58/archive/land13-d4a51b34..3c664fc6.mbox); landing blocked by SKT-0211 (amd64 FMA digest drift). Resume: fix SKT-0211, re-gate, land.

Owner closeout: accepted prior timing/join candidate remains in the retained archived causal stack, not in published database-only delivery. Resume from the explicit engine-only split with accepted timestamp/resend fixes, preserving existing assertions and excluding the held reference migration; final composition, generation, review and exact-tree gate remain required.
<!-- SECTION:NOTES:END -->
