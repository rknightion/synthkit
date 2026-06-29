// SPDX-License-Identifier: AGPL-3.0-only

package elasticache_test

// elasticache_test.go — contract tests for the "elasticache" construct.
//
// Checks (using coretest harness):
//   - Exact series inventory: all 24 base metrics × 5 stat suffixes + aws_elasticache_info.
//   - Dimension identity: dimension_CacheClusterId == fx.Cache.Name, dimension_CacheNodeId
//     matches the fixture node-ID list.
//   - Universal CW label keys present on every series.
//   - Build errors on nil fx.Cache and nil fx.Cloud.

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/elasticache"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// staticNow is a deterministic business-hours timestamp (Friday 14:00 UTC).
var staticNow = time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)

// stdFixture returns a fully populated fixture.Set for the standard test cache.
func stdFixture() *fixture.Set {
	env := coretest.Env()
	cloud := coretest.Cloud()
	cache := coretest.Cache() // Name="test-sessions", NodeIDs=["0001"]
	cache.Env = env
	cache.Cloud = cloud
	return &fixture.Set{
		Seed:  "test",
		Env:   env,
		Cloud: cloud,
		Cache: cache,
	}
}

// buildAndTick constructs the elasticache construct, runs one tick, and returns the
// MetricCapture. It fails the test on any build or tick error.
func buildAndTick(t *testing.T, fx *fixture.Set) *coretest.MetricCapture {
	t.Helper()
	c, err := elasticache.Reg.Build(&elasticache.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), staticNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// wantNames is the authoritative series inventory for one cache cluster with one node.
// 26 base metrics × 5 stat suffixes = 130 named series + 1 info series = 131 total.
var wantNames = func() []string {
	bases := []string{
		"aws_elasticache_cpuutilization",
		"aws_elasticache_engine_cpuutilization",
		"aws_elasticache_curr_connections",
		"aws_elasticache_new_connections",
		"aws_elasticache_curr_items",
		"aws_elasticache_bytes_used_for_cache",
		"aws_elasticache_database_memory_usage_percentage",
		"aws_elasticache_cache_hits",
		"aws_elasticache_cache_misses",
		"aws_elasticache_evictions",
		"aws_elasticache_reclaimed",
		"aws_elasticache_replication_bytes",
		"aws_elasticache_replication_lag",
		"aws_elasticache_freeable_memory",
		"aws_elasticache_swap_usage",
		"aws_elasticache_network_bytes_in",
		"aws_elasticache_network_bytes_out",
		"aws_elasticache_memory_fragmentation_ratio",
		"aws_elasticache_processed_commands",
		"aws_elasticache_error_count",
		"aws_elasticache_blocked_connections",
		"aws_elasticache_save_in_progress",
		"aws_elasticache_is_master",
		"aws_elasticache_set_type_cmds",
	}
	suffixes := []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}
	var out []string
	for _, b := range bases {
		for _, s := range suffixes {
			out = append(out, b+s)
		}
	}
	out = append(out, "aws_elasticache_info")
	sort.Strings(out)
	return out
}()

// TestInventoryExact asserts that exactly the expected series names are emitted.
func TestInventoryExact(t *testing.T) {
	mc := buildAndTick(t, stdFixture())
	got := mc.Names()
	if len(got) != len(wantNames) {
		t.Fatalf("series count: want %d got %d\ngot:  %v\nwant: %v", len(wantNames), len(got), got, wantNames)
	}
	for i, g := range got {
		if g != wantNames[i] {
			t.Errorf("series[%d]: want %q got %q", i, wantNames[i], g)
		}
	}
}

