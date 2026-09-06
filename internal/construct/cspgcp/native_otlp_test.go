// SPDX-License-Identifier: AGPL-3.0-only

package cspgcp_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/cspgcp"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type nativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *nativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func buildNative(t *testing.T, enabled bool) core.Construct {
	t.Helper()
	c, err := cspgcp.Build(&cspgcp.Config{
		Projects: 1,
		Company:  "native",
		OTel:     &cspgcp.OTelObs{Metrics: enabled},
	}, &fixture.Set{Seed: "native-test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

func tickNative(t *testing.T, c core.Construct) (*coretest.MetricCapture, *nativeMetricCapture) {
	return tickNativeAt(t, c, time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
}

func tickNativeAt(t *testing.T, c core.Construct, now time.Time) (*coretest.MetricCapture, *nativeMetricCapture) {
	t.Helper()
	legacy := &coretest.MetricCapture{}
	native := &nativeMetricCapture{}
	w := coretest.World(legacy, nil, nil)
	w.OTLPMetrics = native
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return legacy, native
}

func TestNativeOTLPMetricsDefaultOffPreservesPrometheusLane(t *testing.T) {
	legacyDefault, nativeDefault := tickNative(t, buildNative(t, false))
	legacyEnabled, nativeEnabled := tickNative(t, buildNative(t, true))
	if len(nativeDefault.resources) != 0 {
		t.Fatalf("native resources emitted while otel.metrics is false: %d", len(nativeDefault.resources))
	}
	if len(nativeEnabled.resources) == 0 {
		t.Fatal("native resources missing while otel.metrics is true")
	}
	got := serializedSeries(legacyEnabled.All())
	want := serializedSeries(legacyDefault.All())
	if len(got) != len(want) {
		t.Fatalf("default-off Prometheus sample count changed: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("legacy Prometheus lane changed at first serialized difference %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func serializedSeries(series []promrw.Series) []string {
	out := make([]string, 0, len(series))
	for _, s := range series {
		keys := make([]string, 0, len(s.Labels))
		for key := range s.Labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var b strings.Builder
		fmt.Fprintf(&b, "%s|%.17g|%d|%s|", s.Name, s.Value, s.Kind, s.T.UTC().Format(time.RFC3339Nano))
		for _, key := range keys {
			fmt.Fprintf(&b, "%s=%s;", key, s.Labels[key])
		}
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out
}

func TestNativeOTLPMetricsSignalsAreOptIn(t *testing.T) {
	if got := buildNative(t, false).Signals(); len(got) != 2 || got[0] != core.Metrics || got[1] != core.Logs {
		t.Fatalf("native disabled Signals()=%v, want metrics+logs", got)
	}
	got := buildNative(t, true).Signals()
	if len(got) != 3 || got[0] != core.Metrics || got[1] != core.Logs || got[2] != core.OTLPMetrics {
		t.Fatalf("native enabled Signals()=%v, want metrics+logs+otlp metrics", got)
	}
}

func TestNativeOTLPMetricsUsesReceiverNamesAndKinds(t *testing.T) {
	_, native := tickNative(t, buildNative(t, true))
	if len(native.resources) == 0 {
		t.Fatal("otel.metrics=true emitted no native resources")
	}
	wantFamilies := map[string]bool{
		"compute.googleapis.com/instance/cpu/utilization":              true,
		"cloudsql.googleapis.com/database/up":                          true,
		"alloydb.googleapis.com/instance/postgres/instances":           true,
		"storage.googleapis.com/storage/object_count":                  true,
		"networking.googleapis.com/google_service/request_bytes_count": true,
		"loadbalancing.googleapis.com/https/request_count":             true,
		"pubsub.googleapis.com/subscription/push_request_count":        true,
		"run.googleapis.com/container/containers":                      true,
		"bigtable.googleapis.com/cluster/node_count":                   true,
	}
	found := map[string]bool{}
	kinds := map[string]otlp.MetricKind{}
	for _, resource := range native.resources {
		if resource.Scope.Name != "" || resource.Scope.Version != "" || !resource.PreserveEmptyScope {
			t.Errorf("native resource scope=%#v preserve=%v, want an empty receiver scope", resource.Scope, resource.PreserveEmptyScope)
		}
		if resource.Attrs["gcp.resource_type"] == nil || resource.Attrs["project_id"] == nil {
			t.Errorf("native resource attrs=%v missing gcp.resource_type/project_id", resource.Attrs)
		}
		for _, metric := range resource.Metrics {
			kinds[metric.Name] = metric.Kind
			if _, ok := wantFamilies[metric.Name]; ok {
				found[metric.Name] = true
			}
			if metric.Description == "" {
				t.Errorf("native metric %q has no vendor description", metric.Name)
			}
		}
	}
	for name := range wantFamilies {
		if !found[name] {
			t.Errorf("native family %q missing", name)
		}
	}
	if got := kinds["compute.googleapis.com/instance/cpu/utilization"]; got != otlp.MetricGauge {
		t.Fatalf("CPU utilization kind=%v, want Gauge", got)
	}
	if got := kinds["compute.googleapis.com/instance/cpu/usage_time"]; got != otlp.MetricSum {
		t.Fatalf("CPU usage kind=%v, want Sum", got)
	}
	if got := kinds["loadbalancing.googleapis.com/https/total_latencies"]; got != otlp.MetricHistogram {
		t.Fatalf("load-balancer latency kind=%v, want Histogram", got)
	}
	if got := kinds["networking.googleapis.com/fixed_standard_tier/usage"]; got != otlp.MetricGauge {
		t.Fatalf("fixed-tier usage kind=%v, want Gauge", got)
	}
	if _, ok := kinds["run.googleapis.com/container/cpu/usage"]; ok {
		t.Fatal("receiver-dropped GAUGE distribution run/container/cpu/usage was emitted")
	}
	if _, ok := kinds["run.googleapis.com/container/memory/usage"]; ok {
		t.Fatal("receiver-dropped GAUGE distribution run/container/memory/usage was emitted")
	}
}

func nativeFixtureWithPostgres(t *testing.T) *fixture.Set {
	t.Helper()
	cloud := &fixture.Cloud{Provider: "gcp", AccountID: "native-contract-01", Region: "europe-west1"}
	return &fixture.Set{
		Seed: "native-contract",
		DBs: []*fixture.DB{
			{Engine: "mysql", Name: "mysql-native", Cloud: cloud},
			{Engine: "postgres", Name: "postgres-native-01", Cloud: cloud},
			{Engine: "postgres", Name: "postgres-native-02", Cloud: cloud},
		},
	}
}

func documentedNativeFamilies(t *testing.T) map[string]bool {
	t.Helper()
	var content []byte
	var err error
	for _, path := range []string{"signals/cspgcp.md", "../../../signals/cspgcp.md"} {
		content, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("read signals/cspgcp.md: %v", err)
	}
	const begin = "<!-- cspgcp-otlp-contract:begin -->"
	const end = "<!-- cspgcp-otlp-contract:end -->"
	text := string(content)
	start := strings.Index(text, begin)
	stop := strings.Index(text, end)
	if start < 0 || stop < start {
		t.Fatalf("signals/cspgcp.md missing delimited OTLP contract table")
	}
	families := make(map[string]bool)
	for _, line := range strings.Split(text[start+len(begin):stop], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		columns := strings.Split(line, "|")
		if len(columns) < 2 {
			continue
		}
		name := strings.TrimSpace(columns[1])
		name = strings.Trim(name, "`")
		if name != "" {
			families[name] = true
		}
	}
	return families
}

func TestNativeOTLPMetricsEmittedFamiliesMatchSignalsContract(t *testing.T) {
	cfg := &cspgcp.Config{
		Projects: 1,
		Company:  "native-contract",
		OTel:     &cspgcp.OTelObs{Metrics: true},
	}
	c, err := cspgcp.Build(cfg, nativeFixtureWithPostgres(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, native := tickNativeAt(t, c, time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC))
	want := documentedNativeFamilies(t)
	got := make(map[string]bool)
	for _, resource := range native.resources {
		for _, metric := range resource.Metrics {
			got[metric.Name] = true
		}
	}
	if len(want) != 115 {
		t.Fatalf("signals contract has %d families, want 115", len(want))
	}
	for name := range want {
		if !got[name] {
			t.Errorf("documented native family %q was not emitted", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("emitted native family %q is absent from signals contract", name)
		}
	}
}

func findNativeNumber(resources []otlp.MetricResource, name string) (otlp.MetricResource, otlp.NumberPoint, bool) {
	for _, resource := range resources {
		for _, metric := range resource.Metrics {
			if metric.Name != name {
				continue
			}
			if len(metric.Numbers) > 0 {
				return resource, metric.Numbers[0], true
			}
		}
	}
	return otlp.MetricResource{}, otlp.NumberPoint{}, false
}

func findNativeHistogram(resources []otlp.MetricResource, name string) (otlp.MetricResource, otlp.HistogramPoint, bool) {
	for _, resource := range resources {
		for _, metric := range resource.Metrics {
			if metric.Name != name {
				continue
			}
			if len(metric.Histograms) > 0 {
				return resource, metric.Histograms[0], true
			}
		}
	}
	return otlp.MetricResource{}, otlp.HistogramPoint{}, false
}

func nativeLabels(resource otlp.MetricResource, pointAttrs map[string]any) map[string]string {
	labels := make(map[string]string, len(resource.Attrs)+len(pointAttrs))
	for key, value := range resource.Attrs {
		if key == "gcp.resource_type" {
			continue
		}
		labels[key] = fmt.Sprint(value)
	}
	for key, value := range pointAttrs {
		labels[key] = fmt.Sprint(value)
	}
	return labels
}

func findLegacySeries(capture *coretest.MetricCapture, name string, labels map[string]string) (promrw.Series, bool) {
	for _, series := range capture.Find(name) {
		match := true
		for key, value := range labels {
			if series.Labels[key] != value {
				match = false
				break
			}
		}
		if match {
			return series, true
		}
	}
	return promrw.Series{}, false
}

func TestNativeOTLPMetricsDeltaIntervals(t *testing.T) {
	c := buildNative(t, true)
	t0 := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	legacy1, native1 := tickNativeAt(t, c, t0)
	legacy2, native2 := tickNativeAt(t, c, t0.Add(60*time.Second))

	const nativeScalar = "compute.googleapis.com/instance/cpu/usage_time"
	resource1, scalar1, ok := findNativeNumber(native1.resources, nativeScalar)
	if !ok {
		t.Fatalf("native scalar %q missing at first tick", nativeScalar)
	}
	resource2, scalar2, ok := findNativeNumber(native2.resources, nativeScalar)
	if !ok {
		t.Fatalf("native scalar %q missing at second tick", nativeScalar)
	}
	if !scalar1.Start.Equal(t0.Add(-60*time.Second)) || !scalar2.Start.Equal(t0) {
		t.Fatalf("native scalar starts = %s, %s; want %s, %s", scalar1.Start, scalar2.Start, t0.Add(-60*time.Second), t0)
	}
	if !scalar1.Start.Before(scalar1.Time) || !scalar2.Start.Before(scalar2.Time) {
		t.Fatal("native scalar delta point does not have Start before Time")
	}
	legacyName := "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_usage_time"
	legacyScalar1, ok := findLegacySeries(legacy1, legacyName, nativeLabels(resource1, scalar1.Attrs))
	if !ok {
		t.Fatal("legacy scalar series for first native point missing")
	}
	legacyScalar2, ok := findLegacySeries(legacy2, legacyName, nativeLabels(resource2, scalar2.Attrs))
	if !ok {
		t.Fatal("legacy scalar series for second native point missing")
	}
	if scalar1.Value != legacyScalar1.Value || scalar2.Value != legacyScalar2.Value-legacyScalar1.Value {
		t.Fatalf("native scalar values = %v, %v; want interval values %v, %v", scalar1.Value, scalar2.Value, legacyScalar1.Value, legacyScalar2.Value-legacyScalar1.Value)
	}

	const nativeHistogram = "pubsub.googleapis.com/subscription/push_request_latencies"
	hResource1, hist1, ok := findNativeHistogram(native1.resources, nativeHistogram)
	if !ok {
		t.Fatalf("native histogram %q missing at first tick", nativeHistogram)
	}
	hResource2, hist2, ok := findNativeHistogram(native2.resources, nativeHistogram)
	if !ok {
		t.Fatalf("native histogram %q missing at second tick", nativeHistogram)
	}
	if !hist1.Start.Equal(t0.Add(-60*time.Second)) || !hist2.Start.Equal(t0) {
		t.Fatalf("native histogram starts = %s, %s; want %s, %s", hist1.Start, hist2.Start, t0.Add(-60*time.Second), t0)
	}
	if !hist1.Start.Before(hist1.Time) || !hist2.Start.Before(hist2.Time) {
		t.Fatal("native histogram delta point does not have Start before Time")
	}
	legacyHistName := "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies_count"
	legacyHist1, ok := findLegacySeries(legacy1, legacyHistName, nativeLabels(hResource1, hist1.Attrs))
	if !ok {
		t.Fatal("legacy histogram count for first native point missing")
	}
	legacyHist2, ok := findLegacySeries(legacy2, legacyHistName, nativeLabels(hResource2, hist2.Attrs))
	if !ok {
		t.Fatal("legacy histogram count for second native point missing")
	}
	if hist1.Count != uint64(legacyHist1.Value) || hist2.Count != uint64(legacyHist2.Value-legacyHist1.Value) {
		t.Fatalf("native histogram counts = %d, %d; want interval counts %v, %v", hist1.Count, hist2.Count, legacyHist1.Value, legacyHist2.Value-legacyHist1.Value)
	}
}

func TestNativeOTLPMetricsPreserveVendorUnits(t *testing.T) {
	c := buildNative(t, true)
	t0 := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	legacy, native := tickNativeAt(t, c, t0)

	const waitName = "alloydb.googleapis.com/node/postgres/wait_time"
	waitResource, waitPoint, ok := findNativeNumber(native.resources, waitName)
	if !ok {
		t.Fatalf("native scalar %q missing", waitName)
	}
	const waitLegacyName = "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_time"
	waitLegacy, ok := findLegacySeries(legacy, waitLegacyName, nativeLabels(waitResource, waitPoint.Attrs))
	if !ok {
		t.Fatalf("legacy scalar series for %q missing", waitName)
	}
	if waitPoint.Value != waitLegacy.Value*1000 {
		t.Fatalf("native %q value=%v, want legacy milliseconds %v scaled to microseconds", waitName, waitPoint.Value, waitLegacy.Value*1000)
	}

	const latencyName = "pubsub.googleapis.com/subscription/push_request_latencies"
	latencyResource, latencyPoint, ok := findNativeHistogram(native.resources, latencyName)
	if !ok {
		t.Fatalf("native histogram %q missing", latencyName)
	}
	const latencyLegacyName = "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies_sum"
	latencyLegacy, ok := findLegacySeries(legacy, latencyLegacyName, nativeLabels(latencyResource, latencyPoint.Attrs))
	if !ok {
		t.Fatalf("legacy histogram sum for %q missing", latencyName)
	}
	if latencyPoint.Sum != latencyLegacy.Value*1000 {
		t.Fatalf("native %q sum=%v, want legacy milliseconds %v scaled to microseconds", latencyName, latencyPoint.Sum, latencyLegacy.Value*1000)
	}
	wantBounds := cspgcp.ExpBucketsForTest(1, 1.4, 66)
	if len(latencyPoint.Bounds) != len(wantBounds) {
		t.Fatalf("native %q has %d bounds, want %d", latencyName, len(latencyPoint.Bounds), len(wantBounds))
	}
	for i := range wantBounds {
		if latencyPoint.Bounds[i] != wantBounds[i]*1000 {
			t.Fatalf("native %q bound[%d]=%v, want %v", latencyName, i, latencyPoint.Bounds[i], wantBounds[i]*1000)
		}
	}
	latencyCount, ok := findLegacySeries(legacy,
		"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies_count",
		nativeLabels(latencyResource, latencyPoint.Attrs))
	if !ok || latencyPoint.Count != uint64(latencyCount.Value) {
		t.Fatalf("native %q count=%d, want legacy count %.0f", latencyName, latencyPoint.Count, latencyCount.Value)
	}
}

func TestNativeOTLPGaugesMarkedFromLegacyCountersRemainGauges(t *testing.T) {
	c := buildNative(t, true)
	t0 := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	legacy1, native1 := tickNativeAt(t, c, t0)
	legacy2, native2 := tickNativeAt(t, c, t0.Add(60*time.Second))

	cases := []struct {
		nativeName string
		legacyName string
	}{
		{
			nativeName: "networking.googleapis.com/fixed_standard_tier/usage",
			legacyName: "stackdriver_networking_googleapis_com_location_networking_googleapis_com_fixed_standard_tier_usage",
		},
		{
			nativeName: "alloydb.googleapis.com/node/postgres/uptime",
			legacyName: "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_uptime",
		},
	}
	for _, tc := range cases {
		resource1, point1, ok := findNativeNumber(native1.resources, tc.nativeName)
		if !ok {
			t.Fatalf("native gauge %q missing at first tick", tc.nativeName)
		}
		resource2, point2, ok := findNativeNumber(native2.resources, tc.nativeName)
		if !ok {
			t.Fatalf("native gauge %q missing at second tick", tc.nativeName)
		}
		legacyPoint1, ok := findLegacySeries(legacy1, tc.legacyName, nativeLabels(resource1, point1.Attrs))
		if !ok {
			t.Fatalf("legacy gauge source %q missing at first tick", tc.legacyName)
		}
		legacyPoint2, ok := findLegacySeries(legacy2, tc.legacyName, nativeLabels(resource2, point2.Attrs))
		if !ok {
			t.Fatalf("legacy gauge source %q missing at second tick", tc.legacyName)
		}
		if !point1.Start.IsZero() || !point2.Start.IsZero() {
			t.Fatalf("native gauge %q carries a delta start time: %s, %s", tc.nativeName, point1.Start, point2.Start)
		}
		if point1.Value != legacyPoint1.Value || point2.Value != legacyPoint2.Value-legacyPoint1.Value {
			t.Fatalf("native gauge %q values = %v, %v; want legacy interval values %v, %v", tc.nativeName, point1.Value, point2.Value, legacyPoint1.Value, legacyPoint2.Value-legacyPoint1.Value)
		}
	}
}

func TestNativeOTLPMetricsPreservesDeclaredEnvironmentIdentity(t *testing.T) {
	identities := map[string]bool{}
	for _, environment := range []string{"", "production", "staging"} {
		fx := &fixture.Set{Seed: "native-environment-identity"}
		if environment != "" {
			fx.Env = &fixture.Env{Name: environment, Weight: 1}
		}
		c, err := cspgcp.Build(&cspgcp.Config{
			Projects:   1,
			Company:    "native",
			SubSignals: []string{"compute"},
			OTel:       &cspgcp.OTelObs{Metrics: true},
		}, fx)
		if err != nil {
			t.Fatal(err)
		}
		_, native := tickNative(t, c)
		if len(native.resources) == 0 {
			t.Fatal("missing native resources")
		}
		for _, resource := range native.resources {
			got, present := resource.Attrs["env"]
			if environment == "" && present {
				t.Fatalf("aggregate resource carries env=%v", got)
			}
			if environment != "" && got != environment {
				t.Fatalf("declared environment %q became resource env=%v", environment, got)
			}
			for _, metric := range resource.Metrics {
				for _, point := range metric.Numbers {
					if _, present := point.Attrs["env"]; present {
						t.Fatal("environment leaked into descriptor metric labels")
					}
				}
			}
			// The same modeled projects/instances in different declared environments
			// must not become the same receiver resource.
			key := fmt.Sprintf("%v", resource.Attrs)
			if identities[key] {
				t.Fatalf("duplicate native resource identity across environments: %s", key)
			}
			identities[key] = true
		}
	}
}
