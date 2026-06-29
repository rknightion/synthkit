// SPDX-License-Identifier: AGPL-3.0-only

// Package k8sprofiling implements the "k8s_profiling" construct (ARCHITECTURE §2,
// kind="k8s_profiling", Scope=ScopeSubstrate). It emits Pyroscope profiles for every
// pod in a cluster that has one or more k8s-monitoring profiling feature lanes enabled.
//
// Contract: signals/profiles.md (emitter A/B/C families).
//
// Hard rules honoured here:
//   - Scope=ScopeSubstrate — no blueprint label, ever.
//   - eBPF lane  (Features["profiling"]):        source="alloy/pyroscope.ebpf", process_cpu only.
//   - pprof lane (Features["profiling_pprof"]):  source="alloy/pyroscope.pprof", Go only.
//   - Java lane  (Features["profiling_java"]):   source="alloy/pyroscope.java",  JVM only.
//   - Three independent toggles; any subset (including none) is valid.
//   - language= added on eBPF only when workload Runtime != "" (I13: omit unknown dims).
//   - Build requires fx.Cluster (error if nil).
package k8sprofiling

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/pyroscope"
	psink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

const (
	kind     = "k8s_profiling"
	interval = 60 * time.Second
)

// Config is empty — all identity comes from fx.Cluster.
type Config struct{}

// Construct renders Pyroscope profiles for every pod on a cluster across
// three independent feature-gated lanes: eBPF, pprof-scrape (Go), and Java.
type Construct struct {
	clust *fixture.Cluster
}

// New builds a Construct from cfg (unused — all from fixtures) and fx.
// Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("k8s_profiling: fixture.Cluster is required (nil)")
	}
	return &Construct{clust: fx.Cluster}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals declares PyroscopeProfiles only.
