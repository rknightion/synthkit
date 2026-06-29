// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// buildBeylaWS builds a web_service workload with the observability.beyla switch set to
// (mode, ctx), a cluster placement (so the k8s envelope joins), a db hop, and a service
// hop. Returns the workload only — the Beyla lane reads w.m.calls + the traffic config,
// not a live ledger (we Tick with an empty-ledger world so the configured calls path runs).
func buildBeylaWS(t *testing.T, mode, ctx string) *Workload {
	t.Helper()
	cfg := NewConfig().(*Config)
	cfg.Tracing = true
	cfg.Observability = &struct {
		Beyla *BeylaObs `yaml:"beyla"`
	}{Beyla: &BeylaObs{Mode: mode, Context: ctx}}

	b := testBinding(nil)
	b.Calls = append(b.Calls, fixture.CallTarget{Kind: "service", Service: "payments"})
	wAny, err := build(cfg, b)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return wAny.(*Workload)
}

// tickBeylaWS Ticks the Beyla lane once with an empty-ledger world and returns the capture.
func tickBeylaWS(t *testing.T, w *Workload) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil) // no ledger → configured-calls path
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	w.beylaLane().tick(now, world)
	if err := world.Metrics.Write(context.Background(), w.st.Collect(now)); err != nil {
		t.Fatalf("write: %v", err)
	}
	return mc
}

// TestBeylaEbpfOnlyEmitsFullSurface: ebpf_only k8s → RED server duration with source=beyla,
// network flow, spanmetrics + service-graph with source=beyla, target_info, NO avoided.
func TestBeylaEbpfOnlyEmitsFullSurface(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "ebpf_only")
	mc := tickBeylaWS(t, w)
	got := mc.Names()

	for _, name := range []string{
		beyla.MetricHTTPServerDuration + "_bucket",
		beyla.MetricNetworkFlowBytes,
		"traces_spanmetrics_calls_total",
		"traces_service_graph_request_total",
		"target_info",
		"traces_target_info",
	} {
		if !slices.Contains(got, name) {
			t.Errorf("ebpf_only missing %q (got %v)", name, got)
		}
	}
	// avoided_services must NOT be emitted in ebpf_only.
	if slices.Contains(got, beyla.MetricAvoidedServices) {
		t.Errorf("ebpf_only must NOT emit %q", beyla.MetricAvoidedServices)
	}

	// source=beyla everywhere it applies.
	for _, name := range []string{"traces_spanmetrics_calls_total", "traces_service_graph_request_total", "target_info"} {
		for _, s := range mc.Find(name) {
			if s.Labels["source"] != "beyla" {
				t.Errorf("%s source=%q want beyla", name, s.Labels["source"])
			}
		}
	}
	// RED server duration carries the k8s envelope + http_request_method. ⚠ NO source label
	// on RED (live ground truth — source is span/service-graph/target_info only).
	red := mc.Find(beyla.MetricHTTPServerDuration + "_bucket")
	if len(red) == 0 {
		t.Fatal("no RED server duration buckets")
	}
	for _, s := range red {
		if _, ok := s.Labels["source"]; ok {
			t.Errorf("RED must NOT carry source label (got %q)", s.Labels["source"])
		}
		if s.Labels["http_request_method"] == "" {
			t.Errorf("RED missing http_request_method")
		}
	}
	// target_info carries the eBPF distro marker + the full cloud/host envelope + instance.
	for _, s := range mc.Find("target_info") {
		if s.Labels["telemetry_distro_name"] != beyla.DistroName {
			t.Errorf("target_info telemetry_distro_name=%q want %q", s.Labels["telemetry_distro_name"], beyla.DistroName)
		}
		if s.Labels[beyla.LabelInstance] == "" {
			t.Errorf("target_info missing instance")
		}
		for _, k := range []string{
			beyla.LabelCloudAccountID, beyla.LabelCloudAvailabilityZone, beyla.LabelCloudPlatform,
			beyla.LabelCloudProvider, beyla.LabelCloudRegion, beyla.LabelHostID, beyla.LabelHostImageID,
			beyla.LabelHostName, beyla.LabelHostType, beyla.LabelOSType,
		} {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("target_info missing cloud/host envelope key %q", k)
			}
		}
		if s.Labels[beyla.LabelCloudPlatform] != "aws_ec2" {
			t.Errorf("target_info cloud_platform=%q want aws_ec2", s.Labels[beyla.LabelCloudPlatform])
		}
		if s.Labels[beyla.LabelCloudProvider] != "aws" {
			t.Errorf("target_info cloud_provider=%q want aws", s.Labels[beyla.LabelCloudProvider])
		}
		if s.Labels[beyla.LabelOSType] != "linux" {
			t.Errorf("target_info os_type=%q want linux", s.Labels[beyla.LabelOSType])
		}
	}

	// RED carries instance + server_port (live ground truth). blueprint is stamped by the
	// scoped writer (a synthkit-ism documented in signals/beyla.md) — not asserted here.
	for _, s := range red {
		if s.Labels[beyla.LabelInstance] == "" {
			t.Errorf("RED missing instance")
		}
	}
	if cl := mc.Find(beyla.MetricHTTPClientDuration + "_bucket"); len(cl) > 0 {
		for _, s := range cl {
			if s.Labels[beyla.LabelServerPort] == "" {
				t.Errorf("RED client missing server_port")
			}
		}
	}

	// Span-metrics: instance + k8s_node_name + cloud_availability_zone/cloud_region present;
	// telemetry_distro_name MUST be absent (live: span metrics carry no distro name).
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels[beyla.LabelInstance] == "" {
			t.Errorf("span-metrics missing instance")
		}
		if _, ok := s.Labels[beyla.LabelK8sNodeName]; !ok {
			t.Errorf("span-metrics missing k8s_node_name key")
		}
		if _, ok := s.Labels[beyla.LabelCloudAvailabilityZone]; !ok {
			t.Errorf("span-metrics missing cloud_availability_zone key")
		}
		if _, ok := s.Labels[beyla.LabelCloudRegion]; !ok {
			t.Errorf("span-metrics missing cloud_region key")
		}
		if _, ok := s.Labels[beyla.LabelTelemetryDistroName]; ok {
			t.Errorf("span-metrics must NOT carry telemetry_distro_name (live ground truth)")
		}
	}

	// Service-graph: no job, connection_type ∈ {"", virtual_node}, no bare k8s_cluster_name.
	for _, s := range mc.Find("traces_service_graph_request_total") {
		if _, ok := s.Labels[beyla.LabelJob]; ok {
			t.Errorf("service-graph must NOT carry job label")
		}
		if _, ok := s.Labels["k8s_cluster_name"]; ok {
			t.Errorf("service-graph must NOT carry bare k8s_cluster_name (uses client_/server_ prefix)")
		}
		ct := s.Labels[beyla.LabelConnectionType]
		if ct != beyla.ConnectionTypeEmpty && ct != beyla.ConnectionTypeVirtualNode {
			t.Errorf("service-graph connection_type=%q want one of {\"\", virtual_node}", ct)
		}
	}
}

