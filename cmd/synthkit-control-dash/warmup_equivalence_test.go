// SPDX-License-Identifier: AGPL-3.0-only

//go:build !race

package main

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/dashgen"
)

// TestShortWarmupMatchesFullDeriveOnShippedScenarioBlueprints is the equivalence guard for the
// shortened warmup the shipped-scenario test derives with. It runs the production twelve-cycle
// dashgen.Derive on every blueprint that ships scenarios and requires the metric and log-source
// inventories to equal the impactWarmupCycles derive. Convergence depends on the blueprint, so a
// shipped scenario blueprint that adds a late-appearing family fails here and must either raise
// impactWarmupCycles or stop relying on the shortcut.
//
// It is excluded from the race build because the full derive is the cost the shortcut avoids
// there; plain `just test` and the coverage leg run it (a few seconds without the race detector).
// It is a new guard, not one of the SKT-0112 acceptance tests, which all still run under race.
func TestShortWarmupMatchesFullDeriveOnShippedScenarioBlueprints(t *testing.T) {
	scenarios, err := loadScenarios("../../blueprints")
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no shipped scenarios")
	}
	paths := map[string]bool{}
	for _, sc := range scenarios {
		paths[sc.Path] = true
	}
	for path := range paths {
		requireShortWarmupMatchesFull(t, path)
	}
}

func requireShortWarmupMatchesFull(t *testing.T, path string) {
	t.Helper()
	full, err := dashgen.Derive(path)
	if err != nil {
		t.Fatal(err)
	}
	short, err := dashgen.DeriveWarm(path, impactWarmupCycles)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Metrics) == 0 {
		t.Fatalf("%s derived no metrics", path)
	}
	if !reflect.DeepEqual(full.Metrics, short.Metrics) {
		t.Errorf("%s: metric inventory differs between %d cycles and the full warmup (%d vs %d families; first difference: %s)",
			path, impactWarmupCycles, len(short.Metrics), len(full.Metrics), firstMetricDifference(full, short))
	}
	if !reflect.DeepEqual(full.LogSources, short.LogSources) {
		t.Errorf("%s: log-source inventory differs between %d cycles and the full warmup (%d vs %d sources)",
			path, impactWarmupCycles, len(short.LogSources), len(full.LogSources))
	}
}

// firstMetricDifference names one family present in only one inventory or whose signal differs.
func firstMetricDifference(full, short *dashboard.Manifest) string {
	for _, f := range full.Metrics {
		g, ok := short.Metric(f.Name)
		if !ok {
			return f.Name + " missing from the short warmup"
		}
		if !reflect.DeepEqual(f, g) {
			return f.Name + " differs"
		}
	}
	for _, g := range short.Metrics {
		if _, ok := full.Metric(g.Name); !ok {
			return g.Name + " missing from the full warmup"
		}
	}
	return "none"
}
