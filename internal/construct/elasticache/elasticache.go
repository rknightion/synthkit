// SPDX-License-Identifier: AGPL-3.0-only

// Package elasticache implements the "elasticache" construct kind: AWS ElastiCache
// CloudWatch metrics for one Redis cache cluster.
//
// Build contract: fx.Cache and fx.Cloud must be non-nil (one cluster = one instance).
// Scope: ScopeBlueprint (the runner stamps the blueprint label).
// Signals: Metrics only. Interval: 60 s.
//
// All series are per-period gauges (ARCHITECTURE I5): CloudWatch _sum series represent
// the aggregate within the 1-minute reporting window, not a monotonic counter. Never
// apply rate() or increase() to any _sum series; use state.Set throughout.
//
// Naming (ARCHITECTURE I6): aws_elasticache_<metric>_<stat> where stat is one of
// _sum | _average | _maximum | _minimum | _sample_count (all five always emitted).
// Dimension labels preserve CW casing: dimension_CacheClusterId, dimension_CacheNodeId.
//
// cache_hits / cache_misses are emitted; there is NO cache_hit_rate metric (AWS publishes no
// derived ratio — live-reference-confirmed absent 2026-06-14). Hit rate is computed downstream from the two.
//
// An absent dimension is OMITTED — never "" or "NA" (ARCHITECTURE I13).
package elasticache

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is the per-instance YAML config struct. ElastiCache requires no additional
// configuration beyond what the fixture provides (name, engine, nodeIDs, cloud).
type Config struct{}

// construct holds the per-instance state and resolved fixture identity.
type construct struct {
	cache *fixture.Cache
	cloud *fixture.Cloud
	st    *state.State
}

// Reg is the ConstructReg entry for kind "elasticache". Wired into the catalog by the
// composition root — no init() self-registration (ARCHITECTURE §2).
var Reg = core.ConstructReg{
	Kind:      "elasticache",
	Doc:       "AWS ElastiCache CloudWatch metrics for one Redis cache cluster",
	Scope:     core.ScopeBlueprint,
	NewConfig: func() any { return &Config{} },
	Build:     build,
}

// nodeMaxMemBytes maps ElastiCache node types to their maximum memory in bytes, using the
// real AWS ElastiCache node-type memory figures (source: AWS ElastiCache supported node
// types documentation). Realism over the predecessor's frozen 2 GiB assumption: synthkit is
// corrected to match reality (SK-5). Unknown node types fall back to the cache.r6g.large value.
var nodeMaxMemBytes = map[string]float64{
	// t3 family (burstable)
	"cache.t3.micro":  0.5 * 1024 * 1024 * 1024,  // 0.5 GiB
	"cache.t3.small":  1.37 * 1024 * 1024 * 1024, // 1.37 GiB
	"cache.t3.medium": 3.09 * 1024 * 1024 * 1024, // 3.09 GiB

	// r6g family (memory-optimised, Graviton2) — sizes ~double per step (large→xlarge→2xlarge…).
	"cache.r6g.large":   13.07 * 1024 * 1024 * 1024,  // 13.07 GiB (real AWS value; was Ⓐ 2 GiB in the predecessor)
	"cache.r6g.xlarge":  26.32 * 1024 * 1024 * 1024,  // 26.32 GiB
	"cache.r6g.2xlarge": 52.82 * 1024 * 1024 * 1024,  // 52.82 GiB
	"cache.r6g.4xlarge": 105.81 * 1024 * 1024 * 1024, // 105.81 GiB
}

// maxMemBytesForClass returns the maximum memory in bytes for the given ElastiCache node
// type. Unknown types fall back to the cache.r6g.large default (13.07 GiB).
func maxMemBytesForClass(instanceClass string) float64 {
	if v, ok := nodeMaxMemBytes[instanceClass]; ok {
		return v
	}
	return nodeMaxMemBytes["cache.r6g.large"] // unknown class → r6g.large (13.07 GiB)
}

func build(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cache == nil {
		return nil, fmt.Errorf("elasticache: fx.Cache is nil (fixture.Set must carry a resolved Cache)")
	}
	if fx.Cloud == nil {
		return nil, fmt.Errorf("elasticache: fx.Cloud is nil (fixture.Set must carry a resolved Cloud)")
	}
	return &construct{
		cache: fx.Cache,
		cloud: fx.Cloud,
		st:    state.NewState(),
	}, nil
}

