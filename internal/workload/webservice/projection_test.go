// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// faroCapture is a core.RUMSink that records every beacon (test double for the binding's
// Faro sink).
type faroCapture struct{ payloads []faro.Payload }

func (c *faroCapture) Write(_ context.Context, p []faro.Payload) error {
	c.payloads = append(c.payloads, p...)
	return nil
}

// testBinding builds a binding with a cluster placement (so target_info pod join holds), a
// db call, and (optionally) a RUM sink.
func testBinding(rum core.RUMSink) core.Binding {
	cl := coretest.Cluster() // has a "test-api" placement
	db := coretest.DB("postgres")
	return core.Binding{
		Name:    "test-api",
		Env:     coretest.Env(),
		Cluster: cl,
		Calls:   []fixture.CallTarget{{Kind: "db", DB: db}},
		RUM:     rum,
	}
}

// buildWS builds a web_service workload with tracing+rum enabled and a real ledger driving
// the mint. Returns the workload and the ledger.
func buildWS(t *testing.T, rum core.RUMSink) (*Workload, *ledger.Ledger) {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.RUM = rum != nil
	cfg.Tracing = true
	w, err := build(cfg, testBinding(rum))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	eng := shape.New("", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	return w.(*Workload), led
}

// mintNonEmpty mints repeatedly until the ledger returns a non-empty batch (the master
// clock returns the fresh batch directly). It forces browser-origin coverage by retrying
// until at least one browser-origin request appears when RUM is on.
func mintNonEmpty(t *testing.T, led *ledger.Ledger, now time.Time, wantBrowser bool) []*ledger.Request {
	t.Helper()
	for range 500 {
		batch := led.Mint(now)
		if len(batch) == 0 {
			continue
		}
		if !wantBrowser {
			return batch
		}
		for _, r := range batch {
			if r.BrowserOrigin {
				return batch
			}
		}
	}
	t.Fatal("could not mint a non-empty (browser-origin) batch")
	return nil
}

// TestCorrelationPropagation is the request-correlation invariant: across the ProjectBatch
// output, every span of a request shares r.TraceID; the log structured metadata trace_id
// == r.TraceID; the RUM fetch event traceID == r.TraceID; the browser span id ==
// r.BrowserSpanID and the backend SERVER span's ParentID == r.BrowserSpanID for browser-
// origin requests (== "" otherwise).
func TestCorrelationPropagation(t *testing.T) {
	rum := &faroCapture{}
	w, led := buildWS(t, rum)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	batch := mintNonEmpty(t, led, now, true)

	mc, lc, tc := &coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{}
	world := coretest.World(mc, lc, tc)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// Index requests by TraceID.
	reqByTrace := map[string]*ledger.Request{}
	for _, r := range batch {
		reqByTrace[r.TraceID] = r
	}

	// Collect all spans (backend + browser resources) and group by trace.
	type spanInfo struct {
		spanID, parentID, svc string
		isServer, isBrowser   bool
	}
	spansByTrace := map[string][]spanInfo{}
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		isBrowser := strings.HasSuffix(svc, "-frontend")
		for _, sp := range res.Spans {
			// every span's TraceID must map to a known request.
			if _, ok := reqByTrace[sp.TraceID]; !ok {
				t.Fatalf("span %q has unknown TraceID %q", sp.Name, sp.TraceID)
			}
			spansByTrace[sp.TraceID] = append(spansByTrace[sp.TraceID], spanInfo{
				spanID: sp.SpanID, parentID: sp.ParentID, svc: svc,
				isServer: sp.Kind == 1 /*KindServer*/, isBrowser: isBrowser,
			})
		}
	}

	for trace, r := range reqByTrace {
		spans := spansByTrace[trace]
		if len(spans) == 0 {
			t.Fatalf("no spans for trace %s", trace)
		}
		// find the backend SERVER span.
		var server *spanInfo
		for i := range spans {
			if spans[i].isServer {
				server = &spans[i]
			}
		}
		if server == nil {
			t.Fatalf("no backend SERVER span for trace %s", trace)
		}
		if server.spanID != r.SpanID {
			t.Fatalf("SERVER span id %q != r.SpanID %q", server.spanID, r.SpanID)
		}
		if r.BrowserOrigin {
			if r.BrowserSpanID == "" {
				t.Fatal("browser-origin request has empty BrowserSpanID")
			}
			if server.parentID != r.BrowserSpanID {
				t.Fatalf("browser-origin: SERVER ParentID %q != r.BrowserSpanID %q", server.parentID, r.BrowserSpanID)
			}
			// the browser span must exist with id == BrowserSpanID and parent "".
			var found bool
			for _, s := range spans {
				if s.isBrowser && s.spanID == r.BrowserSpanID {
					found = true
					if s.parentID != "" {
						t.Fatalf("browser root span ParentID %q != \"\"", s.parentID)
					}
				}
			}
			if !found {
				t.Fatalf("no browser span with id %q for trace %s", r.BrowserSpanID, trace)
			}
		} else if server.parentID != "" {
			t.Fatalf("non-browser SERVER ParentID %q != \"\"", server.parentID)
		}
	}

	// Logs: every lifecycle line's structured-metadata trace_id maps to a known request.
	logTraces := map[string]bool{}
	for _, st := range lc.Streams {
		for _, ln := range st.Lines {
			tid := ln.Meta["trace_id"]
			if tid == "" {
				t.Fatal("log line missing trace_id in structured metadata")
			}
			if _, ok := reqByTrace[tid]; !ok {
				t.Fatalf("log trace_id %q has no matching request", tid)
			}
			logTraces[tid] = true
		}
	}
	for trace := range reqByTrace {
		if !logTraces[trace] {
			t.Fatalf("no log line for trace %s", trace)
		}
	}

	// RUM: each fetch event's traceID == a browser-origin request's TraceID.
	for _, p := range rum.payloads {
		for _, ev := range p.Events {
			if ev.Name == "faro.tracing.fetch" {
				if ev.Trace == nil {
					t.Fatal("faro.tracing.fetch missing trace context")
				}
				r, ok := reqByTrace[ev.Trace.TraceID]
				if !ok {
					t.Fatalf("RUM fetch traceID %q has no matching request", ev.Trace.TraceID)
				}
				if !r.BrowserOrigin {
					t.Fatalf("RUM beacon for non-browser-origin request %s", r.TraceID)
				}
			}
		}
	}
}

