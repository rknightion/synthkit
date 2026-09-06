// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
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

// fixedReq returns a Request with a fixed SessionID for deterministic conversation tests.
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

// TestFixedTickTraceLaneByteIdentical proves that two independently built
// ai_agent conversations for the same fixed request render identical trace resources.
func TestFixedTickTraceLaneByteIdentical(t *testing.T) {
	agent := generalOrchAgent()
	build := func() []otlp.Resource {
		r := fixedReq("conv-fixed-tick-1", 90*time.Second)
		r.Route = agent.Name
		r.Provider = agent.Provider
		r.Model = agent.Models[0]
		_, _, resources, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
		return resources
	}
	first, repeat := traceLaneBytes(t, build()), traceLaneBytes(t, build())
	if !bytes.Equal(first, repeat) {
		t.Fatal("fixed-tick trace lane differs between identical runs")
	}
}

// TestFixedTickMinterTraceLaneByteIdentical covers the full ai_agent draw path:
// the fixed clock and seeded shape engine mint the same request, then produce the
// same trace resources on two independent runs.
func TestFixedTickMinterTraceLaneByteIdentical(t *testing.T) {
	agent := generalOrchAgent()
	agent.Activity.SessionsPerMin = 60 // exactly one arrival for a one-second tick
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	build := func() []otlp.Resource {
		m := newMinter("ai-fleet", "prod", "prod-use1", []AgentDecl{agent})
		batch := m.Mint(now, 1, shape.New("UTC", nil))
		if len(batch) != 1 {
			t.Fatalf("fixed tick minted %d requests, want 1", len(batch))
		}
		_, _, resources, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, batch[0])
		return resources
	}
	first, repeat := traceLaneBytes(t, build()), traceLaneBytes(t, build())
	if !bytes.Equal(first, repeat) {
		t.Fatal("fixed-tick minter trace lane differs between identical runs")
	}
}

// TestFirstTickTraceInventoryIgnoresWallClock proves that a fresh runner's first
// deterministic tick keeps the trace inventory stable even when it runs at a
// different wall-clock instant. Tick time belongs on emitted timestamps, not in
// the entropy source for model, turn, or tool selection.
func TestFirstTickTraceInventoryIgnoresWallClock(t *testing.T) {
	agent := generalOrchAgent()
	agent.Activity.SessionsPerMin = 60

	build := func(now time.Time) []byte {
		m := newMinter("ai-fleet", "prod", "prod-use1", []AgentDecl{agent})
		batch := m.Mint(now, 1, shape.New("UTC", nil))
		if len(batch) != 1 {
			t.Fatalf("first tick minted %d requests, want 1", len(batch))
		}
		_, _, resources, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, batch[0])
		return traceInventoryBytes(t, resources)
	}
	first := build(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	repeat := build(time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC))
	if !bytes.Equal(first, repeat) {
		t.Fatal("first fixed tick trace lane changes with wall clock")
	}
}

func traceLaneBytes(t *testing.T, resources []otlp.Resource) []byte {
	t.Helper()
	b, err := json.Marshal(resources)
	if err != nil {
		t.Fatalf("marshal trace lane: %v", err)
	}
	return b
}

// traceInventoryBytes is the trace section's semantic dump shape: service identity,
// resource keys, span names, and span attribute keys. Timestamps and ID values are
// deliberately excluded because they are trace values, not dump inventory.
func traceInventoryBytes(t *testing.T, resources []otlp.Resource) []byte {
	t.Helper()
	type serviceInventory struct {
		Service      string   `json:"service"`
		ResourceKeys []string `json:"resource_keys"`
		SpanNames    []string `json:"span_names"`
		SpanAttrs    []string `json:"span_attrs"`
	}
	type sets struct {
		resource map[string]struct{}
		spans    map[string]struct{}
		attrs    map[string]struct{}
	}
	byService := map[string]*sets{}
	for _, resource := range resources {
		service, _ := resource.Attrs["service.name"].(string)
		if byService[service] == nil {
			byService[service] = &sets{resource: map[string]struct{}{}, spans: map[string]struct{}{}, attrs: map[string]struct{}{}}
		}
		set := byService[service]
		for key := range resource.Attrs {
			set.resource[key] = struct{}{}
		}
		for _, span := range resource.Spans {
			set.spans[span.Name] = struct{}{}
			for key := range span.Attrs {
				set.attrs[key] = struct{}{}
			}
		}
	}
	keys := make([]string, 0, len(byService))
	for service := range byService {
		keys = append(keys, service)
	}
	sort.Strings(keys)
	items := make([]serviceInventory, 0, len(keys))
	for _, service := range keys {
		set := byService[service]
		toSorted := func(values map[string]struct{}) []string {
			out := make([]string, 0, len(values))
			for value := range values {
				out = append(out, value)
			}
			sort.Strings(out)
			return out
		}
		items = append(items, serviceInventory{Service: service, ResourceKeys: toSorted(set.resource), SpanNames: toSorted(set.spans), SpanAttrs: toSorted(set.attrs)})
	}
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal trace inventory: %v", err)
	}
	return b
}

// TestGenTraceSpanEqualsRootSpan: every generation's (TraceID, SpanID) identifies the root LLM
// CLIENT span carrying its sigil.generation.id (R-B2 load-bearing invariant). Keyed by generation
// id — NOT one-per-trace — because orchestration fan-out places sub-agent generations in the same
// trace as their orchestrator turn (each still owns a distinct root span).
func TestGenTraceSpanEqualsRootSpan(t *testing.T) {
	for _, agent := range []AgentDecl{codingAgent(), generalOrchAgent()} {
		r := fixedReq("conv-ids-1", 90*time.Second)
		gens, _, resources, _ := buildConversation(ResourceID{ServiceName: "chatservice"}, agent, r)
		if len(gens) == 0 {
			t.Fatalf("agent %s: no generations", agent.Name)
		}
		// Index the LLM-call CLIENT spans (the only spans carrying sigil.generation.id) by that id.
		type span struct{ trace, id string }
		rootByGen := map[string]span{}
		for _, res := range resources {
			for _, sp := range res.Spans {
				if gid, ok := sp.Attrs["sigil.generation.id"].(string); ok {
					rootByGen[gid] = span{sp.TraceID, sp.SpanID}
				}
			}
		}
		for i, g := range gens {
			root, ok := rootByGen[g.ID]
			if !ok {
				t.Fatalf("agent %s gen %d (%s/%s): no root span carrying its sigil.generation.id",
					agent.Name, i, g.AgentName, g.ID)
			}
			if root.trace != g.TraceID || root.id != g.SpanID {
				t.Fatalf("agent %s gen %d (%s): root span (%s,%s) != gen (%s,%s)",
					agent.Name, i, g.AgentName, root.trace, root.id, g.TraceID, g.SpanID)
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
