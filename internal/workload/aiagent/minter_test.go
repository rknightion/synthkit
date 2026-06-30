// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

func testAgents() []AgentDecl {
	return []AgentDecl{
		{
			Name:      "claude-code",
			Archetype: "coding_claude_code",
			SDK:       "sdk-go",
			Provider:  "anthropic",
			Models:    []string{"claude-sonnet-4-6", "claude-opus-4-8"},
			Activity:  Activity{SessionsPerMin: 60, TurnsP50: 4, TurnsP95: 12},
		},
		{
			Name:      "product_agent",
			Archetype: "general_orchestration",
			SDK:       "sdk-python",
			Provider:  "openai",
			Models:    []string{"gpt-4.1-nano"},
			Streaming: true,
			Activity:  Activity{SessionsPerMin: 60, TurnsP50: 2, TurnsP95: 5},
		},
	}
}

// TestMinterArrivalVolume verifies that, at sessions_per_min=60 across agents, the mean arrivals
// per tick converges to Σ(spm/60)*tickSec (the StochasticRound law).
func TestMinterArrivalVolume(t *testing.T) {
	agents := testAgents()
	m := newMinter("ai-fleet", "prod", "prod-use1", agents)
	eng := shape.New("", nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	const tickSec = 30.0
	const ticks = 4000
	total := 0
	for range ticks {
		total += len(m.Mint(now, tickSec, eng))
	}
	got := float64(total) / float64(ticks)

	// Expected per tick: Σ (spm/60)*tickSec = 2 agents * (60/60)*30 = 60.
	want := 0.0
	for _, a := range agents {
		want += (a.Activity.SessionsPerMin / 60.0) * tickSec
	}
	if math.Abs(got-want) > want*0.05 {
		t.Fatalf("mean arrivals/tick = %.3f, want ≈ %.3f (±5%%)", got, want)
	}
}

// TestMinterRequestShape verifies each minted Request has a recoverable agent Route, a non-empty
// conversation id (SessionID), a model from the agent's set, and a positive Duration.
func TestMinterRequestShape(t *testing.T) {
	agents := testAgents()
	m := newMinter("ai-fleet", "prod", "prod-use1", agents)
	eng := shape.New("", nil)
	now := time.Now()

	names := map[string]map[string]bool{}
	for _, a := range agents {
		set := map[string]bool{}
		for _, mdl := range a.Models {
			set[mdl] = true
		}
		names[a.Name] = set
	}

	var seen int
	for range 200 {
		for _, r := range m.Mint(now, 30, eng) {
			seen++
			models, ok := names[r.Route]
			if !ok {
				t.Fatalf("Route %q is not a known agent name", r.Route)
			}
			if r.SessionID == "" {
				t.Fatalf("agent %q: empty SessionID (conversation_id)", r.Route)
			}
			if r.Duration <= 0 {
				t.Fatalf("agent %q: non-positive Duration %v", r.Route, r.Duration)
			}
			if !models[r.Model] {
				t.Fatalf("agent %q: model %q not in declared set", r.Route, r.Model)
			}
			if r.Workload != "ai-fleet" {
				t.Fatalf("Workload = %q, want ai-fleet", r.Workload)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no requests minted across 200 ticks")
	}
}

// TestTurnCountDeterministic verifies TurnCount is stable per conversation id and bounded.
func TestTurnCountDeterministic(t *testing.T) {
	a := AgentDecl{Archetype: "coding_claude_code", Activity: Activity{TurnsP50: 4, TurnsP95: 12}}
	const id = "conv-abc-123"
	first := TurnCount(a, id)
	for range 50 {
		if got := TurnCount(a, id); got != first {
			t.Fatalf("TurnCount not deterministic: %d vs %d", got, first)
		}
	}
	if first < 1 || first > 12 {
		t.Fatalf("TurnCount %d out of bounds [1,12]", first)
	}

	ss := AgentDecl{Archetype: "general_single_shot", Activity: Activity{TurnsP50: 4, TurnsP95: 12}}
	if got := TurnCount(ss, id); got != 1 {
		t.Fatalf("single_shot TurnCount = %d, want 1", got)
	}
}
