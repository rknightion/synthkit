// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// tickWS builds a workload (rum optional) and Ticks the metric lane once with a populated
// ledger so the active-window path is exercised. Returns the metric capture.
func tickWS(t *testing.T, rum core.RUMSink) (*Workload, *coretest.MetricCapture) {
	t.Helper()
	w, led := buildWS(t, rum)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	// Seed the ledger with a sample so activeRoutes/activeCalls use the live path.
	mintNonEmpty(t, led, now, false)

	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return w, mc
}

// TestMetricInventory: the metric lane emits exactly the expected APM family names.
func TestMetricInventory(t *testing.T) {
	_, mc := tickWS(t, &faroCapture{}) // RUM on → browser target_info + browser→service edge
	got := mc.Names()

	// histograms expand to _bucket/_sum/_count; check on the base set.
	wantContains := []string{
		"traces_spanmetrics_calls_total",
		"traces_spanmetrics_latency_bucket",
		"traces_spanmetrics_latency_sum",
		"traces_spanmetrics_latency_count",
		"traces_spanmetrics_size_total",
		"traces_service_graph_request_total",
		"traces_service_graph_request_failed_total",
		"traces_service_graph_request_server_seconds_bucket",
		"traces_service_graph_request_server_seconds_sum",
		"traces_service_graph_request_server_seconds_count",
		"traces_service_graph_request_client_seconds_bucket",
		"traces_service_graph_request_client_seconds_sum",
		"traces_service_graph_request_client_seconds_count",
		"target_info",
		"traces_target_info",
	}
	for _, name := range wantContains {
		if !slices.Contains(got, name) {
			t.Errorf("missing metric %q (got %v)", name, got)
		}
	}
	// Guard against the compound-word trap and against any native-histogram-less suffix.
	for _, n := range got {
		if strings.Contains(n, "span_metrics") {
			t.Errorf("metric %q uses span_metrics (must be spanmetrics, one word)", n)
		}
		if strings.Contains(n, "_duration_seconds") {
			t.Errorf("metric %q uses _duration_seconds (latency suffix is _latency)", n)
		}
	}
}

// TestCallsTotalLabelSet asserts the exact calls_total label keys including the
// service/service_name dual, the cluster/k8s_cluster_name dual, telemetry_sdk_language
// (calls_total ONLY), deployment_environment_name (_name form per OTEL semconv standard),
// source=tempo, and that NO high-card labels leak in.
func TestCallsTotalLabelSet(t *testing.T) {
	_, mc := tickWS(t, nil)
	keys := mc.LabelKeys("traces_spanmetrics_calls_total")
	want := []string{
		"cluster", "deployment_environment_name", "job", "k8s_cluster_name",
		"k8s_namespace_name", "namespace", "service", "service_name",
		"service_namespace", "service_version", "source", "span_kind",
		"span_name", "status_code", "telemetry_sdk_language",
	}
	assertKeysEqual(t, "traces_spanmetrics_calls_total", keys, want)

	// legacy deployment_environment (without _name) must NOT appear on span-metrics.
	if slices.Contains(keys, "deployment_environment") {
		t.Error("calls_total carries legacy deployment_environment (must use deployment_environment_name)")
	}
	// instance must be omitted (Tempo-sourced span-metrics).
	if slices.Contains(keys, "instance") {
		t.Error("calls_total carries instance (must be omitted on Tempo span-metrics)")
	}
	// source value.
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels["source"] != "tempo" {
			t.Fatalf("source=%q want tempo", s.Labels["source"])
		}
		if s.Labels["service"] != s.Labels["service_name"] {
			t.Fatalf("service/service_name dual mismatch: %q vs %q", s.Labels["service"], s.Labels["service_name"])
		}
		if s.Labels["cluster"] != s.Labels["k8s_cluster_name"] {
			t.Fatalf("cluster/k8s_cluster_name dual mismatch")
		}
	}
}

// TestLatencyAndSizeOmitSDKLanguage: telemetry_sdk_language is on calls_total ONLY (trap §8).
func TestLatencyAndSizeOmitSDKLanguage(t *testing.T) {
	_, mc := tickWS(t, nil)
	for _, name := range []string{"traces_spanmetrics_latency_bucket", "traces_spanmetrics_size_total"} {
		if slices.Contains(mc.LabelKeys(name), "telemetry_sdk_language") {
			t.Errorf("%s carries telemetry_sdk_language (calls_total only — trap §8)", name)
		}
	}
}

