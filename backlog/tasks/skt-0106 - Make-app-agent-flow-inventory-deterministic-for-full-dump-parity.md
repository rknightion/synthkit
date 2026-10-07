---
id: SKT-0106
title: Make app agent-flow inventory deterministic for full dump parity
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-07 21:57'
updated_date: '2026-10-07 22:54'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 228000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Repeated unchanged-base full dump differs in execute_tool classify_triage. App minter crypto-random SpanID drives hashed agent/tool selection, so wall-clock phase scheduling cannot obtain required full byte parity. Existing fixed-tick determinism repair covered other minters, not this path. Repair only this proven cause; do not filter output or make test-only exceptions.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Same fixed tick and blueprint produce identical app agent/tool inventory without collisions between requests or ticks
- [x] #2 Regression fails on current app minter and passes the repair using real minter to trace-inventory boundary
- [x] #3 Two complete just dump outputs on repaired baseline are byte-identical; just check passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use established tick/request deterministic correlation pattern from ai_agent; reproduce first, repair app minter and focused regression, run complete dump parity and final gate, independent review before root landing; then phase candidate rebases on deterministic baseline.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 2, review-repair rounds 0. Landa8610b9725e9f253d4f0b0f01ef08ed90c458330; composed gate green. Integrated CI run37696196429 atf3d701a1a4500eb2944a0a354c53a9c641fed371 green; earliera861 run37694881353 cancelled, not pass. TestAppDefaultWireParity updated six strict digest expectations because intended deterministic correlation removes random draws and shifts later generated child IDs. Independent audit of13 raw payload pairs proves only child span/parent IDs change, all non-ID fields/topology/counts/metrics/logs unchanged. No assertions weakened or baseline blindly regenerated. Two complete dumps byte-identical; no blueprint/config/skill generation needed. Routine aggregate review scheduled.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Seed app request correlations by established tick/request identity, removing random agent/tool inventory selection. Real minter-to-trace regression failed base and passes candidate; uniqueness tested. Evidence /tmp/skh-P106-proof/attempt2/acceptance.json and /tmp/skh-P106-review.log; whole dump parity plus exact composed gate/CI green.
<!-- SECTION:FINAL_SUMMARY:END -->
