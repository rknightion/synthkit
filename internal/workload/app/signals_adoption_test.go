// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/pyroscope"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestAppTraceAdoptionPassesNearestExportedParent(t *testing.T) {
	cfg := traceAdoptionConfig()
	cfg.Services[1].Signals = &NodeSignals{Traces: ptr(false)}
	w := buildApp(t, cfg)
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	logs, traces := &coretest.LogCapture{}, &coretest.TraceCapture{}
	world := coretest.World(nil, logs, traces)
	r := w.m.mintOne(now, world.Shape)
	seedCorrelation(r, "trace-adoption")
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	if got := spansForService(traces, "svc-b"); len(got) != 0 {
		t.Fatalf("untraced service emitted spans: %+v", got)
	}
	aClient := findSpan(t, spansForService(traces, "svc-a"), "call svc-b")
	cServer := findSpan(t, spansForService(traces, "svc-c"), "svc-c")
	if cServer.TraceID != r.TraceID || aClient.TraceID != r.TraceID {
		t.Fatalf("trace IDs do not pass through the untraced node: client=%s server=%s request=%s", aClient.TraceID, cServer.TraceID, r.TraceID)
	}
	if cServer.ParentID != aClient.SpanID {
		t.Fatalf("callee parent = %q, want nearest exported CLIENT %q", cServer.ParentID, aClient.SpanID)
	}

	foundBLog := false
	for _, stream := range logs.Streams {
		if stream.Labels["service_name"] != "svc-b" {
			continue
		}
		foundBLog = true
		for _, line := range stream.Lines {
			if line.Meta["trace_id"] != r.TraceID || line.Meta["correlation_id"] == "" {
				t.Errorf("untraced service lost propagated correlation metadata: %+v", line.Meta)
			}
			if _, exists := line.Meta["span_id"]; exists {
				t.Errorf("untraced service log points at a non-emitted local span: %+v", line.Meta)
			}
		}
	}
	if !foundBLog {
		t.Fatal("logs-enabled untraced service lost its log stream")
	}
}

func TestAppTraceOffEntryLeavesTracedCalleeAsRoot(t *testing.T) {
	w := buildApp(t, &Config{Traffic: Traffic{OffPeakRPS: 1, PeakRPS: 1}, Services: []ServiceNode{
		{Name: "entry", Type: "web", Entry: true, Calls: []string{"callee"}, Signals: &NodeSignals{Traces: ptr(false)}},
		{Name: "callee", Type: "job"},
	}})
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	traces := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, traces)
	r := w.m.mintOne(now, world.Shape)
	seedCorrelation(r, "untraced-entry")
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	if got := spansForService(traces, "entry"); len(got) != 0 {
		t.Fatalf("untraced entry emitted spans: %+v", got)
	}
	callee := findSpan(t, spansForService(traces, "callee"), "callee")
	if callee.ParentID != "" || callee.TraceID != r.TraceID {
		t.Fatalf("callee root = trace %q parent %q, want request trace %q with empty parent", callee.TraceID, callee.ParentID, r.TraceID)
	}
}

func TestAppTraceOffLeafKeepsCallerClientSpan(t *testing.T) {
	w := buildApp(t, &Config{Services: []ServiceNode{
		{Name: "caller", Type: "web", Entry: true, Calls: []string{"store"}},
		{Name: "store", Type: "db", Signals: &NodeSignals{Traces: ptr(false)}},
	}})
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	traces := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, traces)
	r := w.m.mintOne(now, world.Shape)
	seedCorrelation(r, "untraced-leaf")
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	client := findSpan(t, spansForService(traces, "caller"), "call store")
	if client.Kind != otlp.KindClient || client.TraceID != r.TraceID {
		t.Fatalf("caller CLIENT span = %+v", client)
	}
	if got := spansForService(traces, "store"); len(got) != 0 {
		t.Fatalf("DB leaf emitted its own spans: %+v", got)
	}
}

