// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// backdateCfg: fe (entry) → backend (web), backend emits one "request served" completion log.
func backdateCfg() *Config {
	msg := "assist request served"
	return &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50, RequestLatencyP95Ms: 9000}, // multi-second dur → backdating is visible
		Services: []ServiceNode{
			{Name: "web-fe", Type: "frontend", Entry: true, Calls: []string{"backend"}},
			{
				Name: "backend", Type: "web", Runtime: "python",
				Logs: []telemetryspec.LogSpec{{
					Source: "app",
					Body:   map[string]telemetryspec.ValueModel{"msg": {ConstStr: &msg}},
				}},
			},
		},
	}
}

// TestAppTraceBackdatesToCompletion: the fabricated trace ENDS at ~now (≤ now, within the jitter
// window) and STARTS a full Duration before that — realistic completed-span timing, not spans that
// run into the future. Every emitted span ends ≤ now.
func TestAppTraceBackdatesToCompletion(t *testing.T) {
	w := buildApp(t, backdateCfg())
	now := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	r := w.m.mintOne(now, world.Shape)
	tc := world.Traces.(*coretest.TraceCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// Locate the entry root span (SpanID == r.SpanID) and check every span ends ≤ now.
	var entry *otlp.Span
	for _, res := range tc.Resources {
		for i := range res.Spans {
			sp := res.Spans[i]
			if sp.End.After(now) {
				t.Errorf("span %q ends %v AFTER now=%v (backdated traces must end ≤ now)", sp.Name, sp.End, now)
			}
			if sp.SpanID == r.SpanID {
				s := sp
				entry = &s
			}
		}
	}
	if entry == nil {
		t.Fatal("no entry root span (SpanID == r.SpanID)")
	}
	// Trace ENDS at ~now: end == Start − RenderOffset, so end ≤ now and within one jitter window.
	if entry.End.After(now) {
		t.Fatalf("entry span ends %v after now=%v", entry.End, now)
	}
	if d := now.Sub(entry.End); d > ledger.RenderJitterWindow {
		t.Fatalf("entry span ends %v before now (%v) — beyond the %v completion-jitter window", entry.End, d, ledger.RenderJitterWindow)
	}
	// Trace STARTS a full Duration before its end.
	if want := entry.End.Add(-r.Duration); !entry.Start.Equal(want) {
		t.Fatalf("entry span start = %v, want end−Duration = %v", entry.Start, want)
	}
}

// TestAppCompletionLogAtCompletion: the "request served" log is a COMPLETION event, so it lands at
// the trace's end (≈ now), NOT at the request start (a full Duration earlier) — consistent with the
// backdated spans and with the web_service log lane.
func TestAppCompletionLogAtCompletion(t *testing.T) {
	w := buildApp(t, backdateCfg())
	now := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	r := w.m.mintOne(now, world.Shape)
	lc := world.Logs.(*coretest.LogCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	wantT := r.RenderStart().Add(r.Duration) // completion = trace end ≈ now
	var n int
	for _, s := range lc.Streams {
		for _, ln := range s.Lines {
			n++
			if !ln.T.Equal(wantT) {
				t.Errorf("log line T = %v, want completion %v (= trace end, not request start)", ln.T, wantT)
			}
			if ln.T.After(now) {
				t.Errorf("log line T = %v is after now=%v", ln.T, now)
			}
		}
	}
	if n == 0 {
		t.Fatal("no log lines emitted")
	}
	// Sanity: completion is a full Duration after the request start (backdating is real).
	if r.RenderStart().Add(r.Duration).Sub(r.RenderStart()) != r.Duration {
		t.Fatal("internal: duration math")
	}
}
