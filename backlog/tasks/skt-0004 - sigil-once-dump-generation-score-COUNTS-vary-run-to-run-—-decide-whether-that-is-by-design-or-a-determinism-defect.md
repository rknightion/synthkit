---
id: SKT-0004
title: >-
  sigil: -once -dump generation/score COUNTS vary run-to-run — decide whether
  that is by design or a determinism defect
status: Done
assignee: []
created_date: '2026-08-19 07:34'
updated_date: '2026-08-27 08:07'
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
- [x] #1 A decision is recorded on whether run-to-run variance in the -once -dump generation/score counts is by design (wall-clock-dependent in-flight request set) or a determinism defect, with the specific code path that produces the variance named at file:line as the evidence.
- [ ] #2 If it is a defect, the offending draw is changed to derive from a seed unit and two consecutive -once -dump runs then report identical counts.
- [x] #3 If it is by design, the structural-inventory projection (sink, metric name, sorted label keys) is what gets compared, and the determinism wording used in sigil task acceptance criteria is corrected to say so rather than implying the count lines must match.
- [x] #4 The exemplar-sampling behaviour of the [dry-run] lines is documented wherever the -once -dump inventory check is described, so nobody diffs two raw dumps again and reads sampling noise as drift.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
STRONGER EVIDENCE (2026-08-19, same session that filed this): the dump's authoritative inventory section — the '<metric> {[sorted label keys]}' block, not the sampled '[dry-run] N series e.g. …' lines — is BYTE-IDENTICAL across two consecutive runs: 2668 lines each, 'diff' exit 0. So the emitted contract is fully deterministic and the '-once -dump' inventory gate PASSES; the ONLY thing that moves between runs is the single '== sigil: generations=N workflow_steps=50 scores=N ==' summary line (workflow_steps itself is stable at 50). That narrows this task a lot: the by-design reading is now the strong favourite, and whoever picks this up should start from the sigil count summary's arrival path rather than suspecting the emit path. It also means the AC-wording fix in AC #3 is the likely real deliverable here.

RESOLVED 2026-08-27 (lane L6, verified by the wiring pass). Verdict: BY DESIGN. No code change — that is the correct outcome, not a shortfall.

## The code path, named

1. internal/ledger/request.go:190-196 — uuid() mints every correlation identifier from crypto/rand (import confirmed at :14), deliberately unseeded per the package I9 comment: nothing outside the ledger mints request-scoped IDs, and that is what makes one correlation_id thread across every signal class.
2. internal/workload/aiagent/minter.go:71 — mintOne calls ledger.NewCorrelation(), so every conversation gets a run-unique SessionID.
3. internal/workload/aiagent/minter.go:88-124 — TurnCount is a deterministic FNV hash OF THAT SessionID. Deterministic for a given id; the id is fresh each run, so the turn-count population reshuffles.
4. internal/workload/aiagent/conversation.go:73 — turns := TurnCount(agent, r.SessionID) sets how many sigil.Generations a conversation emits.
5. cmd/synthkit/main.go:827 sums across the batch, so generations= and scores= inherit the reshuffle.

Why workflow_steps stays pinned at 50: internal/workload/aiagent/archetypes.go:66-74 emits exactly ONE WorkflowStep per conversation regardless of turn count, and the conversation COUNT is fully deterministic — minter.go:56-63 draws via ledger.StochasticRound over the shape engine fixed-seed PCG stream (internal/shape/shape.go:376-383), which is identical every run.

## Why this is by design rather than an I12 violation

I12 governs SHAPE and CONTENT determinism given an identity, and it holds — TurnCount is a pure function of SessionID. The correlation-ID freshness driving the aggregate variance is I9, a separate and equally load-bearing requirement (unique, unguessable correlation ids). The two rules are not in conflict; the summary line simply aggregates across identities that are deliberately fresh.

Ruled out explicitly: no wall-clock draw and no map-iteration-order dependence anywhere in the path. shape.New/AddBlueprint gives one engine per blueprint with no cross-blueprint sharing, ledger.Mint iterates a slice, and runner.RunOnce is fully serial with minting before any other RNG consumer in the tick.

## The real deliverable — corrected AC wording for future sigil tasks

"Two consecutive `DRY_RUN=true go run ./cmd/synthkit -once -dump` runs produce an IDENTICAL structural inventory: for every sink, the `<name> {[sorted label/attribute keys]}` block (series/stream/span names, label and attribute key sets, and — for sigil — the ingest-kind to operation-name mapping) diffs clean. The sigil `== sigil: generations=N workflow_steps=N scores=N ==` summary line is NOT part of this check and is expected to vary run to run (fresh per-conversation correlation IDs, by design — see docs/cli.md); do not compare it, and do not treat its variance as a regression."

## Documentation

docs/cli.md gained a "What -dump output is deterministic, and what isn not" block covering all three: the structural inventory is the contract and is byte-identical; the [dry-run <sink>] lines each sample ONE random exemplar per batch and must never be raw-diffed; the sigil summary counts vary by design while workflow_steps and the conversation count do not.

## Reproduction

The default blueprint selection emits no sigil traffic at all, so the reproduction needs an explicit blueprint:

  $ for i in 1 2; do DRY_RUN=true BLUEPRINT_NAMES=otlp-native go run ./cmd/synthkit -once -dump 2>&1 | grep "== sigil: gen"; done
  == sigil: generations=176 workflow_steps=50 scores=87 ==
  == sigil: generations=194 workflow_steps=50 scores=90 ==

Exactly the pattern this task recorded: workflow_steps pinned, generations/scores moving.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Verdict: BY DESIGN, not a determinism defect, with the code path named. Correlation IDs are minted from crypto/rand (internal/ledger/request.go:190, invariant I9 — nothing outside the ledger mints request-scoped IDs), so every conversation gets a run-unique SessionID; minter.go:88 derives TurnCount by hashing THAT id, so the turn-count population reshuffles while staying deterministic per identity. workflow_steps stays pinned at 50 because one WorkflowStep is emitted per conversation and the conversation count draws from the fixed-seed shape engine. I12 governs shape-given-identity and holds; wall-clock and map-iteration order were ruled out across shape.New, ledger.Mint and runner.RunOnce.

AC #2 is deliberately unchecked and not applicable: it is conditional on the defect reading, which lost. Forcing a wall-clock-independent in-flight set would make the generator less realistic in order to satisfy a mis-worded check.

The real deliverable was AC #3 — the acceptance wording, now corrected so future sigil tasks compare the STRUCTURAL inventory rather than the count summary. The old wording was literally unsatisfiable for any change, so lanes were being handed a check they could not meet. AC #4 is documented in docs/cli.md and covers all three points: the inventory block is byte-identical run to run, the [dry-run] lines sample one random exemplar per batch and must never be raw-diffed, and the sigil counts vary by design.

Verified: two consecutive DRY_RUN=true BLUEPRINT_NAMES=otlp-native -once -dump runs reproduce the pattern exactly (generations 176 then 194, workflow_steps 50 both). make gate green. No code change was needed, which is the correct outcome here rather than a shortfall.
<!-- SECTION:FINAL_SUMMARY:END -->
