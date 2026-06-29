// SPDX-License-Identifier: AGPL-3.0-only

// Package clusterautoscaler implements the "cluster_autoscaler" construct.
//
// Kind:     "cluster_autoscaler"
// Scope:    core.ScopeSubstrate (cluster disambiguates; no blueprint label)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   min_nodes (default 3), max_nodes (default 10)
//
// Build requires fx.Cluster (non-nil).
//
// Signal contract (signals/k8s-addons.md [slug: k8s-cluster-autoscaler]):
//
//	Families: cluster_autoscaler_* + rest_client_*
//	Job:      "integrations/cluster-autoscaler"
//	Labels:   cluster + k8s_cluster_name on every series; NO blueprint label
//
// Intentionally NOT emitted (not in RegisterAll per verified-names):
//   - cluster_autoscaler_binpacking_heterogeneity
//   - cluster_autoscaler_max_node_skip_eval_duration_seconds
//   - cluster_autoscaler_inconsistent_instances_migs_count
//
// Per-pod correlation (ARCHITECTURE Phase 1 Task 1.A):
//
//	When fx.Cluster.SubstrateWorkloads contains the "cluster-autoscaler" workload
//	(1 pod, kube-system, container cluster-autoscaler), all cluster_autoscaler_* and
//	rest_client_* series are stamped with pod/namespace/container/node/instance labels
//	via k8saddon.StampPods. Port 8085 is the cluster-autoscaler documented metrics port.
//
//	If SubstrateWorkloads is absent (nil workload lookup → nil stamp), the fallback
//	emits the original cluster-scoped series (no pod/namespace/container/instance labels).
//	This preserves back-compat for tests and blueprints that don't declare cluster_autoscaler.
//
// CAUTION – UNVERIFIED SIGNAL NAMES:
//
//	cluster-autoscaler was NOT captured live (Karpenter is the real autoscaler on the reference cluster).
//	ALL cluster_autoscaler_* metric names are assumed from signals/k8s-addons.md and
//	upstream cluster-autoscaler documentation — NOT verified against a running instance.
//	See cantfind.md for the PENDING entries.
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I4:  histograms via state.Observe with LEBare (prom-native scrape style)
//   - I13: no empty/sentinel labels
//   - I21: ScopeSubstrate — no blueprint label
package clusterautoscaler

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "cluster_autoscaler"
	interval = 60 * time.Second

	clusterAutoscalerJob = "integrations/cluster-autoscaler"

	// portMetrics is the cluster-autoscaler documented metrics port.
	// cluster-autoscaler serves its /metrics endpoint on :8085 by default
	// (--address flag default in the upstream binary).
	portMetrics = 8085
)

// Config holds the YAML-configurable knobs for the cluster autoscaler construct.
// min_nodes and max_nodes drive the emitted cpu/memory limit series.
type Config struct {
	MinNodes int `yaml:"min_nodes"`
	MaxNodes int `yaml:"max_nodes"`
}

// caNodeStates are the state label values for nodes_count.
// (from extract §6.6 value enums)
var caNodeStates = []string{
	"ready", "unready", "notStarted", "longNotStarted", "unregistered",
	"longUnregistered", "cloudProviderTarget", "schedulable", "unschedulable",
}

// caNodeGroupTypes are the node_group_type label values.
var caNodeGroupTypes = []string{"autoscaled", "static"}

// caActivities are activity label values for last_activity.
var caActivities = []string{"scaleUp", "scaleDown", "noAction"}

// caFunctions are function label values for function_duration_seconds.
var caFunctions = []string{"RunOnce", "ScaleUp", "ScaleDown", "FilterOutSchedulable"}

// caErrorTypes are error type label values for errors_total.
var caErrorTypes = []string{"cloudProviderError", "apiCallError", "internalError"}

// caScaleDownReasons are reason label values for scaled_down_nodes_total.
var caScaleDownReasons = []string{"Underutilized", "NodeGroupMinSizeReached", "ScaleDownDisabled"}

// caScaleUpFailReasons are reason label values for failed_scale_ups_total.
var caScaleUpFailReasons = []string{"Error", "Timeout", "OutOfResources"}

// caEvictionResults are eviction_result label values for evicted_pods_total.
var caEvictionResults = []string{"evicted", "failed"}

// caUnremovableReasons are reason label values for unremovable_nodes_count.
var caUnremovableReasons = []string{
	"system-pod", "minimum-size-reached", "scale-down-disabled",
	"recent-scale-up", "no-place-to-move",
}

// caAWSEndpoints are endpoint label values for aws_request_duration_seconds.
var caAWSEndpoints = []string{"autoscaling.amazonaws.com", "ec2.amazonaws.com"}

