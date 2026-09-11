// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type hostClassification struct {
	Families []hostClassificationFamily `json:"families"`
}

type hostClassificationFamily struct {
	Name                   string                        `json:"name"`
	Category               string                        `json:"category"`
	Instrument             string                        `json:"instrument"`
	AggregationTemporality string                        `json:"aggregation_temporality"`
	IsMonotonic            *bool                         `json:"is_monotonic"`
	Unit                   string                        `json:"unit"`
	NativePlacements       []hostClassificationPlacement `json:"native_placements"`
}

type hostClassificationPlacement struct {
	ResourceAttributeKeys  []string                `json:"resource_attribute_keys"`
	DatapointAttributeKeys []string                `json:"datapoint_attribute_keys"`
	ResourceSchemaURL      string                  `json:"resource_schema_url"`
	ScopeSchemaURL         string                  `json:"scope_schema_url"`
	Scope                  hostClassificationScope `json:"scope"`
}

type hostClassificationScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func TestSwapBreadthFixtureResourcesMatchVerdict(t *testing.T) {
	verdict := loadHostClassification(t)
	var wantNames []string
	for _, family := range verdict.Families {
		if strings.HasPrefix(family.Name, "system.swap.") && family.Category == "observed_unimplemented_fixture_mechanics_candidate" {
			wantNames = append(wantNames, family.Name)
		}
	}
	sort.Strings(wantNames)
	if got, want := len(wantNames), 7; got != want {
		t.Fatalf("verdict swap candidate count = %d, want %d", got, want)
	}

	constructed, err := Build(&Config{
		Mode: "host", DeploymentEnvironment: "example",
		HostName: "agent-node", ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, &fixture.Set{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	resources := constructed.(*Construct).memoryBreadthFixtureResources(now, &core.World{})
	got := metricResourcesByName(resources)
	if got, want := len(got), len(wantNames); got != want {
		t.Fatalf("emitted swap breadth family count = %d, want %d", got, want)
	}

	byName := make(map[string]hostClassificationFamily, len(verdict.Families))
	for _, family := range verdict.Families {
		byName[family.Name] = family
	}
	for _, name := range wantNames {
		family, ok := byName[name]
		if !ok {
			t.Fatalf("verdict has no %q family", name)
		}
		t.Run(name, func(t *testing.T) {
			candidates := got[name]
			if len(candidates) != 1 {
				t.Fatalf("emitted resources = %d, want one", len(candidates))
			}
			assertSwapEnvelope(t, candidates[0], family, now)
		})
	}
}

func loadHostClassification(t *testing.T) hostClassification {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "reality-corpus", "verdicts", "*host-classification.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("host classification artifacts = %v, error = %v; want exactly one", matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var verdict hostClassification
	if err := json.Unmarshal(b, &verdict); err != nil {
		t.Fatal(err)
	}
	return verdict
}

func assertSwapEnvelope(t *testing.T, resource otlp.MetricResource, family hostClassificationFamily, now time.Time) {
	t.Helper()
	if family.Instrument != "gauge" || family.AggregationTemporality != "" || family.IsMonotonic != nil {
		t.Fatalf("unsupported verdict shape for %q: instrument=%q temporality=%q monotonic=%v", family.Name, family.Instrument, family.AggregationTemporality, family.IsMonotonic)
	}
	if len(family.NativePlacements) != 1 {
		t.Fatalf("native placements = %d, want one", len(family.NativePlacements))
	}
	placement := family.NativePlacements[0]
	if got, want := resource.Scope, (otlp.Scope{Name: placement.Scope.Name, Version: placement.Scope.Version}); got != want {
		t.Fatalf("scope = %#v, want %#v", got, want)
	}
	if resource.ResourceSchemaURL != placement.ResourceSchemaURL {
		t.Fatalf("resource schema URL = %q, want %q", resource.ResourceSchemaURL, placement.ResourceSchemaURL)
	}
	if placement.ScopeSchemaURL != "" {
		t.Fatalf("scope schema URL %q is not representable by the metric seam", placement.ScopeSchemaURL)
	}
	if got, want := sortedKeys(resource.Attrs), sortedStrings(placement.ResourceAttributeKeys); !equalStrings(got, want) {
		t.Fatalf("resource keys = %v, want %v", got, want)
	}
	if len(resource.Metrics) != 1 {
		t.Fatalf("metrics = %d, want one", len(resource.Metrics))
	}
	metric := resource.Metrics[0]
	if metric.Name != family.Name || metric.Kind != otlp.MetricGauge || metric.Unit != family.Unit {
		t.Fatalf("metric = %#v, want gauge %q unit %q", metric, family.Name, family.Unit)
	}
	if len(metric.Numbers) != 1 {
		t.Fatalf("number points = %d, want one", len(metric.Numbers))
	}
	point := metric.Numbers[0]
	if !point.Time.Equal(now) {
		t.Fatalf("point time = %v, want %v", point.Time, now)
	}
	if got, want := sortedKeys(point.Attrs), sortedStrings(placement.DatapointAttributeKeys); !equalStrings(got, want) {
		t.Fatalf("datapoint keys = %v, want %v", got, want)
	}
	if point.Value != 0 {
		t.Fatalf("degenerate swap value = %v, want zero", point.Value)
	}
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
