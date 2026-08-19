---
id: SKT-0004
title: >-
  sigil: -once -dump generation/score COUNTS vary run-to-run — decide whether
  that is by design or a determinism defect
status: To Do
assignee: []
created_date: '2026-08-19 07:34'
updated_date: '2026-08-19 07:38'
labels:
  - needs-triage
dependencies: []
ordinal: 4000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Discovered during the SKT-0001 / SKT-0003 wave (2026-08-19), pre-existing and NOT introduced by that wave — needs a decision, and it changes how every future sigil task's determinism acceptance criterion should be worded.

## What was observed

Two consecutive `DRY_RUN=true go run ./cmd/synthkit -once -dump` runs report DIFFERENT sigil counts:

- On the wave's working tree: `generations=191 workflow_steps=50 scores=92` then `generations=179 workflow_steps=50 scores=86`.
- On a CLEAN checkout of the same base commit, in a throwaway worktree: `generations=183 workflow_steps=50 scores=93` then `generations=199 workflow_steps=50 scores=93`. This is the load-bearing measurement — it proves the variance predates the wave.

## What is NOT varying

The STRUCTURAL inventory is byte-identical across runs once log timestamps are stripped: span names, span-attribute key sets, the sigil ingest-kind → operation-name mapping, and `workflow_steps=50`. Only the `generations=` and `scores=` counts move. So the emitted CONTRACT (names + label/attribute keys) is stable; the VOLUME is not.

Also worth recording for whoever picks this up: the `[dry-run <sink>] N series e.g. <series>` lines each log ONE randomly-sampled exemplar per batch, so a raw `diff` of two dumps is dominated by sampling noise and proves nothing either way. Any real inventory comparison has to project each line to (sink, metric name, sorted label keys) and diff the resulting SET. Projected that way, the two runs above differ only in which exemplars got sampled, not in the shape set.

## The actual question

Is a run-to-run count difference correct behaviour or a defect? The plausible by-design reading is that the set of ledger requests in flight at any instant is a function of wall-clock `now`, so a single tick legitimately catches a different number of in-flight conversations each time — which is what a live generator should do, and is not the same thing as a seeded value flipping. The plausible defect reading is that something in the arrival/eval path draws from the clock or from map iteration order where it should derive from a seed unit (invariant I12).

Establish which, by reading the arrival process rather than by guessing.

## Why it matters beyond itself

Several sigil tasks carry an acceptance criterion of the form 'output stays deterministic across two `-once -dump` runs'. As literally worded that criterion is UNSATISFIABLE today, for any change, because the count lines always move. If the by-design reading wins, that AC wording needs narrowing to the structural inventory (names + label/attribute keys), so future lanes stop being handed an AC they cannot meet and either waste a cycle on it or quietly check it anyway.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A decision is recorded on whether run-to-run variance in the -once -dump generation/score counts is by design (wall-clock-dependent in-flight request set) or a determinism defect, with the specific code path that produces the variance named at file:line as the evidence.
- [ ] #2 If it is a defect, the offending draw is changed to derive from a seed unit and two consecutive -once -dump runs then report identical counts.
- [ ] #3 If it is by design, the structural-inventory projection (sink, metric name, sorted label keys) is what gets compared, and the determinism wording used in sigil task acceptance criteria is corrected to say so rather than implying the count lines must match.
- [ ] #4 The exemplar-sampling behaviour of the [dry-run] lines is documented wherever the -once -dump inventory check is described, so nobody diffs two raw dumps again and reads sampling noise as drift.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
STRONGER EVIDENCE (2026-08-19, same session that filed this): the dump's authoritative inventory section — the '<metric> {[sorted label keys]}' block, not the sampled '[dry-run] N series e.g. …' lines — is BYTE-IDENTICAL across two consecutive runs: 2668 lines each, 'diff' exit 0. So the emitted contract is fully deterministic and the '-once -dump' inventory gate PASSES; the ONLY thing that moves between runs is the single '== sigil: generations=N workflow_steps=50 scores=N ==' summary line (workflow_steps itself is stable at 50). That narrows this task a lot: the by-design reading is now the strong favourite, and whoever picks this up should start from the sigil count summary's arrival path rather than suspecting the emit path. It also means the AC-wording fix in AC #3 is the likely real deliverable here.
<!-- SECTION:NOTES:END -->