// TestDimensionIdentity asserts that every per-node series carries the right
// dimension_CacheClusterId and dimension_CacheNodeId from the fixture.
func TestDimensionIdentity(t *testing.T) {
	fx := stdFixture()
	mc := buildAndTick(t, fx)

	wantCluster := fx.Cache.Name // "test-sessions"
	wantNodeIDs := map[string]bool{}
	for _, nid := range fx.Cache.NodeIDs {
		wantNodeIDs[nid] = true
	}

	// Check every stat-suffixed series (not the info series which has no node dim).
	for _, s := range mc.All() {
		if s.Name == "aws_elasticache_info" {
			continue
		}
		if s.Labels["dimension_CacheClusterId"] != wantCluster {
			t.Errorf("%s: dimension_CacheClusterId want %q got %q",
				s.Name, wantCluster, s.Labels["dimension_CacheClusterId"])
		}
		nodeID := s.Labels["dimension_CacheNodeId"]
		if !wantNodeIDs[nodeID] {
			t.Errorf("%s: unexpected dimension_CacheNodeId %q (want one of %v)",
				s.Name, nodeID, fx.Cache.NodeIDs)
		}
	}
}

// TestUniversalLabelKeys asserts that the universal CW label keys are present on
// every series. Checks account_id, region, namespace, job, name.
func TestUniversalLabelKeys(t *testing.T) {
	mc := buildAndTick(t, stdFixture())
	required := []string{"account_id", "region", "namespace", "job", "name"}
	for _, s := range mc.All() {
		for _, k := range required {
			if s.Labels[k] == "" {
				t.Errorf("%s: missing or empty label %q", s.Name, k)
			}
		}
	}
}

// TestBuildNilCache asserts that Build returns an error when fx.Cache is nil.
func TestBuildNilCache(t *testing.T) {
	fx := stdFixture()
	fx.Cache = nil
	_, err := elasticache.Reg.Build(&elasticache.Config{}, fx)
	if err == nil {
		t.Fatal("expected error for nil fx.Cache, got nil")
	}
}

// TestBuildNilCloud asserts that Build returns an error when fx.Cloud is nil.
func TestBuildNilCloud(t *testing.T) {
	fx := stdFixture()
	fx.Cloud = nil
	_, err := elasticache.Reg.Build(&elasticache.Config{}, fx)
	if err == nil {
		t.Fatal("expected error for nil fx.Cloud, got nil")
	}
}

// ── SK-5: instance_class → memory% tests ─────────────────────────────────────────────────────────

// avgOf returns the _average value of a named ElastiCache series (first match).
func avgOf(t *testing.T, cap *coretest.MetricCapture, name string) float64 {
	t.Helper()
	ss := cap.Find(name)
	if len(ss) == 0 {
		t.Fatalf("%s not found", name)
	}
	return ss[0].Value
}

// TestDefaultInstanceClassMemoryPct verifies that the default class (cache.r6g.large) and an empty
// InstanceClass (which the resolver/construct falls back to cache.r6g.large) produce identical
// memory% (SK-5, requirement c / default stability).
func TestDefaultInstanceClassMemoryPct(t *testing.T) {
	fx := stdFixture()
	fx.Cache.InstanceClass = "cache.r6g.large"
	mc := buildAndTick(t, fx)

	fxEmpty := stdFixture()
	fxEmpty.Cache.InstanceClass = ""
	mcEmpty := buildAndTick(t, fxEmpty)

	const pctName = "aws_elasticache_database_memory_usage_percentage_average"
	if got, gotEmpty := avgOf(t, mc, pctName), avgOf(t, mcEmpty, pctName); got != gotEmpty {
		t.Errorf("memory%% with explicit cache.r6g.large (%v) != empty InstanceClass fallback (%v)", got, gotEmpty)
	}
}

// TestMemoryPctIsClassIndependentAndRealistic verifies the production-like model (SK-5): utilisation
// is driven by load, not capacity, so database_memory_usage_percentage is identical across classes
// and sits in a realistic band (never pinned to 0 or 100).
func TestMemoryPctIsClassIndependentAndRealistic(t *testing.T) {
	const pctName = "aws_elasticache_database_memory_usage_percentage_average"
	classes := []string{"cache.t3.micro", "cache.r6g.large", "cache.r6g.xlarge"}
	var pcts []float64
	for _, cls := range classes {
		fx := stdFixture()
		fx.Cache.InstanceClass = cls
		pcts = append(pcts, avgOf(t, buildAndTick(t, fx), pctName))
	}
	for i, p := range pcts {
		if p != pcts[0] {
			t.Errorf("memory%% for %s (%v) != %s (%v); utilisation must be class-independent",
				classes[i], p, classes[0], pcts[0])
		}
		if p <= 20 || p >= 90 {
			t.Errorf("memory%% for %s = %v, want a realistic band (20,90)", classes[i], p)
		}
	}
}

