// SPDX-License-Identifier: AGPL-3.0-only

// Package beylaagent implements the "beyla_agent" construct — Grafana Beyla's own
// self / internal metrics (the eBPF agent footprint), as scraped from a Beyla
// DaemonSet/process's /internal/metrics endpoint.
//
// Kind:     "beyla_agent"
// Scope:    core.ScopeSubstrate — substrate-scoped; NO blueprint label (I21). Series
//
//	separate by blueprint-declared identity (k8s_cluster_name+k8s_node_name in
//	kubernetes mode, host_name in standalone mode).
//
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Identity: Config-borne (mode + cluster/node | host). NO ledger, NO blueprint name.
//
// Signal contract: signals/beyla.md [slug: beyla-internal]. Metric NAMES come from the
// internal/beyla vocabulary lib (LAW, live-confirmed reference cluster Beyla 3.20.0 /internal/metrics).
//
// ⚠ The per-service beyla_avoided_services metric is NOT emitted here — it is emitted by the
// Beyla observation LANE on web_service (it has the service identity). This construct emits
// only agent/node-global self metrics.
//
// Mode differentiation:
//   - kubernetes: labels carry k8s_cluster_name + k8s_node_name + job=integrations/beyla.
//   - standalone: labels carry host_name; NO k8s_* labels (absent dims OMITTED — this
//     construct's OWN metrics follow the normal synthkit omit rule, unlike the Beyla-observed
//     RED/target_info lane which emits "" per the ABSENT-DIM TRAP).
package beylaagent

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "beyla_agent"

// Config is the construct's YAML config struct.
type Config struct {
	// Mode is the deployment substrate: "kubernetes" (default) | "standalone".
	Mode string `yaml:"mode"`
	// InstrumentedProcesses is the count of eBPF-instrumented processes on this node
	// (drives beyla_instrumented_processes). Default 4.
	InstrumentedProcesses int `yaml:"instrumented_processes"`
	// Version is the Beyla version stamped in the build-info gauge (default "1.9.0").
	Version string `yaml:"version"`
	// Revision is the Beyla git revision stamped in the build-info gauge (default "unknown").
	Revision string `yaml:"revision"`

	// Identity — kubernetes mode:
	Cluster string `yaml:"cluster"`
	Node    string `yaml:"node"`
	// Identity — standalone mode:
	Host string `yaml:"host"`
}

// NewConfig returns a pointer to a default-zero Config for the YAML decoder.
func NewConfig() any { return &Config{} }

const (
	defaultVersion    = "1.9.0"
	defaultRevision   = "unknown"
	defaultProcesses  = 4
	defaultGoVersion  = "go1.23.0"
	defaultMapEntries = 256
	defaultMapMax     = 16384
)

// NOTE: the two internal histograms (beyla_ebpf_tracer_flushes / beyla_kube_cache_forward_lag_seconds)
// are documented in signals/beyla.md but NOT emitted in v1 — their bucket arrays are not yet
// captured. The counters/gauges below cover the live-confirmed /internal/metrics set.

// Construct is the per-instance beyla_agent renderer. Not exported; callers use Build.
type Construct struct {
	mode      beyla.Mode
	cluster   string
	node      string
	host      string
	processes int
	version   string
	revision  string
	st        *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg, applies defaults, and returns a ready core.Construct. fx is unused —
// beyla_agent's identity is config-borne (substrate-scoped, no fixtures, no ledger).
func Build(cfg any, _ *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("beylaagent: Build called with %T, want *Config", cfg)
	}

	mode := beyla.Mode(c.Mode)
	if mode == "" {
		mode = beyla.ModeKubernetes
	}
	if mode != beyla.ModeKubernetes && mode != beyla.ModeStandalone {
		return nil, fmt.Errorf("beylaagent: invalid mode %q (want kubernetes|standalone)", c.Mode)
	}

	processes := c.InstrumentedProcesses
	if processes <= 0 {
		processes = defaultProcesses
	}
	version := c.Version
	if version == "" {
		version = defaultVersion
	}
	revision := c.Revision
	if revision == "" {
		revision = defaultRevision
	}

	return &Construct{
		mode:      mode,
		cluster:   c.Cluster,
		node:      c.Node,
		host:      c.Host,
		processes: processes,
		version:   version,
		revision:  revision,
		st:        state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// seriesVar returns a deterministic per-series multiplier ≈ 1±amp that varies both across
// peer series (via w.Shape.Spread on key) and slowly over time (via w.Shape.Wander). Layer this on
// top of the existing BusinessFactor/Factor coupling.
func (c *Construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// Tick renders one 60-second observation window into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)
	c.render(bf, now, w)
	if w.Metrics != nil {
		if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
			return err
		}
	}
	return nil
}

// identity returns the mode-dependent substrate-identity labels stamped on every LABELLED
// internal series (build-info, instrumented_processes, probe/map stats, prom requests). The
// no-label network-packet + OTel-export counters do NOT receive these (they carry no labels
// upstream — signals/beyla.md). Absent dims are OMITTED (normal synthkit rule).
func (c *Construct) identity() map[string]string {
	m := map[string]string{}
	if c.mode == beyla.ModeStandalone {
		if c.host != "" {
			m[beyla.LabelHostName] = c.host
		}
		return m
	}
	// kubernetes mode
	m[beyla.LabelJob] = "integrations/beyla"
	if c.cluster != "" {
		m[beyla.LabelK8sClusterName] = c.cluster
	}
	if c.node != "" {
		m[beyla.LabelK8sNodeName] = c.node
	}
	return m
}

// withIdentity merges per-series labels onto a fresh copy of the identity label set.
func (c *Construct) withIdentity(extra map[string]string) map[string]string {
	base := c.identity()
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			base[k] = v
		}
	}
	return base
}

