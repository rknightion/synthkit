// SPDX-License-Identifier: AGPL-3.0-only

package cspazure

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

type nativeMetricCapture struct {
	resources []otlp.MetricResource
}

func (c *nativeMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func testAzureFixture() *fixture.Set { return &fixture.Set{Seed: "native-otlp-test"} }

func testAzureNow() time.Time {
	return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
}

func tickAzureNative(t *testing.T, cfg *Config) (*coretest.MetricCapture, *nativeMetricCapture) {
	t.Helper()
	reg := Registration()
	c, err := reg.Build(cfg, testAzureFixture())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	legacy := &coretest.MetricCapture{}
	native := &nativeMetricCapture{}
	w := coretest.World(legacy, &coretest.LogCapture{}, nil)
	w.OTLPMetrics = native
	if err := c.Tick(context.Background(), testAzureNow(), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return legacy, native
}

func promShape(series []promrw.Series) []string {
	out := make([]string, 0, len(series))
	for _, s := range series {
		out = append(out, s.Name+"\x00"+state.LabelSig(s.Labels)+"\x00"+s.T.String()+"\x00"+string(rune(s.Kind)))
	}
	sort.Strings(out)
	return out
}

func TestNativeOTLPDefaultOffPreservesLegacyOutput(t *testing.T) {
	reg := Registration()
	baseline, err := reg.Build(&Config{}, testAzureFixture())
	if err != nil {
		t.Fatalf("baseline Build: %v", err)
	}
	explicit, err := reg.Build(&Config{OTLPMetrics: false}, testAzureFixture())
	if err != nil {
		t.Fatalf("explicit Build: %v", err)
	}
	baseCapture := &coretest.MetricCapture{}
	explicitCapture := &coretest.MetricCapture{}
	baseNative := &nativeMetricCapture{}
	explicitNative := &nativeMetricCapture{}
	baseWorld := coretest.World(baseCapture, &coretest.LogCapture{}, nil)
	baseWorld.OTLPMetrics = baseNative
	explicitWorld := coretest.World(explicitCapture, &coretest.LogCapture{}, nil)
	explicitWorld.OTLPMetrics = explicitNative
	if err := baseline.Tick(context.Background(), testAzureNow(), baseWorld); err != nil {
		t.Fatalf("baseline Tick: %v", err)
	}
	if err := explicit.Tick(context.Background(), testAzureNow(), explicitWorld); err != nil {
		t.Fatalf("explicit Tick: %v", err)
	}
	if got, want := promShape(explicitCapture.All()), promShape(baseCapture.All()); !reflect.DeepEqual(got, want) {
		t.Fatalf("OTLP default changed legacy output: got %d series, want %d", len(got), len(want))
	}
	if len(baseNative.resources) != 0 || len(explicitNative.resources) != 0 {
		t.Fatalf("default-off native output: baseline=%d explicit=%d, want zero", len(baseNative.resources), len(explicitNative.resources))
	}
	if got := baseline.Signals(); !reflect.DeepEqual(got, []core.SignalClass{core.Metrics, core.Logs}) {
		t.Fatalf("default Signals()=%v, want metrics+logs", got)
	}
}

func TestNativeOTLPReceiverContract(t *testing.T) {
	cfg := &Config{OTLPMetrics: true}
	legacy, native := tickAzureNative(t, cfg)
	if len(legacy.All()) == 0 {
		t.Fatal("native configuration unexpectedly removed legacy metrics")
	}
	if len(native.resources) != 2 {
		t.Fatalf("native resources=%d, want one per synthetic subscription", len(native.resources))
	}
	for _, resource := range native.resources {
		for _, key := range []string{"azuremonitor.subscription", "azuremonitor.subscription_id", "azuremonitor.tenant_id"} {
			if value := resource.Attrs[key]; value == nil || value == "" {
				t.Errorf("resource attribute %q=%v, want non-empty", key, value)
			}
		}
		for _, metric := range resource.Metrics {
			if metric.Kind != otlp.MetricGauge {
				t.Errorf("native metric %q kind=%v, want Gauge", metric.Name, metric.Kind)
			}
			if len(metric.Numbers) == 0 {
				t.Errorf("native metric %q has no datapoints", metric.Name)
			}
			for _, point := range metric.Numbers {
				if point.Time != testAzureNow() {
					t.Errorf("native metric %q point time=%s, want %s", metric.Name, point.Time, testAzureNow())
				}
				if point.Start != (time.Time{}) {
					t.Errorf("native gauge %q has start time %s", metric.Name, point.Start)
				}
				if _, ok := point.Attrs["azuremonitor.resource_id"]; !ok {
					t.Errorf("native metric %q point missing receiver resource identity", metric.Name)
				}
			}
		}
	}

	metrics := nativeMetricSet(native)
	for _, want := range []string{
		"azure_vmavailabilitymetric_average",
		"azure_percentage_cpu_average",
		"azure_disk_read_operations/sec_average",
		"azure_connection_successful_total",
		"azure_active_connections_average",
		"azure_containercount_average",
		"azure_blobcount_average",
		"azure_syncount_total",
		"azure_totalrequests_total",
		"azure_percentage4xx_average",
		"azure_incomingrequests_total",
		"azure_messages_average",
	} {
		if !metrics[want] {
			t.Errorf("native receiver family %q missing", want)
		}
	}
	for name := range metrics {
		if len(name) >= len("azure_microsoft_network_virtualnetworks_") &&
			name[:len("azure_microsoft_network_virtualnetworks_")] == "azure_microsoft_network_virtualnetworks_" {
			t.Errorf("unsupported virtual-network native family emitted: %q", name)
		}
	}

	blob := nativeMetric(t, native, "azure_blobcount_average")
	if got := blob.Numbers[0].Attrs["BlobType"]; got == nil {
		t.Fatalf("BlobCount datapoint missing BlobType dimension: %#v", blob.Numbers[0].Attrs)
	}
	if got := blob.Numbers[0].Attrs["Tier"]; got == nil {
		t.Fatalf("BlobCount datapoint missing Tier dimension: %#v", blob.Numbers[0].Attrs)
	}
	if got := nativeMetric(t, native, "azure_percentage_cpu_average").Unit; got != "Percent" {
		t.Fatalf("Percentage CPU unit=%q, want Percent", got)
	}
	if got := nativeMetric(t, native, "azure_disk_read_operations/sec_average").Unit; got != "CountPerSecond" {
		t.Fatalf("Disk Read Operations/Sec unit=%q, want CountPerSecond", got)
	}
}

func TestNativeOTLPCatalogueComplete(t *testing.T) {
	legacy, native := tickAzureNative(t, &Config{
		OTLPMetrics: true,
		SubSignals:  []string{"compute", "databases", "storage", "networking", "messaging", "ai"},
	})
	legacyNames := map[string]bool{}
	for _, series := range legacy.All() {
		legacyNames[series.Name] = true
	}
	got := nativeMetricSet(native)
	want := map[string]nativeMetricSpec{}
	for _, spec := range nativeMetricSpecs {
		want[spec.receiverName()] = spec
		if !legacyNames[spec.LegacyName] {
			t.Errorf("native catalogue legacy family %q was not emitted by its source sub-signal", spec.LegacyName)
		}
	}
	if gotCount, wantCount := len(nativeMetricSpecs), 119; gotCount != wantCount {
		t.Fatalf("native catalogue entries=%d, want documented family/resource entries=%d", gotCount, wantCount)
	}
	if gotCount, wantCount := len(got), 103; gotCount != wantCount {
		t.Fatalf("native receiver names=%d, want documented unique names=%d", gotCount, wantCount)
	}
	if gotCount, wantCount := len(want), 103; gotCount != wantCount {
		t.Fatalf("catalogue receiver names=%d, want %d", gotCount, wantCount)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("documented native receiver family %q missing", name)
		}
	}
}

func TestNativeOTLPReceiverNameRoundTripCases(t *testing.T) {
	for _, tc := range []struct {
		metricName, aggregation, want string
	}{
		{metricName: "Percentage CPU", aggregation: "Average", want: "azure_percentage_cpu_average"},
		{metricName: "Disk Read Operations/Sec", aggregation: "Average", want: "azure_disk_read_operations/sec_average"},
		{metricName: "VmAvailabilityMetric", aggregation: "Average", want: "azure_vmavailabilitymetric_average"},
		{metricName: "eDTU_used", aggregation: "Average", want: "azure_edtu_used_average"},
		{metricName: "Percentage4XX", aggregation: "Average", want: "azure_percentage4xx_average"},
	} {
		if got := receiverMetricName(tc.metricName, tc.aggregation); got != tc.want {
			t.Errorf("receiverMetricName(%q, %q)=%q, want %q", tc.metricName, tc.aggregation, got, tc.want)
		}
	}
}

func TestNativeOTLPIdentityPrefixSeparatesSubstrateBlueprints(t *testing.T) {
	base, _ := tickAzureNative(t, &Config{OTLPMetrics: true})
	prefixed, _ := tickAzureNative(t, &Config{OTLPMetrics: true, IdentityPrefix: "prefixed"})

	baseIDs := map[string]bool{}
	prefixedIDs := map[string]bool{}
	baseResources := map[string]bool{}
	prefixedResources := map[string]bool{}
	for _, series := range base.All() {
		baseIDs[series.Labels["subscriptionID"]] = true
		baseResources[series.Labels["resourceID"]] = true
	}
	for _, series := range prefixed.All() {
		prefixedIDs[series.Labels["subscriptionID"]] = true
		prefixedResources[series.Labels["resourceID"]] = true
	}
	if len(baseIDs) != 2 || len(prefixedIDs) != 2 {
		t.Fatalf("subscription IDs: base=%v prefixed=%v, want two each", baseIDs, prefixedIDs)
	}
	for id := range prefixedIDs {
		if baseIDs[id] {
			t.Errorf("identity prefix did not separate subscription ID %q", id)
		}
	}
	for resourceID := range prefixedResources {
		if baseResources[resourceID] {
			t.Errorf("identity prefix did not separate resource ID %q", resourceID)
		}
	}
}

func TestNativeOTLPSignalSwitch(t *testing.T) {
	reg := Registration()
	for _, tc := range []struct {
		name string
		cfg  *Config
		want []core.SignalClass
	}{
		{name: "off", cfg: &Config{}, want: []core.SignalClass{core.Metrics, core.Logs}},
		{name: "on", cfg: &Config{OTLPMetrics: true}, want: []core.SignalClass{core.Metrics, core.Logs, core.OTLPMetrics}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := reg.Build(tc.cfg, testAzureFixture())
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if got := c.Signals(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Signals()=%v, want %v", got, tc.want)
			}
		})
	}
}

func nativeMetricSet(capture *nativeMetricCapture) map[string]bool {
	set := map[string]bool{}
	for _, resource := range capture.resources {
		for _, metric := range resource.Metrics {
			set[metric.Name] = true
		}
	}
	return set
}

func nativeMetric(t *testing.T, capture *nativeMetricCapture, name string) otlp.Metric {
	t.Helper()
	for _, resource := range capture.resources {
		for _, metric := range resource.Metrics {
			if metric.Name == name {
				return metric
			}
		}
	}
	t.Fatalf("native metric %q not found", name)
	return otlp.Metric{}
}
