// SPDX-License-Identifier: AGPL-3.0-only

// Package karpenter implements the "karpenter" construct (ARCHITECTURE §2,
// kind="karpenter", Scope=ScopeSubstrate). It emits the full karpenter metric
// family scraped from kube-system by the k8s-monitoring Alloy job, plus
// structured JSON log streams matching karpenter's zap logger output.
//
// Kind:     "karpenter"
// Scope:    ScopeSubstrate — cluster+k8s_cluster_name disambiguate; NO blueprint label.
// Signals:  []{Metrics, Logs}.
// Interval: 60s
// Requires: fx.Cluster (non-nil).
//
// Signal contract: signals/k8s-addons.md (karpenter section) sourced from live
// a live reference cluster recon 2026-06-16 (svc-group-b.md §1.A).
//
// # Seam: pod-scoping
//
// karpenter_* domain families are LEADER-ELECTED: only the active leader pod emits them.
//
//	→ k8saddon.StampLeader(cl, "karpenter", base, 8080) — one pod.
//
// go_* / process_* / controller_runtime_* families are emitted by all pods:
//
//	→ k8saddon.StampPods(cl, "karpenter", base, 8080) — both pods.
//
// Fallback: if SubstrateWorkloads is absent, series are emitted cluster-scoped (no
// pod/namespace/container/instance labels) to preserve back-compat.
//
// # Histogram bucket sets (from live capture)
//
// cloudproviderSmallBuckets (12): standard 0.005…+Inf — cloudprovider duration.
// schedulingBuckets (46):         0.005…600.0,+Inf — scheduling / bound / startup histograms.
// batcherBuckets (36):            1,2,4,5,10…1000,+Inf — batcher_batch_size.
// lifetimeBuckets (22):           900…2.592e6,+Inf — nodes_lifetime_duration_seconds.
//
// # SUMMARY metrics (NOT histograms)
//
// karpenter_nodes_termination_duration_seconds and karpenter_pods_startup_duration_seconds
// are Prometheus SUMMARIEs — they emit quantile{quantile="0"|"0.5"|"0.9"|"0.99"|"1"} series
// plus _sum and _count, NOT _bucket series.
//
// # Cardinality cap — offering price
//
// karpenter_cloudprovider_instance_type_offering_price_estimate is ~9261 series live
// (1034 instance types × 3 capacity types × zones). Synthkit emits a BOUNDED representative
// subset: a fixed small set of instance types × 3 AZs × 2 capacity types = ≤60 series.
// This is intentional — unbounded fan-out would make the construct impractical.
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I4:  histograms via state.Observe with LEBare (prom-native scrape style)
//   - I13: absent dimensions are OMITTED — never "" or "NA"
//   - I21: ScopeSubstrate — no blueprint label
package karpenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "karpenter"
	interval = 60 * time.Second

	// karpenterJob is the Prometheus scrape job label (live recon: job="karpenter").
	karpenterJob = "karpenter"

	// karpenterPort is the metrics port (8080/TCP, endpoint "http-metrics").
	karpenterPort = 8080

	// karpenterVersion is the synthetic Karpenter version stamped on build_info.
	karpenterVersion = "1.13.0"

	// zapTimeFormat is the RFC3339 timestamp format with millisecond precision that
	// real karpenter zap logs emit (e.g. "2026-06-16T16:18:11.123Z").
	zapTimeFormat = "2006-01-02T15:04:05.000Z"
)

// cloudproviderSmallBuckets are the 12 standard le bounds for cloudprovider_duration_seconds
// (live recon §1.A).
var cloudproviderSmallBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0,
}

// schedulingBuckets are the 46-bucket le bounds shared by all scheduling / bound / startup
// histograms (live recon §1.A).
var schedulingBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.2, 0.25, 0.3, 0.35, 0.4, 0.45, 0.5,
	0.6, 0.7, 0.8, 0.9, 1.0, 1.25, 1.5, 1.75, 2.0, 2.5, 3.0, 3.5, 4.0, 4.5, 5.0,
	6.0, 7.0, 8.0, 9.0, 10.0, 15.0, 20.0, 25.0, 30.0, 40.0, 50.0, 60.0,
	120.0, 150.0, 300.0, 450.0, 600.0,
}

// batcherBuckets are the le bounds for cloudprovider_batcher_batch_size (live recon §1.A).
var batcherBuckets = []float64{
	1, 2, 4, 5, 10, 15, 20, 25, 30, 40, 50, 60, 70, 80, 90, 100,
	125, 150, 175, 200, 225, 250, 275, 300, 350, 400, 450, 500,
	550, 600, 700, 800, 900, 1000,
}

// lifetimeBuckets are the le bounds for nodes_lifetime_duration_seconds (live recon §1.A).
var lifetimeBuckets = []float64{
	900, 1800, 2700, 3600, 7200, 14400, 21600, 28800, 36000, 43200,
	57600, 72000, 86400, 172800, 259200, 432000, 864000,
	1.296e6, 1.728e6, 2.16e6, 2.592e6,
}

// summaryQuantiles are the quantile label values for the two SUMMARY metrics
// (live recon §1.A: nodes_termination_duration_seconds, pods_startup_duration_seconds).
var summaryQuantiles = []string{"0", "0.5", "0.9", "0.99", "1"}

// cloudproviderControllers are the controller label values for cloudprovider_duration_seconds
// (live recon §1.A — "controller" may be absent for some combinations; that dim is
// emitted where present and omitted where absent per I13).
var cloudproviderControllers = []string{
	"disruption",
	"nodeclaim.lifecycle",
	"nodeclaim.disruption",
	"provisioner",
	"instance.garbagecollection",
	"nodeclaim.garbagecollection",
}

// cloudproviderMethods are the method label values for cloudprovider_duration_seconds.
var cloudproviderMethods = []string{
	"GetInstanceTypes", "List", "Create", "Delete", "Get", "IsDrifted",
}

// cloudproviderErrors are the error label values for cloudprovider_errors_total.
var cloudproviderErrors = []string{
	"InsufficientCapacityError",
	"NodeClaimNotFoundError",
}

