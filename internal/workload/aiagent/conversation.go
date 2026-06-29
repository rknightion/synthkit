// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// metricObs is one per-turn metric observation the workload accumulates into its state.State from
// ProjectBatch (R-M4: ProjectBatch ACCUMULATES, Tick FLUSHES). It carries the identity + the values
// for a single generation turn (plus its execute_tool durations). It deliberately holds only the
// data the metric renderer needs — never high-card ids.
type metricObs struct {
	agentName    string
	agentVersion string
	operation    string // generateText | streamText
	provider     string
	model        string
	streaming    bool

	inputTokens     int64
	outputTokens    int64
	cacheReadTokens int64
	cacheWriteTok   int64
	reasoningTokens int64

	opDurationSec float64
	toolCalls     int
	ttfbSec       float64 // time-to-first-token (streaming only; 0 ⇒ not observed)

	// per-tool execute_tool durations, keyed by tool name.
	toolDurations []toolDur
}

type toolDur struct {
	name string
	sec  float64
}

// turnArtifacts is the per-turn root id triple shared by Lane A (Generation.TraceID/SpanID) and
// Lane B (the root generateText/streamText span). One source per turn.
type turnArtifacts struct {
	traceID    string
	rootSpanID string
	start, end time.Time
}

// buildConversation builds all three lanes for one conversation Request. It is the per-conversation
// fan-out core (R-B2): it derives the turn count deterministically from r.SessionID, assembles the
// content via sigil.AssembleConversation, mints a per-turn trace_id + root span_id from the ledger,
// and slots per-turn timestamps within [r.RenderStart(), r.RenderStart()+r.Duration] so the LAST
// turn ends ≈ r.Start (backdated). The returned Generations carry TraceID/SpanID EQUAL to that
// turn's root span ids (so Lane A and Lane B share one source per turn). traces.go turns the same
// per-turn artifacts into the Lane B span tree; metrics.go consumes the metricObs.
//
// The archetype layer (archetypes.go) post-processes the Generations/WorkflowSteps in place for
// per-archetype behaviour (parent_generation_ids, codex metadata, terminal verdict, single-shot).
func buildConversation(res ResourceID, agent AgentDecl, r *ledger.Request) (gens []sigil.Generation, steps []sigil.WorkflowStep, spans []otlp.Resource, obs []metricObs) {
	turns := TurnCount(agent, r.SessionID)
	seed := seedHash(r.SessionID, "assemble")
	assembled := sigil.AssembleConversation(seed, agent.Archetype, turns)
	if len(assembled) == 0 {
		return nil, nil, nil, nil
	}

	op := sigil.OpGenerateText
	mode := sigil.ModeSync
	if agent.Streaming {
		op = sigil.OpStreamText
		mode = sigil.ModeStream
	}

	arts := slotTurns(r, len(assembled))

	gens = make([]sigil.Generation, 0, len(assembled))
	allObs := make([]metricObs, 0, len(assembled))

	for i, at := range assembled {
		art := arts[i]
		gen := buildGeneration(agent, r, at, art, op, mode, i, len(assembled))
		gens = append(gens, gen)
		allObs = append(allObs, observeForTurn(agent, gen, art, op))
	}

	// Per-archetype layer: stamp metadata, wire parent_generation_ids / workflow steps, enforce
	// single-shot / terminal-verdict invariants (in place on gens; may append steps).
	steps = applyArchetype(agent, r, gens, arts)

	// Lane B span tree (per-turn) — built from the same per-turn artifacts + generations (after the
	// archetype layer so parent_generation_ids land on the spans too).
	spans = buildTraceResources(res, agent, r, gens, arts, assembled)

	return gens, steps, spans, allObs
}