// TestBeylaSpanMetricsFullKeySetSeeded: span-metrics seed the FULL live key set, absent dims
// as "" (ABSENT-DIM TRAP) — keys present, never omitted.
func TestBeylaSpanMetricsFullKeySetSeeded(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "ebpf_only")
	mc := tickBeylaWS(t, w)
	span := mc.Find("traces_spanmetrics_calls_total")
	if len(span) == 0 {
		t.Fatal("no span-metric calls series")
	}
	want := []string{
		beyla.LabelCloudAvailabilityZone, beyla.LabelCloudRegion, beyla.LabelDeploymentEnvironmentName,
		beyla.LabelInstance, beyla.LabelJob, beyla.LabelK8sClusterName, beyla.LabelK8sNamespaceName,
		beyla.LabelK8sNodeName, beyla.LabelServiceName, beyla.LabelServiceNamespace,
		beyla.LabelServiceVersion, beyla.LabelSource, beyla.LabelSpanKind, beyla.LabelSpanName,
		beyla.LabelStatusCode, beyla.LabelTelemetrySDKLanguage,
	}
	for _, s := range span {
		for _, k := range want {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("span-metric series missing key %q (must be present-as-\"\", not omitted)", k)
			}
		}
	}
}

// TestBeylaServiceGraphConnTypeNoDatabase: db/cache edges → connection_type "" (NOT the
// invented "database" value), only off-cluster/virtual edges use virtual_node.
func TestBeylaServiceGraphConnTypeNoDatabase(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "ebpf_only")
	mc := tickBeylaWS(t, w)
	for _, s := range mc.Find("traces_service_graph_request_total") {
		if s.Labels[beyla.LabelConnectionType] == "database" {
			t.Errorf("service-graph carries invented connection_type=database")
		}
	}
}

