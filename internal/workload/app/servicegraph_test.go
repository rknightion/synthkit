// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/telemetryspec"
	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

func ptr[T any](v T) *T { return &v }

// buildApp builds an app workload from a service-graph config bound to the standard test cluster.
func buildApp(t *testing.T, cfg *Config) *Workload {
	t.Helper()
	w, err := build(cfg, core.Binding{
		Name:    "demo",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

// fe→api→pg graph: a frontend entry, an instrumented backend, a leaf db.
func graphCfg() *Config {
	return &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{
			{
				Name: "web-fe", Type: "frontend", Entry: true, Calls: []string{"api"},
				Spans: []telemetryspec.SpanSpec{{NameTemplate: "load /", Kind: "client",
					Attributes: map[string]telemetryspec.ValueModel{"page.route": {ConstStr: ptr("/home")}}}},
			},
			{
				Name: "api", Type: "web", Runtime: "go", Calls: []string{"pg"},
				Metrics: []telemetryspec.MetricSpec{{
					Name: "http_server_active_requests", Instrument: "gauge",
					Value:  telemetryspec.ValueModel{Shape: &telemetryspec.ShapeModel{Base: 20}},
					Labels: map[string]telemetryspec.ValueModel{"http_response_status_code": {Enum: []telemetryspec.EnumEntry{{Value: "200", Weight: 9}, {Value: "500", Weight: 1}}}},
				}},
				Logs: []telemetryspec.LogSpec{{Source: "app", Body: map[string]telemetryspec.ValueModel{
					"msg":      {ConstStr: ptr("served")},
					"route":    {Ref: "route"},
					"trace_id": {Ref: "trace_id"}, // high-card ref → structured metadata
				}}},
			},
			{
				Name: "pg", Type: "db",
				Metrics: []telemetryspec.MetricSpec{{
					Name: "pg_stat_database_blks_hit", Instrument: "counter",
					Value: telemetryspec.ValueModel{IntRange: &telemetryspec.IntRange{Min: 100, Max: 1000}},
				}},
			},
		},
	}
}

func TestApp_SingleTraceAcrossGraph(t *testing.T) {
	w := buildApp(t, graphCfg())
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})

	// Mint exactly one request and project it.
	r := w.m.mintOne(now, world.Shape)
	tc := world.Traces.(*coretest.TraceCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// Collect all spans; every span shares the ONE trace id.
	type span struct{ id, parent, svc string }
	var spans []span
	bySvc := map[string]bool{}
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		bySvc[svc] = true
		for _, sp := range res.Spans {
			if sp.TraceID != r.TraceID {
				t.Fatalf("span %q traceID=%s want the one request trace %s", sp.Name, sp.TraceID, r.TraceID)
			}
			spans = append(spans, span{sp.SpanID, sp.ParentID, svc})
		}
	}
	if len(spans) == 0 {
		t.Fatal("no spans emitted")
	}
	// frontend + api each get their own resource (db is a leaf — represented by the caller's
	// CLIENT span, no own resource).
	for _, want := range []string{"web-fe", "api"} {
		if !bySvc[want] {
			t.Errorf("missing resource for node %q (have %v)", want, bySvc)
		}
	}
	// exactly one root (ParentID==""), and it is the entry's span.
	ids := map[string]bool{}
	var roots []span
	for _, s := range spans {
		ids[s.id] = true
		if s.parent == "" {
			roots = append(roots, s)
		}
	}
	if len(roots) != 1 {
		t.Fatalf("want exactly one root span, got %d", len(roots))
	}
	if roots[0].id != r.SpanID || roots[0].svc != "web-fe" {
		t.Errorf("root span %+v, want entry web-fe / %s", roots[0], r.SpanID)
	}
	// every non-root span's parent exists in the trace (a connected tree — continuous correlation).
	for _, s := range spans {
		if s.parent != "" && !ids[s.parent] {
			t.Errorf("span %s has dangling parent %s (broken trace continuity)", s.id, s.parent)
		}
	}
}

