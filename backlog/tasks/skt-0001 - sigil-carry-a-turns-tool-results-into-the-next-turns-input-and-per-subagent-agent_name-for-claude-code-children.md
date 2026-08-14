---
id: SKT-0001
title: >-
  sigil: carry a turn's tool-results into the next turn's input, and
  per-subagent agent_name for claude-code children
status: To Do
assignee: []
created_date: '2026-08-14 16:09'
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
- [ ] #1 A turn's tool results appear in the NEXT turn's AssembledTurn.Input, not the same one; the first turn of a conversation carries no inherited results and the last turn's results are not silently dropped.
- [ ] #2 claude-code child conversations carry a per-subagent agent_name, consistent with how orchestration.go already attributes general_orchestration sub-agent generations.
- [ ] #3 No new metric, label, span or attribute name is introduced that is not already in signals/sigil.md; if one is needed and cannot be sourced, work stops and a cantfind.md PENDING is added instead.
- [ ] #4 Output stays deterministic across two DRY_RUN=true go run ./cmd/synthkit -once -dump runs (decisions derived from seedUnit, never wall clock).
- [ ] #5 signals/sigil.md "Implementation status & next steps" item 1 is updated to reflect what now ships.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
