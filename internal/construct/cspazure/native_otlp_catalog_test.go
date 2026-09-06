// SPDX-License-Identifier: AGPL-3.0-only

package cspazure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

func TestNativeCatalogueMatchesDocumentedReceiverNamesAndContext(t *testing.T) {
	documented := documentedAzureReceiverNames(t)
	byName := make(map[string][]nativeMetricSpec)
	legacy := make(map[string]nativeMetricSpec, len(nativeMetricSpecs))
	for _, spec := range nativeMetricSpecs {
		if spec.ResourceType == "" || spec.MetricName == "" || spec.Aggregation == "" || spec.Unit == "" {
			t.Errorf("native spec has incomplete receiver context: %#v", spec)
		}
		if spec.LegacyName == "" {
			t.Errorf("native spec %q has no explicit legacy scrape pairing", spec.MetricName)
		}
		if previous, ok := legacy[spec.LegacyName]; ok {
			t.Errorf("legacy scrape family %q is paired more than once: %#v and %#v", spec.LegacyName, previous, spec)
		}
		legacy[spec.LegacyName] = spec
		byName[spec.receiverName()] = append(byName[spec.receiverName()], spec)
	}

	if got, want := len(nativeMetricSpecs), 119; got != want {
		t.Fatalf("native catalogue entries=%d, want %d", got, want)
	}
	if got, want := len(byName), 103; got != want {
		t.Fatalf("unique receiver names=%d, want %d", got, want)
	}
	if got, want := len(documented), 103; got != want {
		t.Fatalf("documented receiver names=%d, want %d", got, want)
	}
	for name := range documented {
		if _, ok := byName[name]; !ok {
			t.Errorf("documented receiver name %q has no explicit catalogue entry", name)
		}
	}
	for name := range byName {
		if _, ok := documented[name]; !ok {
			t.Errorf("catalogue receiver name %q is absent from signals/cspazure.md", name)
		}
	}

	// A receiver name can be shared by resource types (for example cpu_percent),
	// but its explicit context must keep the pairings distinct. This prevents a
	// reverse name rewrite from silently selecting one historical scrape family.
	for name, specs := range byName {
		contexts := map[string]bool{}
		for _, spec := range specs {
			contextKey := strings.Join([]string{spec.ResourceType, spec.Aggregation, spec.Unit}, "\x00")
			if contexts[contextKey] {
				t.Errorf("receiver name %q repeats resource/aggregation/unit context %q", name, contextKey)
			}
			contexts[contextKey] = true
		}
	}
}

func TestNativeCatalogueLegacyNamesAreForwardDerived(t *testing.T) {
	for _, spec := range nativeMetricSpecs {
		if got, want := legacyMetricName(spec), spec.LegacyName; got != want {
			t.Errorf("forward legacy name for %#v=%q, want %q", nativeMetricContext{
				ResourceType: spec.ResourceType,
				MetricName:   spec.MetricName,
				Aggregation:  spec.Aggregation,
				Unit:         spec.Unit,
			}, got, want)
		}
	}
}

func TestNativeCatalogueLegacyNameForwardCases(t *testing.T) {
	tests := []struct {
		name string
		spec nativeMetricSpec
		want string
	}{
		{
			name: "spaces and resource path",
			spec: nativeMetricSpec{
				ResourceType: "Microsoft.Compute/virtualMachines",
				MetricName:   "Percentage CPU",
				Aggregation:  "Average",
				Unit:         "Percent",
			},
			want: "azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent",
		},
		{
			name: "slash in vendor metric",
			spec: nativeMetricSpec{
				ResourceType: "Microsoft.Compute/virtualMachines",
				MetricName:   "Disk Read Operations/Sec",
				Aggregation:  "Average",
				Unit:         "CountPerSecond",
			},
			want: "azure_microsoft_compute_virtualmachines_disk_read_operations_sec_average_countpersecond",
		},
		{
			name: "postgres exporter duplicate token",
			spec: nativeMetricSpec{
				ResourceType: "Microsoft.DBforPostgreSQL/flexibleServers",
				MetricName:   "connections_failed",
				Aggregation:  "Total",
				Unit:         "Count",
			},
			want: "azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count",
		},
		{
			name: "cognitive services snake case",
			spec: nativeMetricSpec{
				ResourceType: "Microsoft.CognitiveServices/accounts",
				MetricName:   "TotalCalls",
				Aggregation:  "Total",
				Unit:         "Count",
			},
			want: "azure_microsoft_cognitiveservices_accounts_total_calls_total_count",
		},
		{
			name: "cognitive services semantic legacy token",
			spec: nativeMetricSpec{
				ResourceType: "Microsoft.CognitiveServices/accounts",
				MetricName:   "GeneratedTokens",
				Aggregation:  "Total",
				Unit:         "Count",
			},
			want: "azure_microsoft_cognitiveservices_accounts_generated_completion_tokens_total_count",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := legacyMetricName(tt.spec); got != tt.want {
				t.Fatalf("legacyMetricName(%#v)=%q, want %q", tt.spec, got, tt.want)
			}
		})
	}
}