// nodeclaimDisruptionReasons are the reason values for nodeclaims_disrupted_total.
// capacity_type is absent when reason=insufficient_capacity (I13).
var nodeclaimDisruptionReasons = []struct {
	reason          string
	hasCapacityType bool
}{
	{"empty", true},
	{"spot_interrupted", true},
	{"underutilized", true},
	{"insufficient_capacity", false},
}

// interruptionMessageTypes are the message_type values for interruption_received_messages_total.
var interruptionMessageTypes = []string{
	"instance_terminated",
	"no_op",
	"rebalance_recommendation",
	"scheduled_change",
	"spot_interrupted",
}

// nodepoolResourceTypes are the resource_type values for nodepools_usage/limit.
var nodepoolResourceTypes = []string{
	"cpu", "memory", "ephemeral_storage",
	"hugepages_1gi", "hugepages_2mi", "hugepages_32mi", "hugepages_64ki",
	"nodes", "pods", "vpc.amazonaws.com/pod_eni",
}

// nodepoolLimitResourceTypes are the resource_type values for nodepools_limit
// (only cpu and memory observed live; recon §1.A).
var nodepoolLimitResourceTypes = []string{"cpu", "memory"}

// clusterUtilizationResourceTypes are the resource_type values for cluster_utilization_percent.
var clusterUtilizationResourceTypes = []string{"cpu", "memory", "pods", "ephemeral_storage"}

// voluntaryDisruptionConsolidationTypes are the consolidation_type label values.
var voluntaryDisruptionConsolidationTypes = []string{"empty", "single", "multi"}

// eligibleNodeReasons are the reason values for voluntary_disruption_eligible_nodes.
var eligibleNodeReasons = []string{"drifted", "empty", "underutilized"}

// allowedDisruptionReasons are the reason values for nodepools_allowed_disruptions.
// NOTE: PascalCase — this is a Karpenter exception to the lowercase-reason rule
// (live recon §1.A: reason ∈ {Empty, Underutilized}).
var allowedDisruptionReasons = []string{"Empty", "Underutilized"}

// podStatePhases are the phase label values for pods_state.
var podStatePhases = []string{"Running", "Pending", "Failed"}

// allocatableResourceTypes are the resource_type values for nodes_allocatable and related
// node-resource metrics.
var allocatableResourceTypes = []string{"cpu", "memory", "ephemeral-storage", "pods"}

// representativeInstanceTypes is the BOUNDED set of instance types for
// karpenter_cloudprovider_instance_type_offering_price_estimate.
//
// DELIBERATE CARDINALITY CAP: live production emits ~9261 series (1034 instance
// types × zones × capacity types). Synthkit emits only this small representative set
// (10 types × 3 AZs × 3 capacity types = 90 series) to keep the construct practical.
// Dashboard panels query this metric with rate/topk filters — 60 series is sufficient
// to exercise those queries.
var representativeInstanceTypes = []string{
	"m6g.large", "m6g.xlarge", "m6g.2xlarge",
	"m6i.large", "m6i.xlarge",
	"c6g.large", "c6g.xlarge",
	"r6g.large", "r6g.xlarge",
	"t4g.large",
}

// representativeZones is the set of AZs for the offering price series.
// Matches a live reference cluster capture.
var representativeZones = []string{"eu-west-1a", "eu-west-1b", "eu-west-1c"}

// offeringCapacityTypes are the capacity_type values for the offering series. Karpenter v1.x always
// emits all three (live reference recon 2026-06-16: on-demand/spot/reserved); "reserved" offerings carry
// offering_available=0 when no capacity reservations are configured.
var offeringCapacityTypes = []string{"on-demand", "spot", "reserved"}

// goRuntimeControllers are the controller-runtime controller names for karpenter.
// These appear on controller_runtime_reconcile_total (live recon).
var goRuntimeControllers = []string{
	"node.termination",
	"node.drift",
	"nodeclaim.disruption",
	"nodeclaim.lifecycle",
	"disruption",
	"provisioner",
}

// reconcileResults are the result label values for controller_runtime_reconcile_total.
var reconcileResults = []string{"error", "requeue", "requeue_after", "success"}

// Config is the construct config struct (empty — all identity comes from fixtures).
type Config struct{}