func TestAppLogSwitchFiltersProfileAndInlineStreams(t *testing.T) {
	cfg := traceAdoptionConfig()
	cfg.Services[1].Profiles = []string{"gateway_export_log"}
	cfg.Services[1].Signals = &NodeSignals{Logs: ptr(false)}
	w := buildApp(t, cfg)
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	logs := &coretest.LogCapture{}
	world := coretest.World(nil, logs, &coretest.TraceCapture{})
	r := w.m.mintOne(now, world.Shape)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	services := map[string]bool{}
	for _, stream := range logs.Streams {
		services[stream.Labels["service_name"]] = true
		if stream.Labels["service_name"] != "svc-b" {
			for _, line := range stream.Lines {
				if line.Meta["trace_id"] != r.TraceID {
					t.Errorf("enabled service %s log trace_id = %q, want %q", stream.Labels["service_name"], line.Meta["trace_id"], r.TraceID)
				}
			}
		}
	}
	if services["svc-b"] {
		t.Fatalf("logs-off service emitted inline or profile streams: %v", services)
	}
	for _, want := range []string{"svc-a", "svc-c"} {
		if !services[want] {
			t.Errorf("enabled service %q has no app log stream: %v", want, services)
		}
	}
}

func TestAppMetricsSwitchGatesAppAndNativeMetricsOnly(t *testing.T) {
	resident := 123.0
	cpu := 4.0
	duration := 0.25
	cfg := &Config{
		OTel:    &OTelObs{Metrics: true},
		Traffic: Traffic{OffPeakRPS: 1, PeakRPS: 1},
		Services: []ServiceNode{
			{Name: "svc-a", Type: "web", Entry: true, Calls: []string{"svc-b"}, Metrics: []telemetryspec.MetricSpec{{Name: "process_resident_memory_bytes", Instrument: telemetryspec.InstrumentGauge, Value: telemetryspec.ValueModel{Const: &resident}}}},
			{Name: "svc-b", Type: "web", Calls: []string{"svc-c"}, Profiles: []string{"runtime_go"}, Signals: &NodeSignals{Metrics: ptr(false)}, Metrics: []telemetryspec.MetricSpec{{Name: "http_server_request_duration_seconds", Instrument: telemetryspec.InstrumentHistogram, Value: telemetryspec.ValueModel{Const: &duration}, Buckets: []float64{0.1, 0.5}}}},
			{Name: "svc-c", Type: "web", Metrics: []telemetryspec.MetricSpec{{Name: "process_cpu_seconds_total", Instrument: telemetryspec.InstrumentCounter, Value: telemetryspec.ValueModel{Const: &cpu}}}},
		},
	}
	w := buildApp(t, cfg)
	metrics, native := &coretest.MetricCapture{}, &appNativeMetricCapture{}
	world := appNativeWorld(metrics, native)
	world.EmitSpanMetrics = true
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, metric := range []string{"go_goroutines", "http_server_request_duration_seconds", "target_info"} {
		if hasMetricForService(metrics, metric, "svc-b") {
			t.Errorf("metrics-off node emitted %s", metric)
		}
	}
	if !hasMetricForService(metrics, "traces_target_info", "svc-b") {
		t.Error("traces_target_info disappeared while service spans remain enabled")
	}
	if !hasMetricForService(metrics, "traces_spanmetrics_calls_total", "svc-b") {
		t.Error("trace-derived rows disappeared while service spans remain enabled")
	}
	if !hasMetricForService(metrics, "process_resident_memory_bytes", "svc-a") || !hasMetricForService(metrics, "process_cpu_seconds_total", "svc-c") {
		t.Error("enabled app metrics from neighboring services were suppressed")
	}
	if hasMetricForService(metrics, "go_goroutines", "svc-b") {
		t.Error("metrics-off node emitted runtime_go profile metrics")
	}
	for _, resource := range native.resources {
		if resource.Attrs["service.name"] == "svc-b" {
			t.Fatalf("metrics-off node created native OTLP resource: %+v", resource)
		}
	}
	if !nativeHasResource(native, "svc-a") || !nativeHasResource(native, "svc-c") {
		t.Fatalf("enabled neighbors missing native resources: %+v", native.resources)
	}
}

