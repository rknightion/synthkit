// SPDX-License-Identifier: AGPL-3.0-only

// Package k8scluster implements the "k8s_cluster" construct (ARCHITECTURE §2,
// kind="k8s_cluster", Scope=ScopeSubstrate). It emits the full Grafana
// k8s-monitoring signal set for one EKS cluster declared in a blueprint.
//
// Contract: signals/k8s.md.
//
// Hard rules honoured here (ARCHITECTURE + extract):
//   - Scope=ScopeSubstrate — no blueprint label, ever. The cluster label disambiguates.
//   - Every series carries cluster AND k8s_cluster_name (same value) + source="kubernetes".
//   - Exact job label strings are load-bearing for dashboard variable queries.
//   - kube_pod_status_phase: ALL four phase values emitted per pod (real KSM behaviour).
//   - container_network_*: pod-scoped (NO container label); adds interface="eth0".
//   - node-exporter series carry the DaemonSet pod labels.
//   - alloy_build_info version MUST start with "v" (conformance check regex).
//   - Cumulative counters use state.Add; gauges use state.Set (I3).
//   - Build requires fx.Cluster (error if nil).
package k8scluster

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "k8s_cluster"
	interval = 60 * time.Second
)

// Config is empty — all identity comes from fx.Cluster.
type Config struct{}

// Construct renders k8s-monitoring substrate telemetry for one cluster.
type Construct struct {
	clust *fixture.Cluster
	st    *state.State

	// High-water marks for scale-down retirement: the largest pod count ever seen per workload
	// and the largest node count ever derived. On a scale-down, ordinals/nodes that existed at the
	// high-water mark but are gone now are dropped from state so they stop re-emitting (go stale in
	// Prometheus) rather than freezing at their last value. nil until the first Tick.
	maxPods  map[string]int
	maxNodes int
}

// New builds a Construct from cfg (unused — all from fixtures) and fx.
// Returns error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("k8s_cluster: fixture.Cluster is required (nil)")
	}
	return &Construct{
		clust:   fx.Cluster,
		st:      state.NewState(),
		maxPods: map[string]int{},
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals declares Metrics and Logs.
func (c *Construct) Signals() []core.SignalClass {
	return []core.SignalClass{core.Metrics, core.Logs}
}

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one full k8s-monitoring snapshot for the cluster.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	// Build the live cluster view (per-tick local copy; the shared c.clust is never mutated). At
	// default scaling (nil w.Scaling) this reproduces c.clust's pods + nodes byte-for-byte.
	lc, replicas := c.liveCluster(w)
	cl := &lc
	cluster := cl.Name
	nodes := lc.Nodes
	km := cl.K8sMonitoring

	// shape factor — use env weight/non-prod from the cluster's env
	factor := 0.5
	if cl.Env != nil {
		factor = w.Shape.Factor(now, cl.Env.Weight, cl.Env.NonProd)
	}

	tickSec := interval.Seconds()
	scale := tickSec / 30.0

	// ── KSM families ────────────────────────────────────────────────────────
	emitKSMNodeObjects(c.st, cluster, cl, nodes, factor)
	emitKSMNamespacePhase(c.st, cluster, cl)
	emitKSMDeploymentMeta(c.st, cluster, cl, replicas)
	emitKSMReplicaSets(c.st, cluster, cl, replicas)
	emitKSMStatefulSets(c.st, cluster, cl, replicas)
	emitKSMDaemonSets(c.st, cluster, cl, len(nodes))
	emitKSMJobsCron(c.st, cluster, cl)
	emitKSMHPAs(c.st, cluster, cl, replicas, factor)
	emitKSMStorage(c.st, cluster, cl)
	emitKSMQuotaConfigSecret(c.st, cluster, cl)

	// node_not_ready (cluster-axis failure): one node flips Ready true→false. nodeNotReadyIdx<0
	// means no node is down. Both the node condition and that node's pod phases honour it.
	notReadyIdx := -1
	if w.Shape.Active(now, "node_not_ready", cluster) && len(nodes) > 0 {
		notReadyIdx = 0
	}
	emitKSMNodeConditions(c.st, cluster, cl, nodes, notReadyIdx)
	emitKSMPods(c.st, cluster, cl, nodes, replicas, scale, now, w, notReadyIdx)

	// ── cAdvisor ────────────────────────────────────────────────────────────
	emitCAdvisor(c.st, cluster, cl, nodes, replicas, factor, tickSec, scale, now, w)

	// ── node-exporter ────────────────────────────────────────────────────────
	emitNodeExporter(c.st, cluster, cl, nodes, factor, tickSec, scale, w)

	// ── kubelet ──────────────────────────────────────────────────────────────
	emitKubelet(c.st, cluster, cl, nodes, replicas, tickSec, scale, w)

	// ── kubelet resources ────────────────────────────────────────────────────
	emitKubeletResources(c.st, cluster, nodes, factor, tickSec)

	// ── conformance (gated on K8sMonitoring flags) ───────────────────────────
	if km.Enabled {
		emitConformance(c.st, cluster, cl, nodes, replicas, tickSec, scale, km, w)
	}

	// ── host info + control-plane families (gated by ControlPlane flags) ─────
	emitHostInfo(c.st, cluster, cl, nodes)
	if km.ControlPlane.KubeProxy {
		emitKubeProxy(c.st, cluster, nodes, tickSec, scale)
	}
	if km.ControlPlane.ApiServer {
		emitApiServer(c.st, cluster, tickSec, scale)
	}
	if km.ControlPlane.KubeScheduler {
		emitScheduler(c.st, cluster, tickSec, scale)
	}
	if km.ControlPlane.KubeControllerManager {
		emitControllerManager(c.st, cluster, tickSec, scale)
	}
	emitWindowsExporter(c.st, cluster, cl, nodes, factor, tickSec, scale, w)

	// ── scale-down retirement ─────────────────────────────────────────────────
	// Drop series for pods/nodes that existed at the high-water mark but are gone now, so they
	// stop re-emitting (state retains every series ever Set and re-emits on Collect).
	c.retireScaledAway(&lc)

	// ── metrics flush ────────────────────────────────────────────────────────
	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}

	// ── k8s Events + node logs + pod logs (Loki) ─────────────────────────────
	if err := emitEvents(ctx, now, cluster, cl, w); err != nil {
		return err
	}
	if err := emitNodeLogs(ctx, now, cluster, cl, w); err != nil {
		return err
	}
	return emitPodLogs(ctx, now, cluster, cl, w)
}

