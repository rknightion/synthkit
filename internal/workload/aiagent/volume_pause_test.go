// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// pauseFleetAgents are two high-rate agents (60 sessions/min each) so a single one-minute tick
// mints several conversations on every lane when the volume knob is not paused.
func pauseFleetAgents() []AgentDecl { return testAgents() }

func buildPauseWorkload(t *testing.T) *Workload {
	t.Helper()
	cfg := &Config{Agents: pauseFleetAgents(), OTel: &OTelObs{Metrics: true}}
	w, err := build(cfg, core.Binding{Name: "ai-fleet", Env: coretest.Env(), Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

// pauseHarness drives the production path for one workload: Mint, ProjectBatch, Tick, writing into
// capturing sinks for every lane the workload owns.
type pauseHarness struct {
	t      *testing.T
	w      *Workload
	eng    *shape.Engine
	native *declarationNativeCapture
	traces *declarationTraceCapture
	prom   *coretest.MetricCapture
	otlp   *aiNativeMetricCapture
	minted int
}

func newPauseHarness(t *testing.T) *pauseHarness {
	return &pauseHarness{
		t:      t,
		w:      buildPauseWorkload(t),
		eng:    shape.New("UTC", nil),
		native: &declarationNativeCapture{},
		traces: &declarationTraceCapture{},
		prom:   &coretest.MetricCapture{},
		otlp:   &aiNativeMetricCapture{},
	}
}

// tick runs one 60 s tick at now with the engine's current volume knob.
func (h *pauseHarness) tick(now time.Time) {
	h.t.Helper()
	world := &core.World{Shape: h.eng, Sigil: h.native, Traces: h.traces, Metrics: h.prom, OTLPMetrics: h.otlp}
	batch := h.w.Minter().Mint(now, 60, h.eng)
	h.minted += len(batch)
	if err := h.w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		h.t.Fatalf("ProjectBatch: %v", err)
	}
	if err := h.w.Tick(context.Background(), now, world); err != nil {
		h.t.Fatalf("Tick: %v", err)
	}
}

// lanes counts what the sinks have received so far.
func (h *pauseHarness) lanes() (generations, steps, scores, spans int) {
	for _, e := range h.native.exports {
		generations += len(e.Generations)
		steps += len(e.WorkflowSteps)
		scores += len(e.Scores)
	}
	for _, r := range h.traces.resources {
		spans += len(r.Spans)
	}
	return
}

// lastPromView is the most recent promrw flush as name+labels -> value (timestamps dropped).
func (h *pauseHarness) lastPromView() map[string]float64 {
	out := map[string]float64{}
	if len(h.prom.Batches) == 0 {
		return out
	}
	for _, s := range h.prom.Batches[len(h.prom.Batches)-1] {
		out[s.Name+"|"+state.LabelSig(s.Labels)] = s.Value
	}
	return out
}

// lastOTLPView is the most recent native OTLP flush as metric+attrs -> cumulative histogram state
// (timestamps dropped).
func (h *pauseHarness) lastOTLPView() map[string]string {
	out := map[string]string{}
	if len(h.otlp.resources) == 0 {
		return out
	}
	last := h.otlp.resources[len(h.otlp.resources)-1]
	for _, m := range last.Metrics {
		for _, p := range m.Histograms {
			keys := make([]string, 0, len(p.Attrs))
			for k := range p.Attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			sig := m.Name
			for _, k := range keys {
				sig += fmt.Sprintf("|%s=%v", k, p.Attrs[k])
			}
			out[sig] = fmt.Sprintf("count=%d sum=%v buckets=%v", p.Count, p.Sum, p.BucketCounts)
		}
	}
	return out
}

// TestMintPausedAtExplicitZero: an explicit master volume of 0 mints no conversation on any tick,
// while the same fleet on an unset engine mints some (so the zero is the cause, not the fleet).
func TestMintPausedAtExplicitZero(t *testing.T) {
	m := newMinter("ai-fleet", "prod", "prod-use1", pauseFleetAgents())
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	live := shape.New("UTC", nil)
	if len(m.Mint(now, 60, live)) == 0 {
		t.Fatal("control: unset volume minted nothing from a 60 sessions/min fleet")
	}

	paused := shape.New("UTC", nil)
	paused.SetVolumeMultiplier(0)
	for i := range 200 {
		if got := m.Mint(now.Add(time.Duration(i)*time.Minute), 60, paused); len(got) != 0 {
			t.Fatalf("tick %d minted %d conversations at volume 0, want none", i, len(got))
		}
	}
}

// TestPausedWorkloadEmitsNothingNew runs the production path through a running -> paused -> running
// transition. While paused no session, generation, workflow step, score or span is written, and
// every cumulative metric stays flat at its last value on both metric lanes.
func TestPausedWorkloadEmitsNothingNew(t *testing.T) {
	h := newPauseHarness(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	for i := range 3 {
		h.tick(now.Add(time.Duration(i) * time.Minute))
	}
	gens, steps, scores, spans := h.lanes()
	if h.minted == 0 || gens == 0 || spans == 0 {
		t.Fatalf("control: running fleet minted=%d generations=%d spans=%d, want all non-zero", h.minted, gens, spans)
	}
	promBefore, otlpBefore := h.lastPromView(), h.lastOTLPView()
	if len(promBefore) == 0 || len(otlpBefore) == 0 {
		t.Fatalf("control: running fleet flushed prom=%d otlp=%d series", len(promBefore), len(otlpBefore))
	}
	mintedBefore := h.minted

	h.eng.SetVolumeMultiplier(0)
	for i := 3; i < 8; i++ {
		h.tick(now.Add(time.Duration(i) * time.Minute))
	}
	if h.minted != mintedBefore {
		t.Fatalf("minted %d conversations while paused", h.minted-mintedBefore)
	}
	g2, st2, sc2, sp2 := h.lanes()
	if g2 != gens || st2 != steps || sc2 != scores || sp2 != spans {
		t.Fatalf("paused ticks wrote new data: generations %d->%d steps %d->%d scores %d->%d spans %d->%d",
			gens, g2, steps, st2, scores, sc2, spans, sp2)
	}
	if got := h.lastPromView(); !reflect.DeepEqual(got, promBefore) {
		t.Fatalf("promrw metrics moved while paused:\n before %v\n after  %v", promBefore, got)
	}
	if got := h.lastOTLPView(); !reflect.DeepEqual(got, otlpBefore) {
		t.Fatalf("native OTLP metrics moved while paused:\n before %v\n after  %v", otlpBefore, got)
	}

	h.eng.SetVolumeMultiplier(1)
	h.tick(now.Add(8 * time.Minute))
	if g3, _, _, _ := h.lanes(); g3 <= g2 {
		t.Fatalf("resuming volume 1 wrote no generations (%d -> %d)", g2, g3)
	}
}

// TestNonzeroVolumeDoesNotScaleAIAgent pins the out-of-scope half of the pause contract: volumes
// other than 0 leave ai_agent exactly as an unset knob, because the fleet is driven by its own
// sessions_per_min and is not scaled by the master volume.
func TestNonzeroVolumeDoesNotScaleAIAgent(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mint := func(set func(*shape.Engine)) []ledger.Request {
		m := newMinter("ai-fleet", "prod", "prod-use1", pauseFleetAgents())
		eng := shape.New("UTC", nil)
		if set != nil {
			set(eng)
		}
		var out []ledger.Request
		for i := range 20 {
			for _, r := range m.Mint(now.Add(time.Duration(i)*time.Minute), 60, eng) {
				out = append(out, *r)
			}
		}
		return out
	}
	want := mint(nil)
	if len(want) == 0 {
		t.Fatal("control: unset engine minted nothing")
	}
	for _, v := range []float64{0.5, 1, 2} {
		got := mint(func(e *shape.Engine) { e.SetVolumeMultiplier(v) })
		if !reflect.DeepEqual(got, want) {
			t.Errorf("volume %v changed minted requests (%d vs %d)", v, len(got), len(want))
		}
	}
}