func TestNativeCataloguePairingsReachTheirLegacyFamilies(t *testing.T) {
	construct, err := Registration().Build(&Config{
		OTLPMetrics: true,
		SubSignals:  []string{"compute", "databases", "storage", "networking", "messaging", "ai"},
	}, &fixture.Set{Seed: "native-catalog-pairing"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	legacy := &coretest.MetricCapture{}
	native := &catalogOTLPCapture{}
	world := coretest.World(legacy, &coretest.LogCapture{}, nil)
	world.OTLPMetrics = native
	if err := construct.Tick(context.Background(), time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	legacyNames := make(map[string]bool)
	for _, series := range legacy.All() {
		legacyNames[series.Name] = true
	}
	nativeNames := make(map[string]bool)
	for _, resource := range native.resources {
		for _, metric := range resource.Metrics {
			nativeNames[metric.Name] = true
		}
	}
	for _, spec := range nativeMetricSpecs {
		if !legacyNames[spec.LegacyName] {
			t.Errorf("native %q lost its explicit legacy family %q", spec.receiverName(), spec.LegacyName)
		}
		if !nativeNames[spec.receiverName()] {
			t.Errorf("legacy family %q did not produce receiver family %q", spec.LegacyName, spec.receiverName())
		}
	}
}

func TestNativeMetricPointsKeepDistinctResourceIdentities(t *testing.T) {
	construct, err := Registration().Build(&Config{OTLPMetrics: true}, &fixture.Set{Seed: "native-resource-identity"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	native := &catalogOTLPCapture{}
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, nil)
	world.OTLPMetrics = native
	if err := construct.Tick(context.Background(), time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	idsByName := map[string]map[string]bool{}
	points := map[string]bool{}
	for _, resource := range native.resources {
		for _, metric := range resource.Metrics {
			for _, point := range metric.Numbers {
				id, ok := point.Attrs["azuremonitor.resource_id"].(string)
				if !ok || id == "" {
					t.Errorf("native metric %q point has no resource identity: %#v", metric.Name, point.Attrs)
					continue
				}
				if idsByName[metric.Name] == nil {
					idsByName[metric.Name] = map[string]bool{}
				}
				idsByName[metric.Name][id] = true
				pointKey := metric.Name + "\x00" + id + "\x00" + attrsKey(point.Attrs)
				if points[pointKey] {
					t.Errorf("duplicate native point identity %q", pointKey)
				}
				points[pointKey] = true
			}
		}
	}
	if got := len(idsByName["azure_cpu_percent_average"]); got < 2 {
		t.Fatalf("shared receiver family lost distinct resource IDs: got %d, want at least 2", got)
	}
}

type catalogOTLPCapture struct {
	resources []otlp.MetricResource
}

func (c *catalogOTLPCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.resources = append(c.resources, resources...)
	return nil
}

func documentedAzureReceiverNames(t *testing.T) map[string]bool {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(source), "../../../signals/cspazure.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(raw)
	start := strings.Index(text, "## Native OTLP metrics — Azure Monitor receiver form")
	if start < 0 {
		t.Fatal("signals/cspazure.md has no native Azure section")
	}
	end := strings.Index(text[start:], "### Explicit exclusions")
	if end < 0 {
		t.Fatal("signals/cspazure.md native Azure section has no exclusions boundary")
	}
	section := text[start : start+end]
	row := regexp.MustCompile("(?m)^\\|\\s*`([^`]+)`\\s*\\|")
	names := map[string]bool{}
	for _, match := range row.FindAllStringSubmatch(section, -1) {
		names[match[1]] = true
	}
	if len(names) == 0 {
		t.Fatal("signals/cspazure.md native Azure section has no documented receiver rows")
	}
	return names
}

func attrsKey(attrs map[string]any) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		if b.Len() > 0 {
			b.WriteByte('\x00')
		}
		fmt.Fprintf(&b, "%s=%v", key, attrs[key])
	}
	return b.String()
}
