// SPDX-License-Identifier: AGPL-3.0-only

// Package vpccni implements the "vpc_cni" construct (ARCHITECTURE §2, kind="vpc_cni",
// Scope=ScopeSubstrate). It emits the full AWS VPC CNI (amazon-vpc-cni-k8s) metric family
// that the Grafana integrations/aws-vpc-cni scrape job collects. One instance per cluster.
//
// Kind: "vpc_cni"
// Scope: ScopeSubstrate — cluster+k8s_cluster_name disambiguate; NO blueprint label.
// Signals: Metrics only.
// Interval: 60s
// Requires: fx.Cluster (for cluster name and node count).
//
// Signal contract: signals/k8s-addons.md [slug: k8s-vpc-cni]
// Predecessor reference: generator/internal/emit/vpccni.go (READ-ONLY)
//
// ⚠ All metrics have prefix "awscni_"; the full metric Name is the name (no namespace prefix).
//
// Families:
//
//	awscni_eni_allocated          G  (base)
//	awscni_total_ip_addresses     G  (base)
//	awscni_assigned_ip_addresses  G  (base)
//	awscni_eni_max                G  (base)
//	awscni_ip_max                 G  (base)
//	awscni_ipamd_action_inprogress  G  fn
//	awscni_ipamd_error_count      C  fn
//	awscni_add_ip_req_count       C  (base)
//	awscni_del_ip_req_count       C  reason
//	awscni_aws_api_latency_ms_count  C  api,error,status  (Prometheus summary: count+sum, NO quantiles)
//	awscni_aws_api_latency_ms_sum    C  api,error,status
//	awscni_aws_api_error_count    C  api,error
//	awscni_ec2api_req_count       C  fn
//	awscni_ec2api_error_count     C  fn
//	awscni_reconcile_count        C  fn
//	awscni_total_ipv4_prefixes    G  (base)  =0 (no prefix delegation)
//	awscni_no_available_ip_addresses  C  (base)  =0 normally
//	awscni_build_info             G  version,goversion
//
// Value enums:
//
//	fn (IPAMD): nodeIPPoolReconcile, eniIPPoolReconcile, decreaseIPPool, increaseIPPool
//	fn (EC2):   AssignPrivateIpAddresses, DescribeNetworkInterfaces, AttachNetworkInterface
//	api:        EC2:DescribeNetworkInterfaces, EC2:AssignPrivateIpAddresses
//	reason:     pod_deleted, failed_node
package vpccni

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "vpc_cni"
	interval = 60 * time.Second

	// vpcCNIJob is the Prometheus scrape job label. DEPLOYMENT-DEFINED default (SK-10):
	// k8s-monitoring does NOT scrape vpc-cni by default (EKS-managed addon), so there is
	// no canonical integrations/<addon> job — the real label is whatever the operator's
	// PodMonitor/scrape config sets (instance=<podIP>:61678 for the ipamd endpoint).
	vpcCNIJob = "integrations/aws-vpc-cni"
)

// vpcCNIIPAMDFns are the IPAMD action/error functions reported by VPC CNI.
var vpcCNIIPAMDFns = []string{
	"nodeIPPoolReconcile",
	"eniIPPoolReconcile",
	"decreaseIPPool",
	"increaseIPPool",
}

// vpcCNIEC2Fns are the EC2 API function labels.
var vpcCNIEC2Fns = []string{
	"AssignPrivateIpAddresses",
	"DescribeNetworkInterfaces",
	"AttachNetworkInterface",
}

// vpcCNIAPIs are the API names used in aws_api_latency_ms series.
var vpcCNIAPIs = []string{
	"EC2:DescribeNetworkInterfaces",
	"EC2:AssignPrivateIpAddresses",
}

// Config is the construct config struct (empty — all identity from fixtures).
type Config struct{}

