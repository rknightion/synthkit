// SPDX-License-Identifier: AGPL-3.0-only

// Package fleetmgmt implements the "fleet_management" construct.
//
// Kind:     "fleet_management"
// Scope:    ScopeSubstrate  — no blueprint label ever (I21/§5)
// Signals:  [Metrics]
// Interval: 60s
//
// Emits the Alloy self-metric set per fake Fleet Management collector — the series
// that populate the Alloy integration "Collector" dashboard (running-component counts,
// evaluation histogram, resource gauges). The FM API registration and heartbeat are
// out-of-scope for this lane (Phase 6 fleet controller); this construct only emits
// metrics.
//
// Config:
//
//	collectors_per_os: { linux: 6, darwin: 2 }  # any subset of [linux, windows, darwin]
//
// Mirror mode is DERIVED from the fixture (DD6b): when a cluster fixture is present and
// its k8s_monitoring.fleet_management is true, the construct mirrors that cluster's
// k8s-monitoring collector fleet. Mirror mode is mutually exclusive with collectors_per_os:
// a cluster-path mirror instance carries an empty Config.
//
// Collector identities are DETERMINISTIC from fx.Seed via fixture.HexID so the same
// blueprint always produces the same collector_id / instance values across runs.
//
// Counters are cumulative (state.Add — I3); gauges use state.Set.
// alloy_build_info carries a "v"-prefixed version (I22/T10 in the extract).
//
// Reference: signals/fm.md [slug: fm-fleet]
package fleetmgmt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key for this construct.
const Kind = "fleet_management"

// collectorVersion is the Alloy version stamped on alloy_build_info. Must be
// "v"-prefixed (I22 / ARCHITECTURE T10: k8s-monitoring plugin filters
// alloy_build_info{version=~"v.+"}).
const collectorVersion = "v1.16.3"

// Component counts per collector.
const (
	fmComponentCount = 24 // running components per healthy fake collector
	fmUnhealthyCount = 4  // components flipped to unhealthy on an unhealthy collector
	fmEvalSecPerTick = 0.012
)

// fmEvalBuckets mirrors Alloy's controller evaluation histogram buckets.
var fmEvalBuckets = []float64{.005, .025, .1, .5, 1, 5, 10, 30, 60, 120, 300, 600}

// Config is the construct config struct decoded from blueprint YAML.
type Config struct {
	// CollectorsPerOS maps OS name (linux/windows/darwin) → desired fake collector count.
	// Absent OS types emit no collectors (not an error — I13: absent ⇒ omitted).
	CollectorsPerOS map[string]int `yaml:"collectors_per_os"`
}

// NewConfig returns an empty *Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// collectorSpec is the fully-resolved identity of one fake Alloy collector.
type collectorSpec struct {
	id         string // "fleet-<os>-<i:02d>-<hexsuffix>"  (deterministic per seed+os+index)
	instance   string // "alloy-<os>-<i:02d>-<hexsuffix>" (standalone) or pod IP:port (k8s)
	cluster    string // from fx.Cluster.Name (required)
	os         string // "linux" | "windows" | "darwin"
	healthy    bool   // true in normal operation
	version    string // per-collector Alloy version; "" ⇒ collectorVersion (standalone)
	namespace  string // k8s namespace ("" ⇒ standalone, label stays "infra")
	app        string // short role "alloy-logs" ("" ⇒ standalone — no k8s labels)
	workload   string // full "<release>-alloy-logs"
	controller string // "daemonset"|"statefulset"|"deployment"
	pod        string // k8s pod name
}

// specFromCollector adapts an exported Collector into the internal collectorSpec.
func specFromCollector(c Collector) collectorSpec {
	return collectorSpec{
		id:         c.ID,
		instance:   c.Instance,
		cluster:    c.Cluster,
		os:         c.OS,
		healthy:    true,
		version:    c.Version,
		namespace:  c.Namespace,
		app:        c.App,
		workload:   c.Workload,
		controller: c.Controller,
		pod:        c.Pod,
	}
}