// liveCluster returns a per-tick COPY of the cluster with live pod counts + a cascaded node set,
// plus the scalar `replicas` (max live workload replica count) the legacy emit helpers expect. The
// shared c.clust is NEVER mutated. At default scaling (nil w.Scaling) it reproduces c.clust's pods
// and nodes byte-for-byte: PodNames/NodeIdx are regenerated from the SAME seed + ordinal as the
// resolver used, and DeriveNodes is the same pure function buildNodes delegates to.
// clusterSeed is the CLUSTER-SCOPED pod-name seed (seed:cluster). The resolver mints app-workload
// pod names with this (resolve.go), so every live re-mint here MUST use it too — otherwise pods
// would revert to the bare-seed (cross-cluster-colliding) form and diverge from the -dump baseline
// on any live scaling override. Byte-for-byte identical to the resolver: cl.Seed is the bare
// blueprint seed and cl.Name is the cluster name (resolve.go sets both).
func clusterSeed(cl *fixture.Cluster) string { return cl.Seed + ":" + cl.Name }

func (c *Construct) liveCluster(w *core.World) (fixture.Cluster, int) {
	lc := *c.clust // shallow copy; the two slices below are replaced (originals untouched).

	// Default scaling: preserve the resolved workloads + nodes EXACTLY (byte-for-byte parity, so
	// existing tests and the -dump inventory are unaffected). Only when a live override is present
	// do we re-derive identities from the cluster seed/ordinals.
	if w.Scaling == nil {
		maxRep := 1
		for _, wl := range c.clust.Workloads {
			if wl.Replicas > maxRep {
				maxRep = wl.Replicas
			}
		}
		return lc, maxRep
	}

	total := 0
	maxRep := 1
	lc.Workloads = make([]fixture.Workload, len(c.clust.Workloads))
	for i, wl := range c.clust.Workloads {
		// DaemonSet replicas track the node count (not a scaling target) and never inflate node
		// demand — handle pod names/placement below, once the live node set is known.
		if fixture.IsDaemonSet(wl.Controller) {
			lc.Workloads[i] = fixture.Workload{
				Name: wl.Name, Namespace: wl.Namespace, Controller: wl.Controller,
				HasHPA: wl.HasHPA, VolumeClaims: wl.VolumeClaims, Runtime: wl.Runtime, Resources: wl.Resources,
			}
			continue
		}
		n := w.Scaling.Count(wl.Name, wl.Replicas)
		if n < 0 {
			n = 0
		}
		total += n
		if n > maxRep {
			maxRep = n
		}
		nw := fixture.Workload{
			Name: wl.Name, Namespace: wl.Namespace, Replicas: n, Controller: wl.Controller,
			HasHPA: wl.HasHPA, VolumeClaims: wl.VolumeClaims, Runtime: wl.Runtime, Resources: wl.Resources,
		}
		// If the live count equals the declared count, keep the resolver-minted identities verbatim
		// (so a no-op override still reproduces the fixture); otherwise mint controller-aware from the
		// cluster seed (StatefulSet → ordinal names, Deployment → ReplicaSet form — never reverting a
		// StatefulSet to the ReplicaSet form, the live-revert bug).
		if n == wl.Replicas && len(wl.PodNames) == n {
			nw.PodNames = append(nw.PodNames, wl.PodNames...)
		} else {
			nw.PodNames = fixture.WorkloadPodNames(clusterSeed(c.clust), nw, nil)
		}
		lc.Workloads[i] = nw
	}
	lc.Nodes = fixture.LiveNodes(c.clust, w.Scaling.Count)
	if len(lc.Nodes) == 0 {
		// No node groups declared (e.g. minimal fixtures): fall back to the resolved node set so
		// pods still place. Determinism is preserved (same resolved nodes every tick).
		lc.Nodes = c.clust.Nodes
	}
	for i := range lc.Workloads {
		wl := &lc.Workloads[i]
		// DaemonSet: one pod per current node (names keyed by node hostname so they survive node-count
		// changes; the `node` label lets retireScaledAway drop a pod when its node is retired).
		if fixture.IsDaemonSet(wl.Controller) {
			wl.PodNames = fixture.WorkloadPodNames(clusterSeed(c.clust), *wl, lc.Nodes)
			wl.Replicas = len(lc.Nodes)
			for ni := range lc.Nodes {
				wl.NodeIdx = append(wl.NodeIdx, ni)
			}
			continue
		}
		for p := 0; p < wl.Replicas; p++ {
			wl.NodeIdx = append(wl.NodeIdx, p%len(lc.Nodes))
		}
	}
	return lc, maxRep
}

