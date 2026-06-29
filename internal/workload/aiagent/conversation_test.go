// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
)

func codingAgent() AgentDecl {
	return AgentDecl{
		Name:        "claude-code",
		Archetype:   "coding_claude_code",
		SDK:         "sdk-go",
		Provider:    "anthropic",
		Models:      []string{"claude-sonnet-4-6"},
		Tools:       []string{"Read", "Edit", "Bash"},
		CaptureMode: "full",
		Version:     "2.1.0",
		Activity:    Activity{SessionsPerMin: 60, TurnsP50: 5, TurnsP95: 12},
		Tags:        map[string]string{"cwd": "/repo", "git.branch": "main"},
	}
}

// fixedReq returns a Request with a fixed SessionID for deterministic-shape tests. Note trace/span
// ids inside the conversation are still freshly minted per call (ledger crypto/rand) — only the
// SHAPE is asserted deterministic (per review B1).
func fixedReq(sessionID string, dur time.Duration) *ledger.Request {
	r := &ledger.Request{
		Workload: "ai-fleet",
		Env:      "prod",
		Cluster:  "prod-use1",
		Route:    "claude-code",
		Model:    "claude-sonnet-4-6",
		Provider: "anthropic",
		Start:    time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC),
		Duration: dur,
	}
	r.Correlation.SessionID = sessionID
	r.Correlation.TraceID = "0123456789abcdef0123456789abcdef"
	return r
}

// shapeOf extracts the deterministic SHAPE of a conversation (turn count + per-turn token usage +
// stop reasons) — everything that must be stable across runs given a fixed SessionID.
type genShape struct {
	turns      int
	input      []int64
	output     []int64
	cacheRead  []int64
	cacheWrite []int64
	stop       []string
}

func shapeOf(gens []sigilGen) genShape {
	s := genShape{turns: len(gens)}
	for _, g := range gens {
		s.input = append(s.input, g.input)
		s.output = append(s.output, g.output)
		s.cacheRead = append(s.cacheRead, g.cacheRead)
		s.cacheWrite = append(s.cacheWrite, g.cacheWrite)
		s.stop = append(s.stop, g.stop)
	}
	return s
}

type sigilGen struct {
	input, output, cacheRead, cacheWrite int64
	stop                                 string
}

// TestBuildConversationShapeDeterministic: fixed SessionID → identical SHAPE (turn count, token
// monotonicity) across runs (B1: shape stable, ids vary).
func TestBuildConversationShapeDeterministic(t *testing.T) {
	agent := codingAgent()
	mk := func() genShape {
		r := fixedReq("conv-shape-1", 90*time.Second)
		gens, _, _, _ := buildConversation(ResourceID{}, agent, r)
		conv := make([]sigilGen, len(gens))
		for i, g := range gens {
			conv[i] = sigilGen{g.Usage.Input, g.Usage.Output, g.Usage.CacheRead, g.Usage.CacheWrite, g.StopReason}
		}
		return shapeOf(conv)
	}
	a := mk()
	b := mk()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("conversation SHAPE not deterministic for fixed SessionID:\n%#v\n%#v", a, b)
	}
	if a.turns < 1 {
		t.Fatalf("turns = %d, want >= 1", a.turns)
	}
}

// TestGenTraceSpanEqualsRootSpan: gen.TraceID == its root span TraceID and gen.SpanID == root span
// SpanID for every turn (R-B2 load-bearing invariant).
func TestGenTraceSpanEqualsRootSpan(t *testing.T) {
	for _, agent := range []AgentDecl{codingAgent(), generalOrchAgent()} {
		r := fixedReq("conv-ids-1", 90*time.Second)
		gens, _, resources, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
		if len(gens) == 0 {
			t.Fatalf("agent %s: no generations", agent.Name)
		}
		// Index root spans by (traceID -> spanID) for the generateText/streamText CLIENT spans.
		rootBy := map[string]string{} // traceID -> rootSpanID (the LLM-call CLIENT span)
		for _, res := range resources {
			for _, sp := range res.Spans {
				if sp.Name == spanName(gens[0].OperationName, gens[0].Model) {
					// The LLM-call CLIENT span carries the sigil.generation.id attr.
					if _, ok := sp.Attrs["sigil.generation.id"]; ok {
						rootBy[sp.TraceID] = sp.SpanID
					}
				}
			}
		}
		for i, g := range gens {
			rootSpan, ok := rootBy[g.TraceID]
			if !ok {
				t.Fatalf("agent %s turn %d: no root span with gen.TraceID=%s", agent.Name, i, g.TraceID)
			}
			if rootSpan != g.SpanID {
				t.Fatalf("agent %s turn %d: gen.SpanID=%s != root span id=%s", agent.Name, i, g.SpanID, rootSpan)
			}
		}
	}
}

// TestCodingTokenLaw: coding archetype → CacheRead non-decreasing across turns, Input tiny (<=6).
func TestCodingTokenLaw(t *testing.T) {
	agent := codingAgent()
	r := fixedReq("conv-token-1", 200*time.Second)
	gens, _, _, _ := buildConversation(ResourceID{}, agent, r)
	if len(gens) < 2 {
		// force more turns
		agent.Activity.TurnsP50 = 8
		r = fixedReq("conv-token-2", 200*time.Second)
		gens, _, _, _ = buildConversation(ResourceID{}, agent, r)
	}
	var prev int64 = -1
	for i, g := range gens {
		if g.Usage.Input <= 0 || g.Usage.Input > 6 {
			t.Fatalf("turn %d: coding Input=%d, want 1..6", i, g.Usage.Input)
		}
		if g.Usage.CacheRead < prev {
			t.Fatalf("turn %d: CacheRead=%d < prev=%d (must be non-decreasing)", i, g.Usage.CacheRead, prev)
		}
		prev = g.Usage.CacheRead
	}
}

// TestBackdateLastTurnEndsAtStart: last turn EndedAt ≈ r.Start (within Duration of backdate).
func TestBackdateLastTurnEndsAtStart(t *testing.T) {
	agent := codingAgent()
	r := fixedReq("conv-backdate-1", 120*time.Second)
	gens, _, _, _ := buildConversation(ResourceID{}, agent, r)
	if len(gens) == 0 {
		t.Fatal("no generations")
	}
	last := gens[len(gens)-1]
	// trace end = RenderStart + Duration = Start - RenderOffset ≈ Start. RenderOffset is 0 here.
	want := r.Start
	diff := last.EndedAt.Sub(want)
	if diff < -time.Second || diff > time.Second {
		t.Fatalf("last turn EndedAt=%v, want ≈ r.Start=%v (diff %v)", last.EndedAt, want, diff)
	}
	// every generation shares the conversation id.
	for i, g := range gens {
		if g.ConversationID != r.SessionID {
			t.Fatalf("turn %d: ConversationID=%q, want %q", i, g.ConversationID, r.SessionID)
		}
	}
}
