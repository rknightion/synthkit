// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// hostBreadthOutOfScope names the two non-network candidates with no admissible
// merged-operation source. They are tracked separately and must not be emitted.
var hostBreadthOutOfScope = map[string]bool{
	"system.io.rrqm_s": true,
	"system.io.wrqm_s": true,
}

func hostBreadthFamilies(t *testing.T) []storageVerdictFamily {
	t.Helper()
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
		if family.Category != "observed_unimplemented_fixture_mechanics_candidate" ||
			strings.HasPrefix(family.Name, "system.net.") || hostBreadthOutOfScope[family.Name] {
			continue
		}
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
	return families
}

func TestHostTickEmitsAll78NonNetworkVerdictEnvelopes(t *testing.T) {
	families := hostBreadthFamilies(t)
	if got, want := len(families), 78; got != want {
		t.Fatalf("non-network acceptance families = %d, want %d", got, want)
	}
	constructed, err := Build(hostConfig(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	capture := &otlpMetricCapture{}
	first := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, now := range []time.Time{first, first.Add(time.Minute)} {
		capture.resources = nil
		if err := constructed.Tick(context.Background(), now, &core.World{OTLPMetrics: capture}); err != nil {
			t.Fatal(err)
		}
	}
	got := metricResourcesByName(capture.resources)

	for _, family := range families {
		resources := got[family.Name]
		if len(resources) == 0 {
			t.Errorf("%s was not emitted in host mode", family.Name)
			continue
		}
		want := storageCapturedEnvelope(t, family)
		for _, resource := range resources {
			if err := matchesCapturedEnvelope(resource, want); err != nil {
				t.Errorf("%s: %v", family.Name, err)
			}
		}
	}
	for name := range hostBreadthOutOfScope {
		if len(got[name]) != 0 {
			t.Errorf("%s has no admissible source and must not be emitted", name)
		}
	}

	value := func(name string) float64 {
		t.Helper()
		if len(got[name]) == 0 {
			t.Fatalf("%s missing", name)
		}
		return got[name][0].Metrics[0].Numbers[0].Value
	}
	if v := value("system.cpu.num_cores"); v != 2 {
		t.Errorf("num_cores = %v, want declared cpus 2", v)
	}
	if n := len(got["system.cpu.user.total"]); n != 2 {
		t.Errorf("per-core user.total series = %d, want one per declared CPU", n)
	}
	if v := value("system.load.1") / value("system.load.norm.1"); math.Abs(v-2) > 1e-9 {
		t.Errorf("load.1 / load.norm.1 = %v, want declared cpus 2", v)
	}
	total := value("system.mem.total")
	if total != 8*1024 {
		t.Errorf("mem.total = %v MiB, want declared 8 GiB", total)
	}
	free, usable := value("system.mem.free"), value("system.mem.usable")
	nonAvailable := total - usable
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	near("system.mem.used", value("system.mem.used"), total-free)
	near("system.mem.slab_reclaimable", value("system.mem.slab_reclaimable"), nonAvailable*0.04)
	near("system.mem.cached", value("system.mem.cached"), usable-free+nonAvailable*0.04)
	near("system.mem.commit_limit", value("system.mem.commit_limit"), total*0.5)
	near("system.mem.committed_as", value("system.mem.committed_as"), nonAvailable*1.2)
	near("system.mem.slab", value("system.mem.slab"), nonAvailable*0.06)
	near("system.mem.shared", value("system.mem.shared"), nonAvailable*0.05)
	near("system.mem.page_tables", value("system.mem.page_tables"), nonAvailable*0.01)
	if b := value("system.mem.buffered"); b < 256*0.9 || b > 256*1.1 {
		t.Errorf("mem.buffered = %v MiB, want nodeexp's 256 MiB within 10%%", b)
	}
}