func (c *construct) Kind() string                { return "elasticache" }
func (c *construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one minute's worth of CW metrics for every node in the cluster and
// emits the batch. All series are per-period gauges (state.Set, never state.Add).
func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.render(now, w.Shape)
	return w.Metrics.Write(ctx, batch)
}

// render builds the per-period gauge batch for all node IDs.
func (c *construct) render(now time.Time, eng *shape.Engine) []promrw.Series {
	// Universal CW labels shared across all series for this cluster.
	baseLbls := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  "AWS/ElastiCache",
		"job":        "cloud/aws/elasticache",
		"name": fmt.Sprintf("arn:aws:elasticache:%s:%s:cluster:%s",
			c.cloud.Region, c.cloud.AccountID, c.cache.Name),
	}

	// Emit one set of series per node ID.
	for _, nodeID := range c.cache.NodeIDs {
		lbls := cloneWith(baseLbls, map[string]string{
			"dimension_CacheClusterId": c.cache.Name,
			"dimension_CacheNodeId":    nodeID,
		})
		c.renderNode(now, eng, lbls)
	}

	// aws_elasticache_info — per-cluster info series (no stat suffix, gauge=1).
	infoLbls := cloneWith(baseLbls, map[string]string{
		"dimension_CacheClusterId": c.cache.Name,
	})
	c.st.Set("aws_elasticache_info", infoLbls, 1)

	return c.st.Collect(now)
}