func TestTracesTargetInfoRequiresAnExportedNodeSpanWhenSignalsDiffer(t *testing.T) {
	w := buildApp(t, &Config{Services: []ServiceNode{
		{Name: "entry", Type: "web", Entry: true, Calls: []string{"store", "callee"}, Signals: &NodeSignals{Traces: ptr(false)}},
		{Name: "store", Type: "db"},
		{Name: "callee", Type: "job"},
		{Name: "disconnected", Type: "web"},
	}})
	metrics := &coretest.MetricCapture{}
	world := coretest.World(metrics, nil, nil)
	if err := w.Tick(context.Background(), time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	for _, service := range []string{"entry", "store", "disconnected"} {
		if hasMetricForService(metrics, "traces_target_info", service) {
			t.Errorf("service %q has traces_target_info without exporting a span", service)
		}
	}
	if !hasMetricForService(metrics, "traces_target_info", "callee") {
		t.Fatal("reachable traced callee lost traces_target_info")
	}
}

func TestSignalsDoesNotDeclareMetricsForUnspannedTraceInfoNodes(t *testing.T) {
	w := buildApp(t, &Config{Services: []ServiceNode{
		{Name: "entry", Type: "web", Entry: true, Calls: []string{"store"}, Signals: &NodeSignals{Traces: ptr(false), Metrics: ptr(false)}},
		{Name: "store", Type: "db", Signals: &NodeSignals{Metrics: ptr(false)}},
		{Name: "disconnected", Type: "web", Signals: &NodeSignals{Metrics: ptr(false)}},
	}})
	if got := w.Signals(); slices.Contains(got, core.Metrics) {
		t.Fatalf("Signals() = %v; no emitted app metrics or spans require a metric writer", got)
	}
}

func TestAppDisabledMetricsDoNotShiftNeighborValues(t *testing.T) {
	newConfig := func(disableB bool) *Config {
		var signals *NodeSignals
		if disableB {
			signals = &NodeSignals{Metrics: ptr(false)}
		}
		return &Config{Services: []ServiceNode{
			{Name: "svc-a", Type: "web", Entry: true, Calls: []string{"svc-b"}, Metrics: []telemetryspec.MetricSpec{{Name: "process_resident_memory_bytes", Instrument: telemetryspec.InstrumentGauge, Value: telemetryspec.ValueModel{FloatRange: &telemetryspec.FloatRange{Min: 1, Max: 100}}}}},
			{Name: "svc-b", Type: "web", Calls: []string{"svc-c"}, Profiles: []string{"runtime_go"}, Signals: signals},
			{Name: "svc-c", Type: "web", Metrics: []telemetryspec.MetricSpec{{Name: "process_cpu_seconds_total", Instrument: telemetryspec.InstrumentCounter, Value: telemetryspec.ValueModel{FloatRange: &telemetryspec.FloatRange{Min: 1, Max: 100}}}}},
		}}
	}
	collect := func(disableB bool) map[string]float64 {
		t.Helper()
		metrics := &coretest.MetricCapture{}
		world := coretest.World(metrics, nil, nil)
		world.Shape = shape.New("", nil)
		if err := buildApp(t, newConfig(disableB)).Tick(context.Background(), time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		values := map[string]float64{}
		for _, series := range metrics.All() {
			if series.Labels["service"] == "svc-a" || series.Labels["service"] == "svc-c" {
				values[series.Labels["service"]+"/"+series.Name+"/"+metricLabelSignature(series.Labels)] = series.Value
			}
		}
		return values
	}
	baseline := collect(false)
	disabled := collect(true)
	if len(disabled) != len(baseline) {
		t.Fatalf("neighbor series count with B metrics disabled = %d, want %d", len(disabled), len(baseline))
	}
	for key, want := range baseline {
		if got, ok := disabled[key]; !ok || got != want {
			t.Errorf("neighbor sample %s = %v with B metrics disabled, want unchanged %v", key, got, want)
		}
	}
}

func TestAppAllMetricsOffLeavesOTLPOptionInert(t *testing.T) {
	value := 5.0
	cfg := &Config{
		OTel: &OTelObs{Metrics: true},
		Services: []ServiceNode{{
			Name: "svc-a", Type: "web", Entry: true,
			Signals: &NodeSignals{Traces: ptr(false), Logs: ptr(false), Metrics: ptr(false)},
			Metrics: []telemetryspec.MetricSpec{{Name: "process_resident_memory_bytes", Instrument: telemetryspec.InstrumentGauge, Value: telemetryspec.ValueModel{Const: &value}}},
		}},
	}
	w := buildApp(t, cfg)
	native := &appNativeMetricCapture{}
	world := appNativeWorld(nil, native)
	if err := w.Tick(context.Background(), time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(native.resources) != 0 {
		t.Fatalf("all metrics-off app emitted native resources: %+v", native.resources)
	}
}

func TestAppSignalsMatchEnabledProducers(t *testing.T) {
	logSpec := adoptionLogSpec()
	value := 1.0
	metric := []telemetryspec.MetricSpec{{Name: "process_resident_memory_bytes", Instrument: telemetryspec.InstrumentGauge, Value: telemetryspec.ValueModel{Const: &value}}}
	profile := &pyroscope.ProfilingCfg{Enabled: true, Runtime: "go", SpanProfiles: true, Types: []string{"process_cpu"}}
	cases := []struct {
		name string
		cfg  *Config
		want []core.SignalClass
	}{
		{
			name: "defaults keep current class order",
			cfg:  &Config{Services: []ServiceNode{{Name: "api", Type: "web", Entry: true, Logs: []telemetryspec.LogSpec{logSpec}}}},
			want: []core.SignalClass{core.Metrics, core.Traces, core.Logs},
		},
		{
			name: "all signals off",
			cfg: &Config{
				OTel: &OTelObs{Metrics: true},
				Services: []ServiceNode{{Name: "api", Type: "web", Entry: true,
					Signals: &NodeSignals{Traces: ptr(false), Logs: ptr(false), Metrics: ptr(false)}, Metrics: metric}},
			},
			want: nil,
		},
		{
			name: "traces retain their metric producer",
			cfg: &Config{Services: []ServiceNode{{Name: "api", Type: "web", Entry: true,
				Signals: &NodeSignals{Traces: ptr(true), Logs: ptr(false), Metrics: ptr(false)}}}},
			want: []core.SignalClass{core.Metrics, core.Traces},
		},
		{
			name: "logs only",
			cfg: &Config{Services: []ServiceNode{{Name: "api", Type: "web", Entry: true,
				Signals: &NodeSignals{Traces: ptr(false), Metrics: ptr(false)}, Logs: []telemetryspec.LogSpec{logSpec}}}},
			want: []core.SignalClass{core.Logs},
		},
		{
			name: "profiles only",
			cfg: &Config{Services: []ServiceNode{{Name: "api", Type: "web", Entry: true,
				Signals: &NodeSignals{Traces: ptr(false), Logs: ptr(false), Metrics: ptr(false)}, Pyroscope: profile}}},
			want: []core.SignalClass{core.PyroscopeProfiles},
		},
		{
			name: "native inline candidate",
			cfg: &Config{
				OTel: &OTelObs{Metrics: true},
				Services: []ServiceNode{{Name: "api", Type: "web", Entry: true,
					Signals: &NodeSignals{Traces: ptr(false), Logs: ptr(false)}, Metrics: metric}},
			},
			want: []core.SignalClass{core.Metrics, core.OTLPMetrics},
		},
		{
			name: "enabled DB leaf has no own trace class",
			cfg:  &Config{Services: []ServiceNode{{Name: "store", Type: "db", Entry: true}}},
			want: []core.SignalClass{core.Metrics},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildApp(t, tc.cfg).Signals()
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Signals() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAppSignalLoadTimeContradictions(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "rum entry requires traces",
			cfg:  &Config{Services: []ServiceNode{{Name: "browser", Type: "frontend", Entry: true, Profiles: []string{"rum_faro"}, Signals: &NodeSignals{Traces: ptr(false)}}}},
			want: `app: entry service "browser": rum_faro requires signals.traces enabled`,
		},
		{
			name: "agent flow requires traces",
			cfg:  &Config{Services: []ServiceNode{{Name: "worker", Type: "web", Entry: true, AgenticFlow: &AgenticFlow{Workflow: "assistant", Agents: []AgentDecl{{Name: "assistant"}}}, Signals: &NodeSignals{Traces: ptr(false)}}}},
			want: `app: service "worker": agentic_flow requires signals.traces enabled`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(tc.cfg, core.Binding{Name: "signal-validation"})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("build error = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := build(&Config{Services: []ServiceNode{{Name: "external", Type: "web", Entry: true, External: true, Signals: &NodeSignals{Traces: ptr(false), Logs: ptr(false), Metrics: ptr(false)}}}}, core.Binding{Name: "signal-validation"}); err != nil {
		t.Fatalf("external node with all signals disabled should be allowed: %v", err)
	}
	if _, err := build(&Config{Services: []ServiceNode{{Name: "profiled", Type: "web", Entry: true, Signals: &NodeSignals{Traces: ptr(false)}, Pyroscope: &pyroscope.ProfilingCfg{Enabled: true, SpanProfiles: true}}}}, core.Binding{Name: "signal-validation"}); err != nil {
		t.Fatalf("span profiling without trace links should be allowed: %v", err)
	}
}

func TestAppProfilesContinueWithoutMetricWritersAndDropUntracedLinks(t *testing.T) {
	w := buildApp(t, &Config{Services: []ServiceNode{{
		Name: "profiled", Type: "web", Entry: true,
		Signals:   &NodeSignals{Traces: ptr(false), Logs: ptr(false), Metrics: ptr(false)},
		Pyroscope: &pyroscope.ProfilingCfg{Enabled: true, Runtime: "go", SpanProfiles: true, Types: []string{"process_cpu"}},
	}}})
	engine := shape.New("", nil)
	led := ledger.New(engine, 0, 0)
	led.AddMinter(w.Minter())
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	led.Mint(now)
	profiles := &pyroCapture{}
	world := &core.World{Shape: shape.New("", nil), Ledger: led, Pyroscope: profiles}
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	series := profiles.all()
	if len(series) == 0 {
		t.Fatal("profiles-only workload did not tick without metric writers")
	}
	for _, s := range series {
		if ids := spanIDsInProfile(s.Profile); len(ids) != 0 {
			t.Errorf("untraced CPU profile carries span links: %v", ids)
		}
	}
	if got := w.Signals(); !slices.Equal(got, []core.SignalClass{core.PyroscopeProfiles}) {
		t.Fatalf("profiles-only Signals() = %v", got)
	}
}

func TestAppSpanMetricsFollowExportedSpansAndCollapsedEdges(t *testing.T) {
	cfg := traceAdoptionConfig()
	cfg.Services[1].Signals = &NodeSignals{Traces: ptr(false)}
	w := buildApp(t, cfg)
	metrics := &coretest.MetricCapture{}
	world := coretest.World(metrics, nil, nil)
	world.EmitSpanMetrics = true
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if hasMetricForService(metrics, "traces_spanmetrics_calls_total", "svc-b") {
		t.Fatal("untraced node contributed span-derived rows")
	}
	if !hasMetricForService(metrics, "traces_spanmetrics_calls_total", "svc-c") {
		t.Fatal("traced downstream node lost its span-derived rows")
	}
	for _, series := range metrics.Find("traces_service_graph_request_total") {
		client, server := series.Labels["client"], series.Labels["server"]
		if client == "svc-a" && server == "svc-b" || client == "svc-b" && server == "svc-c" {
			t.Fatalf("service graph contains an edge with an untraced endpoint: %v", series.Labels)
		}
	}
	if !hasServiceGraphEdge(metrics, "svc-a", "svc-c") {
		t.Fatal("service graph did not pair the nearest emitted CLIENT to the traced downstream SERVER")
	}
}

func TestAppUntracedEntryServiceGraphUsesVirtualUser(t *testing.T) {
	w := buildApp(t, &Config{Traffic: Traffic{OffPeakRPS: 1, PeakRPS: 1}, Services: []ServiceNode{
		{Name: "entry", Type: "web", Entry: true, Calls: []string{"callee"}, Signals: &NodeSignals{Traces: ptr(false)}},
		{Name: "callee", Type: "job"},
	}})
	metrics := &coretest.MetricCapture{}
	world := coretest.World(metrics, nil, nil)
	world.EmitSpanMetrics = true
	if err := w.Tick(context.Background(), time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !hasServiceGraphEdge(metrics, "user", "callee") {
		t.Fatal("root SERVER without an exported parent did not get the documented virtual user edge")
	}
	if !hasMetricForService(metrics, "traces_spanmetrics_calls_total", "callee") {
		t.Fatal("traced callee lost span metrics after an untraced entry")
	}
	for _, series := range metrics.Find("traces_service_graph_request_failed_total") {
		if series.Labels["client"] == "user" && series.Labels["server"] == "callee" {
			t.Fatalf("successful virtual edge emitted a failed counter: %+v", series)
		}
	}
	for _, side := range []string{"client", "server"} {
		countFound := false
		for _, series := range metrics.Find("traces_service_graph_request_" + side + "_seconds_count") {
			if series.Labels["client"] == "user" && series.Labels["server"] == "callee" && series.Value > 0 {
				countFound = true
			}
		}
		if !countFound {
			t.Errorf("virtual edge lacks %s latency histogram observations", side)
		}
	}
	for _, series := range metrics.Find("traces_service_graph_request_client_seconds_sum") {
		if series.Labels["client"] == "user" && series.Labels["server"] == "callee" && series.Value != 0 {
			t.Errorf("missing virtual client span has non-zero latency: %+v", series)
		}
	}
}

func traceAdoptionConfig() *Config {
	return &Config{
		Traffic: Traffic{OffPeakRPS: 1, PeakRPS: 1},
		Services: []ServiceNode{
			{Name: "svc-a", Type: "web", Entry: true, Calls: []string{"svc-b"}, Logs: []telemetryspec.LogSpec{adoptionLogSpec()}},
			{Name: "svc-b", Type: "web", Calls: []string{"svc-c"}, Logs: []telemetryspec.LogSpec{adoptionLogSpec()}},
			{Name: "svc-c", Type: "web", Logs: []telemetryspec.LogSpec{adoptionLogSpec()}},
		},
	}
}

func adoptionLogSpec() telemetryspec.LogSpec {
	return telemetryspec.LogSpec{Source: "app", Body: map[string]telemetryspec.ValueModel{
		"trace_id":       {Ref: "trace_id"},
		"span_id":        {Ref: "span_id"},
		"correlation_id": {Ref: "correlation_id"},
	}}
}

func spansForService(capture *coretest.TraceCapture, service string) []otlp.Span {
	var spans []otlp.Span
	for _, resource := range capture.Resources {
		if resource.Attrs["service.name"] == service {
			spans = append(spans, resource.Spans...)
		}
	}
	return spans
}

func findSpan(t *testing.T, spans []otlp.Span, name string) otlp.Span {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q not found in %+v", name, spans)
	return otlp.Span{}
}

func hasMetricForService(capture *coretest.MetricCapture, name, service string) bool {
	for _, series := range capture.Find(name) {
		if series.Labels["service"] == service || series.Labels["service_name"] == service {
			return true
		}
	}
	return false
}

func nativeHasResource(capture *appNativeMetricCapture, service string) bool {
	for _, resource := range capture.resources {
		if resource.Attrs["service.name"] == service && len(resource.Metrics) > 0 {
			return true
		}
	}
	return false
}

func hasServiceGraphEdge(capture *coretest.MetricCapture, client, server string) bool {
	for _, series := range capture.Find("traces_service_graph_request_total") {
		if series.Labels["client"] == client && series.Labels["server"] == server {
			return true
		}
	}
	return false
}