// retireScaledAway drops state series for pod ordinals and node identities that existed at the
// running high-water mark but are gone at the current live counts. It only retires identities it
// KNOWS are managed (scalable-workload pod ordinals + derived node hostnames) — substrate pods
// (alloy) and node-exporter pods are never minted from a scalable workload, so they are never
// dropped. A no-op when nothing shrank (steady state / scale-up).
func (c *Construct) retireScaledAway(lc *fixture.Cluster) {
	if c.maxPods == nil {
		c.maxPods = map[string]int{}
	}
	// Pods retired since the high-water mark, by deterministic name.
	retiredPods := map[string]bool{}
	for _, wl := range lc.Workloads {
		// DaemonSet pods are keyed by node hostname (not replica ordinal); they are retired via the
		// node-retire path (the `node` label check below) when their node disappears, never here.
		if fixture.IsDaemonSet(wl.Controller) {
			continue
		}
		hw := c.maxPods[wl.Name]
		for i := wl.Replicas; i < hw; i++ {
			retiredPods[fixture.PodName(clusterSeed(c.clust), wl.Name, i)] = true
		}
		if wl.Replicas > c.maxPods[wl.Name] {
			c.maxPods[wl.Name] = wl.Replicas
		}
	}

	// Nodes still present this tick (by hostname — the `node`/`instance` label value).
	activeNodes := map[string]bool{}
	for _, n := range lc.Nodes {
		activeNodes[n.Hostname] = true
	}
	nodesShrank := len(lc.Nodes) < c.maxNodes
	if len(lc.Nodes) > c.maxNodes {
		c.maxNodes = len(lc.Nodes)
	}

	if len(retiredPods) == 0 && !nodesShrank {
		return
	}
	c.st.DropWhere(func(_ string, lbls map[string]string) bool {
		// Pod-keyed series (kube_pod_*, cAdvisor, kepler, opencost) carry the `pod` label.
		if p := lbls["pod"]; p != "" && retiredPods[p] {
			return true
		}
		// Node-keyed series carry the node hostname under `node` (KSM/cAdvisor/kubelet/opencost) or
		// `instance` (node-exporter/cAdvisor/kubelet, where instance == node hostname).
		if nodesShrank {
			if nd := lbls["node"]; nd != "" && !activeNodes[nd] {
				return true
			}
			if inst := lbls["instance"]; inst != "" && !activeNodes[inst] && looksLikeNodeHost(inst) {
				return true
			}
		}
		return false
	})
}

// looksLikeNodeHost reports whether an `instance` label value is a node hostname (so node-exporter /
// cadvisor / kubelet series for a retired node are dropped) rather than a fixed pod IP:port (KSM /
// alloy / opencost telemetry instances, which must never be dropped on a node scale-down).
func looksLikeNodeHost(instance string) bool {
	return strings.HasSuffix(instance, ".compute.internal")
}
