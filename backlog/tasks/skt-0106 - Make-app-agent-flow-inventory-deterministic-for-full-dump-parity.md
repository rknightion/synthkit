---
id: SKT-0106
title: Make app agent-flow inventory deterministic for full dump parity
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-07 21:57'
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
- [ ] #1 Same fixed tick and blueprint produce identical app agent/tool inventory without collisions between requests or ticks
- [ ] #2 Regression fails on current app minter and passes the repair using real minter to trace-inventory boundary
- [ ] #3 Two complete just dump outputs on repaired baseline are byte-identical; just check passes
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Use established tick/request deterministic correlation pattern from ai_agent; reproduce first, repair app minter and focused regression, run complete dump parity and final gate, independent review before root landing; then phase candidate rebases on deterministic baseline.
<!-- SECTION:PLAN:END -->