// TestServiceGraphNoBarePromotedLabels: service-graph series carry client_*/server_*
// promoted dims and bare convention labels (service/namespace/cluster) but NO bare
// promoted dimension (e.g. no bare deployment_environment_name / k8s_namespace_name — those
// are prefixed per edge-side; trap §9). instance omitted. Uses _name form per semconv.
func TestServiceGraphNoBarePromotedLabels(t *testing.T) {
	_, mc := tickWS(t, &faroCapture{})
	keys := mc.LabelKeys("traces_service_graph_request_total")
	want := []string{
		"client", "client_cluster", "client_deployment_environment_name",
		"client_k8s_cluster_name", "client_k8s_namespace_name", "client_service_namespace",
		"client_service_version", "cluster", "connection_type", "job",
		"k8s_cluster_name", "namespace", "server", "server_cluster",
		"server_deployment_environment_name", "server_k8s_cluster_name",
		"server_k8s_namespace_name", "server_service_namespace", "server_service_version",
		"service", "source",
	}
	assertKeysEqual(t, "traces_service_graph_request_total", keys, want)

	// Bare promoted dims must NOT appear on service-graph (they are client_/server_ split).
	for _, bare := range []string{"deployment_environment", "deployment_environment_name", "k8s_namespace_name", "service_namespace", "service_version", "blueprint"} {
		if slices.Contains(keys, bare) {
			t.Errorf("service-graph carries bare promoted label %q (must be client_/server_ split — trap §9)", bare)
		}
	}
	if slices.Contains(keys, "instance") {
		t.Error("service-graph carries instance (must be omitted)")
	}

	// connection_type=database on the db edge.
	var sawDBEdge bool
	for _, s := range mc.Find("traces_service_graph_request_total") {
		if s.Labels["connection_type"] == "database" {
			sawDBEdge = true
		}
	}
	if !sawDBEdge {
		t.Error("no service-graph edge with connection_type=database for the db hop")
	}
}

// TestTargetInfoLabelSet: target_info carries service_name (no bare service),
// deployment_environment_name (legacy deployment_environment DROPPED — clean cutover per
// OTEL semconv standard), k8s_pod_name + k8s_deployment_name (the entity-graph join),
// telemetry_sdk_language. source must NOT appear on target_info (source="tempo" is correct
// only on span-metric/service-graph series — live OTLP-path target_info has no source
// label). k8s_node_name and service_instance_id must be present.
func TestTargetInfoLabelSet(t *testing.T) {
	w, mc := tickWS(t, nil)
	keys := mc.LabelKeys("target_info")
	for _, k := range []string{
		"service_name", "service_namespace", "service_version",
		"deployment_environment_name",
		"k8s_cluster_name", "k8s_namespace_name", "k8s_pod_name", "k8s_deployment_name",
		"cluster", "job", "telemetry_sdk_language",
		"k8s_node_name", "service_instance_id",
	} {
		if !slices.Contains(keys, k) {
			t.Errorf("target_info missing %q", k)
		}
	}
	if slices.Contains(keys, "service") {
		t.Error("target_info carries bare service (target_info has service_name ONLY — trap §4)")
	}
	// Legacy deployment_environment (without _name) must NOT appear on target_info.
	if slices.Contains(keys, "deployment_environment") {
		t.Error("target_info carries legacy deployment_environment (must use deployment_environment_name — clean cutover)")
	}
	// source must NOT appear on target_info (live OTLP-path target_info has no source label;
	// source="tempo" belongs only on span-metric/service-graph series).
	if slices.Contains(keys, "source") {
		t.Error("target_info carries source label (must be absent — source belongs on span-metrics/service-graph only)")
	}
	// k8s_pod_name must equal the placement's pod 0 (the kube_pod_info join, I12).
	for _, s := range mc.Find("target_info") {
		if s.Labels["service_name"] == w.name {
			if s.Labels["k8s_pod_name"] != w.podName {
				t.Fatalf("target_info k8s_pod_name=%q != placement pod %q", s.Labels["k8s_pod_name"], w.podName)
			}
			if s.Labels["k8s_deployment_name"] != w.name {
				t.Fatalf("target_info k8s_deployment_name=%q != %q", s.Labels["k8s_deployment_name"], w.name)
			}
			// service_instance_id must equal the pod name (I12 join: service_instance_id = pod 0).
			if s.Labels["service_instance_id"] != w.podName {
				t.Fatalf("target_info service_instance_id=%q != pod name %q", s.Labels["service_instance_id"], w.podName)
			}
			// k8s_node_name must be non-empty when there is a cluster placement with nodes.
			if s.Labels["k8s_node_name"] == "" {
				t.Fatal("target_info k8s_node_name is empty — must be resolved from cluster node placement")
			}
			// k8s_node_name must equal the workload's resolved node hostname.
			if s.Labels["k8s_node_name"] != w.nodeName {
				t.Fatalf("target_info k8s_node_name=%q != workload.nodeName %q", s.Labels["k8s_node_name"], w.nodeName)
			}
		}
	}
}