func (c *Construct) Signals() []core.SignalClass {
	return []core.SignalClass{core.PyroscopeProfiles}
}

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one batch of profiles for every applicable pod in the cluster
// across all enabled lanes. Returns nil immediately if the Pyroscope writer is
// absent or no profiling feature is enabled on the cluster.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if w.Pyroscope == nil {
		return nil
	}
	km := c.clust.K8sMonitoring

	ebpfEnabled := km.Features["profiling"]
	pprofEnabled := km.Features["profiling_pprof"]
	javaEnabled := km.Features["profiling_java"]

	if !ebpfEnabled && !pprofEnabled && !javaEnabled {
		return nil
	}

	factor := w.Shape.BusinessFactor(now)

	// Build failure-mode intensities for Load.Modes.
	// AxisCluster scope = cluster name (the declared disambiguation identity for substrate constructs).
	modes := map[string]float64{}
	if _, inten := w.Shape.Eval(now, "cpu_hotspot", c.clust.Name); inten > 0 {
		modes["cpu_hotspot"] = inten
	}
	load := pyroscope.Load{Factor: factor, Now: now, Modes: modes}

	var series []psink.Series

	for i := range c.clust.Workloads {
		wl := &c.clust.Workloads[i]

		for ri := range wl.PodNames {
			pod := wl.PodNames[ri]
			nodeIdx := wl.NodeIdx[ri]
			if nodeIdx >= len(c.clust.Nodes) {
				nodeIdx = 0
			}
			node := c.clust.Nodes[nodeIdx].Hostname

			// ── Lane A: eBPF (runtime-agnostic, process_cpu only) ───────────────────
			if ebpfEnabled {
				builder := pyroscope.NewBuilder("ebpf", wl.Name)
				profile := builder.Build(pyroscope.ProcessCPU, load)

				labels := []psink.LabelPair{
					{Name: "__name__", Value: "process_cpu"},
					{Name: "__profile_type__", Value: pyroscope.ProcessCPU.Selector()},
					{Name: "source", Value: "alloy/pyroscope.ebpf"},
					{Name: "service_name", Value: wl.Name},
					{Name: "service_namespace", Value: wl.Namespace},
					{Name: "namespace", Value: wl.Namespace},
					{Name: "pod", Value: pod},
					{Name: "node", Value: node},
					{Name: "container", Value: wl.Name},
					{Name: "cluster", Value: c.clust.Name},
					{Name: "k8s_cluster_name", Value: c.clust.Name},
					// NO blueprint label (ScopeSubstrate — I21).
					// NO span_id (eBPF lane is not span-correlated).
				}
				// language= only when Runtime is known (I13: omit absent dims).
				// Real data shows language=python on Python-runtime pods; absent on Go/Java.
				if wl.Runtime != "" {
					labels = append(labels, psink.LabelPair{Name: "language", Value: wl.Runtime})
				}

				series = append(series, psink.Series{Labels: labels, Profile: profile})
			}

			// ── Lane B: pprof-scrape (Go only) ──────────────────────────────────────
			if pprofEnabled && wl.Runtime == "go" {
				builder := pyroscope.NewBuilder("go", wl.Name)

				// Full Go pprof set but with GoroutinePprof (SINGULAR) replacing GoroutinesSDK.
				// RuntimeTypes("go") returns GoroutinesSDK (plural — SDK push path).
				// The pprof scrape path emits goroutine:goroutine:count:goroutine:count (singular).
				pprofTypes := make([]pyroscope.ProfileType, 0, len(pyroscope.RuntimeTypes("go")))
				for _, pt := range pyroscope.RuntimeTypes("go") {
					if pt == pyroscope.GoroutinesSDK {
						pprofTypes = append(pprofTypes, pyroscope.GoroutinePprof)
					} else {
						pprofTypes = append(pprofTypes, pt)
					}
				}

				coreLabels := []psink.LabelPair{
					{Name: "source", Value: "alloy/pyroscope.pprof"},
					{Name: "service_name", Value: wl.Name},
					{Name: "service_namespace", Value: wl.Namespace},
					{Name: "namespace", Value: wl.Namespace},
					{Name: "pod", Value: pod},
					{Name: "node", Value: node},
					{Name: "container", Value: wl.Name},
					{Name: "cluster", Value: c.clust.Name},
					{Name: "k8s_cluster_name", Value: c.clust.Name},
					{Name: "profiles_grafana_com_scrape", Value: "true"},
					// NO blueprint label (ScopeSubstrate).
					// NO span_id.
					// Discovery labels (app.kubernetes.io/*, helm_sh_chart, topology_*, instance)
					// are NOT in the fixture — OMITTED (see PENDING SK-69 in cantfind.md).
				}

				for _, pt := range pprofTypes {
					profile := builder.Build(pt, load)
					labels := make([]psink.LabelPair, 0, len(coreLabels)+2)
					labels = append(labels, psink.LabelPair{Name: "__name__", Value: pt.Name})
					labels = append(labels, psink.LabelPair{Name: "__profile_type__", Value: pt.Selector()})
					labels = append(labels, coreLabels...)
					series = append(series, psink.Series{Labels: labels, Profile: profile})
				}
			}

			// ── Lane C: Java async-profiler (JVM only) ──────────────────────────────
			if javaEnabled && wl.Runtime == "jvm" {
				builder := pyroscope.NewBuilder("jvm", wl.Name)
				// service_instance_id = <namespace>.<pod>.<container>
				svcInstanceID := fmt.Sprintf("%s.%s.%s", wl.Namespace, pod, wl.Name)

				coreLabels := []psink.LabelPair{
					{Name: "source", Value: "alloy/pyroscope.java"},
					{Name: "service_name", Value: wl.Name},
					{Name: "service_namespace", Value: wl.Namespace},
					{Name: "namespace", Value: wl.Namespace},
					{Name: "pod", Value: pod},
					{Name: "node", Value: node},
					{Name: "container", Value: wl.Name},
					{Name: "cluster", Value: c.clust.Name},
					{Name: "k8s_cluster_name", Value: c.clust.Name},
					{Name: "pyroscope_spy", Value: "alloy.java"},
					{Name: "jfr_event", Value: "itimer"},
					{Name: "service_instance_id", Value: svcInstanceID},
					// NO blueprint label (ScopeSubstrate).
					// NO span_id.
				}

				for _, pt := range pyroscope.RuntimeTypes("jvm") {
					profile := builder.Build(pt, load)
					labels := make([]psink.LabelPair, 0, len(coreLabels)+2)
					labels = append(labels, psink.LabelPair{Name: "__name__", Value: pt.Name})
					labels = append(labels, psink.LabelPair{Name: "__profile_type__", Value: pt.Selector()})
					labels = append(labels, coreLabels...)
					series = append(series, psink.Series{Labels: labels, Profile: profile})
				}
			}
		}
	}

	if len(series) == 0 {
		return nil
	}
	return w.Pyroscope.Write(ctx, series)
}

// FailureModes declares the failure modes this construct responds to.
var FailureModes = []failuremode.Mode{
	{
		Name: "cpu_hotspot",
		Axis: failuremode.AxisCluster,
		Help: "elevated CPU concentrated in a hot frame on the cluster's profiled pods",
	},
}