// slotTurns computes per-turn time windows + per-turn id triples within the conversation's backdated
// window so the LAST turn ends ≈ r.Start (= r.RenderStart()+r.Duration). Turns are laid out
// sequentially across the window; each turn gets a fresh ledger trace_id + root span_id (I9).
func slotTurns(r *ledger.Request, n int) []turnArtifacts {
	base := r.RenderStart()
	dur := r.Duration
	if dur <= 0 {
		dur = time.Duration(n) * time.Second
	}
	step := dur / time.Duration(maxInt(n, 1))
	if step <= 0 {
		step = time.Millisecond
	}
	out := make([]turnArtifacts, n)
	for i := range out {
		ts := base.Add(step * time.Duration(i))
		te := ts.Add(step)
		end := base.Add(dur)
		if te.After(end) || i == n-1 {
			te = end
		}
		if !te.After(ts) {
			te = ts.Add(time.Millisecond)
		}
		out[i] = turnArtifacts{
			traceID:    ledger.NewTraceID(),
			rootSpanID: ledger.NewSpanID(),
			start:      ts,
			end:        te,
		}
	}
	return out
}

// buildGeneration populates one sigil.Generation for turn i. The Generation's TraceID/SpanID equal
// the turn's root span ids (the load-bearing R-B2 invariant). Usage follows the archetype token law
// (coding: monotonic-growing cache_read across turns + tiny fresh input; general: modest in/out).
func buildGeneration(agent AgentDecl, r *ledger.Request, at sigil.AssembledTurn, art turnArtifacts, op, mode string, turnIdx, totalTurns int) sigil.Generation {
	genID := uuidLike(r.SessionID, fmt.Sprintf("gen-%d", turnIdx))
	usage := tokenLaw(agent, r.SessionID, turnIdx, totalTurns, at)

	gen := sigil.Generation{
		ID:               genID,
		ConversationID:   r.SessionID,
		OperationName:    op,
		Mode:             mode,
		TraceID:          art.traceID,    // == Lane B root span TraceID (R-B2)
		SpanID:           art.rootSpanID, // == Lane B root span SpanID (R-B2)
		Provider:         r.Provider,
		Model:            r.Model,
		ResponseID:       uuidLike(r.SessionID, fmt.Sprintf("resp-%d", turnIdx)),
		ResponseModel:    r.Model,
		SystemPrompt:     at.SystemPrompt,
		Input:            at.Input,
		Output:           at.Output,
		Tools:            at.Tools,
		Usage:            usage,
		StopReason:       at.StopReason,
		StartedAt:        art.start,
		EndedAt:          art.end,
		Tags:             agent.Tags,
		AgentName:        agent.Name,
		AgentVersion:     agent.Version,
		EffectiveVersion: sigil.EffectiveVersion(at.SystemPrompt),
		Metadata:         baseMetadata(agent),
	}

	// thinking enabled for coding archetypes (extended-thinking).
	if sigil.IsCoding(agent.Archetype) {
		te := true
		gen.ThinkingEnabled = &te
	}
	return gen
}

// baseMetadata stamps the sigil sdk + capture-mode metadata every generation carries.
func baseMetadata(agent AgentDecl) map[string]any {
	md := map[string]any{
		sigil.AttrSDKName: agent.SDK,
	}
	if agent.CaptureMode != "" {
		md["content_capture_mode"] = agent.CaptureMode
	}
	return md
}