// TestHistogramWireForm: traces_spanmetrics_latency is emitted as a CLASSIC histogram with
// _bucket{le}/_sum/_count and LEDotZero le formatting (forced ".0" on integer bounds),
// matching the predecessor's app_apm.go wire form (the signals/apm.md "native cutover" is documented
// but NOT implemented in the predecessor code — see metrics.go wire-form note).
func TestHistogramWireForm(t *testing.T) {
	_, mc := tickWS(t, nil)

	buckets := mc.Find("traces_spanmetrics_latency_bucket")
	if len(buckets) == 0 {
		t.Fatal("no _bucket series — latency must be a classic histogram")
	}
	// LEDotZero: integer-valued bounds carry a trailing ".0" and +Inf is present.
	les := map[string]bool{}
	for _, s := range buckets {
		les[s.Labels["le"]] = true
	}
	for _, want := range []string{"0.0", "1.0", "5.0", "10.0", "+Inf"} {
		if !les[want] {
			t.Errorf("missing le=%q (LEDotZero classic histogram)", want)
		}
	}
	// the bare integer forms must NOT appear (would be LEBare).
	for _, bad := range []string{"0", "1", "5", "10"} {
		if les[bad] {
			t.Errorf("le=%q present — should be LEDotZero (%q.0)", bad, bad)
		}
	}
	// _sum and _count exist.
	if len(mc.Find("traces_spanmetrics_latency_sum")) == 0 || len(mc.Find("traces_spanmetrics_latency_count")) == 0 {
		t.Error("classic histogram missing _sum/_count")
	}
}

// TestNoHighCardMetricLabels: no request-id-class key appears as a metric label anywhere.
func TestNoHighCardMetricLabels(t *testing.T) {
	_, mc := tickWS(t, &faroCapture{})
	forbidden := []string{"trace_id", "span_id", "request_id", "session_id", "correlation_id", "user_id", "run_id"}
	for _, s := range mc.All() {
		for k := range s.Labels {
			if slices.Contains(forbidden, k) {
				t.Fatalf("metric %q carries high-card label %q", s.Name, k)
			}
		}
	}
}

// TestAbsentDimensionOmitted (I13): a binding with no cluster placement must NOT emit an
// empty/"NA" cluster — it falls back to the binding name-derived identity, never a sentinel.
func TestAbsentDimensionOmitted(t *testing.T) {
	cfg := NewConfig().(*Config)
	// Binding with NO cluster and NO calls.
	b := core.Binding{Name: "lonely-api", Env: coretest.Env()}
	wAny, err := build(cfg, b)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	w := wAny.(*Workload)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = ledger.New(shape.New("", nil), 0, 0)
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, s := range mc.All() {
		for k, v := range s.Labels {
			if v == "" || v == "NA" {
				t.Fatalf("metric %q label %q has sentinel value %q (absent dims must be omitted, I13)", s.Name, k, v)
			}
		}
	}
	// With no cluster placement, cluster falls back to "" — which means it must be OMITTED,
	// not emitted empty. Verify cluster key is absent rather than "".
	for _, s := range mc.Find("target_info") {
		if cv, ok := s.Labels["cluster"]; ok && cv == "" {
			t.Fatal("empty cluster label emitted instead of omitted (I13)")
		}
	}
}

// ── REGRESSION: cumulative-discipline (M5 verification pins) ─────────────────

