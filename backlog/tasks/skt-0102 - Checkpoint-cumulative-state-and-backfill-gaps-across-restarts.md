---
id: SKT-0102
title: Checkpoint cumulative state and backfill gaps across restarts
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-07 20:31'
updated_date: '2026-10-15 15:10'
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
- [x] #1 Design note choosing the checkpoint store and cadence
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

Selected opt-in shared-filesystem backend; existing ConfigMaps retain only small control state. Commit an immutable exact-payload plus complete post-cycle-state transaction before delivery for every emitted cycle. Compact snapshots by bounded byte/count thresholds after measuring write volume; no arbitrary periodic snapshot interval can certify continuity. Runtime mode remains disabled until full state admission, genuine lease epochs, provider durability and timestamp-bounded replay are proven.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-15 source assessment: periodic generic-state snapshots alone cannot meet acceptance. Queue background failures are discarded and later Flush is not a durable delivery receipt; emitters also own lifecycle/native-OTLP/minter state beyond state.State, and RNG/ledger replay is not deterministic. Correct design requires versioned per-instance restoration, durable exact payload plus post-cycle-state transactions before delivery, per-fragment acknowledgement, destination/configuration binding and stale-writer CAS. Owner store/stack decisions pending via async prompt. No runtime change or continuity claim.

Design revision1 retained at /Users/rob/repos/synthkit/codex/backlog-pickup-20261015/checkpoint-design.md. Explicit transaction/outbox, state coverage, conditional store fencing, protocol acceptance and timestamp cutoff requirements; store and validation-stack decisions remain pending. Current source alone does not establish counter continuity.

Independent design challenge required epoch-bound CAS, immutable serialized wire bytes and complete chronological master-tick replay, not only emitter due positions. Proposal revision2 corrects these and explicitly rejects checkpoint mode for any unsupported selected runtime path. Store/stack/scope decisions remain pending.

Independent re-review accepted design revision2 with no remaining design blockers. Revision3 clarifies explicit gaps and leadership-authorized epoch acquisition. Store/provider and live-stack selections remain pending; no checkpoint runtime or live verification is claimed.

Owner 2026-10-15 clarified existing ConfigMap expectation, approved shared filesystem mounts as the checkpoint direction, selected the isolated staff stack for bounded verification, and explicitly approved delivery-acknowledgement/checkpoint foundations as increments while retaining this task open until takeover/backfill is proved. Standalone Docker defaults remain unchanged; no S3 dependency. Existing ConfigMaps remain small control-state stores, not per-series payload storage.

Public tracker wording redacts the deployment identifier; the exact owner-selected verification destination remains in machine-local evidence. No scan exception or history rewrite.

Foundation increment implements bounded OTLP/RW2 acceptance classification, sanitized status counts, queue background-failure reporting and an optional binding-aware filesystem store with checksummed atomic manifests, immutable payloads, exclusive locks and epoch/revision CAS. Actual HTTP and cross-process filesystem tests include fail-first and negative proofs; affected race packages pass. CodeRabbit completed all 20 files with one minor suggestion to delete cancelled temporary writes, deliberately retained under the evidence-preserving fail-closed contract and documented recovery limits. Root found and corrected malformed OTLP diagnostic JSON acceptance; delta review and composed final gate pending. No runtime snapshots, durable outbox, takeover, backfill or live deployment claimed; acceptance criteria 2 and 3 remain open.

Foundation increment published as 86634ea40952074beedeb4751609f63b5b9cf8ac. Full composed just check passed, affected race/HTTP/subprocess/mutation proofs passed, initial CodeRabbit reviewed all 20 files with the documented minor cancellation-retention choice, and the three-file parser/documentation delta review completed with zero findings. Exact CI 38753576146 attempt 2 passed all required jobs, including real e2e; the sole first-attempt chart failure was a dependency proxy download error, and unchanged-SHA retry passed. Evidence: /Users/rob/repos/synthkit/codex/backlog-pickup-20261015/checkpoint-composed-check.log and /Users/rob/repos/synthkit/codex/backlog-pickup-20261015/foundation-ci-final.json; detailed protocol/filesystem evidence retained under /Users/rob/.local/share/synthkit-worktrees/interactive-checkpoint-20261015/codex/. Parent remains In Progress: per-emitter and auxiliary-state restore, durable exact-byte outbox/fragment receipts, real lease integration, enrollment/lost-volume distinction, shared-provider validation, bounded backfill and live out-of-order readback are not implemented or proven. No deployment or runtime checkpoint enablement.
<!-- SECTION:NOTES:END -->
