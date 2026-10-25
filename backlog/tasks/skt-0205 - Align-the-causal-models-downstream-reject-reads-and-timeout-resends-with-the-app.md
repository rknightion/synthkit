---
id: SKT-0205
title: >-
  Align the causal model's downstream reject reads and timeout resends with the
  app
status: In Progress
assignee:
  - '@loop59-root'
created_date: '2026-10-25T13:38:00Z'
updated_date: '2026-10-25T21:00:00Z'
labels:
  - causal
dependencies: []
ordinal: 409000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Landing-12 review (codex/loop58/rv12/review.md m6): the flow model reads a downstream capacity resource's reject share one step late when that resource solves after the gated one, so model arrivals and app attempts disagree for one step at fault onset; and the app resends timeouts and legacy hop error draws that the model does not count as demand.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Model arrivals equal app delivered attempts at fault onset
- [ ] #2 Timeout and legacy hop error resends count as demand where the app resends them
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Restore the accepted archived stack as a disposable baseline, rebase the accepted downstream-reject/resend candidate, and replace the known nested-child timeout approximation with sequential child elapsed-time accounting. Preserve onset fixed-point behavior, prove model/app attempts parity, and return only the task delta for composition.

After first convergence trip, share the actual admitted acquisition-wait law and clipping with expected-flow timeout timing, preserve sampler and no-timeout identity, and audit newly introduced binary64 products with an actual cross-architecture timeout fixture. No ordinary repair loop beyond the escalation.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop58 closeout (2026-10-25): parked unlanded; see /Users/rob/repos/synthkit/codex/closeout-2026-10-25-loop58.md. Accepted on scratch: codex/loop58/maln/candidate.patch (4e34f6eb… on d61cf24e) plus fielddocs/docs hunks; R2 (timeout share approximates child calls nested in own work) and R3 (spike test correction) for ratification.

First convergence tripwire after legacy-duration omission and independent review findings for admitted pool-wait distribution plus architecture-dependent fused arithmetic. Senior escalation will share actual acquisition-wait semantics with expected timing and preserve all sampler, timing and identity checks. Current candidate is not accepted for landing.
<!-- SECTION:NOTES:END -->