// mirrorEnabled reports whether this instance should mirror a cluster's k8s-monitoring
// collectors. Derived from the fixture (DD6b): a cluster-path instance carries a cluster
// whose k8s_monitoring has FleetManagement set; the feature-path standalone instance
// carries no cluster.
func mirrorEnabled(fx *fixture.Set) bool {
	return fx != nil && fx.Cluster != nil &&
		fx.Cluster.K8sMonitoring.Enabled && fx.Cluster.K8sMonitoring.FleetManagement
}

// Construct is one fleet_management instance covering all declared OS types or mirroring
// a cluster's k8s-monitoring collector fleet.
type Construct struct {
	collectors []collectorSpec  // STANDALONE collectors (fixed at Build time)
	cluster    *fixture.Cluster // non-nil ⇒ mirror this cluster's k8s-monitoring collectors
	st         *state.State
	lastIDs    map[string]bool // roster ID-set last seen — rebuild st on change (departed collector guard)
}

// Build validates cfg and fx, resolves the collector roster deterministically, and
// returns a ready Construct. fx.Cluster is OPTIONAL: fleet collectors are standalone
// machines, so when no cluster fixture is present the cluster/k8s_cluster_name labels
// are OMITTED entirely (I13 — never a sentinel). fx.Seed drives deterministic identity.
//
// Mirror mode is derived from the fixture (DD6b): when fx.Cluster.K8sMonitoring.FleetManagement
// is true, the construct operates in mirror mode. An empty CollectorsPerOS is allowed in
// mirror mode (the k8s roster takes the place of standalone collectors).
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("fleetmgmt: Build called with %T, want *Config", cfg)
	}
	if fx == nil {
		return nil, fmt.Errorf("fleetmgmt: fixture.Set is required (nil)")
	}

	seed := fx.Seed
	cluster := ""
	if fx.Cluster != nil {
		cluster = fx.Cluster.Name
	}
	out := &Construct{
		collectors: buildRoster(seed, cluster, c.CollectorsPerOS),
		st:         state.NewState(),
		lastIDs:    map[string]bool{},
	}
	if mirrorEnabled(fx) {
		out.cluster = fx.Cluster
	}
	if len(out.collectors) == 0 && out.cluster == nil {
		return nil, fmt.Errorf("fleetmgmt: no collectors — set collectors_per_os or enable cluster k8s_monitoring.fleet_management")
	}
	return out, nil
}

// buildRoster constructs the deterministic collector roster from config.
// Ordering: for each OS, collectors 0..n-1; OS order follows osOrder to be stable.
func buildRoster(seed, cluster string, perOS map[string]int) []collectorSpec {
	// Stable OS ordering so roster is reproducible regardless of map iteration order.
	osOrder := []string{"linux", "windows", "darwin"}
	var out []collectorSpec
	for _, os := range osOrder {
		n, ok := perOS[os]
		if !ok || n <= 0 {
			continue
		}
		for i := range n {
			// Deterministic 8-hex suffix from seed+os+index so IDs are stable across runs.
			suffix := fixture.HexID(seed, 8, "fm", os, fmt.Sprintf("%02d", i))
			id := fmt.Sprintf("fleet-%s-%02d-%s", os, i, suffix)
			instance := fmt.Sprintf("alloy-%s-%02d-%s", os, i, suffix)
			out = append(out, collectorSpec{
				id:       id,
				instance: instance,
				cluster:  cluster,
				os:       os,
				healthy:  true, // all healthy at baseline
			})
		}
	}
	return out
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one metric batch covering all fake collectors in the roster.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.BuildWith(now, w)
	if len(batch) == 0 {
		return nil
	}
	return w.Metrics.Write(ctx, batch)
}

