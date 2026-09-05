// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/inventory"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/loki"
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

func TestLokiDumpKeepsSourceLessFamiliesSeparate(t *testing.T) {
	sink := loki.New("", "", "", true)
	sink.Capture = true
	sink.Quiet = true
	err := sink.Write(context.Background(), []loki.Stream{
		{Labels: map[string]string{"namespace": "demo", "container": "proxy"}, Lines: []loki.Line{{T: time.Unix(1, 0), Body: "pod", Meta: map[string]string{"pod": "proxy-1"}}}},
		{Labels: map[string]string{"action": "manifest", "k8s_kind": "Pod"}, Lines: []loki.Line{{T: time.Unix(1, 0), Body: "manifest"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	streams, metadata := lokiDumpInventory(sink)
	if len(streams) != 2 {
		t.Fatalf("dump fused source-less families: %v", streams)
	}
	if !reflect.DeepEqual(streams[inventory.LogFamilyPodLogs], []string{"container", "namespace"}) {
		t.Fatalf("pod stream keys = %v", streams)
	}
	if !reflect.DeepEqual(metadata[inventory.LogFamilyPodLogs], []string{"pod"}) {
		t.Fatalf("pod metadata = %v", metadata)
	}
	if !reflect.DeepEqual(streams[inventory.LogFamilyKubernetesManifests], []string{"action", "k8s_kind"}) {
		t.Fatalf("manifest keys = %v", streams)
	}
}