// buildAndTick2 builds a workload and runs two metric-lane Ticks 60 s apart,
// returning (mc1, mc2). Uses a fixed business-hours now so BusinessFactor > 0.
func buildAndTick2(t *testing.T) (*coretest.MetricCapture, *coretest.MetricCapture) {
	t.Helper()
	w, led := buildWS(t, nil)
	now1 := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // business hours
	now2 := now1.Add(60 * time.Second)
	mintNonEmpty(t, led, now1, false)

	mc1 := &coretest.MetricCapture{}
	mc2 := &coretest.MetricCapture{}

	world1 := coretest.World(mc1, nil, nil)
	world1.Ledger = led
	if err := w.Tick(context.Background(), now1, world1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	world2 := coretest.World(mc2, nil, nil)
	world2.Ledger = led
	if err := w.Tick(context.Background(), now2, world2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	return mc1, mc2
}

// metricLabelSig returns a stable string key from a label map (test helper).
func metricLabelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// TestCounterFamiliesStrictlyIncreaseAcrossTicks pins that the four counter families
// (traces_spanmetrics_calls_total, traces_spanmetrics_size_total,
// traces_service_graph_request_total, traces_service_graph_request_failed_total)
// strictly increase across two consecutive ticks. This is the cumulative-discipline
// invariant (I3): state.Add must be used, not state.Set.
func TestCounterFamiliesStrictlyIncreaseAcrossTicks(t *testing.T) {
	mc1, mc2 := buildAndTick2(t)
	counterNames := []string{
		"traces_spanmetrics_calls_total",
		"traces_spanmetrics_size_total",
		"traces_service_graph_request_total",
		"traces_service_graph_request_failed_total",
	}
	for _, name := range counterNames {
		s1 := mc1.Find(name)
		s2 := mc2.Find(name)
		if len(s1) == 0 || len(s2) == 0 {
			t.Errorf("counter %q not found (tick1=%d tick2=%d)", name, len(s1), len(s2))
			continue
		}
		prev := make(map[string]float64, len(s1))
		for _, s := range s1 {
			prev[metricLabelSig(s.Labels)] = s.Value
		}
		for _, s := range s2 {
			sig := metricLabelSig(s.Labels)
			v1, ok := prev[sig]
			if !ok {
				continue
			}
			if s.Value <= v1 {
				t.Errorf("counter %q did NOT strictly increase: tick1=%.6f tick2=%.6f",
					name, v1, s.Value)
			}
		}
	}
}

// TestGaugesDoNotAccumulateAcrossTicks pins that target_info and traces_target_info
// (both state.Set gauges) stay at their instantaneous value (~1.0) and do NOT
// accumulate across ticks. If they were state.Add the value would double each tick.
func TestGaugesDoNotAccumulateAcrossTicks(t *testing.T) {
	_, mc2 := buildAndTick2(t)
	for _, name := range []string{"target_info", "traces_target_info"} {
		for _, s := range mc2.Find(name) {
			// target_info / traces_target_info must be 1.0 (set, not accumulated).
			// After 2 ticks with state.Add it would be 2.0.
			if s.Value > 1.5 {
				t.Errorf("gauge %q accumulated to %.4f after 2 ticks — should stay at 1.0 (state.Set)",
					name, s.Value)
			}
		}
		if len(mc2.Find(name)) == 0 {
			t.Errorf("gauge %q not found in tick2", name)
		}
	}
}

// TestHistogramCountIncreasesAcrossTicks pins that traces_spanmetrics_latency
// produces _bucket/_sum/_count and the _count value increases from tick1 to tick2.
// This covers the histogram expand-and-accumulate path (state.Observe, I3).
func TestHistogramCountIncreasesAcrossTicks(t *testing.T) {
	mc1, mc2 := buildAndTick2(t)

	// Must have _bucket and _sum and _count.
	for _, suf := range []string{"_bucket", "_sum", "_count"} {
		full := "traces_spanmetrics_latency" + suf
		if len(mc1.Find(full)) == 0 {
			t.Errorf("histogram traces_spanmetrics_latency: %q absent in tick1", full)
		}
	}

	// _count must increase across ticks.
	cnt1 := mc1.Find("traces_spanmetrics_latency_count")
	cnt2 := mc2.Find("traces_spanmetrics_latency_count")
	if len(cnt1) == 0 || len(cnt2) == 0 {
		t.Fatalf("latency_count not found (tick1=%d tick2=%d)", len(cnt1), len(cnt2))
	}
	prev := make(map[string]float64, len(cnt1))
	for _, s := range cnt1 {
		prev[metricLabelSig(s.Labels)] = s.Value
	}
	increased := false
	for _, s := range cnt2 {
		if v1, ok := prev[metricLabelSig(s.Labels)]; ok && s.Value > v1 {
			increased = true
			break
		}
	}
	if !increased {
		t.Error("traces_spanmetrics_latency_count did NOT increase across ticks — histogram _count must accumulate (state.Observe)")
	}
}

var _ = fixture.CallTarget{} // fixture import used by testBinding in projection_test.go

func TestServiceGraphConnectionTypeByKind(t *testing.T) {
	// Build a workload whose binding has BOTH a db hop and a service hop, then Tick the metric
	// lane with an empty ledger so activeCalls falls back to w.m.calls (the configured specs).
	cfg := NewConfig().(*Config)
	cfg.Tracing = true
	b := testBinding(nil)
	b.Calls = append(b.Calls, fixture.CallTarget{Kind: "service", Service: "payments"})
	w, err := build(cfg, b)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ws := w.(*Workload)

	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil) // no ledger → activeCalls uses w.m.calls
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	if err := ws.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	var sawDB, sawService bool
	for _, s := range mc.Find("traces_service_graph_request_total") {
		switch s.Labels["server"] {
		case "test-db": // the db hop edge
			if s.Labels["connection_type"] != "database" {
				t.Errorf("db edge connection_type=%q, want database", s.Labels["connection_type"])
			}
			sawDB = true
		case "payments": // the service hop edge
			if s.Labels["connection_type"] != "" {
				t.Errorf("service edge connection_type=%q, want \"\" (empty)", s.Labels["connection_type"])
			}
			sawService = true
		}
	}
	if !sawDB || !sawService {
		t.Fatalf("expected both a db edge and a service edge (sawDB=%v sawService=%v)", sawDB, sawService)
	}
}

// ── Exemplar production (real-trace exemplars on request-correlated families) ────

// TestSampleRequestsPullsRealTraceIDs (B1): sampleRequests pulls real 32-hex trace ids
// from the workload's active ledger window.
func TestSampleRequestsPullsRealTraceIDs(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	world := coretest.World(&coretest.MetricCapture{}, nil, nil)
	world.Ledger = led
	samples := w.sampleRequests(now, world)
	if len(samples) == 0 {
		t.Fatal("expected real request samples from the ledger window")
	}
	for _, s := range samples {
		if len(s.traceID) != 32 {
			t.Fatalf("expected 32-hex trace id, got %q", s.traceID)
		}
	}
	// newest-first ordering.
	for i := 1; i < len(samples); i++ {
		if samples[i].start.After(samples[i-1].start) {
			t.Fatalf("samples not sorted newest-first at %d", i)
		}
	}
}

// TestSpanMetricsLatencyCarriesTraceExemplars (B2): spanmetrics latency buckets +
// calls/size counters carry real trace_id exemplars.
func TestSpanMetricsLatencyCarriesTraceExemplars(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, name := range []string{
		"traces_spanmetrics_latency_bucket",
		"traces_spanmetrics_calls_total",
		"traces_spanmetrics_size_total",
	} {
		exes := mc.Exemplars(name)
		if len(exes) == 0 {
			t.Fatalf("expected trace_id exemplars on %s", name)
		}
		for _, e := range exes {
			if len(e.Labels["trace_id"]) != 32 {
				t.Fatalf("%s: exemplar must carry a 32-hex trace_id, got %q", name, e.Labels["trace_id"])
			}
		}
	}
}

// TestServiceGraphSecondsCarriesTraceExemplars (B3): service-graph seconds histograms +
// request counter carry real trace_id exemplars.
func TestServiceGraphSecondsCarriesTraceExemplars(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, name := range []string{
		"traces_service_graph_request_server_seconds_bucket",
		"traces_service_graph_request_client_seconds_bucket",
		"traces_service_graph_request_total",
	} {
		exes := mc.Exemplars(name)
		if len(exes) == 0 {
			t.Fatalf("expected trace exemplars on %s", name)
		}
		for _, e := range exes {
			if len(e.Labels["trace_id"]) != 32 {
				t.Fatalf("%s: exemplar must carry a 32-hex trace_id, got %q", name, e.Labels["trace_id"])
			}
		}
	}
}

// TestGenAIHistogramsCarryTraceExemplars (B4): gen_ai histograms carry real trace_id
// exemplars (every request traverses the AI hops, so all samples apply).
func TestGenAIHistogramsCarryTraceExemplars(t *testing.T) {
	w, led := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, name := range []string{
		genai.MetricClientOpDuration + "_bucket",
		genai.MetricClientTokenUsage + "_bucket",
		genai.MetricClientTTFC + "_bucket",
	} {
		exes := mc.Exemplars(name)
		if len(exes) == 0 {
			t.Fatalf("expected trace exemplars on %s", name)
		}
		for _, e := range exes {
			if len(e.Labels["trace_id"]) != 32 {
				t.Fatalf("%s: exemplar must carry a 32-hex trace_id, got %q", name, e.Labels["trace_id"])
			}
		}
	}
}

// TestEmitSpanMetricsSwitchOff: with world.EmitSpanMetrics=false, synthkit emits NO backend
// spanmetrics/service-graph families (deferring them — and their exemplars — to metrics-generator/beyla).
func TestEmitSpanMetricsSwitchOff(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	world.EmitSpanMetrics = false
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, n := range []string{
		"traces_spanmetrics_latency_bucket",
		"traces_spanmetrics_calls_total",
		"traces_spanmetrics_size_total",
		"traces_service_graph_request_total",
		"traces_service_graph_request_server_seconds_bucket",
	} {
		if len(mc.Find(n)) != 0 {
			t.Fatalf("%s must NOT be emitted when EmitSpanMetrics=false", n)
		}
	}
}

// TestEmitSpanMetricsSwitchOnByDefault: the coretest World defaults EmitSpanMetrics=true, so the
// backend spanmetrics/service-graph families emit (current behavior preserved).
func TestEmitSpanMetricsSwitchOnByDefault(t *testing.T) {
	w, led := buildWS(t, nil)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil) // coretest default: EmitSpanMetrics true
	world.Ledger = led
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(mc.Find("traces_spanmetrics_latency_bucket")) == 0 {
		t.Fatal("spanmetrics must emit when EmitSpanMetrics=true (coretest default)")
	}
	if len(mc.Find("traces_service_graph_request_total")) == 0 {
		t.Fatal("service-graph must emit when EmitSpanMetrics=true (coretest default)")
	}
}

