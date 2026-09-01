// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
	"github.com/rknightion/synthkit/internal/runner"
)

func TestSynthInventoryDeclaresTheRunnerSelectorLabel(t *testing.T) {
	schema := withSynthProvenance(inventory.New())
	if schema.Provenance == nil {
		t.Fatal("provenance: the synth export must declare its own producer provenance")
	}
	if !reflect.DeepEqual(schema.Provenance.SelectorLabels, []string{runner.BlueprintLabel}) {
		t.Fatalf("selector labels=%v, want the constant the runner defines", schema.Provenance.SelectorLabels)
	}
}

func TestSynthInventoryDeclaresNoSubstrate(t *testing.T) {
	// With BLUEPRINT_NAMES='*' the export is the union of every blueprint, so no single
	// substrate describes it. Declaring one would silently exclude every corpus document
	// captured on another substrate. See docs/reality-corpus.md.
	schema := withSynthProvenance(inventory.New())
	if schema.Provenance.Substrate != "" {
		t.Fatalf("substrate=%q, want an undeclared substrate until the export is substrate-scoped", schema.Provenance.Substrate)
	}
}

func TestSynthInventoryCarriesRunnerAllowListSuppressions(t *testing.T) {
	schema := withMetricSuppressions(inventory.New(), []runner.MetricSuppression{{
		Name: "node_disk_read_bytes_total", Producer: "promrw",
		AllowListVersion: "4.5.0", AllowListVariant: "node-exporter=default",
	}})
	if got := schema.AllowListSuppressions; len(got) != 1 || got[0].Name != "node_disk_read_bytes_total" || got[0].Producer.Name != "promrw" || got[0].Producer.AllowListVersion != "4.5.0" || got[0].Producer.AllowListVariant != "node-exporter=default" {
		t.Fatalf("allow-list suppressions=%+v, want runner provenance", got)
	}
}