// TestProjectBatchExactlyOnce: ProjectBatch on a batch of N emits exactly N backend SERVER
// spans (one per request — exactly-once by construction, I10).
func TestProjectBatchExactlyOnce(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	batch := mintNonEmpty(t, led, now, false)

	tc := &coretest.TraceCapture{}
	world := coretest.World(nil, &coretest.LogCapture{}, tc)
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	serverSpans := 0
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			if sp.Kind == 1 { // KindServer
				serverSpans++
			}
		}
	}
	if serverSpans != len(batch) {
		t.Fatalf("exactly-once: %d SERVER spans for a batch of %d requests", serverSpans, len(batch))
	}
}

// TestNoHighCardLogStreamLabels: log STREAM labels carry no request-id-class keys; those
// keys appear only in structured metadata (I14).
func TestNoHighCardLogStreamLabels(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	batch := mintNonEmpty(t, led, now, false)

	lc := &coretest.LogCapture{}
	world := coretest.World(nil, lc, nil)
	w.cfg.Tracing = false // logs-only for this check
	if err := w.ProjectBatch(context.Background(), now, world, batch); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	forbidden := []string{"trace_id", "span_id", "request_id", "session_id", "correlation_id"}
	allowed := []string{"env", "service_namespace", "service_name", "level", "source", "cluster", "job"}
	for _, st := range lc.Streams {
		for k := range st.Labels {
			if slices.Contains(forbidden, k) {
				t.Fatalf("forbidden high-card key %q used as a Loki stream label", k)
			}
			if !slices.Contains(allowed, k) {
				t.Fatalf("unexpected stream label %q (not in the low-card set)", k)
			}
		}
		// the high-card keys MUST be present in structured metadata instead.
		for _, ln := range st.Lines {
			for _, k := range []string{"trace_id", "span_id", "correlation_id", "request_id", "session_id"} {
				if ln.Meta[k] == "" {
					t.Fatalf("structured metadata missing %q", k)
				}
			}
		}
	}
}

