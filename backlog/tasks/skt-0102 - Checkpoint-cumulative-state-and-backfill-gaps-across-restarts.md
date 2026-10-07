---
id: SKT-0102
title: Checkpoint cumulative state and backfill gaps across restarts
status: To Do
assignee: []
created_date: '2026-10-07 20:31'
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