// tokenLaw computes Usage per the archetype token shape:
//   - coding: tiny fresh input (1..6), monotonically-growing cache_read across turns, modest output,
//     a cache_write on the first turn (prompt caching), reasoning tokens (extended thinking).
//   - general: modest input/output, minimal cache.
func tokenLaw(agent AgentDecl, convID string, turnIdx, totalTurns int, at sigil.AssembledTurn) sigil.Usage {
	if sigil.IsCoding(agent.Archetype) {
		input := int64(1 + int(seedUnit(convID, fmt.Sprintf("in-%d", turnIdx))*6)) // 1..6
		// cache_read grows monotonically across turns (the conversation history is cached).
		base := int64(8000 + int(seedUnit(convID, "cacheBase")*40000))
		grow := int64(turnIdx) * int64(4000+int(seedUnit(convID, "cacheGrow")*12000))
		cacheRead := base + grow
		output := int64(80 + int(seedUnit(convID, fmt.Sprintf("out-%d", turnIdx))*900))
		var cacheWrite int64
		if turnIdx == 0 {
			cacheWrite = int64(2000 + int(seedUnit(convID, "cw")*6000))
		}
		reasoning := int64(0)
		if at.StopReason == "tool_use" || seedUnit(convID, fmt.Sprintf("reason-%d", turnIdx)) < 0.5 {
			reasoning = int64(200 + int(seedUnit(convID, fmt.Sprintf("rt-%d", turnIdx))*2000))
		}
		total := input + output + cacheRead + cacheWrite + reasoning
		return sigil.Usage{
			Input:      input,
			Output:     output,
			CacheRead:  cacheRead,
			CacheWrite: cacheWrite,
			Reasoning:  reasoning,
			Total:      total,
		}
	}
	// general: modest input/output, minimal cache.
	input := int64(200 + int(seedUnit(convID, fmt.Sprintf("gin-%d", turnIdx))*1500))
	output := int64(50 + int(seedUnit(convID, fmt.Sprintf("gout-%d", turnIdx))*800))
	return sigil.Usage{Input: input, Output: output, Total: input + output}
}

// observeForTurn builds the metric observation for one generation turn.
func observeForTurn(agent AgentDecl, gen sigil.Generation, art turnArtifacts, op string) metricObs {
	o := metricObs{
		agentName:       agent.Name,
		agentVersion:    agent.Version,
		operation:       op,
		provider:        gen.Provider,
		model:           gen.Model,
		streaming:       agent.Streaming,
		inputTokens:     gen.Usage.Input,
		outputTokens:    gen.Usage.Output,
		cacheReadTokens: gen.Usage.CacheRead,
		cacheWriteTok:   gen.Usage.CacheWrite,
		reasoningTokens: gen.Usage.Reasoning,
		opDurationSec:   art.end.Sub(art.start).Seconds(),
		toolCalls:       countOutputToolCalls(gen),
	}
	if o.opDurationSec <= 0 {
		o.opDurationSec = 0.001
	}
	if agent.Streaming {
		// time-to-first-token: a fraction of the op duration.
		o.ttfbSec = o.opDurationSec * (0.05 + seedUnit(gen.ConversationID, gen.ID)*0.2)
		if o.ttfbSec <= 0 {
			o.ttfbSec = 0.001
		}
	}
	// per-tool durations: split a slice of the op window across the tool calls.
	o.toolDurations = toolDurations(gen, o.opDurationSec)
	return o
}

// toolDurations derives a plausible per-execute_tool duration for each tool_call in the generation
// output, deterministically from the generation id.
func toolDurations(gen sigil.Generation, opSec float64) []toolDur {
	var out []toolDur
	idx := 0
	for _, msg := range gen.Output {
		for _, p := range msg.Parts {
			if p.ToolCall != nil {
				frac := 0.05 + seedUnit(gen.ID, fmt.Sprintf("tool-%d", idx))*0.3
				out = append(out, toolDur{name: p.ToolCall.Name, sec: opSec * frac})
				idx++
			}
		}
	}
	return out
}

// countOutputToolCalls counts tool_call parts in a generation's output messages.
func countOutputToolCalls(gen sigil.Generation) int {
	n := 0
	for _, msg := range gen.Output {
		for _, p := range msg.Parts {
			if p.ToolCall != nil {
				n++
			}
		}
	}
	return n
}

// uuidLike derives a stable uuid-shaped id from (convID, salt). Deterministic per conversation
// (not a request-scoped trace/span id — those come from the ledger). Used for generation/response
// ids, which are sigil-domain identifiers, not W3C trace ids.
func uuidLike(convID, salt string) string {
	h1 := seedHash(convID, salt)
	h2 := seedHash(convID, salt+"-2")
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(h1>>32), uint16(h1>>16), uint16(h1), uint16(h2>>48), h2&0xffffffffffff)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
