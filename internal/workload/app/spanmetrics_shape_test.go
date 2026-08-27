// SPDX-License-Identifier: AGPL-3.0-only

package app

// SKT-0008 — shape conformance for synthkit's OWN span-derived metric emission.
//
// synthkit models the TEMPO METRICS-GENERATOR (family names traces_spanmetrics_* /
// traces_service_graph_*, proto span-kind/status enum strings, source="tempo"), not the
// collector-side spanmetrics connector (which publishes traces_span_metrics_calls /
// _duration under source="spanmetrics" and produces no service graph at all). These tests
// pin the properties a real metrics-generator guarantees because it derives every row from
// the same span stream this workload projects.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/shape"
)

// spanDerivedFamilies are every family gated by the EmitSpanMetrics opt-in, in the wire forms
// state.Collect publishes (classic buckets + native bare name + _count/_sum).
var spanDerivedFamilies = []string{
	"traces_spanmetrics_calls_total",
	"traces_spanmetrics_size_total",
	"traces_spanmetrics_latency",
	"traces_spanmetrics_latency_bucket",
	"traces_spanmetrics_latency_count",
	"traces_spanmetrics_latency_sum",
	"traces_service_graph_request_total",
	"traces_service_graph_request_failed_total",
	"traces_service_graph_request_server_seconds",
	"traces_service_graph_request_server_seconds_bucket",
	"traces_service_graph_request_client_seconds",
	"traces_service_graph_request_client_seconds_bucket",
}

// TestSpanMetricsDefaultOffDefersToMetricsGenerator (SKT-0008 AC#4) pins the OUT-OF-THE-BOX
// path: a World whose EmitSpanMetrics field was never set — the Go zero value, which is what
// runner.spanMetricsEnabled produces with no control state loaded — must synthesize NONE of
// the span-derived families, so Tempo's metrics-generator (or Beyla) remains their sole
// producer. The node's own declared metrics must still emit, so "OFF" is proven to gate the
// span lane rather than the whole tick.
func TestSpanMetricsDefaultOffDefersToMetricsGenerator(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	w := buildApp(t, graphCfg())
	mc := &coretest.MetricCapture{}

	// Deliberately NOT coretest.World (which opts in): a bare World, EmitSpanMetrics unset.
	world := &core.World{Shape: shape.New("", nil), Metrics: mc}
	if world.EmitSpanMetrics {
		t.Fatal("core.World zero value must leave EmitSpanMetrics OFF — the default-off contract")
	}
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, name := range spanDerivedFamilies {
		if got := len(mc.Find(name)); got != 0 {
			t.Errorf("default OFF: %s must not be synthesized (%d series) — defer to metrics-generator/beyla", name, got)
		}
	}
	if len(mc.Find("http_server_active_requests")) == 0 {
		t.Error("default OFF gated too much: the node's own declared metrics must still emit")
	}
}

// TestSpanMetricSpanNamesTrackEmittedSpans (SKT-0008 AC#2/#3) pins the span_name DIMENSION to
// the names this workload's trace lane really emits. A real producer reads span.Name() off the
// span, so a routed node contributes one row per route (project.go serverSpanName), an unrouted
// callee its node name, and an unrouted entry the minter's default route.
func TestSpanMetricSpanNamesTrackEmittedSpans(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	cfg := &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{
			{Name: "edge", Type: "web", Entry: true, Routes: []string{"GET /a", "POST /b"}, Calls: []string{"api", "plain"}},
			{Name: "api", Type: "web", Runtime: "go", Routes: []string{"GET /v1/thing"}},
			{Name: "plain", Type: "web", Runtime: "go"},
		},
	}
	w := buildApp(t, cfg)
	mc := &coretest.MetricCapture{}
	if err := w.Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	byService := map[string]map[string]bool{}
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels["span_kind"] == spanKindClient {
			continue // edge rows are named "call <target>", asserted separately below
		}
		svc := s.Labels["service"]
		if byService[svc] == nil {
			byService[svc] = map[string]bool{}
		}
		byService[svc][s.Labels["span_name"]] = true
	}
	for svc, want := range map[string][]string{
		"edge":  {"GET /a", "POST /b"}, // entry routes, one row each
		"api":   {"GET /v1/thing"},     // callee route (serverSpanName)
		"plain": {"plain"},             // unrouted callee → node name
	} {
		if got := sortedKeys(byService[svc]); !equalStrings(got, want) {
			t.Errorf("service %q span_name set: got %v want %v", svc, got, want)
		}
	}
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels["span_kind"] == spanKindClient && !strings.HasPrefix(s.Labels["span_name"], "call ") {
			t.Errorf("CLIENT row span_name %q must match the projected client span name %q", s.Labels["span_name"], "call <target>")
		}
	}

	// An unrouted entry falls back to the same literal the minter draws (defaultEntryRoute).
	bare := buildApp(t, &Config{
		Traffic:  Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{{Name: "solo", Type: "web", Entry: true}},
	})
	if got := bare.m.drawRoute(shape.New("", nil)); got != defaultEntryRoute {
		t.Fatalf("defaultEntryRoute drifted from minter.drawRoute: %q vs %q", defaultEntryRoute, got)
	}
	mcBare := &coretest.MetricCapture{}
	if err := bare.Tick(context.Background(), now, coretest.World(mcBare, nil, nil)); err != nil {
		t.Fatalf("Tick bare: %v", err)
	}
	for _, s := range mcBare.Find("traces_spanmetrics_calls_total") {
		if s.Labels["span_name"] != defaultEntryRoute {
			t.Errorf("unrouted entry span_name: got %q want %q", s.Labels["span_name"], defaultEntryRoute)
		}
	}
}

