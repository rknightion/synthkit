// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// untouchedResources: gen-dep is the dependency the fault slows; editor-pool is held across
// editor-dep, which editor's own database call runs (the held-pool shape of SKT-0168.09.36).
const untouchedResources = `
- {name: gen-dep, kind: dependency, latency_ms: 100, slow_factor: 4}
- {name: editor-pool, kind: pool, size: 400, hold_ms: 120, held_across: editor-dep, acquire_timeout_ms: 3000,
   demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: editor-dep, kind: dependency, latency_ms: 80}
`

// untouchedApp: the entry calls auth (an unbound service with its own database call), editor
// (holding editor-pool across its database call) and gen (bound to gen-dep). A gen-dep fault
// reaches gen and the entry only.
const untouchedApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - {name: front, entry: true, calls: [auth, editor, gen]}
  - {name: auth, calls: [auth-db]}
  - {name: auth-db, type: db}
  - {name: editor, uses: [editor-pool], calls: [editor-db]}
  - {name: editor-db, type: db, uses: [editor-dep]}
  - {name: gen, uses: [gen-dep]}
`

type untouchedRun struct {
	rows  faultRun
	spans map[[3]string][]float64 // (service, kind, name) -> span durations in ms
	reqs  int
}

// untouchedSpansRun warms up on a healthy window, then mints and ticks the window under test and
// projects the traces of its requests (only those carrying a fault when faulted is set).
func untouchedSpansRun(t *testing.T, eng *shape.Engine, faulted bool) untouchedRun {
	t.Helper()
	w := causalApps(t, untouchedResources, untouchedApp)[0]
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	run := untouchedRun{rows: faultRun{warm: &coretest.MetricCapture{}}, spans: map[[3]string][]float64{}}
	before := causalNow.Add(-90 * time.Minute) // the incident starts at 12:00
	led.Mint(before)
	world := coretest.World(run.rows.warm, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), before, world); err != nil {
		t.Fatalf("warm-up Tick: %v", err)
	}
	reqs := led.Mint(causalNow)
	mc := &coretest.MetricCapture{}
	world = coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	run.rows.mc, run.rows.reqs = mc, reqs
	var sel []*ledger.Request
	for _, r := range reqs {
		if (r.Fault != nil) == faulted {
			sel = append(sel, r)
		}
	}
	run.reqs = len(sel)
	p := project(t, w, eng, sel)
	for id, sp := range p.spans {
		k := [3]string{p.owner[id], kindLabel(sp.Kind), sp.Name}
		run.spans[k] = append(run.spans[k], float64(sp.End.Sub(sp.Start))/float64(time.Millisecond))
	}
	return run
}

func kindLabel(k otlp.SpanKind) string {
	switch k {
	case otlp.KindServer:
		return spanKindServer
	case otlp.KindClient:
		return spanKindClient
	}
	return ""
}

func meanOf(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / math.Max(float64(len(v)), 1)
}

// SKT-0200: on requests a fault slows, the trace spans of services the fault never reaches keep
// their healthy duration distribution, and the trace spans and span-metric rows agree on which
// services slowed. Healthy and faulted requests lay out an untouched subtree by one rule (the
// engine's timeline), so an unbound caller and a held-pool caller read the same on both.
func TestCausalUntouchedSpansKeepHealthyDuration(t *testing.T) {
	healthy := untouchedSpansRun(t, shape.New("UTC", nil), false)
	faulted := untouchedSpansRun(t, incident(causal.ModeDependencySlow, "gen-dep", 1), true)
	if healthy.reqs < 1000 || faulted.reqs < 1000 {
		t.Fatalf("too few requests: healthy %d, faulted %d", healthy.reqs, faulted.reqs)
	}
	ratio := func(k [3]string) float64 {
		h, f := healthy.spans[k], faulted.spans[k]
		if len(h) == 0 || len(f) == 0 {
			t.Fatalf("span %v: healthy %d, faulted %d", k, len(h), len(f))
		}
		return meanOf(f) / meanOf(h)
	}
	rowRatio := func(service, kind, name string) float64 {
		h, f := rowMeanLatency(healthy.rows, service, kind, name), rowMeanLatency(faulted.rows, service, kind, name)
		if h <= 0 || f <= 0 {
			t.Fatalf("span-metric rows %s %s %q: healthy mean %v, faulted mean %v, want both observed", service, kind, name, h, f)
		}
		return f / h
	}

	for _, c := range []struct {
		service, kind, name string
		slowed              bool
	}{
		{"auth", spanKindServer, "auth", false},
		{"front", spanKindClient, "call auth", false},
		{"auth", spanKindClient, "call auth-db", false},
		{"editor", spanKindServer, "editor", false},
		{"front", spanKindClient, "call editor", false},
		{"editor", spanKindClient, "call editor-db", false},
		{"gen", spanKindServer, "gen", true},
		{"front", spanKindClient, "call gen", true},
	} {
		name := c.name
		tr := ratio([3]string{c.service, c.kind, name})
		rowName := name
		if c.kind == spanKindServer {
			rowName = ""
		}
		rr := rowRatio(c.service, c.kind, rowName)
		t.Logf("%s %s %q: trace x%.3f, span-metric row x%.3f", c.service, c.kind, name, tr, rr)
		if !c.slowed && math.Abs(tr-1) > 0.03 {
			t.Errorf("%s %s %q: untouched span x%.3f on faulted requests, want its healthy duration (within 3%%)", c.service, c.kind, name, tr)
		}
		if c.slowed && tr < 3 {
			t.Errorf("%s %s %q: slowed span x%.3f, want about the x4 dependency factor", c.service, c.kind, name, tr)
		}
		if (tr > 1.5) != (rr > 1.5) {
			t.Errorf("%s %s %q: trace x%.3f and span-metric row x%.3f disagree on whether it slowed", c.service, c.kind, name, tr, rr)
		}
	}
}