// assertKeysEqual fails if got != want (both sorted).
func assertKeysEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	g := slices.Clone(got)
	wnt := slices.Clone(want)
	slices.Sort(g)
	slices.Sort(wnt)
	if !slices.Equal(g, wnt) {
		t.Errorf("%s label keys mismatch:\n got:  %v\n want: %v", name, g, wnt)
	}
}

// TestSpanHistogramsEmitNativeAndClassic (SK-28): the three Tempo-derived span histograms
// must emit BOTH a native histogram series (bare name, Native!=nil) AND the classic
// _bucket/_count/_sum series. gen_ai_* histograms must stay classic-only. Uses the AI binding
// (buildAIWS) so the full gen_ai_* family is actually present in the capture — the negative
// guard would be vacuous against the plain testBinding, which has no AI hops.
func TestSpanHistogramsEmitNativeAndClassic(t *testing.T) {
	w, led := buildAIWS(t)
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mintNonEmpty(t, led, now, false)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Ledger = led
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	out := mc.All()

	check := func(name string) {
		t.Helper()
		var native, cb, cc, cs bool
		for _, m := range out {
			switch m.Name {
			case name:
				if m.Native != nil {
					native = true
				}
			case name + "_bucket":
				cb = true
			case name + "_count":
				cc = true
			case name + "_sum":
				cs = true
			}
		}
		if !native {
			t.Errorf("%s: missing NATIVE series (bare name, Native!=nil)", name)
		}
		if !cb || !cc || !cs {
			t.Errorf("%s: missing classic _bucket/_count/_sum (bucket=%v count=%v sum=%v)", name, cb, cc, cs)
		}
	}
	check("traces_spanmetrics_latency")
	check("traces_service_graph_request_server_seconds")
	check("traces_service_graph_request_client_seconds")

	// gen_ai_* must stay classic-only (prefix guard covers ALL gen_ai histograms, not a
	// hand-picked subset). Assert the guard is non-vacuous: gen_ai series MUST be present.
	var sawGenAI bool
	for _, m := range out {
		if strings.HasPrefix(m.Name, "gen_ai_") {
			sawGenAI = true
			if m.Native != nil {
				t.Errorf("%s must remain classic-only; found native series", m.Name)
			}
		}
	}
	if !sawGenAI {
		t.Fatal("no gen_ai_* series in capture — the classic-only guard is vacuous (AI binding must emit gen_ai histograms)")
	}
}