// Construct renders karpenter metrics for one cluster.
type Construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// New builds a Construct from cfg and the resolved fixture set.
// Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("karpenter: fixture.Cluster is required (nil)")
	}
	return &Construct{
		clust: fx.Cluster,
		st:    state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics + logs.
func (c *Construct) Signals() []core.SignalClass {
	return []core.SignalClass{core.Metrics, core.Logs}
}

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one karpenter metric batch for the cluster.
// Counters accumulate (state.Add); gauges set per tick (state.Set);
// histograms accumulate observations (state.Observe). Running totals not deltas (I3).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)
	scale := interval.Seconds() / 30.0 // cadence-invariant volume (I3)

	// base returns a fresh label map with universal labels for this cluster.
	// Each call returns an independent map (no aliasing between series).
	base := func() map[string]string {
		return map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"job":              karpenterJob,
		}
	}

	// mergeInto merges extra labels onto a cloned base map. Never mutates base.
	mergeInto := func(b map[string]string, extra map[string]string) map[string]string {
		out := cloneMap(b)
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// ── Pod-scoped label sets ────────────────────────────────────────────────────
	//
	// leaderMaps: ONE label-set (leader-elected pod) for karpenter_* domain families.
	//   nil when SubstrateWorkloads absent → fallback to cluster-scoped series.
	//
	// allPodMaps: label-set per replica for go_*/process_*/controller_runtime_* families.
	//   nil when SubstrateWorkloads absent → fallback to cluster-scoped series.
	leaderMaps := k8saddon.StampLeader(c.clust, "karpenter", base(), karpenterPort)
	allPodMaps := k8saddon.StampPods(c.clust, "karpenter", base(), karpenterPort)

	// emitDomain emits a metric for each leader pod label-set (or cluster-scoped fallback).
	// extra labels are merged on top of each pod map.
	emitDomain := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(leaderMaps) > 0 {
			for _, pm := range leaderMaps {
				emit(mergeInto(pm, extra))
			}
		} else {
			// Fallback: cluster-scoped (no pod labels).
			emit(mergeInto(base(), extra))
		}
	}

	// emitAllPods emits a metric for each pod label-set (or cluster-scoped fallback).
	emitAllPods := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(allPodMaps) > 0 {
			for _, pm := range allPodMaps {
				emit(mergeInto(pm, extra))
			}
		} else {
			// Fallback: cluster-scoped (no pod labels).
			emit(mergeInto(base(), extra))
		}
	}

	// ── karpenter_build_info (G=1; per-pod — both replicas emit) ────────────────
	// Live recon: 2 series (one per replica), build_info gauge=1.
	emitAllPods(map[string]string{
		"version":   karpenterVersion,
		"goversion": "go1.26.4",
		"goarch":    "arm64",
		"commit":    "2be9554",
	}, func(lbls map[string]string) {
		c.st.Set("karpenter_build_info", lbls, 1)
	})

	// ── karpenter_cluster_state_node_count (G; leader) ───────────────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Set("karpenter_cluster_state_node_count", lbls, float64(len(c.clust.Nodes)))
	})

	// ── karpenter_cluster_state_synced (G=1; leader) ────────────────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Set("karpenter_cluster_state_synced", lbls, 1)
	})

	// ── karpenter_cluster_state_unsynced_time_seconds (G; leader) ───────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Set("karpenter_cluster_state_unsynced_time_seconds", lbls, 0)
	})

	// ── karpenter_cluster_utilization_percent (G; leader) ───────────────────────
	utilValues := map[string]float64{
		"cpu":               79.6,
		"memory":            87.7,
		"pods":              92.9,
		"ephemeral_storage": 0,
	}
	for _, rt := range clusterUtilizationResourceTypes {
		rt := rt // capture
		v := utilValues[rt]
		emitDomain(map[string]string{"resource_type": rt}, func(lbls map[string]string) {
			c.st.Set("karpenter_cluster_utilization_percent", lbls, v*(0.9+factor*0.2))
		})
	}

	// ── karpenter_cloudprovider_duration_seconds (H; leader; controller×method×provider) ──
	for _, ctrl := range cloudproviderControllers {
		for _, meth := range cloudproviderMethods {
			extra := map[string]string{
				"controller": ctrl,
				"method":     meth,
				"provider":   "aws",
			}
			emitDomain(extra, func(lbls map[string]string) {
				c.st.Observe("karpenter_cloudprovider_duration_seconds", lbls,
					cloudproviderSmallBuckets, state.LEBare,
					0.05+factor*0.5*w.Shape.Noise(0.3))
			})
		}
	}

	// ── karpenter_cloudprovider_batcher_batch_size (H; leader) ──────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_cloudprovider_batcher_batch_size", lbls,
			batcherBuckets, state.LEBare,
			1+factor*5*w.Shape.Noise(0.4))
	})

	// ── karpenter_cloudprovider_batcher_batch_time_seconds (H; leader) ──────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_cloudprovider_batcher_batch_time_seconds", lbls,
			cloudproviderSmallBuckets, state.LEBare,
			0.001+factor*0.01*w.Shape.Noise(0.4))
	})

	// ── karpenter_cloudprovider_errors_total (C; leader; controller×error×method×provider) ─
	for _, ctrl := range cloudproviderControllers {
		for _, errVal := range cloudproviderErrors {
			for _, meth := range []string{"Create", "GetInstanceTypes"} {
				extra := map[string]string{
					"controller": ctrl,
					"error":      errVal,
					"method":     meth,
					"provider":   "aws",
				}
				emitDomain(extra, func(lbls map[string]string) {
					c.st.Add("karpenter_cloudprovider_errors_total", lbls, 0)
				})
			}
		}
	}

	// ── karpenter_cloudprovider_instance_type_cpu_cores (G; leader) ─────────────
	// Per-instance-type gauge. Using the representative set (bounded for cardinality).
	for _, it := range representativeInstanceTypes {
		it := it
		cores := instanceTypeCores(it)
		emitDomain(map[string]string{"instance_type": it}, func(lbls map[string]string) {
			c.st.Set("karpenter_cloudprovider_instance_type_cpu_cores", lbls, cores)
		})
	}

	// ── karpenter_cloudprovider_instance_type_memory_bytes (G; leader) ──────────
	for _, it := range representativeInstanceTypes {
		it := it
		mem := instanceTypeMemBytes(it)
		emitDomain(map[string]string{"instance_type": it}, func(lbls map[string]string) {
			c.st.Set("karpenter_cloudprovider_instance_type_memory_bytes", lbls, mem)
		})
	}

	// ── karpenter_cloudprovider_instance_type_offering_available (G=1; leader) ──
	// Bounded to representative set × zones × capacity_type.
	for _, it := range representativeInstanceTypes {
		for _, zone := range representativeZones {
			for _, ct := range offeringCapacityTypes {
				it, zone, ct := it, zone, ct
				// reserved offerings exist but are unavailable (available=0) when no capacity
				// reservation is configured — matches live reference recon.
				avail := 1.0
				if ct == "reserved" {
					avail = 0
				}
				emitDomain(map[string]string{
					"instance_type": it,
					"zone":          zone,
					"capacity_type": ct,
				}, func(lbls map[string]string) {
					c.st.Set("karpenter_cloudprovider_instance_type_offering_available", lbls, avail)
				})
			}
		}
	}

	// ── karpenter_cloudprovider_instance_type_offering_price_estimate (G; leader) ─
	//
	// DELIBERATE CARDINALITY CAP: 10 instance types × 3 AZs × 3 capacity types = 90
	// series. Live production emits ~9261 series (1034 types × all AZs × capacity types).
	// See package doc for rationale.
	for _, it := range representativeInstanceTypes {
		for _, zone := range representativeZones {
			for _, ct := range offeringCapacityTypes {
				it, zone, ct := it, zone, ct
				price := offeringPrice(it, ct)
				emitDomain(map[string]string{
					"instance_type": it,
					"zone":          zone,
					"capacity_type": ct,
				}, func(lbls map[string]string) {
					c.st.Set("karpenter_cloudprovider_instance_type_offering_price_estimate", lbls, price)
				})
			}
		}
	}

	// ── karpenter_nodeclaims_created_total (C; leader) ───────────────────────────
	emitDomain(map[string]string{
		"min_values_relaxed": "false",
		"nodepool":           "default",
		"reason":             "provisioned",
	}, func(lbls map[string]string) {
		c.st.Add("karpenter_nodeclaims_created_total", lbls, factor*0.1*scale)
	})

	// ── karpenter_nodeclaims_disrupted_total (C; leader; per reason) ─────────────
	// capacity_type absent when reason=insufficient_capacity (I13).
	for _, rr := range nodeclaimDisruptionReasons {
		rr := rr
		extra := map[string]string{"nodepool": "default", "reason": rr.reason}
		if rr.hasCapacityType {
			extra["capacity_type"] = "spot"
		}
		emitDomain(extra, func(lbls map[string]string) {
			c.st.Add("karpenter_nodeclaims_disrupted_total", lbls, 0)
		})
	}

	// ── karpenter_nodeclaims_instance_termination_duration_seconds (H; leader) ───
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Observe("karpenter_nodeclaims_instance_termination_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			5+factor*10*w.Shape.Noise(0.4))
	})

	// ── karpenter_nodeclaims_terminated_total (C; leader) ─────────────────────────
	emitDomain(map[string]string{"nodepool": "default", "reason": "underutilized"}, func(lbls map[string]string) {
		c.st.Add("karpenter_nodeclaims_terminated_total", lbls, factor*0.05*scale)
	})

	// ── karpenter_nodeclaims_termination_duration_seconds (H; leader) ────────────
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Observe("karpenter_nodeclaims_termination_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			3+factor*8*w.Shape.Noise(0.4))
	})

	// ── karpenter_nodepools_allowed_disruptions (G; leader) ──────────────────────
	// EXCEPTION: reason is PascalCase here (Empty, Underutilized) — NOT lowercase.
	// All other karpenter reason labels are lowercase. Live recon §1.A confirms this.
	for _, reason := range allowedDisruptionReasons {
		reason := reason
		emitDomain(map[string]string{
			"nodepool": "default",
			"reason":   reason,
		}, func(lbls map[string]string) {
			c.st.Set("karpenter_nodepools_allowed_disruptions", lbls, 2)
		})
	}

	// ── karpenter_nodepools_cost_total (G; leader) ──────────────────────────────
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Set("karpenter_nodepools_cost_total", lbls, 0.0407*(1+factor*0.1))
	})

	// ── karpenter_nodepools_limit (G; leader) ────────────────────────────────────
	nodepoolLimitValues := map[string]float64{"cpu": 100, "memory": 128 * 1024 * 1024 * 1024}
	for _, rt := range nodepoolLimitResourceTypes {
		rt := rt
		v := nodepoolLimitValues[rt]
		emitDomain(map[string]string{"nodepool": "default", "resource_type": rt}, func(lbls map[string]string) {
			c.st.Set("karpenter_nodepools_limit", lbls, v)
		})
	}

	// ── karpenter_nodepools_nodes_consuming_budgets (G; leader) ─────────────────
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Set("karpenter_nodepools_nodes_consuming_budgets", lbls, 0)
	})

	// ── karpenter_nodepools_usage (G; leader; per resource_type) ─────────────────
	for _, rt := range nodepoolResourceTypes {
		rt := rt
		v := nodepoolUsageValue(rt, factor)
		emitDomain(map[string]string{"nodepool": "default", "resource_type": rt}, func(lbls map[string]string) {
			c.st.Set("karpenter_nodepools_usage", lbls, v)
		})
	}

	// ── karpenter_nodes_allocatable (G; leader; per node × resource_type) ────────
	// Rich instance-topology labels (live recon §1.A).
	nodeTopology := map[string]string{
		"arch":                                     "arm64",
		"capacity_type":                            "spot",
		"instance_capability_flex":                 "false",
		"instance_category":                        "m",
		"instance_cpu":                             "2",
		"instance_cpu_manufacturer":                "aws",
		"instance_cpu_sustained_clock_speed_mhz":   "2500",
		"instance_ebs_bandwidth":                   "4750",
		"instance_encryption_in_transit_supported": "false",
		"instance_family":                          "m6g",
		"instance_generation":                      "6",
		"instance_hypervisor":                      "nitro",
		"instance_memory":                          "8192",
		"instance_network_bandwidth":               "750",
		"instance_size":                            "large",
		"instance_tenancy":                         "default",
		"instance_type":                            "m6g.large",
		"nodepool":                                 "default",
		"os":                                       "linux",
		"region":                                   "eu-west-1",
		"zone":                                     "eu-west-1a",
		"zone_id":                                  "euw1-az1",
	}
	allocValues := map[string]float64{
		"cpu":               2,
		"memory":            8192 * 1024 * 1024,
		"ephemeral-storage": 20 * 1024 * 1024 * 1024,
		"pods":              58,
	}
	for _, rt := range allocatableResourceTypes {
		rt := rt
		v := allocValues[rt]
		extra := make(map[string]string, len(nodeTopology)+2)
		for k, v := range nodeTopology {
			extra[k] = v
		}
		extra["resource_type"] = rt
		// node_name uses the first cluster node's hostname (live shape: compute.internal hostname)
		if len(c.clust.Nodes) > 0 {
			extra["node_name"] = c.clust.Nodes[0].Hostname
		}
		emitDomain(extra, func(lbls map[string]string) {
			c.st.Set("karpenter_nodes_allocatable", lbls, v)
		})
	}

	// Same topology labels on related node-resource metrics.
	for _, rt := range allocatableResourceTypes {
		rt := rt
		extra := make(map[string]string, len(nodeTopology)+2)
		for k, v := range nodeTopology {
			extra[k] = v
		}
		extra["resource_type"] = rt
		if len(c.clust.Nodes) > 0 {
			extra["node_name"] = c.clust.Nodes[0].Hostname
		}
		emitDomain(extra, func(lbls map[string]string) {
			c.st.Set("karpenter_nodes_system_overhead", lbls, allocValues[rt]*0.05)
			c.st.Set("karpenter_nodes_total_daemon_limits", lbls, allocValues[rt]*0.1)
			c.st.Set("karpenter_nodes_total_daemon_requests", lbls, allocValues[rt]*0.08)
			c.st.Set("karpenter_nodes_total_pod_limits", lbls, allocValues[rt]*0.7)
			c.st.Set("karpenter_nodes_total_pod_requests", lbls, allocValues[rt]*0.6)
		})
	}

	// ── karpenter_nodes_created_total (C; leader) ────────────────────────────────
	emitDomain(map[string]string{
		"nodepool":      "default",
		"capacity_type": "spot",
		"arch":          "arm64",
		"os":            "linux",
		"instance_type": "m6g.large",
	}, func(lbls map[string]string) {
		c.st.Add("karpenter_nodes_created_total", lbls, factor*0.1*scale)
	})

	// ── karpenter_nodes_current_lifetime_seconds (G; leader) ─────────────────────
	// Node-topology labels (arch, capacity_type, instance_type, node_name, nodepool, etc.).
	nodeLifetimeLabels := map[string]string{
		"arch":          "arm64",
		"capacity_type": "spot",
		"instance_type": "m6g.large",
		"nodepool":      "default",
		"os":            "linux",
		"region":        "eu-west-1",
		"zone":          "eu-west-1a",
		"zone_id":       "euw1-az1",
	}
	if len(c.clust.Nodes) > 0 {
		nodeLifetimeLabels["node_name"] = c.clust.Nodes[0].Hostname
	}
	emitDomain(nodeLifetimeLabels, func(lbls map[string]string) {
		c.st.Set("karpenter_nodes_current_lifetime_seconds", lbls, 3600+factor*7200)
	})

	// ── karpenter_nodes_drained_total (C; leader) ────────────────────────────────
	emitDomain(map[string]string{"nodepool": "default", "reason": "underutilized"}, func(lbls map[string]string) {
		c.st.Add("karpenter_nodes_drained_total", lbls, factor*0.03*scale)
	})

	// ── karpenter_nodes_lifetime_duration_seconds (H; leader; terminated nodes) ──
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Observe("karpenter_nodes_lifetime_duration_seconds", lbls,
			lifetimeBuckets, state.LEBare,
			3600+factor*7200*w.Shape.Noise(0.5))
	})

	// ── karpenter_nodes_terminated_total (C; leader) ─────────────────────────────
	emitDomain(map[string]string{
		"nodepool":      "default",
		"capacity_type": "spot",
		"arch":          "arm64",
		"os":            "linux",
		"instance_type": "m6g.large",
	}, func(lbls map[string]string) {
		c.st.Add("karpenter_nodes_terminated_total", lbls, factor*0.08*scale)
	})

	// ── karpenter_nodes_termination_duration_seconds (SUMMARY; leader) ───────────
	// This is a Prometheus SUMMARY — emit quantile series + _sum + _count; NO _bucket.
	// live recon: quantile ∈ {0, 0.5, 0.9, 0.99, 1}, labels: nodepool.
	summaryDurationValues := map[string]float64{
		"0": 1.0, "0.5": 5.0, "0.9": 15.0, "0.99": 30.0, "1": 60.0,
	}
	for _, q := range summaryQuantiles {
		q := q
		v := summaryDurationValues[q]
		emitDomain(map[string]string{
			"nodepool": "default",
			"quantile": q,
		}, func(lbls map[string]string) {
			c.st.Set("karpenter_nodes_termination_duration_seconds", lbls, v*(0.8+factor*0.4))
		})
	}
	// _sum and _count (cumulative).
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Add("karpenter_nodes_termination_duration_seconds_count", lbls, factor*0.1*scale)
		c.st.Add("karpenter_nodes_termination_duration_seconds_sum", lbls, factor*5*scale)
	})

	// ── karpenter_interruption_deleted_messages_total (C; leader) ───────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Add("karpenter_interruption_deleted_messages_total", lbls, factor*0.05*scale)
	})

	// ── karpenter_interruption_message_queue_duration_seconds (H; leader) ────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_interruption_message_queue_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			0.1+factor*1.0*w.Shape.Noise(0.4))
	})

	// ── karpenter_interruption_received_messages_total (C; leader; per message_type) ─
	for _, mt := range interruptionMessageTypes {
		mt := mt
		emitDomain(map[string]string{"message_type": mt}, func(lbls map[string]string) {
			vol := factor * 0.1 * scale
			if mt == "no_op" {
				vol = factor * 0.5 * scale
			}
			c.st.Add("karpenter_interruption_received_messages_total", lbls, vol)
		})
	}

	// ── karpenter_pods_bound_duration_seconds (H; leader) ────────────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_pods_bound_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			0.5+factor*2*w.Shape.Noise(0.3))
	})

	// ── karpenter_pods_drained_total (C; leader) ─────────────────────────────────
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Add("karpenter_pods_drained_total", lbls, factor*0.5*scale)
	})

	// ── karpenter_pods_eviction_requests_total (C; leader) ───────────────────────
	emitDomain(map[string]string{"nodepool": "default"}, func(lbls map[string]string) {
		c.st.Add("karpenter_pods_eviction_requests_total", lbls, factor*0.3*scale)
	})

	// ── karpenter_pods_provisioning_bound_duration_seconds (H; leader) ───────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_pods_provisioning_bound_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			1+factor*5*w.Shape.Noise(0.4))
	})

	// ── karpenter_pods_provisioning_startup_duration_seconds (H; leader) ─────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_pods_provisioning_startup_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			10+factor*20*w.Shape.Noise(0.4))
	})

	// ── karpenter_pods_scheduling_decision_duration_seconds (H; leader) ──────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_pods_scheduling_decision_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			0.01+factor*0.1*w.Shape.Noise(0.3))
	})

	// ── karpenter_pods_startup_duration_seconds (SUMMARY; leader) ────────────────
	// This is a Prometheus SUMMARY — quantile series + _sum + _count; NO _bucket.
	startupValues := map[string]float64{
		"0": 5.0, "0.5": 15.0, "0.9": 30.0, "0.99": 60.0, "1": 120.0,
	}
	for _, q := range summaryQuantiles {
		q := q
		v := startupValues[q]
		emitDomain(map[string]string{"quantile": q}, func(lbls map[string]string) {
			c.st.Set("karpenter_pods_startup_duration_seconds", lbls, v*(0.8+factor*0.4))
		})
	}
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Add("karpenter_pods_startup_duration_seconds_count", lbls, factor*0.5*scale)
		c.st.Add("karpenter_pods_startup_duration_seconds_sum", lbls, factor*30*scale)
	})

	// ── karpenter_pods_state (G; leader; per phase) ──────────────────────────────
	// Simplified: one series per phase, representative pod topology labels.
	podStateTopology := map[string]string{
		"arch":               "arm64",
		"capacity_type":      "spot",
		"exported_namespace": "default",
		"instance_type":      "m6g.large",
		"nodepool":           "default",
		"zone":               "eu-west-1a",
	}
	podStateCounts := map[string]float64{"Running": 10, "Pending": 1, "Failed": 0}
	for _, phase := range podStatePhases {
		phase := phase
		v := podStateCounts[phase] * (0.9 + factor*0.2)
		extra := make(map[string]string, len(podStateTopology)+3)
		for k, val := range podStateTopology {
			extra[k] = val
		}
		extra["phase"] = phase
		extra["ready"] = "true"
		extra["scheduled"] = "true"
		emitDomain(extra, func(lbls map[string]string) {
			c.st.Set("karpenter_pods_state", lbls, v)
		})
	}

	// ── karpenter_scheduler_ignored_pods_count (G; leader) ───────────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Set("karpenter_scheduler_ignored_pods_count", lbls, 0)
	})

	// ── karpenter_scheduler_queue_depth (G; leader) ──────────────────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Set("karpenter_scheduler_queue_depth", lbls, float64(int(factor*5)))
	})

	// ── karpenter_scheduler_scheduling_duration_seconds (H; leader) ──────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Observe("karpenter_scheduler_scheduling_duration_seconds", lbls,
			schedulingBuckets, state.LEBare,
			0.005+factor*0.05*w.Shape.Noise(0.3))
	})

	// ── karpenter_scheduler_unschedulable_pods_count (G; leader) ─────────────────
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Set("karpenter_scheduler_unschedulable_pods_count", lbls, float64(int(factor*3)))
	})

	// ── karpenter_voluntary_disruption_consolidation_timeouts_total (C; leader) ──
	emitDomain(nil, func(lbls map[string]string) {
		c.st.Add("karpenter_voluntary_disruption_consolidation_timeouts_total", lbls, 0)
	})

	// ── karpenter_voluntary_disruption_decision_evaluation_duration_seconds (H; leader) ─
	// consolidation_type adds a dimension on this histogram (live recon §1.A).
	for _, ct := range voluntaryDisruptionConsolidationTypes {
		ct := ct
		emitDomain(map[string]string{"consolidation_type": ct}, func(lbls map[string]string) {
			c.st.Observe("karpenter_voluntary_disruption_decision_evaluation_duration_seconds", lbls,
				schedulingBuckets, state.LEBare,
				0.1+factor*1*w.Shape.Noise(0.4))
		})
	}

	// ── karpenter_voluntary_disruption_decisions_by_nodepool_total (C; leader) ───
	emitDomain(map[string]string{
		"consolidation_type": "single",
		"decision":           "delete",
		"nodepool":           "default",
		"reason":             "underutilized",
	}, func(lbls map[string]string) {
		c.st.Add("karpenter_voluntary_disruption_decisions_by_nodepool_total", lbls, factor*0.05*scale)
	})

	// ── karpenter_voluntary_disruption_decisions_total (C; leader) ───────────────
	for _, ct := range voluntaryDisruptionConsolidationTypes {
		ct := ct
		emitDomain(map[string]string{
			"consolidation_type": ct,
			"decision":           "delete",
			"reason":             "underutilized",
		}, func(lbls map[string]string) {
			c.st.Add("karpenter_voluntary_disruption_decisions_total", lbls, factor*0.05*scale)
		})
	}

	// ── karpenter_voluntary_disruption_eligible_nodes (G; leader) ────────────────
	for _, reason := range eligibleNodeReasons {
		reason := reason
		v := 0.0
		if reason == "underutilized" {
			v = float64(int(factor * 3))
		}
		emitDomain(map[string]string{"reason": reason}, func(lbls map[string]string) {
			c.st.Set("karpenter_voluntary_disruption_eligible_nodes", lbls, v)
		})
	}

	// ── go_* + process_* + controller_runtime_* (all pods) ───────────────────────
	c.emitGoProcMetrics(allPodMaps, base, mergeInto, factor, scale, w)

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}

	// ── JSON-structured logs (OTel/Alloy Loki stream labels) ─────────────────
	//
	// NOTE: karpenter pods are absent from Loki on the reference cluster because the static t4g.large
	// nodes they run on lack Alloy log-scrape coverage (svc-group-b.md §1.C). A real
	// production cluster with Alloy node-agent on ALL nodes ships these streams.
	// We synthesize them anyway to exercise dashboards that query karpenter logs.
	if w.Logs != nil {
		streams := c.buildLogStreams(now, cluster, leaderMaps)
		if len(streams) > 0 {
			if err := w.Logs.Write(ctx, streams); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitGoProcMetrics emits go_*, process_*, and controller_runtime_* metrics for all pods.
// These are emitted by every pod replica (not leader-elected).
func (c *Construct) emitGoProcMetrics(
	podMaps []map[string]string,
	base func() map[string]string,
	merge func(map[string]string, map[string]string) map[string]string,
	factor, scale float64,
	w *core.World,
) {
	// Helper: emit against all pods (or cluster-scoped fallback).
	emit := func(extra map[string]string, fn func(lbls map[string]string)) {
		if len(podMaps) > 0 {
			for _, pm := range podMaps {
				fn(merge(pm, extra))
			}
		} else {
			fn(merge(base(), extra))
		}
	}

	// ── go_goroutines ────────────────────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("go_goroutines", lbls, 50+factor*100)
	})

	// ── go_threads ───────────────────────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("go_threads", lbls, 12)
	})

	// ── go_gc_duration_seconds (SUMMARY; all pods) ────────────────────────────────
	for _, q := range []string{"0", "0.25", "0.5", "0.75", "1"} {
		q := q
		v := map[string]float64{"0": 0.0001, "0.25": 0.0002, "0.5": 0.0005, "0.75": 0.001, "1": 0.005}[q]
		emit(map[string]string{"quantile": q}, func(lbls map[string]string) {
			c.st.Set("go_gc_duration_seconds", lbls, v)
		})
	}
	emit(nil, func(lbls map[string]string) {
		c.st.Add("go_gc_duration_seconds_count", lbls, scale)
		c.st.Add("go_gc_duration_seconds_sum", lbls, 0.001*scale)
	})

	// ── go_memstats_alloc_bytes ───────────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("go_memstats_alloc_bytes", lbls, 50e6+factor*50e6)
	})

	// ── go_memstats_heap_alloc_bytes ─────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("go_memstats_heap_alloc_bytes", lbls, 50e6+factor*50e6)
	})

	// ── go_memstats_heap_inuse_bytes ─────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("go_memstats_heap_inuse_bytes", lbls, 60e6+factor*60e6)
	})

	// ── process_cpu_seconds_total ─────────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Add("process_cpu_seconds_total", lbls, factor*0.1*scale)
	})

	// ── process_resident_memory_bytes ────────────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("process_resident_memory_bytes", lbls, 200e6+factor*100e6)
	})

	// ── process_open_fds / process_max_fds ───────────────────────────────────────
	emit(nil, func(lbls map[string]string) {
		c.st.Set("process_open_fds", lbls, 20+factor*10)
		c.st.Set("process_max_fds", lbls, 1048576)
	})

	// ── controller_runtime_reconcile_total (all pods; per controller × result) ───
	for _, ctrl := range goRuntimeControllers {
		for _, result := range reconcileResults {
			ctrl, result := ctrl, result
			emit(map[string]string{"controller": ctrl, "result": result}, func(lbls map[string]string) {
				v := factor * 0.1 * scale
				if result == "success" {
					v = factor * scale
				}
				c.st.Add("controller_runtime_reconcile_total", lbls, v)
			})
		}
	}

	// ── controller_runtime_active_workers (all pods; per controller) ─────────────
	for _, ctrl := range goRuntimeControllers {
		ctrl := ctrl
		emit(map[string]string{"controller": ctrl}, func(lbls map[string]string) {
			c.st.Set("controller_runtime_active_workers", lbls, 1)
		})
	}

	// ── controller_runtime_max_concurrent_reconciles (all pods; per controller) ──
	for _, ctrl := range goRuntimeControllers {
		ctrl := ctrl
		emit(map[string]string{"controller": ctrl}, func(lbls map[string]string) {
			c.st.Set("controller_runtime_max_concurrent_reconciles", lbls, 10)
		})
	}
}