// TestSpanMetricEntryRowCarriesRootSpanKind (SKT-0008 AC#2/#3) pins span_kind on the entry row
// to the entry's REAL root span kind. A browser entry roots a CLIENT span and a worker entry a
// CONSUMER span, so a producer deriving metrics from those traces never reports SPAN_KIND_SERVER
// for them.
func TestSpanMetricEntryRowCarriesRootSpanKind(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ entryType, wantKind string }{
		{"frontend", spanKindClient},
		{"worker", spanKindConsumer},
		{"job", spanKindInternal},
		{"web", spanKindServer},
	} {
		cfg := &Config{
			Traffic:  Traffic{OffPeakRPS: 20, PeakRPS: 50},
			Services: []ServiceNode{{Name: "entry", Type: tc.entryType, Entry: true}},
		}
		mc := &coretest.MetricCapture{}
		if err := buildApp(t, cfg).Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
			t.Fatalf("%s: Tick: %v", tc.entryType, err)
		}
		rows := mc.Find("traces_spanmetrics_calls_total")
		if len(rows) == 0 {
			t.Fatalf("%s: no calls_total rows", tc.entryType)
		}
		for _, s := range rows {
			if s.Labels["span_kind"] != tc.wantKind {
				t.Errorf("entry type %q: span_kind %q, want %q (the projected root span kind)",
					tc.entryType, s.Labels["span_kind"], tc.wantKind)
			}
		}
	}
}

// TestSpanMetricLatencySharesCallsLabelSet (SKT-0008 AC#2/#3) pins the identity a real producer
// cannot break: latency and calls_total are derived from the same spans, so every calls_total
// series has a latency series with the identical label set minus telemetry_sdk_language (which
// rides on calls_total only) — including the STATUS_CODE_ERROR rows. It also pins that the
// histogram is observed once per counted call rather than once per tick.
func TestSpanMetricLatencySharesCallsLabelSet(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	w := buildApp(t, graphCfg())
	if err := w.Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	callsSigs := map[string]bool{}
	var sawError bool
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		lbls := map[string]string{}
		for k, v := range s.Labels {
			if k == "telemetry_sdk_language" {
				continue
			}
			lbls[k] = v
		}
		callsSigs[labelSig(lbls)] = true
		if s.Labels["status_code"] == statusCodeError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("no STATUS_CODE_ERROR calls_total row — the APM RED error dimension is missing")
	}
	latSigs := map[string]bool{}
	for _, s := range mc.Find("traces_spanmetrics_latency_count") {
		latSigs[labelSig(s.Labels)] = true
	}
	for sig := range callsSigs {
		if !latSigs[sig] {
			t.Errorf("calls_total series has no matching traces_spanmetrics_latency series: %s", sig)
		}
	}
	for sig := range latSigs {
		if !callsSigs[sig] {
			t.Errorf("latency series has no matching traces_spanmetrics_calls_total series: %s", sig)
		}
	}

	// One observation per tick would leave every _count at 1; a real producer observes every
	// span it counts. Assert the busiest OK row tracked its call volume (up to the budget).
	var maxCount float64
	for _, s := range mc.Find("traces_spanmetrics_latency_count") {
		if s.Labels["status_code"] == statusCodeOK && s.Value > maxCount {
			maxCount = s.Value
		}
	}
	if maxCount < 2 {
		t.Errorf("traces_spanmetrics_latency_count max = %v: the histogram must be observed once per counted call, not once per tick", maxCount)
	}

	// The same holds for the service-graph latency families.
	for _, name := range []string{"traces_service_graph_request_server_seconds_count", "traces_service_graph_request_client_seconds_count"} {
		rows := mc.Find(name)
		if len(rows) == 0 {
			t.Fatalf("%s: no series", name)
		}
		for _, s := range rows {
			if s.Value < 2 {
				t.Errorf("%s = %v: edge histograms must be observed once per counted request", name, s.Value)
			}
		}
	}
}

// TestSpanMetricPerRowVolumeSumsToTickVolume (SKT-0008 AC#3) pins that splitting a node's rows
// across its routes redistributes the node's tick volume rather than multiplying it — a
// cardinality-shaped change must not inflate the counted total.
func TestSpanMetricPerRowVolumeSumsToTickVolume(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	one := tickCallsTotal(t, now, []string{"GET /only"})
	three := tickCallsTotal(t, now, []string{"GET /a", "GET /b", "GET /c"})
	if diff := one - three; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("per-route split changed the node total: 1 route = %v, 3 routes = %v", one, three)
	}
}

func tickCallsTotal(t *testing.T, now time.Time, routes []string) float64 {
	t.Helper()
	cfg := &Config{
		Traffic:  Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{{Name: "entry", Type: "web", Entry: true, Routes: routes}},
	}
	mc := &coretest.MetricCapture{}
	if err := buildApp(t, cfg).Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var total float64
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		total += s.Value
	}
	return total
}

func labelSig(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(l[k])
	}
	return b.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