// render builds the full per-tick internal-metric batch into c.st.
// now and w are threaded in so that seriesVar can apply per-series variation.
func (c *Construct) render(bf float64, now time.Time, w *core.World) {
	// ── Build info (gauge=1; static build labels). /internal/metrics surface. ──────────
	// Kept constant: build-info is an informational gauge that must always be exactly 1.
	c.st.Set(beyla.MetricInternalBuildInfo, c.withIdentity(map[string]string{
		"goarch":    runtime.GOARCH,
		"goos":      runtime.GOOS,
		"goversion": defaultGoVersion,
		"revision":  c.revision,
		"version":   c.version,
	}), 1)

	// ── Instrumented processes (gauge; per process_name) ───────────────────────────────
	// One series per modelled instrumented process; count of eBPF-attached processes.
	// instrumented_processes is always exactly 1 per process (it's a count gauge — kept constant).
	for i := 0; i < c.processes; i++ {
		proc := fmt.Sprintf("svc-%d", i)
		c.st.Set(beyla.MetricInstrumentedProcesses, c.withIdentity(map[string]string{
			"process_name": proc,
		}), 1)
		// Per-process instrumentation errors (counter; usually low). Apply seriesVar to
		// differentiate per-process rates — keyed on metric+process.
		key := beyla.MetricInstrumentationErrors + "|" + proc
		c.st.Add(beyla.MetricInstrumentationErrors, c.withIdentity(map[string]string{
			"process_name": proc,
			"error_type":   "attach",
		}), bf*0.01*c.seriesVar(w, now, key, 0.18))
	}

	// ── eBPF probe execution stats (counters; per probe identity) ───────────────────────
	probes := []struct{ id, name, typ string }{
		{"1", "kprobe_tcp_sendmsg", "kprobe"},
		{"2", "uprobe_http_handler", "uprobe"},
	}
	for _, p := range probes {
		plbls := c.withIdentity(map[string]string{
			"probe_id":   p.id,
			"probe_name": p.name,
			"probe_type": p.typ,
		})
		key := beyla.MetricBPFProbeExecutions + "|" + p.name
		c.st.Add(beyla.MetricBPFProbeExecutions, plbls, bf*5000*c.seriesVar(w, now, key, 0.18))
		c.st.Add(beyla.MetricBPFProbeLatency, plbls, bf*0.25*c.seriesVar(w, now, key+"_lat", 0.18)) // cumulative seconds
	}

	// ── eBPF map sizing (gauges; per map identity) ──────────────────────────────────────
	bpfMaps := []struct{ id, name, typ string }{
		{"10", "http_requests", "BPF_MAP_TYPE_HASH"},
		{"11", "active_connections", "BPF_MAP_TYPE_LRU_HASH"},
	}
	for _, m := range bpfMaps {
		mlbls := c.withIdentity(map[string]string{
			"map_id":   m.id,
			"map_name": m.name,
			"map_type": m.typ,
		})
		key := beyla.MetricBPFMapEntries + "|" + m.name
		c.st.Set(beyla.MetricBPFMapEntries, mlbls, float64(defaultMapEntries)*bf*c.seriesVar(w, now, key, 0.18))
		// map_max_entries is a static capacity configuration value — keep constant.
		c.st.Set(beyla.MetricBPFMapMaxEntries, mlbls, defaultMapMax)
	}

	// ── eBPF network packet counters (NO labels — signals/beyla.md) ─────────────────────
	// Single series each: no peer variation needed. Apply Wander for temporal drift.
	c.st.Add(beyla.MetricBPFNetworkPackets, map[string]string{}, bf*20000*c.seriesVar(w, now, beyla.MetricBPFNetworkPackets, 0.18))
	c.st.Add(beyla.MetricBPFNetworkIgnoredPkts, map[string]string{}, bf*300*c.seriesVar(w, now, beyla.MetricBPFNetworkIgnoredPkts, 0.18))

	// ── Export / scrape counters ────────────────────────────────────────────────────────
	// OTel export counters carry NO labels (signals/beyla.md). Single series — temporal drift only.
	c.st.Add(beyla.MetricOTelMetricExports, map[string]string{}, bf*60*c.seriesVar(w, now, beyla.MetricOTelMetricExports, 0.18))
	c.st.Add(beyla.MetricOTelTraceExports, map[string]string{}, bf*120*c.seriesVar(w, now, beyla.MetricOTelTraceExports, 0.18))
	// Prometheus scrape requests carry path + port labels. Single series — temporal drift only.
	c.st.Add(beyla.MetricPromHTTPRequests, c.withIdentity(map[string]string{
		"path": "/internal/metrics",
		"port": "9090",
	}), bf*60*c.seriesVar(w, now, beyla.MetricPromHTTPRequests, 0.18))
}