// Construct is one vpc_cni instance covering one EKS cluster.
type Construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// New builds a Construct from the cfg pointer returned by NewConfig and the resolved
// fixture set. Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("vpc_cni: fixture.Cluster is required (nil)")
	}
	return &Construct{
		clust: fx.Cluster,
		st:    state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one metric batch for the AWS VPC CNI instance on this cluster.
// Counters accumulate across ticks (state.Add — ARCHITECTURE I3); gauges are set per
// tick (state.Set). No histogram: awscni_aws_api_latency_ms is a Prometheus SUMMARY that
// emits ONLY _count + _sum (no {quantile=…} series) — live-confirmed (SK-11, ipamd
// :61678/metrics capture 2026-06-13): labels api/error/status, error a string bool, status
// an HTTP code string.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	nodeCount := len(c.clust.Nodes)
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)
	scale := interval.Seconds() / 30.0 // cadence-invariant volume (I3)

	// base returns a fresh label map with the universal labels for this cluster.
	base := func() map[string]string {
		return map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"job":              vpcCNIJob,
		}
	}

	// withExtra merges base labels with extra labels into a new map.
	withExtra := func(extra map[string]string) map[string]string {
		b := base()
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	// Per-cluster pool capacity scaled with node count.
	// ~3 ENIs per instance, ~14 IPs per instance are typical EKS values. ⓐ
	maxENIs := float64(nodeCount * 3)
	maxIPs := float64(nodeCount * 14)
	assignedIPs := maxIPs * (0.3 + factor*0.5)
	allocatedENIs := 2.0 + factor*float64(nodeCount)

	// ── awscni_eni_allocated (G) ─────────────────────────────────────────────
	c.st.Set("awscni_eni_allocated", base(), allocatedENIs)

	// ── awscni_total_ip_addresses (G) — warm IP pool (slightly above assigned) ─
	c.st.Set("awscni_total_ip_addresses", base(), assignedIPs*1.1)

	// ── awscni_assigned_ip_addresses (G) — IPs currently assigned to pods ──────
	c.st.Set("awscni_assigned_ip_addresses", base(), assignedIPs)

	// ── awscni_eni_max (G) — maximum ENIs for this instance type ────────────────
	c.st.Set("awscni_eni_max", base(), maxENIs)

	// ── awscni_ip_max (G) — maximum IPs for this instance type ──────────────────
	c.st.Set("awscni_ip_max", base(), maxIPs)

	// ── awscni_ipamd_action_inprogress (G; fn) ────────────────────────────────
	for _, fn := range vpcCNIIPAMDFns {
		inProgress := 0.0
		if w.Shape.Float64() < 0.1*factor {
			inProgress = 1
		}
		c.st.Set("awscni_ipamd_action_inprogress", withExtra(map[string]string{"fn": fn}), inProgress)
	}

	// ── awscni_ipamd_error_count (C; fn) ─────────────────────────────────────
	for _, fn := range vpcCNIIPAMDFns {
		c.st.Add("awscni_ipamd_error_count", withExtra(map[string]string{"fn": fn}), 0)
	}

	// ── awscni_add_ip_req_count (C) — pod scheduling requests ────────────────
	c.st.Add("awscni_add_ip_req_count", base(), factor*3*scale)

	// ── awscni_del_ip_req_count (C; reason) ──────────────────────────────────
	c.st.Add("awscni_del_ip_req_count", withExtra(map[string]string{"reason": "pod_deleted"}), factor*2*scale)
	c.st.Add("awscni_del_ip_req_count", withExtra(map[string]string{"reason": "failed_node"}), 0)

	// ── awscni_aws_api_latency_ms (summary: count+sum, NO quantiles) ─────────
	// Live-confirmed (SK-11): the real VPC CNI summary emits only _count + _sum, no
	// {quantile} series. error is a string bool, status an HTTP code string.
	for _, api := range vpcCNIAPIs {
		lbls := withExtra(map[string]string{"api": api, "error": "false", "status": "200"})
		c.st.Add("awscni_aws_api_latency_ms_count", lbls, (1+factor*3)*scale)
		c.st.Add("awscni_aws_api_latency_ms_sum", lbls, (50+factor*100)*scale)
	}

	// ── awscni_aws_api_error_count (C; api,error) ────────────────────────────
	c.st.Add("awscni_aws_api_error_count", withExtra(map[string]string{
		"api": "EC2:DescribeNetworkInterfaces", "error": "RequestLimitExceeded",
	}), 0)

	// ── awscni_ec2api_req_count (C; fn) ─────────────────────────────────────
	for _, fn := range vpcCNIEC2Fns {
		c.st.Add("awscni_ec2api_req_count", withExtra(map[string]string{"fn": fn}), (1+factor*3)*scale)
	}

	// ── awscni_ec2api_error_count (C; fn) ────────────────────────────────────
	for _, fn := range vpcCNIEC2Fns {
		c.st.Add("awscni_ec2api_error_count", withExtra(map[string]string{"fn": fn}), 0)
	}

	// ── awscni_reconcile_count (C; fn) ───────────────────────────────────────
	for _, fn := range vpcCNIIPAMDFns {
		c.st.Add("awscni_reconcile_count", withExtra(map[string]string{"fn": fn}), scale)
	}

	// ── awscni_total_ipv4_prefixes (G) — prefix delegation disabled ⓐ ────────
	c.st.Set("awscni_total_ipv4_prefixes", base(), 0)

	// ── awscni_no_available_ip_addresses (G) — gauge per signals/k8s-addons.md [slug: k8s-vpc-cni] (no (C) marker) ─
	c.st.Set("awscni_no_available_ip_addresses", base(), 0)

	// ── awscni_build_info (G; version,goversion) ──────────────────────────────
	c.st.Set("awscni_build_info", withExtra(map[string]string{
		"version":   "v1.18.2",
		"goversion": "go1.22.3",
	}), 1)

	return w.Metrics.Write(ctx, c.st.Collect(now))
}
