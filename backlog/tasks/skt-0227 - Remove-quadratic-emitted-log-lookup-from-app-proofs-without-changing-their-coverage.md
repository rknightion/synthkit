---
id: SKT-0227
title: >-
  Remove quadratic emitted-log lookup from app proofs without changing their
  coverage
status: In Progress
assignee: []
created_date: '2026-10-26T01:06:00Z'
updated_date: '2026-10-26T04:58:00Z'
labels:
  - performance
dependencies: []
priority: high
type: bug
ordinal: 433000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The causal stack passes plain tests but the app race package exceeds its unchanged 600-second budget. Static inspection shows the real-output test helper rescans every service log for every request, and handled-worker checks add another full scan. This is redundant lookup work, not redundant assertions; its material share of the current exact-source failure is not yet proven. Establish a narrow baseline, index the actual captured logs while retaining every match and assertion, and verify the original package budget without reducing populations or weakening the gate.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Indexed lookups return exactly the original full-scan corpus, preserving service/span membership, structured-metadata precedence including explicit empty values, all duplicate matches and emission order; a discriminating negative control detects omitted eligible rows
- [ ] #2 Existing app proofs retain their complete cases, seeds, request counts, emitted surfaces, assertions and 600-second race timeout; the exact repaired app race package passes within that unchanged budget
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Read-only bounded attribution on materially changed exact integrated source65ef after actual app race600.180s failure: at most two scoped profiles, meaningful race-instrumented costs and per-test contributions, no source/test/case/population/timeout changes or full-gate retry. Return quantified minimal repair scope or blocker; no implementation authority inferred.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Loop60 closeout: Final68e11900 is correctness-reviewed only: independent raw-capture oracle and omitted-corpus controls resolve the service-review major, with all original cases retained. Actual app race still fails600.052s (181 passed, one in flight,83 unreached). Not Done or a budget fix; no unchanged broad resampling. Resume on a genuinely changed exact final composition after cost resolution.

Exact integrated65ef full gate failed app-race600.180s; terminal AppDefaultWireParity had run42s and is not established as the cause of cumulative timeout. SpanID/index/worker/lifecycle changes are present, CodeRabbit54of54 clean, but no race-budget clearance or publication. Independent senior read-only attribution starts from immutable65ef; original first-dispatch lineage retained, four-hour point remains05:06Z. Private demo proof second-stop does not authorize numerical or private repair through this task.

Clock correction from the native append-only dispatch event: first appindex dispatch was2026-10-26T01:07:59Z, so the four-hour point is05:07:59Z. Earlier01:06/05:06 shorthand used the board-creation time; this correction is not an attempt or clock reset. Current read-only attribution dispatch04:40:35Z does not reset the lineage.

Read-only exact-source profiles completed: parity helper duplicates complete JSON encoding, a material old helper cost; State.Collect was not its sampled CPU bottleneck. Selected causal model callers separately dominate sampled allocation, outside new repair authority. A distinct byte-exact parity-helper prerequisite is tracked; the original aggregate-budget lineage/four-hour point remains unchanged, and the600.180s gate is not cleared by scoped passes.
<!-- SECTION:NOTES:END -->
