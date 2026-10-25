---
id: SKT-0203
title: Join a synchronously invoked function's trace to its caller during the call
status: In Progress
assignee: []
created_date: '2026-10-25T12:55:00Z'
updated_date: '2026-10-25T23:05:00Z'
labels:
  - causal
dependencies: []
ordinal: 407000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Trace context currently joins only queue consumers, and their span starts after the producer ends. A function invoked synchronously by an app hop (request/response invoke) should run inside the caller's span window and share its trace, so a synchronous receiver is not shown running after the API answered. Found while validating a private demo blueprint against the current engine (codex/loop58/pbp3/return.md).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A synchronously invoked bound function's root span shares the caller's trace and sits inside the calling hop's window
- [ ] #2 Queue-triggered workers and unbound functions unchanged
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

Integrated native review found count-based pending expiry can discard fresh synchronous calls after competing consumption or bounded eviction. Same composition lane repairs with a real failing shared-exchange regression and stable identity/watermark; preserve FIFO, caps and late same-tick publication. No source acceptance yet.
<!-- SECTION:NOTES:END -->
