---
id: SKT-0001
title: >-
  sigil: carry a turn's tool-results into the next turn's input, and
  per-subagent agent_name for claude-code children
status: Done
assignee: []
created_date: '2026-08-14 16:09'
updated_date: '2026-08-19 07:40'
labels: []
dependencies: []
references:
  - signals/sigil.md
  - internal/workload/aiagent/orchestration.go
  - internal/workload/aiagent/conversation.go
ordinal: 1000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two modeling nits left open by the sigil feature, recorded in signals/sigil.md "Implementation status & next steps" item 1 (the resume list).

1. **Tool-result carry-forward is in the wrong turn.** Today a turn's tool results sit in the SAME `AssembledTurn.Input` as the call that produced them. Real agent transcripts carry a tool result forward into the NEXT turn's input, because that is when the model actually sees it. The current shape is structurally wrong for anyone reading a generation trail turn by turn.

2. **Per-subagent `agent_name` for `claude-code/<subagent>` child conversations.** The `general_orchestration` archetype already fans out correctly (internal/workload/aiagent/orchestration.go mints sub-agent generations with distinct AgentName drawn from agent.Subagents, parented to the orchestrator gen). The coding `claude-code` archetype does not do the equivalent for its child conversations, so its sub-agent activity is not attributable by agent_name.

Source of truth for the vocabulary is signals/sigil.md. Do NOT invent an attribute name — every name comes from that file or from vendor docs via ctx7.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A turn's tool results appear in the NEXT turn's AssembledTurn.Input, not the same one; the first turn of a conversation carries no inherited results and the last turn's results are not silently dropped.
- [x] #2 claude-code child conversations carry a per-subagent agent_name, consistent with how orchestration.go already attributes general_orchestration sub-agent generations.
- [x] #3 No new metric, label, span or attribute name is introduced that is not already in signals/sigil.md; if one is needed and cannot be sourced, work stops and a cantfind.md PENDING is added instead.
- [x] #4 Output stays deterministic across two DRY_RUN=true go run ./cmd/synthkit -once -dump runs (decisions derived from seedUnit, never wall clock).
- [x] #5 signals/sigil.md "Implementation status & next steps" item 1 is updated to reflect what now ships.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [x] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [x] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave lane (Sonnet subagent, dispatched 2026-08-19): (1) TDD the tool-result carry-forward shift so a turn's results land in the NEXT AssembledTurn.Input; (2) mirror orchestration.go's per-subagent AgentName minting into the claude-code archetype's child conversations; (3) lane returns the signals/sigil.md item-1 replacement prose for the main thread to apply, since signals/ is single-owner.
<!-- SECTION:PLAN:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Both modelling nits shipped in 63ababe.

AC #1 — tool-result carry-forward. internal/sigil/assemble.go: AssembleConversation now threads a carry slice turn-to-turn; assembleTurn takes carryIn and RETURNS this turn's own tool-result for the caller to feed into the NEXT turn's Input, instead of appending it to the same turn's inputMsgs. Turn 0 inherits nothing. The last turn's results, having no following turn to carry into, are appended to that same final turn's own Input rather than dropped — the documented no-drop decision. Tests written first and watched fail for the right reason ('turn 0 Input carries an inherited tool_result'): TestAssembleConversationToolResultCarriesToNextTurn, TestAssembleConversationLastTurnToolResultNotDropped.

AC #2 — coding subagent attribution. internal/workload/aiagent/orchestration.go: makeSubAgent generalised to take agentName + parentSpanID instead of hardcoding the peer name and art.envSpanID, and a new buildCodingSubagentFanout mirrors buildOrchestrationFanout's shape (turn 0 to every declared peer, later turns one seeded peer at delegationProb), wired in conversation.go:122. agent_name is agent.Name + '/' + peer, which is DERIVED FROM BLUEPRINT IDENTITY rather than hardcoded: the showcase blueprint declares 'name: claude-code' with 'subagents: [general-purpose, explore, subagent]', so it yields exactly the captured claude-code/<subagent> form, while a differently-named coding agent gets its own correct prefix. Sub-agent spans nest under the delegating turn's OWN root span, not an agents.base envelope — coding's 2-level tree has none, so nesting under one would have produced a dangling parent reference. Covered by TestCodingClaudeCodeSubagentFanout, which the lane proved catches a regression by no-op'ing the wiring and watching it fail.

AC #3 — no invented names. Both agent_name forms are sourced from signals/sigil.md (lines 64, 250, 320 document claude-code/<subagent>). No new metric, label, span or attribute name was introduced, so no cantfind.md PENDING was needed.

AC #4 — determinism. Two DRY_RUN -once -dump runs produce a BYTE-IDENTICAL series inventory: 2668 metric-name + sorted-label-key shapes, 'diff' exit 0. The seeded rng draw order is unchanged by the carry-forward fix (pure routing of an already-drawn message), and the new fanout derives every decision from seedUnit/seedHash on the generation ID. Note the run-to-run variance in the '== sigil: generations=N ... scores=N ==' summary line is PRE-EXISTING and NOT from this change — reproduced at 183 vs 199 on a clean throwaway worktree of the base commit; filed as SKT-0004.

AC #5 — signals/sigil.md updated: the shipped work moved INTO the 'Emitted + verified' paragraph (coding sub-agent fan-out + turn-accurate tool-result carry-forward) rather than left sitting under the 'PENDING — next steps' heading, which a CodeRabbit minor finding correctly flagged; the pending list now holds only the heuristic-evaluator item.

Verification: 'make gate' GREEN, exit 0 — build, vet, test, race (no data races), rw-proto-check, spdx-check (593 .go files), forbidden-words (846 files) (DoD #1). 'make blueprint-schema' regenerated: the generalised subAgentGen.parentEnv doc comment made internal/blueprintschema/fielddocs.json stale and TestSchemaCurrent caught it; one line changed, committed (DoD #2). Inventory diff as above (DoD #3). CodeRabbit review: 1 finding, minor, on signals/sigil.md — applied, not dismissed; zero critical or major.
<!-- SECTION:FINAL_SUMMARY:END -->
