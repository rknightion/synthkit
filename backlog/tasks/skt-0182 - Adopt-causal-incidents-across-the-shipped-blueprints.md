---
id: SKT-0182
title: Adopt causal incidents across the shipped blueprints
status: Parked
assignee: []
created_date: '2026-10-25T02:12:00Z'
updated_date: '2026-10-26T00:47:00Z'
labels:
  - needs-owner
dependencies:
  - SKT-0168.09.15
priority: medium
ordinal: 370000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0168.09.15 adds an opt-in model where request latency and errors come from resource state and declared faults, so logs, traces and metrics carry the same cause. Shipped blueprints keep today's rate-scaled scenarios until migrated. Migrate those with failure stories and make the causal form the authoring default.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every shipped blueprint with a failure scenario declares a cause whose evidence appears in the failing node's logs and traces, or records why it cannot
- [ ] #2 Blueprint authoring docs and the authoring skill teach the causal form first
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Owner approved labelled reference-workload assumptions anchored to published latency ranges, with explicit query mix, deadlines and affected input groups. Compose the reference family on repaired causal source, preserve ordinary rates and both database lanes, reconcile bound legacy incidents, prove actual loaded healthy/onset/recovery output and retain unsupported-family reasons. Parent completion still requires the broader shipped-family ledger.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58 closeout (2026-10-25): parked unlanded; see /Users/rob/repos/synthkit/codex/closeout-2026-10-25-loop58.md. Acme migration rebased to d61cf24e (commit in /Users/rob/repos/synthkit/codex/loop58/archive/land13-d4a51b34..3c664fc6.mbox); owner held it for realism: source database call times and reject_share (realism lane partial diff in codex/loop58/archive/worktrees/loop58-real); also needs SKT-0211 (amd64 digest) first.

Owner closeout: Owner realism decision open: exact application latency/overhead/deadline/affected-input shares lack matched evidence. Recommend labelled assumptions anchored to sourced ranges; alternative matched captures. No migration.

Loop60 second convergence stop: reference migration remains unlanded. The supported distinct DB call-site experiment restored the missing generation CLIENT operation, but same-service orchestration still masks additive store elapsed time and DocumentDB physical identity is unsupported. Explicit call occurrence/physical-target and additive-work contracts are engine dependencies, not a blueprint workaround. Owner decision required before another reference repair; recommend authorizing those contracts after evaluator cost admission, then resuming per-client proof and complete golden attribution. Original failed proposals, red runner evidence and disputed service review remain retained; no golden acceptance or all-family completion.
<!-- SECTION:NOTES:END -->
