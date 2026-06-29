// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// testMinter builds a minter with the default-ish config for the expectation tests.
func testMinter() *minter {
	cfg := *(NewConfig().(*Config))
	return newMinter("shop-api", "prod", "shop-prod-use1", 1.0, false, cfg)
}

// TestMinterCadenceInvariance is the load-bearing TDD test: the expected (mean) volume
// of one 30s reference tick must equal the summed expected volume of six 5s ticks at the
// same instant — i.e. minting more often mints proportionally less each time, so the
// long-run request rate is independent of the master-tick cadence (ARCHITECTURE I10).
// Asserted on the deterministic expectation math (expectedVolume), NOT on stochastic
// Mint output, so the test is not flaky.
func TestMinterCadenceInvariance(t *testing.T) {
	m := testMinter()
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC) // Mon, business-hours plateau
	eng := shape.New("", nil)

	const refTick = ledger.ReferenceTickSeconds // 30s
	const fastTick = 5.0

	oneSlow := m.expectedVolume(now, refTick, eng)
	sixFast := 0.0
	for range 6 {
		sixFast += m.expectedVolume(now, fastTick, eng)
	}

	if math.Abs(oneSlow-sixFast) > 1e-9 {
		t.Fatalf("cadence variance: one 30s tick expects %.6f, six 5s ticks expect %.6f (must be equal)", oneSlow, sixFast)
	}
	if oneSlow <= 0 {
		t.Fatalf("expected positive volume at business-hours plateau, got %.6f", oneSlow)
	}
}

// TestMinterFullVolume verifies the mint expectation is the TRUE request volume
// (rps × tickSec) — NOT a clamped narrative sample. 1 request ⇒ 1 trace requires the mint
// to track real RPS so the trace volume matches the metric lane's own rps×interval math.
func TestMinterFullVolume(t *testing.T) {
	m := testMinter()
	eng := shape.New("", nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC) // business-hours plateau

	// The expectation equals rps×tickSec for every tick size (the contract that makes
	// cadence invariance exact AND traces match metric volume).
	for _, tickSec := range []float64{1, 5, 30, 60} {
		rps := m.rpsAt(now, eng)
		got := m.expectedVolume(now, tickSec, eng)
		if want := rps * tickSec; math.Abs(got-want) > 1e-9 {
			t.Fatalf("expectedVolume(%.0fs)=%.6f want rps×tick=%.6f", tickSec, got, want)
		}
	}

	// A high peak_rps is NOT clamped — full volume scales with rps (old ceiling was 40).
	cfg := *(NewConfig().(*Config))
	cfg.Traffic.PeakRPS = 1000
	hot := newMinter("shop-api", "prod", "shop-prod-use1", 1.0, false, cfg)
	rps := hot.rpsAt(now, eng)
	got := hot.expectedVolume(now, 60, eng) // ≥ off_peak(5)×60 = 300, far above old ceiling
	if want := rps * 60; math.Abs(got-want) > 1e-9 {
		t.Fatalf("high-rps expectation %.6f want rps×60=%.6f (unclamped)", got, want)
	}
	if got <= 40 {
		t.Fatalf("expectation %.6f appears clamped (want full rps×60)", got)
	}
}