// buildLogStreams constructs karpenter zap-JSON log streams for one tick.
//
// Stream labels follow the OTel/Alloy convention (svc-group-b.md §1.C):
// low-card keys only — cluster, k8s_cluster_name, k8s_namespace_name, k8s_container_name,
// k8s_pod_name (leader pod), k8s_deployment_name, service_name, service_namespace,
// detected_level, log_iostream.
//
// High-card fields (reconcileID, nodeclaim name, node name) stay in the body.
// ~60 % of ticks emit a disruption/reconcile log line; ~30 % emit an error.
// Leader pod name is taken from leaderMaps when available; fallback uses the deployment name.
func (c *Construct) buildLogStreams(now time.Time, cluster string, leaderMaps []map[string]string) []loki.Stream {
	// Deterministic variation: use minute-of-hour to select message template.
	minute := now.Minute()

	// Pick a "tick type" from the minute: most ticks emit info; ~1 in 6 emit error.
	type tickShape struct {
		level string // zap level
		msg   string
		ctrl  string
		extra map[string]any // additional JSON fields (high-card → body)
	}

	// reconcileID and nodeclaim names are high-card — body only, never stream labels.
	reconcileID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		minute*1000003, minute*7, minute*13, minute*17, minute*19+now.Second())
	nodeclaimName := fmt.Sprintf("default-%c%c%c%c%c",
		'a'+rune(minute%26), 'b'+rune((minute+1)%26),
		'c'+rune((minute+2)%26), 'd'+rune((minute+3)%26),
		'e'+rune((minute+4)%26))
	nodeName := fmt.Sprintf("ip-10-1-%d-%d.eu-west-1.compute.internal",
		(minute*7+3)%256, (minute*13+5)%256)

	shapes := []tickShape{
		{
			level: "INFO",
			msg:   "disrupting node(s)",
			ctrl:  "disruption",
			extra: map[string]any{
				"command":                fmt.Sprintf("Underutilized/%s: delete: nodepools=[default]: [%s] (savings: $0.03)", nodeclaimName, nodeName),
				"decision":               "delete",
				"disrupted-node-count":   1,
				"replacement-node-count": 0,
				"pod-count":              5,
				"reconcileID":            reconcileID,
			},
		},
		{
			level: "INFO",
			msg:   "annotated nodeclaim",
			ctrl:  "nodeclaim.lifecycle",
			extra: map[string]any{
				"NodeClaim":   map[string]any{"name": nodeclaimName},
				"reconcileID": reconcileID,
			},
		},
		{
			level: "INFO",
			msg:   "tainted node",
			ctrl:  "node.termination",
			extra: map[string]any{
				"Node":        map[string]any{"name": nodeName},
				"reconcileID": reconcileID,
			},
		},
		{
			level: "INFO",
			msg:   "deleted node",
			ctrl:  "node.termination",
			extra: map[string]any{
				"Node":        map[string]any{"name": nodeName},
				"NodeClaim":   map[string]any{"name": nodeclaimName},
				"reconcileID": reconcileID,
			},
		},
		{
			level: "INFO",
			msg:   "registered node",
			ctrl:  "nodeclaim.lifecycle",
			extra: map[string]any{
				"NodeClaim":   map[string]any{"name": nodeclaimName},
				"Node":        map[string]any{"name": nodeName},
				"reconcileID": reconcileID,
			},
		},
		{
			level: "ERROR",
			msg:   "reconciling node",
			ctrl:  "node.drift",
			extra: map[string]any{
				"Node":        map[string]any{"name": nodeName},
				"reconcileID": reconcileID,
				"error":       "node not found",
			},
		},
	}

	shape := shapes[minute%len(shapes)]

	// Determine the leader pod name for the stream label.
	podName := "karpenter"
	if len(leaderMaps) > 0 {
		if p, ok := leaderMaps[0]["pod"]; ok && p != "" {
			podName = p
		}
	}

	// detected_level: map zap INFO→info, ERROR→error.
	detectedLevel := "info"
	if shape.level == "ERROR" {
		detectedLevel = "error"
	}

	// Build the zap-JSON body. Fields: level, time, logger, message, commit, controller, + extras.
	// zapTimeFormat gives RFC3339 with millisecond precision (e.g. "2026-06-16T16:18:11.123Z")
	// matching real karpenter zap output (NIT-1: RFC3339 without ms was too coarse).
	bodyFields := map[string]any{
		"level":      shape.level,
		"time":       now.UTC().Format(zapTimeFormat),
		"logger":     "controller",
		"message":    shape.msg,
		"commit":     "2be9554",
		"controller": shape.ctrl,
	}
	for k, v := range shape.extra {
		bodyFields[k] = v
	}

	bodyBytes, err := json.Marshal(bodyFields)
	if err != nil {
		// Should never happen with a fixed map; fallback to minimal body.
		bodyBytes = []byte(fmt.Sprintf(`{"level":%q,"time":%q,"message":%q}`,
			shape.level, now.UTC().Format(zapTimeFormat), shape.msg))
	}

	streamLabels := map[string]string{
		"cluster":             cluster,
		"k8s_cluster_name":    cluster,
		"k8s_namespace_name":  "kube-system",
		"k8s_container_name":  "controller",
		"k8s_pod_name":        podName,
		"k8s_deployment_name": "karpenter",
		"service_name":        "karpenter",
		"service_namespace":   "kube-system",
		"detected_level":      detectedLevel,
		"log_iostream":        "stderr",
	}

	return []loki.Stream{
		{
			Labels: streamLabels,
			Lines:  []loki.Line{{T: now, Body: string(bodyBytes)}},
		},
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// cloneMap returns a shallow copy of m.
func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// instanceTypeCores returns a representative CPU core count for an instance type.
// Values are sourced from the representative set — not exhaustive.
func instanceTypeCores(instanceType string) float64 {
	cores := map[string]float64{
		"m6g.large": 2, "m6g.xlarge": 4, "m6g.2xlarge": 8,
		"m6i.large": 2, "m6i.xlarge": 4,
		"c6g.large": 2, "c6g.xlarge": 4,
		"r6g.large": 2, "r6g.xlarge": 4,
		"t4g.large": 2,
	}
	if v, ok := cores[instanceType]; ok {
		return v
	}
	return 2
}

// instanceTypeMemBytes returns a representative memory in bytes for an instance type.
func instanceTypeMemBytes(instanceType string) float64 {
	const gib = 1024 * 1024 * 1024
	mem := map[string]float64{
		"m6g.large": 8 * gib, "m6g.xlarge": 16 * gib, "m6g.2xlarge": 32 * gib,
		"m6i.large": 8 * gib, "m6i.xlarge": 16 * gib,
		"c6g.large": 4 * gib, "c6g.xlarge": 8 * gib,
		"r6g.large": 16 * gib, "r6g.xlarge": 32 * gib,
		"t4g.large": 8 * gib,
	}
	if v, ok := mem[instanceType]; ok {
		return v
	}
	return 8 * gib
}

// offeringPrice returns a representative on-demand/spot/reserved price (USD/hr) for an instance type.
func offeringPrice(instanceType, capacityType string) float64 {
	// Approximate on-demand prices (eu-west-1, 2026 approx).
	onDemand := map[string]float64{
		"m6g.large": 0.0770, "m6g.xlarge": 0.1540, "m6g.2xlarge": 0.3080,
		"m6i.large": 0.0960, "m6i.xlarge": 0.1920,
		"c6g.large": 0.0680, "c6g.xlarge": 0.1360,
		"r6g.large": 0.1008, "r6g.xlarge": 0.2016,
		"t4g.large": 0.0672,
	}
	base := onDemand[instanceType]
	if base == 0 {
		base = 0.077
	}
	if capacityType == "spot" {
		return base * 0.4 // ~60% spot discount typical
	}
	if capacityType == "reserved" {
		return base * 0.6 // ~40% 1yr no-upfront RI discount typical
	}
	return base
}

// nodepoolUsageValue returns a representative usage value for a resource_type.
func nodepoolUsageValue(resourceType string, factor float64) float64 {
	switch resourceType {
	case "cpu":
		return 1.5 + factor*0.5
	case "memory":
		return float64(6 * 1024 * 1024 * 1024)
	case "nodes":
		return 3 + factor*2
	case "pods":
		return 15 + factor*10
	default:
		return 0
	}
}