// liveCollectors returns the standalone roster plus, when mirroring, the cluster's
// k8s-monitoring collectors derived from the CURRENT live node set.
//
// The node set comes from fixture.LiveNodes(c.cluster, count) where count resolves live
// replica counts from w.Scaling (declared defaults when w/Scaling is nil). This is the same
// pure derivation the fleet controller's RosterProvider uses, so DaemonSet collector
// identities stay byte-identical between emitted metrics and FM registration — and the set
// churns in lockstep as nodes scale (the T8 guard in BuildWith drops departed series).
func (c *Construct) liveCollectors(w *core.World) []collectorSpec {
	specs := c.collectors
	if c.cluster != nil {
		count := func(target string, declared int) int { return declared } // nil scaling ⇒ declared counts
		if w != nil && w.Scaling != nil {
			count = w.Scaling.Count
		}
		nodes := fixture.LiveNodes(c.cluster, count)
		for _, kc := range K8sRoster(c.cluster.Seed, "", c.cluster.Name, "", nodes, c.cluster.K8sMonitoring) {
			specs = append(specs, specFromCollector(kc))
		}
	}
	return specs
}

// BuildWith accumulates one tick into c.st and returns the collected series. Exported so
// tests in the _test package can assert the wire contract without a live sink.
func (c *Construct) BuildWith(now time.Time, w *core.World) []promrw.Series {
	live := c.liveCollectors(w)
	ids := make(map[string]bool, len(live))
	for _, spec := range live {
		ids[spec.id] = true
	}
	if !sameIDSet(ids, c.lastIDs) {
		c.st = state.NewState() // T8 churn guard: drop departed collectors' series
		c.lastIDs = ids
	}

	for _, spec := range live {
		// Base label set — present on ALL series for this collector. The cluster pair
		// appears only when a cluster fixture was bound (standalone fleets omit it, I13).
		// k8s collectors override namespace from "infra" to the monitoring namespace.
		base := map[string]string{
			"job":          "integrations/alloy",
			"namespace":    "infra",
			"instance":     spec.instance,
			"collector_id": spec.id,
			"os":           spec.os,
		}
		if spec.app != "" {
			// k8s-mirror collector: reproduce the real k8s-monitoring Alloy self-metric label set
			// (live-captured from a live reference cluster 2026-06-15). These metrics carry NO `os` label (real uses
			// goos on build_info only); collector_id is retained as a synthkit metrics↔FM-registration
			// correlation label (real k8s collectors don't emit it).
			delete(base, "os")
			base["namespace"] = spec.namespace // monitoring
			base["app"] = spec.app
			base["workload"] = spec.workload
			base["workload_type"] = metricWorkloadType(spec.controller) // deployment→replicaset
			base["pod"] = spec.pod
			base["source"] = "kubernetes"
			base["container"] = "alloy"
		}
		if spec.cluster != "" {
			base["cluster"] = spec.cluster
			base["k8s_cluster_name"] = spec.cluster
		}

		// alloy_build_info: extra label `version` with "v"-prefixed value (I22/T10).
		ver := spec.version
		if ver == "" {
			ver = collectorVersion
		}
		infoLabels := copyLabels(base)
		infoLabels["version"] = ver
		c.st.Set("alloy_build_info", infoLabels, 1)

		// up / component counts.
		upVal := 1.0
		healthy := float64(fmComponentCount)
		unhealthy := 0.0
		if !spec.healthy {
			upVal = 0
			healthy = fmComponentCount - fmUnhealthyCount
			unhealthy = fmUnhealthyCount
		}
		c.st.Set("up", base, upVal)

		healthyLabels := copyLabels(base)
		healthyLabels["health_type"] = "healthy"
		c.st.Set("alloy_component_controller_running_components", healthyLabels, healthy)

		unhealthyLabels := copyLabels(base)
		unhealthyLabels["health_type"] = "unhealthy"
		c.st.Set("alloy_component_controller_running_components", unhealthyLabels, unhealthy)

		unknownLabels := copyLabels(base)
		unknownLabels["health_type"] = "unknown"
		c.st.Set("alloy_component_controller_running_components", unknownLabels, 0)

		exitedLabels := copyLabels(base)
		exitedLabels["health_type"] = "exited"
		c.st.Set("alloy_component_controller_running_components", exitedLabels, 0)

		// alloy_component_evaluation_seconds: one observation per component per tick.
		// Evaluation latency varies per collector (resource-shaped, amp 0.18).
		evalSec := fmEvalSecPerTick * seriesVar(w, now, "alloy_eval/"+spec.id, 0.18)
		for range fmComponentCount {
			c.st.Observe("alloy_component_evaluation_seconds", base, fmEvalBuckets, state.LEBare, evalSec)
		}

		// Resource counters — increment varies per collector (volume-shaped, amp 0.30).
		cpuInc := 0.02 * seriesVar(w, now, "cpu/"+spec.id, 0.30)
		c.st.Add("alloy_resources_process_cpu_seconds_total", base, cpuInc)

		// Memory gauge — varies per collector (resource-shaped, amp 0.18).
		memVar := seriesVar(w, now, "mem/"+spec.id, 0.18)
		c.st.Set("alloy_resources_process_resident_memory_bytes", base, 110e6*memVar)

		// Network counters — vary per collector (volume-shaped, amp 0.30).
		rxInc := 50000 * seriesVar(w, now, "rx/"+spec.id, 0.30)
		txInc := 30000 * seriesVar(w, now, "tx/"+spec.id, 0.30)
		c.st.Add("alloy_resources_machine_rx_bytes_total", base, rxInc)
		c.st.Add("alloy_resources_machine_tx_bytes_total", base, txInc)

		// alloy_config_hash: deterministic per-collector hash label. CONSTANT — config hash
		// is a static config value, not a live measurement.
		hashLabels := copyLabels(base)
		hashLabels["hash"] = sha256Hex(spec.id)
		c.st.Set("alloy_config_hash", hashLabels, 1)

		// go_goroutines — varies per collector (resource-shaped, amp 0.18).
		goVar := seriesVar(w, now, "goroutines/"+spec.id, 0.18)
		c.st.Set("go_goroutines", base, 80*goVar)

		// go_memstats_heap_inuse_bytes — varies per collector (resource-shaped, amp 0.18).
		heapVar := seriesVar(w, now, "heap/"+spec.id, 0.18)
		c.st.Set("go_memstats_heap_inuse_bytes", base, 45e6*heapVar)

		// scrape_duration_seconds — varies per collector (rate-shaped, amp 0.30).
		scrapeVar := seriesVar(w, now, "scrape/"+spec.id, 0.30)
		c.st.Set("scrape_duration_seconds", base, 0.011*scrapeVar)
	}
	return c.st.Collect(now)
}

// seriesVar returns a multiplicative per-series variation factor ≈ 1±amp. The factor
// is a product of a time-invariant spread component (keyed hash → static phase offset)
// and a slow-drifting Wander component, giving each series a distinct baseline AND
// time-varying drift so peer series never move in lockstep.
//
// Returns 1.0 when w==nil or w.Shape==nil (zero-Shape tests are unaffected).
func seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	// Spread: deterministic time-invariant per-key baseline offset.
	spread := w.Shape.Spread(key, amp)
	// Wander: slow sinusoidal drift at the actual time.
	wander := w.Shape.Wander(key, now, amp*0.4)
	return spread * wander
}

// sha256Hex returns the hex-encoded SHA-256 digest of s (deterministic per-collector
// config hash — matches the predecessor's fm_collectors.go pattern).
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// sameIDSet reports whether a and b contain exactly the same keys.
func sameIDSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

// copyLabels returns a shallow copy of a label map so each series variant carries its
// own independent map (state.Set's signature-based keying requires independence).
func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}
