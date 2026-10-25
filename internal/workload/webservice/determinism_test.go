// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// tickMultiRoute builds a fresh multi-route workload, mints a ledger window with traffic on every
// route, Ticks the metric lane once and returns the canonical (sorted) rendering of the emitted
// Prometheus series and OTLP metrics. Everything is built from scratch with the same seed and clock.
func tickMultiRoute(t *testing.T) []string {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.Tracing = true
	cfg.OTel = &OTelObs{Metrics: true, Mode: otelModeK8sMonitoring}
	cfg.Endpoints = []Endpoint{
		{Route: "GET /", ErrorRate: 0.01, P95ms: 120},
		{Route: "GET /api/orders", ErrorRate: 0.02, P95ms: 240},
		{Route: "POST /api/orders", ErrorRate: 0.03, P95ms: 380},
		{Route: "GET /api/cart", ErrorRate: 0.01, P95ms: 90},
		{Route: "POST /api/checkout", ErrorRate: 0.04, P95ms: 520},
		{Route: "GET /healthz", ErrorRate: 0, P95ms: 15},
	}
	w, err := build(cfg, testBinding(nil))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	wl := w.(*Workload)
	led := ledger.New(shape.New("", nil), 0, 0)
	led.AddMinter(wl.Minter())
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	for range 40 {
		led.Mint(now)
	}

	mc := &coretest.MetricCapture{}
	cap := &captureOTLP{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.OTLPMetrics = cap
	if err := wl.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	var out []string
	for _, s := range mc.All() {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("prom " + s.Name + "{")
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%q,", k, s.Labels[k])
		}
		fmt.Fprintf(&b, "} %v", s.Value)
		out = append(out, b.String())
	}
	for _, r := range cap.got {
		for _, m := range r.Metrics {
			// One line per data point (maps print in sorted key order), so only a drawn value
			// can differ, not the order the state store happens to return points in.
			for _, p := range m.Numbers {
				out = append(out, fmt.Sprintf("otlp %s %v", m.Name, p))
			}
			for _, p := range m.Histograms {
				out = append(out, fmt.Sprintf("otlp %s %v", m.Name, p))
			}
			for _, p := range m.ExpHistograms {
				out = append(out, fmt.Sprintf("otlp %s %v", m.Name, p))
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestActiveRoutesDrawOrderDeterministic requires two runs from fresh state with the same seed and
// clock to emit identical values. activeRoutes feeds the per-route RNG draws of the spanmetrics
// latency histogram and the OTLP http.server.request.duration histogram, so its order must not
// follow Go map iteration over the per-route stats.
func TestActiveRoutesDrawOrderDeterministic(t *testing.T) {
	first := tickMultiRoute(t)
	if len(first) == 0 {
		t.Fatal("no metrics emitted")
	}
	for run := 1; run < 5; run++ {
		got := tickMultiRoute(t)
		if len(got) != len(first) {
			t.Fatalf("run %d: %d series, run 0 had %d", run, len(got), len(first))
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("run %d differs from run 0 (same seed, fresh state):\n got: %.400s\nwant: %.400s", run, got[i], first[i])
			}
		}
	}
}
