---
id: SKT-0218
title: Bind all five declared document-store clients to their causal resources
status: Parked
assignee: []
created_date: '2026-10-25T19:30:00Z'
updated_date: '2026-10-26T05:56:00Z'
labels:
  - causal
dependencies: []
ordinal: 422000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Only one of the reference application document-store clients is currently causally bound, leaving most client calls disconnected from resource faults.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 All five declared document-store clients in the reference application have explicit matching causal resource bindings
- [ ] #2 Healthy and faulted public-entrypoint checks retain correct per-client isolation and plausible sourced call timing
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Single reference-blueprint owner binds all five declared document-store clients to matching causal resources with sourced, labelled timing assumptions and per-client healthy/fault isolation proof.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Owner closeout: Five-client document-store binding held for sourced reference migration.

Loop60 second convergence stop, paired with SKT-0182 (reference-family realism): five pool declarations did not establish five actual store calls. Shared-leaf DFS suppresses generation; a distinct leaf restores its CLIENT span but not the required additive caller elapsed or common DocumentDB wire identity. No supported blueprint-only fix. Owner approval to reopen after explicit engine contracts and evaluator cost admission; actual per-client healthy/onset/recovery/isolation proof and golden attribution remain required.

Loop60 closeout: Second convergence stop retained: the alias restores a missing actual CLIENT call but not correct additive caller elapsed or common physical database identity. No five-client completion; explicit owner reopening plus engine contracts, actual per-client healthy/fault/recovery/isolation and golden attribution required.

Owner explicitly approves the recommended bounded engine-contract follow-up on senior/Astra: separate call occurrence from shared physical target, correct database identity, compose independent own work with synchronous calls. Implementation AFTER evaluator cost admission; then actual per-client healthy/fault/recovery/isolation and full golden attribution. Prior failed proposals and stops retained; no blueprint workaround or proof waiver.

Final loop closeout: retained unlanded causal candidateaf34 has native/component review and final CodeRabbit passes, but the exact integrated full gate fails app-race600.139s. No candidate source was pushed or deployed. Preserve task-specific earlier evidence/limits; resume only after the aggregate owner stop and relevant dependency/owner decisions are resolved, with no timeout/assertion/case/isolation reduction or unchanged full-gate retry.
<!-- SECTION:NOTES:END -->