// prometheusDefaultSecondsBuckets mirrors the Prometheus client_golang default histogram
// bounds used by real cluster-autoscaler (LEBare style — prom-native scrape).
var prometheusDefaultSecondsBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Construct renders cluster autoscaler metrics for one cluster.
type Construct struct {
	clust    *fixture.Cluster
	minNodes int
	maxNodes int
	st       *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// New builds a Construct from cfg (pointer to Config) and the resolved fixtures.
// Returns an error if fx.Cluster is nil.
// Config defaults: MinNodes=3, MaxNodes=10.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("cluster_autoscaler: fixture.Cluster is required (nil)")
	}
	c := &Config{MinNodes: 3, MaxNodes: 10}
	if typed, ok := cfg.(*Config); ok && typed != nil {
		if typed.MinNodes > 0 {
			c.MinNodes = typed.MinNodes
		}
		if typed.MaxNodes > 0 {
			c.MaxNodes = typed.MaxNodes
		}
	}
	return &Construct{
		clust:    fx.Cluster,
		minNodes: c.MinNodes,
		maxNodes: c.MaxNodes,
		st:       state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one cluster autoscaler snapshot for the cluster.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	nodeCount := len(c.clust.Nodes)
	tickSec := interval.Seconds()
	scale := tickSec / 30.0

	// shape factor from cluster env
	factor := 0.5
	if c.clust.Env != nil {
		factor = w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)
	}

	base := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              clusterAutoscalerJob,
	}

	// ── Per-pod stamp ─────────────────────────────────────────────────────────
	//
	// StampPods returns one label-set per pod of the "cluster-autoscaler" workload
	// (1 pod, kube-system, container cluster-autoscaler, port 8085).
	// Returns nil when SubstrateWorkloads is absent → fallback emits cluster-scoped.
	podMaps := k8saddon.StampPods(c.clust, "cluster-autoscaler", base, portMetrics)

	// withPod calls emit(lbls) for each pod label-map (or base if no pods were found).
	// extra dims are merged on top of each pod map.
	withPod := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(podMaps) > 0 {
			for _, pm := range podMaps {
				lbls := make(map[string]string, len(pm)+len(extra))
				for k, v := range pm {
					lbls[k] = v
				}
				for k, v := range extra {
					lbls[k] = v
				}
				emit(lbls)
			}
		} else {
			// Fallback: cluster-scoped (no pod/namespace/container/instance labels).
			lbls := make(map[string]string, len(base)+len(extra))
			for k, v := range base {
				lbls[k] = v
			}
			for k, v := range extra {
				lbls[k] = v
			}
			emit(lbls)
		}
	}

	hb := prometheusDefaultSecondsBuckets

	// ── cluster_autoscaler_cluster_safe_to_autoscale (G 0/1) ─────────────────
	withPod(nil, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_cluster_safe_to_autoscale", lbls, 1)
	})

	// ── cluster_autoscaler_nodes_count (G; state) ─────────────────────────────
	// nodes_count must reflect len(fx.Cluster.Nodes) for state=ready and state=schedulable.
	for _, st := range caNodeStates {
		var count float64
		switch st {
		case "ready", "schedulable":
			count = float64(nodeCount)
		default:
			count = 0
		}
		stCopy := st
		countCopy := count
		withPod(map[string]string{"state": stCopy}, func(lbls map[string]string) {
			c.st.Set("cluster_autoscaler_nodes_count", lbls, countCopy)
		})
	}

	// ── cluster_autoscaler_node_groups_count (G; node_group_type) ────────────
	for _, ngt := range caNodeGroupTypes {
		v := 1.0
		if ngt == "static" {
			v = 0
		}
		ngtCopy, vCopy := ngt, v
		withPod(map[string]string{"node_group_type": ngtCopy}, func(lbls map[string]string) {
			c.st.Set("cluster_autoscaler_node_groups_count", lbls, vCopy)
		})
	}

	// ── cluster_autoscaler_node_group_target_count (G; node_group) ───────────
	// One node group per cluster: named "asg-<cluster>".
	nodeGroup := "asg-" + cluster
	nodeCountF := float64(nodeCount)
	withPod(map[string]string{"node_group": nodeGroup}, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_node_group_target_count", lbls, nodeCountF)
	})

	// ── cluster_autoscaler_unschedulable_pods_count (G; type) ────────────────
	withPod(map[string]string{"type": "unschedulable"}, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_unschedulable_pods_count", lbls, 0)
	})
	withPod(map[string]string{"type": "timeout"}, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_unschedulable_pods_count", lbls, 0)
	})

	// ── CPU and memory current / limits ───────────────────────────────────────
	// 4 vCPUs and 16 GiB per node (m6i.large baseline; adapt if InstanceType is known).
	const vcpusPerNode = 4
	const gibPerNode = 16
	const gib = 1 << 30

	totalCPU := float64(nodeCount * vcpusPerNode)
	totalMem := float64(nodeCount) * gibPerNode * gib

	withPod(nil, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_cluster_cpu_current_cores", lbls, totalCPU)
		c.st.Set("cluster_autoscaler_cluster_memory_current_bytes", lbls, totalMem)
	})

	// Limits are coherent with min/max node config:
	//   direction=down: floor at minNodes
	//   direction=up:   ceiling at maxNodes
	for _, dir := range []string{"up", "down"} {
		var limitNodes int
		if dir == "up" {
			limitNodes = c.maxNodes
		} else {
			limitNodes = c.minNodes
		}
		dirCopy := dir
		cpuLimit := float64(limitNodes * vcpusPerNode)
		memLimit := float64(limitNodes) * gibPerNode * gib
		withPod(map[string]string{"direction": dirCopy}, func(lbls map[string]string) {
			c.st.Set("cluster_autoscaler_cpu_limits_cores", lbls, cpuLimit)
			c.st.Set("cluster_autoscaler_memory_limits_bytes", lbls, memLimit)
		})
	}

	// ── cluster_autoscaler_last_activity (G; activity) ────────────────────────
	for _, act := range caActivities {
		offset := float64(30 + w.Shape.IntN(300))
		ts := float64(now.Unix()) - offset
		actCopy := act
		withPod(map[string]string{"activity": actCopy}, func(lbls map[string]string) {
			c.st.Set("cluster_autoscaler_last_activity", lbls, ts)
		})
	}

	// ── cluster_autoscaler_function_duration_seconds (H; function) ────────────
	for _, fn := range caFunctions {
		fnCopy := fn
		obs := 0.01 + factor*0.2*w.Shape.Noise(0.4)
		withPod(map[string]string{"function": fnCopy}, func(lbls map[string]string) {
			c.st.Observe("cluster_autoscaler_function_duration_seconds", lbls, hb, state.LEBare, obs)
		})
	}

	// ── cluster_autoscaler_errors_total (C; type) ─────────────────────────────
	for _, errType := range caErrorTypes {
		etCopy := errType
		withPod(map[string]string{"type": etCopy}, func(lbls map[string]string) {
			c.st.Add("cluster_autoscaler_errors_total", lbls, 0)
		})
	}

	// ── cluster_autoscaler_scaled_up_nodes_total (C) ──────────────────────────
	withPod(nil, func(lbls map[string]string) {
		c.st.Add("cluster_autoscaler_scaled_up_nodes_total", lbls, 0)
	})

	// ── cluster_autoscaler_failed_scale_ups_total (C; reason) ────────────────
	for _, reason := range caScaleUpFailReasons {
		rCopy := reason
		withPod(map[string]string{"reason": rCopy}, func(lbls map[string]string) {
			c.st.Add("cluster_autoscaler_failed_scale_ups_total", lbls, 0)
		})
	}

	// ── cluster_autoscaler_scaled_down_nodes_total (C; reason) ───────────────
	for _, reason := range caScaleDownReasons {
		rCopy := reason
		withPod(map[string]string{"reason": rCopy}, func(lbls map[string]string) {
			c.st.Add("cluster_autoscaler_scaled_down_nodes_total", lbls, 0)
		})
	}

	// ── cluster_autoscaler_evicted_pods_total (C; eviction_result) ───────────
	for _, res := range caEvictionResults {
		resCopy := res
		withPod(map[string]string{"eviction_result": resCopy}, func(lbls map[string]string) {
			c.st.Add("cluster_autoscaler_evicted_pods_total", lbls, 0)
		})
	}

	// ── cluster_autoscaler_unneeded_nodes_count (G) ───────────────────────────
	withPod(nil, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_unneeded_nodes_count", lbls, 0)
	})

	// ── cluster_autoscaler_unremovable_nodes_count (G; reason) ───────────────
	for _, reason := range caUnremovableReasons {
		rCopy := reason
		withPod(map[string]string{"reason": rCopy}, func(lbls map[string]string) {
			c.st.Set("cluster_autoscaler_unremovable_nodes_count", lbls, 0)
		})
	}

	// ── cluster_autoscaler_scale_down_in_cooldown (G 0/1) ────────────────────
	withPod(nil, func(lbls map[string]string) {
		c.st.Set("cluster_autoscaler_scale_down_in_cooldown", lbls, 0)
	})

	// ── cluster_autoscaler_aws_request_duration_seconds (H; endpoint,status) ─
	for _, ep := range caAWSEndpoints {
		epCopy := ep
		obs := 0.05 + factor*0.1*w.Shape.Noise(0.3)
		withPod(map[string]string{"endpoint": epCopy, "status": "200"}, func(lbls map[string]string) {
			c.st.Observe("cluster_autoscaler_aws_request_duration_seconds", lbls, hb, state.LEBare, obs)
		})
	}

	// ── rest_client_requests_total (C; code,method,host) ─────────────────────
	// cluster-autoscaler imports client-go which emits rest_client_* (LBC does NOT).
	apiHost := "172.20.0.1:443"
	for _, rc := range []struct{ code, method string }{
		{"200", "GET"}, {"200", "PATCH"}, {"201", "POST"},
	} {
		rcCopy := rc
		val := (2 + factor*6) * scale
		withPod(map[string]string{
			"code": rcCopy.code, "method": rcCopy.method, "host": apiHost,
		}, func(lbls map[string]string) {
			c.st.Add("rest_client_requests_total", lbls, val)
		})
	}

	return w.Metrics.Write(ctx, c.st.Collect(now))
}