// The installed native app selects indexed service_namespace/service_name, then filters
// deployment_environment. Exercise the public projection and real Loki push encoder so
// metadata cannot accidentally satisfy an indexed-identity assertion.
func TestProjectBatchNativeAppLogIdentityOnLokiWire(t *testing.T) {
	type wireStream struct {
		Labels map[string]string   `json:"stream"`
		Values [][]json.RawMessage `json:"values"`
	}
	received := make(chan []wireStream, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer gz.Close()
		var body struct {
			Streams []wireStream `json:"streams"`
		}
		if err := json.NewDecoder(gz).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- body.Streams
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	for _, identity := range []struct{ service, namespace, environment string }{
		{"checkout", "backend", "prod"},
		{"checkout", "backend", "staging"},
		{"checkout", "other", "prod"},
		{"receipts", "backend", "prod"},
	} {
		t.Run(identity.service+"/"+identity.namespace+"/"+identity.environment, func(t *testing.T) {
			cfg := NewConfig().(*Config)
			cfg.Tracing, cfg.RUM = true, false
			binding := testBinding(nil)
			binding.Name = identity.service
			binding.Env = &fixture.Env{Name: identity.environment, Weight: 1}
			binding.Cluster.Env = binding.Env
			binding.Cluster.Workloads[0].Name = identity.service
			binding.Cluster.Workloads[0].Namespace = identity.namespace
			built, err := build(cfg, binding)
			if err != nil {
				t.Fatal(err)
			}
			workload := built.(*Workload)
			led := ledger.New(shape.New("", nil), 0, 0)
			led.AddMinter(workload.Minter())
			now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
			batch := mintNonEmpty(t, led, now, false)
			traces := &coretest.TraceCapture{}
			world := coretest.World(nil, nil, traces)
			world.Logs = loki.New(server.URL, "fixture", "fixture", false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := workload.ProjectBatch(ctx, now, world, batch); err != nil {
				t.Fatal(err)
			}
			if len(traces.Resources) != 1 {
				t.Fatalf("expected the request's backend resource, got %d", len(traces.Resources))
			}
			resource := traces.Resources[0].Attrs
			if resource["service.namespace"] != identity.namespace || resource["deployment.environment.name"] != identity.environment {
				t.Fatalf("trace identity disagrees with the declared placement: %v", resource)
			}
			var streams []wireStream
			select {
			case streams = <-received:
			case <-ctx.Done():
				t.Fatal("no Loki push received")
			}
			requests := map[string]*ledger.Request{}
			for _, request := range batch {
				requests[request.TraceID] = request
			}
			completed := map[string]int{}
			for _, stream := range streams {
				if stream.Labels["service_namespace"] != resource["service.namespace"] || stream.Labels["service_name"] != resource["service.name"] || stream.Labels["env"] != identity.environment {
					t.Fatalf("native app cannot isolate the emitted indexed identity: %v", stream.Labels)
				}
				if _, exists := stream.Labels["deployment_environment"]; exists {
					t.Fatal("app environment filter must remain metadata; env already indexes the environment")
				}
				if _, exists := stream.Labels["exporter"]; exists {
					t.Fatal("direct Loki push acquired exporter provenance")
				}
				for _, key := range []string{"trace_id", "span_id", "request_id", "session_id", "correlation_id"} {
					if _, exists := stream.Labels[key]; exists {
						t.Fatalf("request identifier %s entered the Loki index", key)
					}
				}
				for _, tuple := range stream.Values {
					if len(tuple) != 3 {
						t.Fatalf("expected body plus correlation metadata, got %d tuple elements", len(tuple))
					}
					var metadata map[string]string
					if err := json.Unmarshal(tuple[2], &metadata); err != nil {
						t.Fatal(err)
					}
					request := requests[metadata["trace_id"]]
					if request == nil || metadata["span_id"] != request.SpanID || metadata["request_id"] != request.RequestID || metadata["correlation_id"] != request.CorrelationID {
						t.Fatalf("request correlation changed on the wire: %v", metadata)
					}
					if metadata["deployment_environment"] != resource["deployment.environment.name"] {
						t.Fatalf("native app environment filter cannot isolate this request: %v", metadata)
					}
					var body struct{ Msg string }
					var line string
					if err := json.Unmarshal(tuple[1], &line); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(line), &body); err != nil {
						t.Fatal(err)
					}
					if body.Msg == "request completed" {
						completed[request.TraceID]++
					}
				}
			}
			for _, request := range batch {
				if completed[request.TraceID] != 1 {
					t.Fatalf("request %s produced %d lifecycle logs, want one", request.TraceID, completed[request.TraceID])
				}
			}
		})
	}
}

func TestProjectTraces_ServiceHopTree(t *testing.T) {
	// buildWS gives a real Workload (name "test-api", env "prod", cluster "test-prod-use1") via the
	// existing testBinding; we drive projectTraces directly with a hand-built Request so we control
	// the hop tree + span ids. (The binding's own db call is irrelevant here — we pass r.Calls.)
	w, _ := buildWS(t, nil)
	r := &ledger.Request{
		Correlation: ledger.Correlation{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "1111111111111111"},
		Workload:    "test-api", Env: "prod", Cluster: "test-prod-use1", Route: "GET /v1/items",
		Start: time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), Duration: 100 * time.Millisecond, Outcome: ledger.OutcomeSuccess,
		Calls: []ledger.Call{
			{Kind: "service", Target: "payments", SpanID: "aaaaaaaaaaaaaaaa", PeerSpanID: "bbbbbbbbbbbbbbbb", ParentHopIndex: -1, Duration: 30 * time.Millisecond},
			{Kind: "db", Target: "app-db", Engine: "postgres", SpanID: "cccccccccccccccc", ParentHopIndex: 0, Duration: 20 * time.Millisecond},
		},
	}
	tc := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, tc)
	if err := w.projectTraces(context.Background(), world, []*ledger.Request{r}); err != nil {
		t.Fatal(err)
	}
	// Flatten captured resources→spans, and remember which resource each span sat in.
	by := map[string]otlp.Span{}
	svcOf := map[string]string{} // spanID → owning resource's service.name
	calleeResSeen := false
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		if svc == "payments" {
			calleeResSeen = true
		}
		for _, s := range res.Spans {
			by[s.SpanID] = s
			svcOf[s.SpanID] = svc
		}
	}
	// service CLIENT parents to backend SERVER; service SERVER parents to its CLIENT;
	// db CLIENT nests under the service SERVER (its parent hop's inner span).
	if by["aaaaaaaaaaaaaaaa"].ParentID != "1111111111111111" {
		t.Fatalf("service CLIENT parent=%q want backend SERVER", by["aaaaaaaaaaaaaaaa"].ParentID)
	}
	if by["bbbbbbbbbbbbbbbb"].ParentID != "aaaaaaaaaaaaaaaa" || by["bbbbbbbbbbbbbbbb"].Kind != otlp.KindServer {
		t.Fatalf("service SERVER span malformed: %+v", by["bbbbbbbbbbbbbbbb"])
	}
	if by["cccccccccccccccc"].ParentID != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("db CLIENT parent=%q want service SERVER (bbbb…)", by["cccccccccccccccc"].ParentID)
	}
	// callee SERVER span must live in the per-callee resource (service.name == payments).
	if !calleeResSeen || svcOf["bbbbbbbbbbbbbbbb"] != "payments" {
		t.Fatalf("callee SERVER span must live in a resource with service.name=payments (seen=%v, owner=%q)", calleeResSeen, svcOf["bbbbbbbbbbbbbbbb"])
	}
}