// renderNode emits all 24 per-node metric families (5 stat suffixes each) for one
// CacheNodeId. All values are per-period gauges (state.Set).
func (c *construct) renderNode(now time.Time, eng *shape.Engine, lbls map[string]string) {
	factor := eng.Factor(now, 1.0, false)*0.4 + 0.3 // 0.3–0.7 diurnal baseline

	// ── CPU ──────────────────────────────────────────────────────────────────
	cpuPct := clamp(15.0*factor+eng.NormFloat64()*2, 1, 95)
	setStats(c.st, "aws_elasticache_cpuutilization", lbls, cpuPct, eng)

	// Engine CPU is usually lower than host CPU (Redis single-threaded engine).
	engCPU := clamp(cpuPct*0.6, 0.1, 95)
	setStats(c.st, "aws_elasticache_engine_cpuutilization", lbls, engCPU, eng)

	// ── Connections ──────────────────────────────────────────────────────────
	currConn := clamp(50.0*factor+eng.NormFloat64()*5, 1, 1e6)
	setStats(c.st, "aws_elasticache_curr_connections", lbls, currConn, eng)
	setStats(c.st, "aws_elasticache_new_connections", lbls, currConn*0.1, eng)

	// ── Items / Memory ───────────────────────────────────────────────────────
	currItems := 50_000.0 * factor
	setStats(c.st, "aws_elasticache_curr_items", lbls, currItems, eng)

	// Working set scales with node capacity (a larger node holds more data) at a
	// production-like utilisation band driven by load, so memory% is realistic across
	// classes and bytes_used reflects the declared instance_class (SK-5).
	maxMemBytes := maxMemBytesForClass(c.cache.InstanceClass)
	memUtil := clamp(0.35+factor*0.4, 0.05, 0.95) // ~47–63% at the 0.3–0.7 diurnal factor
	bytesUsed := memUtil * maxMemBytes
	setStats(c.st, "aws_elasticache_bytes_used_for_cache", lbls, bytesUsed, eng)

	dbMemPct := clamp(memUtil*100, 0, 100)
	setStats(c.st, "aws_elasticache_database_memory_usage_percentage", lbls, dbMemPct, eng)

	// ── Cache Hit/Miss ────────────────────────────────────────────────────────
	// CacheHits/CacheMisses are real CW metrics. There is NO CacheHitRate metric —
	// AWS never publishes derived ratios (live-confirmed: absent from a live reference cluster 2026-06-14);
	// hit rate is computed downstream from hits/misses.
	hits := factor * 1_000 * 60 // ~60 k lookups/min at full load
	misses := hits * 0.05       // ~5% miss rate at baseline
	setStats(c.st, "aws_elasticache_cache_hits", lbls, hits, eng)
	setStats(c.st, "aws_elasticache_cache_misses", lbls, misses, eng)

	// ── Evictions / Reclaims ─────────────────────────────────────────────────
	setStats(c.st, "aws_elasticache_evictions", lbls, factor*0.5, eng)
	setStats(c.st, "aws_elasticache_reclaimed", lbls, factor*1.0, eng)

	// ── Replication ──────────────────────────────────────────────────────────
	repBytes := factor * 512 * 1024 // ~512 KiB/min
	setStats(c.st, "aws_elasticache_replication_bytes", lbls, repBytes, eng)
	setStats(c.st, "aws_elasticache_replication_lag", lbls, 0, eng) // primary node

	// ── Free Memory / Swap ───────────────────────────────────────────────────
	// freeable_memory is the consistent remainder of the node's capacity (SK-5).
	freeableMem := maxMemBytes - bytesUsed
	setStats(c.st, "aws_elasticache_freeable_memory", lbls, freeableMem, eng)
	setStats(c.st, "aws_elasticache_swap_usage", lbls, 0, eng) // near-zero at baseline

	// ── Network ──────────────────────────────────────────────────────────────
	netIn := factor * 10 * 1024 * 1024  // ~10 MiB/s
	netOut := factor * 15 * 1024 * 1024 // ~15 MiB/s
	setStats(c.st, "aws_elasticache_network_bytes_in", lbls, netIn, eng)
	setStats(c.st, "aws_elasticache_network_bytes_out", lbls, netOut, eng)

	// ── Memory Fragmentation ─────────────────────────────────────────────────
	memFrag := clamp(1.2+eng.NormFloat64()*0.1, 1.0, 5.0) // healthy ≈ 1.0–1.5
	setStats(c.st, "aws_elasticache_memory_fragmentation_ratio", lbls, memFrag, eng)

	// ── Commands ─────────────────────────────────────────────────────────────
	processedCmds := factor * 2_000 // ~2000 cmds/min
	setStats(c.st, "aws_elasticache_processed_commands", lbls, processedCmds, eng)

	// ── Error / Blocked ──────────────────────────────────────────────────────
	setStats(c.st, "aws_elasticache_error_count", lbls, 0, eng)
	setStats(c.st, "aws_elasticache_blocked_connections", lbls, 0, eng)

	// ── State flags ──────────────────────────────────────────────────────────
	setStats(c.st, "aws_elasticache_save_in_progress", lbls, 0, eng)
	setStats(c.st, "aws_elasticache_is_master", lbls, 1, eng) // single-node = always primary

	// ── Command type breakdown ────────────────────────────────────────────────
	// set_type_cmds is a real CW metric (live-confirmed in a live reference cluster 2026-06-14). There is NO
	// GetTypeCmds metric — get_type_cmds was synthkit-invented and is removed (absent from
	// a live reference cluster). The full real command-type family (cmd_*, key_based_cmds, etc.) is a phase-2 add.
	setStats(c.st, "aws_elasticache_set_type_cmds", lbls, processedCmds*0.4, eng)
}

// setStats emits the five CW stat suffixes for a per-period gauge metric (I5/I6).
// n=1 models one 1-minute CW aggregate point. All use state.Set (never state.Add)
// because CW _sum series are per-period, not monotonically cumulative.
func setStats(st *state.State, root string, lbls map[string]string, mean float64, eng *shape.Engine) {
	const n = 1.0 // 1-minute resolution = 1 CW aggregate point per period
	// Draw _maximum then _minimum jitter in that order — the RNG sequence is part of the
	// deterministic output; reordering would shift every subsequent draw. The suffix
	// mechanic + per-period-gauge rule (I5) live in cw.EmitStats.
	maxV := mean * (1.0 + eng.Float64()*0.4)
	minV := mean * (1.0 - eng.Float64()*0.3)
	cw.EmitStats(st, root, lbls, cw.StatSet{Sum: mean * n, Average: mean, Maximum: maxV, Minimum: minV, SampleCount: n})
}

// cloneWith returns a shallow copy of base with extra key-value pairs merged in.
func cloneWith(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// clamp returns v clamped to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
