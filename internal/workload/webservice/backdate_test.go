// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// TestWebserviceTraceBackdatesToCompletion: the web_service trace lane times spans purely from
// r.RenderStart(), so backdating (RenderStart = Start − Duration − RenderOffset) flows through with
// NO webservice code change — the backend SERVER span ENDS at ~Start (≤ now, within the jitter
// window) and STARTS a full Duration before that.
func TestWebserviceTraceBackdatesToCompletion(t *testing.T) {
	w, _ := buildWS(t, nil)
	now := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	r := &ledger.Request{
		Correlation: ledger.Correlation{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "1111111111111111"},
		Workload:    "test-api", Env: "prod", Cluster: "test-prod-use1", Route: "GET /v1/items",
		Start:        now,
		Duration:     9 * time.Second,
		RenderOffset: 2 * time.Second,
		Outcome:      ledger.OutcomeSuccess,
	}
	tc := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, tc)
	if err := w.projectTraces(context.Background(), world, []*ledger.Request{r}); err != nil {
		t.Fatalf("projectTraces: %v", err)
	}

	var server *otlp.Span
	for _, res := range tc.Resources {
		for i := range res.Spans {
			sp := res.Spans[i]
			if sp.SpanID == r.SpanID && sp.Kind == otlp.KindServer {
				s := sp
				server = &s
			}
		}
	}
	if server == nil {
		t.Fatal("no backend SERVER span")
	}
	if server.End.After(now) {
		t.Fatalf("backend SERVER span ends %v after now=%v — backdated traces must end ≤ now", server.End, now)
	}
	if d := now.Sub(server.End); d > ledger.RenderJitterWindow {
		t.Fatalf("backend SERVER span ends %v before now (%v) — beyond the %v completion-jitter window", server.End, d, ledger.RenderJitterWindow)
	}
	if want := server.End.Add(-r.Duration); !server.Start.Equal(want) {
		t.Fatalf("backend SERVER span start = %v, want end−Duration = %v", server.Start, want)
	}
}