func TestProjectLogs_ServiceHopFailureLine(t *testing.T) {
	w, _ := buildWS(t, nil) // name "test-api", env "prod", cluster "test-prod-use1", namespace "test-api"
	r := &ledger.Request{
		Correlation: ledger.Correlation{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "1111111111111111", CorrelationID: "corr", RequestID: "req", SessionID: "sess"},
		Workload:    "test-api", Env: "prod", Cluster: "test-prod-use1", Route: "POST /v1/pay",
		Start: time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), Duration: 100 * time.Millisecond, Outcome: ledger.OutcomeServerError, StatusCode: 500,
		Calls: []ledger.Call{
			{Kind: "service", Target: "payments", SpanID: "aaaaaaaaaaaaaaaa", PeerSpanID: "bbbbbbbbbbbbbbbb", ParentHopIndex: -1, Duration: 30 * time.Millisecond, Failed: true},
		},
	}
	lc := &coretest.LogCapture{}
	world := coretest.World(nil, lc, nil)
	if err := w.ProjectBatch(context.Background(), r.Start, world, []*ledger.Request{r}); err != nil {
		t.Fatal(err)
	}
	// Expect a stream with service_name=payments carrying a line whose span_id == the service
	// hop's CLIENT span id (aaaa…), trace_id == request trace. (Inline scan — there is no hasLine helper.)
	var found bool
	for _, st := range lc.Streams {
		if st.Labels["service_name"] != "payments" {
			continue
		}
		if st.Labels["service_namespace"] != "test-api" || st.Labels["env"] != "prod" {
			t.Fatalf("callee log identity disagrees with its trace resource: %v", st.Labels)
		}
		for _, ln := range st.Lines {
			if ln.Meta["deployment_environment"] != "prod" {
				t.Fatalf("callee log lost the native app environment filter: %v", ln.Meta)
			}
			if ln.Meta["span_id"] == "aaaaaaaaaaaaaaaa" && ln.Meta["trace_id"] == r.TraceID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected a payments-stream failure line carrying the hop span_id")
	}
}