func TestApp_PerNodeMetricsAndLogs(t *testing.T) {
	w := buildApp(t, graphCfg())
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc, lc := &coretest.MetricCapture{}, &coretest.LogCapture{}
	world := coretest.World(mc, lc, &coretest.TraceCapture{})

	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	r := w.m.mintOne(now, world.Shape)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// api's gauge is stamped with the api node identity; pg's counter with pg's.
	apiSeries := mc.Find("http_server_active_requests")
	if len(apiSeries) == 0 {
		t.Fatal("api node emitted no http_server_active_requests")
	}
	if got := apiSeries[0].Labels["service"]; got != "api" {
		t.Errorf("api metric service label=%q want api", got)
	}
	// enum label expanded to its full domain (both 200 and 500 series present — determinism §3.4).
	statuses := map[string]bool{}
	for _, s := range apiSeries {
		statuses[s.Labels["http_response_status_code"]] = true
	}
	if !statuses["200"] || !statuses["500"] {
		t.Errorf("enum label domain not fully enumerated: %v", statuses)
	}
	if len(mc.Find("pg_stat_database_blks_hit")) == 0 {
		t.Error("pg node emitted no pg_stat_database_blks_hit")
	}

	// api's log: trace_id rode in structured metadata (not a stream label, not the body).
	var found bool
	for _, st := range lc.Streams {
		if st.Labels["source"] != "app" || st.Labels["service_name"] != "api" {
			continue
		}
		if _, bad := st.Labels["trace_id"]; bad {
			t.Error("trace_id leaked into a Loki stream label")
		}
		for _, ln := range st.Lines {
			if ln.Meta["trace_id"] == r.TraceID {
				found = true
			}
		}
	}
	if !found {
		t.Error("api log line did not carry trace_id in structured metadata")
	}
}

// TestApp_NodeIdentityNoDupSeries: two nodes sharing the SAME inline metric must NOT collide —
// the node identity (service label) is auto-stamped before author labels (B3).
func TestApp_NodeIdentityNoDupSeries(t *testing.T) {
	shared := []telemetryspec.MetricSpec{{
		Name: "process_resident_memory_bytes", Instrument: "gauge",
		Value: telemetryspec.ValueModel{Const: ptr(1.0e8)},
	}}
	cfg := &Config{
		Services: []ServiceNode{
			{Name: "svc-a", Type: "web", Entry: true, Calls: []string{"svc-b"}, Metrics: shared},
			{Name: "svc-b", Type: "web", Metrics: shared},
		},
	}
	w := buildApp(t, cfg)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, &coretest.LogCapture{}, &coretest.TraceCapture{})
	if err := w.Tick(context.Background(), time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	series := mc.Find("process_resident_memory_bytes")
	if len(series) != 2 {
		t.Fatalf("want 2 distinct process_resident_memory_bytes series (one per node), got %d", len(series))
	}
	svcs := map[string]bool{}
	sigs := map[string]bool{}
	for _, s := range series {
		svcs[s.Labels["service"]] = true
		sig := seriesSig(s)
		if sigs[sig] {
			t.Fatalf("duplicate series signature %q (node identity not stamped)", sig)
		}
		sigs[sig] = true
	}
	if !svcs["svc-a"] || !svcs["svc-b"] {
		t.Errorf("series not distinguished by node identity: %v", svcs)
	}
}

// TestApp_ReservedLabelRejected: an author label that collides with an auto-stamped node-identity
// key is rejected at load (review H1 — prevents silent identity clobbering / dup series).
func TestApp_ReservedLabelRejected(t *testing.T) {
	cfg := &Config{Services: []ServiceNode{{
		Name: "svc", Type: "web", Entry: true,
		Metrics: []telemetryspec.MetricSpec{{
			Name: "x", Instrument: "gauge", Value: telemetryspec.ValueModel{Const: ptr(1.0)},
			Labels: map[string]telemetryspec.ValueModel{"cluster": {ConstStr: ptr("oops")}},
		}},
	}}}
	if _, err := build(cfg, core.Binding{Name: "t", Env: coretest.Env(), Cluster: coretest.Cluster()}); err == nil {
		t.Fatal("a metric label colliding with the reserved identity key 'cluster' must be rejected at load")
	}
}

// TestApp_AllCatalogProfilesCompose: every catalog profile composes onto an app node without a
// reserved-key collision or validation failure — i.e. the profiles are migration-ready.
func TestApp_AllCatalogProfilesCompose(t *testing.T) {
	names := profiles.Names()
	if len(names) == 0 {
		t.Fatal("no catalog profiles registered")
	}
	for _, name := range names {
		cfg := &Config{Services: []ServiceNode{{Name: "svc", Type: "web", Entry: true, Profiles: []string{name}}}}
		if _, err := build(cfg, core.Binding{Name: "t", Env: coretest.Env(), Cluster: coretest.Cluster()}); err != nil {
			t.Errorf("profile %q does not compose onto an app node: %v", name, err)
		}
	}
}

