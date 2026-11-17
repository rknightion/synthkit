// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

func TestHostTickEmitsFileHandleFamiliesWithVerdictEnvelope(t *testing.T) {
	names := []string{
		"system.fs.file_handles.allocated_unused",
		"system.fs.file_handles.in_use",
		"system.fs.file_handles.used",
	}
	verdict := map[string]storageVerdictFamily{}
	for _, family := range storageCandidateFamilies(t) {
		verdict[family.Name] = family
	}

	constructed, err := Build(hostConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if err := constructed.Tick(context.Background(), now, &core.World{OTLPMetrics: capture}); err != nil {
		t.Fatal(err)
	}
	got := metricResourcesByName(capture.resources)

	for _, name := range names {
		family, ok := verdict[name]
		if !ok {
			t.Fatalf("%s is not a fixture-mechanics candidate in the host verdict", name)
		}
		resources := got[name]
		if len(resources) != 1 {
			t.Fatalf("%s emitted %d resources, want 1", name, len(resources))
		}
		if err := matchesCapturedEnvelope(resources[0], storageCapturedEnvelope(t, family)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	value := func(name string) float64 {
		t.Helper()
		resources := got[name]
		if len(resources) != 1 {
			t.Fatalf("%s emitted %d resources, want 1", name, len(resources))
		}
		return resources[0].Metrics[0].Numbers[0].Value
	}
	allocated := value("system.fs.file_handles.allocated")
	maximum := value("system.fs.file_handles.max")
	if unused := value("system.fs.file_handles.allocated_unused"); unused != 0 {
		t.Errorf("allocated_unused = %v, want the kernel-documented 0", unused)
	}
	used := value("system.fs.file_handles.used")
	if used != allocated {
		t.Errorf("used = %v, want allocated %v minus zero unused", used, allocated)
	}
	if inUse := value("system.fs.file_handles.in_use"); inUse != used/maximum {
		t.Errorf("in_use = %v, want used/max = %v", inUse, used/maximum)
	}
}