// TestMinterStampsCorrelationAndIdentity verifies each minted request carries a fresh
// internally-consistent correlation key-set and the binding identity, and that downstream
// calls are present and sum to less than the request duration.
func TestMinterStampsCorrelationAndIdentity(t *testing.T) {
	cfg := *(NewConfig().(*Config))
	cfg.RUM = true // allow browser-origin draws
	m := newMinter("shop-api", "prod", "shop-prod-use1", 1.0, false, cfg)
	m.calls = []callSpec{{Kind: "db", Target: "shop-app-db", Engine: "postgres"}}
	eng := shape.New("", nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	// Mint a large slow tick so we reliably get requests.
	var batch []*ledger.Request
	for range 50 {
		batch = append(batch, m.Mint(now, ledger.ReferenceTickSeconds, eng)...)
	}
	if len(batch) == 0 {
		t.Fatal("expected at least one minted request across 50 ticks")
	}
	for _, r := range batch {
		if r.Workload != "shop-api" || r.Env != "prod" || r.Cluster != "shop-prod-use1" {
			t.Fatalf("identity not stamped: %+v", r)
		}
		if r.Route == "" {
			t.Fatal("route not stamped")
		}
		if len(r.TraceID) != 32 || len(r.SpanID) != 16 {
			t.Fatalf("bad ID widths: trace=%q span=%q", r.TraceID, r.SpanID)
		}
		if r.CorrelationID == "" || r.RequestID == "" || r.SessionID == "" {
			t.Fatal("correlation IDs not minted")
		}
		if r.StatusCode == 0 {
			t.Fatal("status code not set")
		}
		if len(r.Calls) != 1 || r.Calls[0].Target != "shop-app-db" {
			t.Fatalf("calls not stamped from spec: %+v", r.Calls)
		}
		var callSum time.Duration
		for _, c := range r.Calls {
			callSum += c.Duration
		}
		if callSum >= r.Duration {
			t.Fatalf("sub-call durations %v must be < request duration %v", callSum, r.Duration)
		}
		if r.Outcome == ledger.OutcomeSuccess && r.StatusCode >= 400 {
			t.Fatalf("success outcome with %d status", r.StatusCode)
		}
	}
}

// TestCallSpecsFrom_AIHops verifies the Task-3b AI-hop carrier: an AI CallTarget flows
// through callSpecsFrom → callSpec → ledger.Call carrying its gen_ai identity, the join
// target falls back Subject→Model, and drawCalls mints a PeerSpanID for the gateway hop
// (the connected SERVER span, Path-B) but not for a plain model hop.
func TestCallSpecsFrom_AIHops(t *testing.T) {
	b := core.Binding{
		Calls: []fixture.CallTarget{
			{Kind: fixture.KindWorkflow, AI: &fixture.AICall{Op: "invoke_workflow", Subject: "rag-pipeline"}, ParentHop: -1},
			{Kind: fixture.KindAgent, AI: &fixture.AICall{Op: "invoke_agent", Subject: "planner"}, ParentHop: 0},
			{Kind: fixture.KindLLMGateway, AI: &fixture.AICall{Op: "chat", Subject: "portkey", Model: "gpt-4o", Provider: "azure-openai"}, ParentHop: 1},
			{Kind: fixture.KindLLMModel, AI: &fixture.AICall{Op: "chat", Model: "gpt-4o", Provider: "azure-openai"}, ParentHop: 2},
		},
	}
	specs := callSpecsFrom(b)
	if len(specs) != 4 {
		t.Fatalf("len=%d want 4", len(specs))
	}
	gw := specs[2]
	if gw.Kind != fixture.KindLLMGateway || gw.AI == nil ||
		gw.AI.Op != "chat" || gw.AI.Model != "gpt-4o" || gw.AI.Provider != "azure-openai" || gw.AI.Subject != "portkey" {
		t.Fatalf("gateway carrier wrong: spec=%+v ai=%+v", gw, gw.AI)
	}
	if specs[0].Target != "rag-pipeline" || specs[1].Target != "planner" {
		t.Fatalf("subject targets: %q,%q want rag-pipeline,planner", specs[0].Target, specs[1].Target)
	}
	if specs[3].Target != "gpt-4o" { // model hop has no Subject → falls back to Model
		t.Fatalf("model hop target=%q want gpt-4o", specs[3].Target)
	}

	mm := newMinter("svc", "prod", "c", 1.0, false, *(NewConfig().(*Config)))
	mm.calls = specs
	eng := shape.New("", nil)
	calls := mm.drawCalls(200*time.Millisecond, ledger.OutcomeSuccess, eng)
	if len(calls) != 4 {
		t.Fatalf("len=%d want 4", len(calls))
	}
	for i, c := range calls {
		if c.SpanID == "" {
			t.Fatalf("hop %d missing SpanID", i)
		}
		if c.AI == nil {
			t.Fatalf("hop %d missing AI carrier on ledger.Call", i)
		}
	}
	if calls[2].Kind != fixture.KindLLMGateway || calls[2].PeerSpanID == "" {
		t.Fatal("llm_gateway hop must carry a PeerSpanID (connected SERVER span, Path-B)")
	}
	if calls[3].PeerSpanID != "" {
		t.Fatal("llm_model hop must not carry a PeerSpanID")
	}
	if calls[2].AI.Op != "chat" || calls[3].AI.Model != "gpt-4o" {
		t.Fatalf("AI carrier not plumbed into ledger.Call: gw=%+v model=%+v", calls[2].AI, calls[3].AI)
	}
}

// TestMinterWorkloadName checks the dispatch key.
func TestMinterWorkloadName(t *testing.T) {
	if got := testMinter().Workload(); got != "shop-api" {
		t.Fatalf("Workload()=%q want shop-api", got)
	}
}

func TestDrawCalls_SpanIDsAndTree(t *testing.T) {
	cfg := *(NewConfig().(*Config))
	m := newMinter("shop-api", "prod", "shop-prod-use1", 1.0, false, cfg)
	m.calls = []callSpec{
		{Kind: "service", Target: "payments", ParentHop: -1},
		{Kind: "db", Target: "app-db", Engine: "postgres", ParentHop: 0},
	}
	eng := shape.New("", nil) // the engine constructor the rest of minter_test.go uses
	calls := m.drawCalls(100*time.Millisecond, ledger.OutcomeSuccess, eng)
	if len(calls) != 2 {
		t.Fatalf("len=%d want 2", len(calls))
	}
	if calls[0].SpanID == "" || calls[1].SpanID == "" {
		t.Fatal("every hop must carry a minted SpanID")
	}
	if calls[0].Kind == "service" && calls[0].PeerSpanID == "" {
		t.Fatal("service hop must carry a PeerSpanID (callee SERVER span)")
	}
	if calls[1].Kind == "db" && calls[1].PeerSpanID != "" {
		t.Fatal("db hop must not carry a PeerSpanID")
	}
	if calls[0].ParentHopIndex != -1 || calls[1].ParentHopIndex != 0 {
		t.Fatalf("parents: got %d,%d want -1,0", calls[0].ParentHopIndex, calls[1].ParentHopIndex)
	}
}
