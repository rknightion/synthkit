---
id: SKT-0102
title: Checkpoint cumulative state and backfill gaps across restarts
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-15 12:41'
labels:
  - feature
  - ha
dependencies:
  - SKT-0092
  - SKT-0095
ordinal: 224000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Phase 2 of HA. Even with lease handoff, a takeover resets cumulative counters (I29) and loses the ticks between the last push and takeover. Leader checkpoints cumulative series state and the last pushed tick; a new leader resumes counters and backfills missed ticks within out-of-order windows (Grafana Cloud metrics accept 2h behind newest per series, Loki 1h per stream). Per-series state is too large and too frequent for a ConfigMap: needs a pluggable external store. Decide store and checkpoint cadence first; verify OOO behaviour against a real stack.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Design note choosing the checkpoint store and cadence
- [ ] #2 Takeover in a test resumes counters without a reset and backfills missed ticks
- [ ] #3 Backfill never exceeds the configured out-of-order window
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
2026-10-15 authorized backlog pickup: inspect cumulative state, delivery acknowledgement and HA ownership seams; select durable store/cadence with an independently challenged design before implementation; preserve exact delivery and out-of-order-window evidence.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-15 source assessment: periodic generic-state snapshots alone cannot meet acceptance. Queue background failures are discarded and later Flush is not a durable delivery receipt; emitters also own lifecycle/native-OTLP/minter state beyond state.State, and RNG/ledger replay is not deterministic. Correct design requires versioned per-instance restoration, durable exact payload plus post-cycle-state transactions before delivery, per-fragment acknowledgement, destination/configuration binding and stale-writer CAS. Owner store/stack decisions pending via async prompt. No runtime change or continuity claim.

Design revision1 retained at /Users/rob/repos/synthkit/codex/backlog-pickup-20261015/checkpoint-design.md. Explicit transaction/outbox, state coverage, conditional store fencing, protocol acceptance and timestamp cutoff requirements; store and validation-stack decisions remain pending. Current source alone does not establish counter continuity.

Independent design challenge required epoch-bound CAS, immutable serialized wire bytes and complete chronological master-tick replay, not only emitter due positions. Proposal revision2 corrects these and explicitly rejects checkpoint mode for any unsupported selected runtime path. Store/stack/scope decisions remain pending.

Independent re-review accepted design revision2 with no remaining design blockers. Revision3 clarifies explicit gaps and leadership-authorized epoch acquisition. Store/provider and live-stack selections remain pending; no checkpoint runtime or live verification is claimed.

Owner 2026-10-15 clarified existing ConfigMap expectation, approved shared filesystem mounts as the checkpoint direction, selected rkaidev for bounded verification, and explicitly approved delivery-acknowledgement/checkpoint foundations as increments while retaining this task open until takeover/backfill is proved. Standalone Docker defaults remain unchanged; no S3 dependency. Existing ConfigMaps remain small control-state stores, not per-series payload storage.
<!-- SECTION:NOTES:END -->