// TestApp_EmitSpanMetricsParity: the app honors the per-blueprint EmitSpanMetrics opt-in exactly
// like web_service — synthesizing traces_spanmetrics_* / traces_service_graph_* from its OWN graph
// when ON (Spec 4 dashgen needs them), and NOT when OFF (metrics-generator derives them).
func TestApp_EmitSpanMetricsParity(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	// ON (coretest.World defaults EmitSpanMetrics:true).
	wOn := buildApp(t, graphCfg())
	mcOn := &coretest.MetricCapture{}
	if err := wOn.Tick(context.Background(), now, coretest.World(mcOn, nil, nil)); err != nil {
		t.Fatalf("Tick on: %v", err)
	}
	for _, name := range []string{
		"traces_spanmetrics_calls_total", "traces_spanmetrics_latency",
		"traces_service_graph_request_total", "traces_service_graph_request_server_seconds",
		"traces_service_graph_request_failed_total", // review H2 — the edge error-rate family
	} {
		if len(mcOn.Find(name)) == 0 {
			t.Errorf("EmitSpanMetrics ON: missing %s", name)
		}
	}
	// review H2: the APM RED error dimension — a STATUS_CODE_ERROR calls row must be present.
	var hasErrRow bool
	for _, s := range mcOn.Find("traces_spanmetrics_calls_total") {
		if s.Labels["status_code"] == "STATUS_CODE_ERROR" {
			hasErrRow = true
		}
	}
	if !hasErrRow {
		t.Error("EmitSpanMetrics ON: no STATUS_CODE_ERROR calls_total row (APM error dimension missing)")
	}
	// no duplicate series in the synthesized output.
	sigs := map[string]bool{}
	for _, s := range mcOn.All() {
		if sig := seriesSig(s); sigs[sig] {
			t.Errorf("duplicate series %q in spanmetrics synthesis", sig)
		} else {
			sigs[sig] = true
		}
	}

	// OFF.
	wOff := buildApp(t, graphCfg())
	mcOff := &coretest.MetricCapture{}
	world := coretest.World(mcOff, nil, nil)
	world.EmitSpanMetrics = false
	if err := wOff.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick off: %v", err)
	}
	if len(mcOff.Find("traces_spanmetrics_calls_total")) != 0 {
		t.Error("EmitSpanMetrics OFF: must NOT synthesize spanmetrics (metrics-generator derives them)")
	}
}

// TestApp_GenAIModelProviderFlow: an app declaring `models`/`providers` draws one per request and
// stamps it into the correlation, so the gen_ai CLIENT span's gen_ai.request.model AND the Portkey
// export log's ai_model carry the REAL model (not the empty string the bare profile refs would
// otherwise resolve to — the seam-B fix that keeps Spec 4's per-model Portkey rules meaningful).
func TestApp_GenAIModelProviderFlow(t *testing.T) {
	// Paired routings (the #2 realism fix): a request draws a (model,provider) PAIR, so it can never
	// produce an impossible combination like claude on azure-openai.
	choices := []ModelChoice{{Model: "gpt-4o", Provider: "azure-openai"}, {Model: "claude-3.5-sonnet", Provider: "bedrock"}}
	validPair := map[string]string{"gpt-4o": "azure-openai", "claude-3.5-sonnet": "bedrock"}

	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Models:  choices,
		Services: []ServiceNode{{
			Name: "backend", Type: "web", Runtime: "python", Entry: true,
			Routes:   []string{"POST /v1/assist"}, // #1 realism fix: real route, not hardcoded "GET /"
			Profiles: []string{"gen_ai_client", "gateway_export_log"},
		}},
	}
	w := buildApp(t, cfg)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})

	r := w.m.mintOne(now, world.Shape)
	// #2: model+provider must be a VALID declared pair (not an independent cross-product).
	if want, ok := validPair[r.Model]; !ok || r.Provider != want {
		t.Fatalf("minted (model=%q, provider=%q) is not a declared valid pair %v", r.Model, r.Provider, validPair)
	}
	// #1: the request carries the declared route, not the hardcoded default.
	if r.Route != "POST /v1/assist" {
		t.Errorf("minted route=%q want POST /v1/assist (entry routes not drawn)", r.Route)
	}

	tc := world.Traces.(*coretest.TraceCapture)
	lc := world.Logs.(*coretest.LogCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	// the gen_ai CLIENT span attr must carry the request's real model (not "").
	var sawModelAttr bool
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			if m, ok := sp.Attrs["gen_ai.request.model"].(string); ok && m != "" {
				if m != r.Model {
					t.Errorf("gen_ai.request.model=%q want %q (the request's drawn model)", m, r.Model)
				}
				sawModelAttr = true
			}
		}
	}
	if !sawModelAttr {
		t.Error("no span carried a non-empty gen_ai.request.model (model/provider correlation broken)")
	}

	// the Portkey export log body's ai_model must be the request's real model.
	var sawLogModel bool
	for _, st := range lc.Streams {
		if st.Labels["source"] != "portkey" {
			continue
		}
		for _, ln := range st.Lines {
			if strings.Contains(ln.Body, `"ai_model":"`+r.Model+`"`) {
				sawLogModel = true
			}
		}
	}
	if !sawLogModel {
		t.Error("Portkey export log body did not carry the request's ai_model (export-log model correlation broken)")
	}
}

// seriesSig is a stable name+sorted-labels signature for dup detection.
func seriesSig(s promrw.Series) string {
	keys := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(s.Name)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(s.Labels[k])
	}
	return b.String()
}
