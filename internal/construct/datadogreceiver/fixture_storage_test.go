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
)

type storageVerdict struct {
	Families []storageVerdictFamily `json:"families"`
}

type storageVerdictFamily struct {
	AggregationTemporality string                   `json:"aggregation_temporality"`
	Category               string                   `json:"category"`
	Instrument             string                   `json:"instrument"`
	IsMonotonic            *bool                    `json:"is_monotonic"`
	Name                   string                   `json:"name"`
	NativePlacements       []storageNativePlacement `json:"native_placements"`
	Unit                   string                   `json:"unit"`
}

type storageNativePlacement struct {
	DatapointAttributeKeys []string      `json:"datapoint_attribute_keys"`
	ResourceAttributeKeys  []string      `json:"resource_attribute_keys"`
	ResourceSchemaURL      string        `json:"resource_schema_url"`
	Scope                  capturedScope `json:"scope"`
	ScopeSchemaURL         string        `json:"scope_schema_url"`
}

func TestStorageFixtureMechanicsUseVerdictEnvelopes(t *testing.T) {
	families := storageCandidateFamilies(t)
	if got, want := len(families), 34; got != want {
		t.Fatalf("storage candidate families in verdict = %d, want %d", got, want)
	}
	unsupported := map[string]bool{
		"system.io.rrqm_s":                        true,
		"system.io.wrqm_s":                        true,
		"system.fs.file_handles.allocated_unused": true,
		"system.fs.file_handles.in_use":           true,
		"system.fs.file_handles.used":             true,
	}

	constructed, err := Build(&Config{
		Mode: "host", DeploymentEnvironment: "production", HostName: "agent-node",
		ServiceName: "example-service", Source: "agent", IncrementsPerMinute: 0.5,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := constructed.(*Construct)
	first := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c.start = first
	got := metricResourcesByName(c.storageFixtureResources(first, &core.World{}))
	if got, want := len(got), len(families)-len(unsupported); got != want {
		t.Fatalf("storage emitted family count = %d, want %d (%v)", got, want, sortedStorageFamilyNames(families))
	}

	for _, family := range families {
		family := family
		t.Run(family.Name, func(t *testing.T) {
			if unsupported[family.Name] {
				if len(got[family.Name]) != 0 {
					t.Errorf("unsupported storage family %q was emitted", family.Name)
				}
				return
			}
			resources := got[family.Name]
			if len(resources) == 0 {
				t.Errorf("storage fixture family %q was not emitted", family.Name)
				return
			}
			want := storageCapturedEnvelope(t, family)
			matched := false
			for _, resource := range resources {
				if err := matchesCapturedEnvelope(resource, want); err == nil {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("storage fixture family %q did not match its verdict envelope", family.Name)
			}
		})
	}
}

func TestStorageFixtureSumsAccumulateFromDeclaredStart(t *testing.T) {
	first := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c := &Construct{hostName: "agent-node", source: "agent", start: first}
	w := &core.World{}
	initial := metricResourcesByName(c.storageFixtureResources(first, w))
	later := metricResourcesByName(c.storageFixtureResources(first.Add(time.Minute), w))
	for _, name := range []string{"system.disk.read_time", "system.disk.write_time"} {
		initialPoint := initial[name][0].Metrics[0].Numbers[0]
		laterPoint := later[name][0].Metrics[0].Numbers[0]
		if !laterPoint.Start.Equal(first) {
			t.Errorf("%s start = %v, want %v", name, laterPoint.Start, first)
		}
		if laterPoint.Value <= initialPoint.Value {
			t.Errorf("%s did not accumulate: initial=%v later=%v", name, initialPoint.Value, laterPoint.Value)
		}
	}
}

func storageCandidateFamilies(t *testing.T) []storageVerdictFamily {
	t.Helper()
	// Discover the standalone-host classification artifact without coupling this
	// construct test to a blueprint name embedded in the artifact filename.
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "reality-corpus", "verdicts", "*host-classification.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("host classification artifacts = %v, error = %v; want exactly one", paths, err)
	}
	b, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var verdict storageVerdict
	if err := json.Unmarshal(b, &verdict); err != nil {
		t.Fatal(err)
	}
	var families []storageVerdictFamily
	for _, family := range verdict.Families {
		if family.Category != "observed_unimplemented_fixture_mechanics_candidate" {
			continue
		}
		if strings.HasPrefix(family.Name, "system.io.") || strings.HasPrefix(family.Name, "system.disk.") || strings.HasPrefix(family.Name, "system.fs.") {
			families = append(families, family)
		}
	}
	return families
}

func storageCapturedEnvelope(t *testing.T, family storageVerdictFamily) capturedEnvelope {
	t.Helper()
	if len(family.NativePlacements) != 1 {
		t.Fatalf("%s native placements = %d, want 1", family.Name, len(family.NativePlacements))
	}
	placement := family.NativePlacements[0]
	resourceAttrs := make([]capturedAttr, 0, len(placement.ResourceAttributeKeys))
	for _, key := range placement.ResourceAttributeKeys {
		resourceAttrs = append(resourceAttrs, capturedAttr{Key: key})
	}
	datapointAttrs := make([]capturedAttr, 0, len(placement.DatapointAttributeKeys))
	for _, key := range placement.DatapointAttributeKeys {
		datapointAttrs = append(datapointAttrs, capturedAttr{Key: key})
	}
	return capturedEnvelope{
		Name: family.Name, Instrument: family.Instrument,
		AggregationTemporality: family.AggregationTemporality,
		IsMonotonic:            family.IsMonotonic != nil && *family.IsMonotonic,
		Unit:                   family.Unit, ResourceAttributes: resourceAttrs, DatapointAttributes: datapointAttrs,
		ResourceSchemaURL: placement.ResourceSchemaURL, ScopeSchemaURL: placement.ScopeSchemaURL,
		Scope: placement.Scope,
	}
}

func sortedStorageFamilyNames(families []storageVerdictFamily) []string {
	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.Name)
	}
	sort.Strings(names)
	return names
}