// TestBeylaStandaloneSpanNoK8s: standalone span-metrics carry no k8s_* keys (host/service
// identity instead) — mode branch in spanMetricBase + sgLabels.
func TestBeylaStandaloneSpanNoK8s(t *testing.T) {
	w := buildBeylaWS(t, "standalone", "ebpf_only")
	mc := tickBeylaWS(t, w)
	for _, name := range []string{"traces_spanmetrics_calls_total", "traces_service_graph_request_total"} {
		for _, k := range mc.LabelKeys(name) {
			if len(k) >= 4 && k[:4] == "k8s_" {
				t.Errorf("standalone %s carries k8s label %q", name, k)
			}
			if len(k) >= 11 && (k[:11] == "client_k8s_" || (len(k) >= 11 && k[:11] == "server_k8s_")) {
				t.Errorf("standalone %s carries prefixed k8s label %q", name, k)
			}
		}
	}
}

// TestBeylaCoexistEmitsOnlyNetworkTargetAvoided: coexist_sdk → ONLY network + target_info +
// avoided; NO RED/spanmetric/service-graph series.
func TestBeylaCoexistEmitsOnlyNetworkTargetAvoided(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "coexist_sdk")
	mc := tickBeylaWS(t, w)
	got := mc.Names()

	for _, name := range []string{
		beyla.MetricNetworkFlowBytes,
		"target_info",
		beyla.MetricAvoidedServices,
	} {
		if !slices.Contains(got, name) {
			t.Errorf("coexist_sdk missing %q (got %v)", name, got)
		}
	}
	// RED + span + service-graph must be ABSENT in coexist_sdk.
	for _, name := range []string{
		beyla.MetricHTTPServerDuration + "_bucket",
		"traces_spanmetrics_calls_total",
		"traces_service_graph_request_total",
	} {
		if slices.Contains(got, name) {
			t.Errorf("coexist_sdk must NOT emit %q (SDK-covered)", name)
		}
	}
	// avoided_services = 1 with the service identity.
	av := mc.Find(beyla.MetricAvoidedServices)
	if len(av) == 0 {
		t.Fatal("no beyla_avoided_services series")
	}
	for _, s := range av {
		if s.Value != 1 {
			t.Errorf("avoided value=%v want 1", s.Value)
		}
		if s.Labels["service_name"] != w.name {
			t.Errorf("avoided service_name=%q want %q", s.Labels["service_name"], w.name)
		}
	}
}

// TestBeylaStandaloneNoK8sLabels: standalone mode emits host_* and NO k8s_* on RED series.
func TestBeylaStandaloneNoK8sLabels(t *testing.T) {
	w := buildBeylaWS(t, "standalone", "ebpf_only")
	mc := tickBeylaWS(t, w)

	keys := mc.LabelKeys(beyla.MetricHTTPServerDuration + "_bucket")
	for _, k := range keys {
		if len(k) >= 4 && k[:4] == "k8s_" {
			t.Errorf("standalone RED carries k8s label %q", k)
		}
	}
	if !slices.Contains(keys, "host_name") {
		t.Errorf("standalone RED missing host_name (got %v)", keys)
	}
}

// TestBeylaGenAIChunkHistograms: the Beyla lane emits both chunk histograms
// (TTFC + time-per-output-chunk) when an AI hop is present.
func TestBeylaGenAIChunkHistograms(t *testing.T) {
	cfg := NewConfig().(*Config)
	cfg.Tracing = true
	cfg.Observability = &struct {
		Beyla *BeylaObs `yaml:"beyla"`
	}{Beyla: &BeylaObs{Mode: "kubernetes", Context: "ebpf_only"}}

	b := core.Binding{
		Name:    "ai-svc",
		Env:     coretest.Env(),
		Cluster: coretest.Cluster(),
		Calls: []fixture.CallTarget{
			{Kind: fixture.KindLLMModel, AI: &fixture.AICall{Op: genai.OpChat, Model: "gpt-4o", Provider: "openai"}, ParentHop: -1},
		},
	}
	wAny, err := build(cfg, b)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	w := wAny.(*Workload)
	mc := tickBeylaWS(t, w)

	for _, base := range []string{
		genai.MetricClientTTFC,
		genai.MetricClientTimePerOutputChunk,
	} {
		if got := mc.Find(base + "_bucket"); len(got) == 0 {
			t.Errorf("beyla lane missing gen_ai chunk histogram %q", base+"_bucket")
		}
	}
}