// TestBytesUsedAndFreeableScaleWithClass verifies the working set and freeable memory both scale
// with node capacity (SK-5): a larger instance_class holds more data and has more headroom, and
// freeable == capacity - used stays consistent.
func TestBytesUsedAndFreeableScaleWithClass(t *testing.T) {
	const usedName = "aws_elasticache_bytes_used_for_cache_average"
	const freeName = "aws_elasticache_freeable_memory_average"
	classes := []string{"cache.t3.micro", "cache.r6g.large", "cache.r6g.xlarge"}
	var used, free []float64
	for _, cls := range classes {
		fx := stdFixture()
		fx.Cache.InstanceClass = cls
		mc := buildAndTick(t, fx)
		used = append(used, avgOf(t, mc, usedName))
		free = append(free, avgOf(t, mc, freeName))
	}
	for i := 1; i < len(classes); i++ {
		if used[i] <= used[i-1] {
			t.Errorf("bytes_used for %s (%v) should exceed %s (%v) — must scale with capacity",
				classes[i], used[i], classes[i-1], used[i-1])
		}
		if free[i] <= free[i-1] {
			t.Errorf("freeable_memory for %s (%v) should exceed %s (%v)", classes[i], free[i], classes[i-1], free[i-1])
		}
	}
	// Consistency: freeable must be positive (capacity - used), never the old frozen ~2 GiB.
	for i, cls := range classes {
		if free[i] <= 0 {
			t.Errorf("freeable_memory for %s = %v, want > 0", cls, free[i])
		}
	}
}

// TestUnknownInstanceClassFallsBackToDefault verifies that an unknown/unrecognized instance class
// falls back to the cache.r6g.large default and produces the same memory% (SK-5, requirement c).
func TestUnknownInstanceClassFallsBackToDefault(t *testing.T) {
	fxDefault := stdFixture()
	fxDefault.Cache.InstanceClass = "cache.r6g.large"
	mcDefault := buildAndTick(t, fxDefault)

	fxUnknown := stdFixture()
	fxUnknown.Cache.InstanceClass = "cache.x99.supergiant" // not in the map
	mcUnknown := buildAndTick(t, fxUnknown)

	const usedName = "aws_elasticache_bytes_used_for_cache_average"
	if d, u := avgOf(t, mcDefault, usedName), avgOf(t, mcUnknown, usedName); d != u {
		t.Errorf("unknown class bytes_used %v != default class %v (should fall back to cache.r6g.large)", u, d)
	}
}

// TestMultiNodeFan verifies that a fixture with multiple NodeIDs produces the expected
// number of per-node series (5 stats × 26 metrics × len(NodeIDs)) plus one info series.
func TestMultiNodeFan(t *testing.T) {
	fx := stdFixture()
	fx.Cache.NodeIDs = []string{"0001", "0002", "0003"}

	mc := buildAndTick(t, fx)

	const baseMetrics = 24 // cache_hit_rate + get_type_cmds removed (not real CW metrics — SK live reference audit)
	const statSuffixes = 5
	const infoSeries = 1
	wantCount := baseMetrics*statSuffixes*len(fx.Cache.NodeIDs) + infoSeries
	if got := len(mc.All()); got != wantCount {
		t.Errorf("multi-node series count: want %d got %d", wantCount, got)
	}

	// Each node ID must appear.
	nodesSeen := map[string]bool{}
	for _, s := range mc.All() {
		if nid := s.Labels["dimension_CacheNodeId"]; nid != "" {
			nodesSeen[nid] = true
		}
	}
	for _, nid := range fx.Cache.NodeIDs {
		if !nodesSeen[nid] {
			t.Errorf("node ID %q not seen in emitted series", nid)
		}
	}
}
