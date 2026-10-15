---
id: SKT-0101
title: Profile and reduce the per-process memory baseline
status: Done
assignee: []
created_date: '2026-10-07 20:31'
updated_date: '2026-10-15 12:34'
labels:
  - performance
dependencies: []
references:
  - /Users/rob/repos/synthkit/codex/memory-profile-20261015/baseline.heap
  - /Users/rob/repos/synthkit/codex/memory-profile-20261015/candidate.heap
  - /Users/rob/repos/synthkit/codex/memory-profile-20261015/method.txt
  - /Users/rob/repos/synthkit/codex/memory-profile-20261015/final-per-item.heap
ordinal: 223000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A single small blueprint has a heap floor of about 250 MiB (docs/kubernetes.md measurements), mostly fixed cost rather than per-blueprint. Deployments running many small instances pay it per pod, twice under HA. Profile the floor on Linux under live delivery (not DRY_RUN), identify the fixed allocations (registry, catalogue, queue preallocation), and reduce what is safe.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Heap profile of a one-blueprint process on Linux attached to the task
- [x] #2 Baseline reduced with measurements before and after; no inventory change in just dump
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Authorized 2026-10-15. Profiling one otlp-native blueprint on Linux under non-DRY_RUN delivery to a local TLS HTTP receiver; no external stack, credentials or .env mutation. Queue capacity currently eagerly allocates full value slots. Baseline measurement precedes a bounded queue-only reduction and before/after comparison.

2026-10-15 candidate: pointer channel slots plus value-owned chunks (up to 256 items) reduce idle queue allocation while preserving capacity/sharding/backpressure. Linux arm64 non-DRY_RUN otlp-native 90s local-TLS receiver proof: retained heap 235061168 -> 51474528 bytes (78.1% reduction), four signal paths received real bodies. Profiles and receiver counts retained under /Users/rob/repos/synthkit/codex/memory-profile-20261015. No Grafana ingestion claim or resource-limit change. Microbenchmark median 1000-item batch 260us -> 374us, ~262KiB additional temporary allocation, four additional allocations. Chunk retention under skewed backpressure documented. Fail-first memory regression: 25626112 allocated for 100000 empty 256-byte slots; candidate passes <=4MiB. Alias fault injection fails ownership/order assertion; corrected code passes focused race. Queue, runner/Sigil delivery checks pass; otlp-native just dump 1201-line inventories are byte-identical. Baseline/candidate windows differ in tick phase; concurrent additive podlog LogResource field is present in candidate. Root owns final review, gates and integration.

Root review found and reproduced cross-shard chunk retention: 1000 stalled values retained 65.4 MB after 255000 fast-shard values drained. Independent per-item ownership overlay retains 244 KB, with similar median batch time (380us versus 374us). Initial CodeRabbit completed with zero findings, but root pressure finding remains material. Initial composed gate passed plain tests then was deliberately interrupted before completing race; no full-gate pass claimed. Correct candidate and Linux remeasurement required before landing.

Final correction 2026-10-15: root review identified cross-shard chunk retention. Reproduced 65,447,208 bytes retained by 1000 stalled items after 255000 fast items drained; independent per-item ownership reduced this to 244,192 bytes. Replaced shared chunks with one owned value per queued item, allocating after cancellation/stop guards; permanent skew regression and focused queue race pass. Final Linux arm64 90s real TLS delivery profile: HeapAlloc 51,482,040 bytes (49.1 MiB), 78.1% below original 235,061,168 bytes. Receiver consumed 44 RW2, 68 Loki, 7 OTLP metric and 34 OTLP trace requests. Final profiles/binary/hash/inventory retained with final-per-item prefix under /Users/rob/repos/synthkit/codex/memory-profile-20261015. Inventory byte-identical; focused runner/Sigil delivery tests pass. Paired local arm64 -cpu=2 benchmark median per 1000 256-byte items: original 160us/19 allocations, final 221us/1019 allocations; +250KiB temporary allocation. This supersedes the earlier chunk implementation and its tradeoff figures, whose evidence remains retained. Root owns final delta review/full gate; no local commit.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Independent per-item queue storage reduces Linux live-delivery retained heap from 224.2 to 49.1 MiB (78.1%). Pressure regression prevents cross-shard retention; focused race/delivery and byte-identical inventory passed. Paired no-op benchmark increases median batch time 160 to 221 us with 250 KiB and 1000 added allocations; chart sizing unchanged. Earlier chunk candidate rejected and retained as evidence. Final corrected just check passed; just gen and docs validation passed; initial CodeRabbit and corrective-delta CodeRabbit completed with zero findings. Landed in 0ca33e28187f539f090a423326c63a05e8af501a; exact CI https://github.com/rknightion/synthkit/actions/runs/38732291551 passed every required job. Initial full gate was deliberately interrupted after the chunk-pressure finding and is not claimed green.
<!-- SECTION:FINAL_SUMMARY:END -->