// TestBeylaNetworkFlowDirections: the network-flow counter carries direction ∈ {request,response}
// and the k8s src/dst owner keys in k8s mode.
func TestBeylaNetworkFlowDirections(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "ebpf_only")
	mc := tickBeylaWS(t, w)

	dirs := map[string]bool{}
	for _, s := range mc.Find(beyla.MetricNetworkFlowBytes) {
		dirs[s.Labels["direction"]] = true
		if s.Labels[beyla.LabelK8sSrcOwnerName] == "" {
			t.Errorf("network flow missing k8s_src_owner_name")
		}
	}
	for _, d := range []string{beyla.DirectionRequest, beyla.DirectionResponse} {
		if !dirs[d] {
			t.Errorf("network flow missing direction=%q (got %v)", d, dirs)
		}
	}
}

// TestBeylaNoHighCardLabels: no request-id-class key appears as a Beyla metric label.
func TestBeylaNoHighCardLabels(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "ebpf_only")
	mc := tickBeylaWS(t, w)
	forbidden := []string{"trace_id", "span_id", "request_id", "session_id", "correlation_id"}
	for _, s := range mc.All() {
		for k := range s.Labels {
			if slices.Contains(forbidden, k) {
				t.Fatalf("metric %q carries high-card label %q", s.Name, k)
			}
		}
	}
}

// ── Beyla boundary traces (Task 1b) ──────────────────────────────────────────

// beylaReq builds a request with a queue delay, a db hop and a service hop for trace tests.
func beylaReq() *ledger.Request {
	return &ledger.Request{
		Correlation: ledger.Correlation{
			TraceID: "0123456789abcdef0123456789abcdef", SpanID: "1111111111111111",
			CorrelationID: "corr", RequestID: "req", SessionID: "sess",
		},
		Workload: "test-api", Env: "prod", Cluster: "test-prod-use1", Route: "GET /v1/items",
		Start: time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), Duration: 100 * time.Millisecond,
		Outcome: ledger.OutcomeSuccess, StatusCode: 200,
		Calls: []ledger.Call{
			{Kind: "db", Target: "app-db", Engine: "postgres", SpanID: "cccccccccccccccc", ParentHopIndex: -1, Duration: 20 * time.Millisecond},
			{Kind: "service", Target: "payments", SpanID: "aaaaaaaaaaaaaaaa", PeerSpanID: "bbbbbbbbbbbbbbbb", ParentHopIndex: -1, Duration: 30 * time.Millisecond},
		},
	}
}

// TestBeylaTracesEbpfOnlyBoundary: ebpf_only → SERVER boundary span + INTERNAL queue/processing
// children + CLIENT span per hop; resource carries the eBPF distro marker; no business spans.
func TestBeylaTracesEbpfOnlyBoundary(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "ebpf_only")
	r := beylaReq()
	resources := w.projectBeylaTraces(r)
	if len(resources) == 0 {
		t.Fatal("ebpf_only must produce trace resources")
	}

	// distro marker on the boundary resource.
	var distro bool
	kinds := map[otlp.SpanKind]int{}
	var sawServer bool
	internalNames := map[string]bool{}
	for _, res := range resources {
		if res.Attrs[beyla.AttrDistroName] == beyla.DistroName {
			distro = true
		}
		for _, sp := range res.Spans {
			kinds[sp.Kind]++
			if sp.Kind == otlp.KindServer {
				sawServer = true
				if sp.SpanID != r.SpanID {
					t.Errorf("SERVER span id %q != ledger SpanID %q (never mint)", sp.SpanID, r.SpanID)
				}
				if sp.TraceID != r.TraceID {
					t.Errorf("SERVER trace %q != ledger TraceID", sp.TraceID)
				}
			}
			if sp.Kind == otlp.KindInternal {
				internalNames[sp.Name] = true
			}
		}
	}
	if !distro {
		t.Error("no resource carried the eBPF distro marker")
	}
	if !sawServer {
		t.Error("no SERVER boundary span")
	}
	if kinds[otlp.KindClient] != len(r.Calls) {
		t.Errorf("got %d CLIENT spans, want %d (one per hop)", kinds[otlp.KindClient], len(r.Calls))
	}
	for _, n := range []string{"in queue", "processing"} {
		if !internalNames[n] {
			t.Errorf("missing INTERNAL child span %q (got %v)", n, internalNames)
		}
	}
}

// TestBeylaTracesCoexistNil: coexist_sdk → projectBeylaTraces returns nil (SDK owns the trace).
func TestBeylaTracesCoexistNil(t *testing.T) {
	w := buildBeylaWS(t, "kubernetes", "coexist_sdk")
	if got := w.projectBeylaTraces(beylaReq()); got != nil {
		t.Fatalf("coexist_sdk projectBeylaTraces must be nil, got %d resources", len(got))
	}
}

var _ = core.Metrics // keep the core import if helpers shift
