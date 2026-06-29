// SPDX-License-Identifier: AGPL-3.0-only

// Package ec2 implements the "ec2" construct (ARCHITECTURE §2, kind="ec2",
// Scope=ScopeBlueprint). It emits aws_ec2_* per-instance metrics for every node in the
// fixture Cluster, plus ASG-level aggregates. One series set per node, each keyed by
// dimension_InstanceId — the EC2↔EKS correlation seam (ARCHITECTURE I12).
//
// CW naming law (ARCHITECTURE I6 / signals/cw.md [slug: cw-naming]):
//   - cpuutilization — NO underscore (CPUUtilization → consecutive-caps run together)
//   - ebsread_bytes / ebswrite_bytes / ebsread_ops / ebswrite_ops — EBS runs together
//   - status_check_failed / _instance / _system / _attached_ebs — underscores preserved
//   - cpucredit_balance — Credit→credit / Balance transition splits (single-cap B)
//
// Per-period gauge invariant (ARCHITECTURE I5 / signals/cw.md [slug: cw-ec2] traps):
//
//	All _sum series are per-period gauges — never rate(). Only state.Set is used here;
//	no state.Add so no cumulative Add behaviour on any series.
package ec2

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "ec2"
	interval = 60 * time.Second
)

// Config is the construct config struct (empty — all identity comes from fixtures).
// NewConfig returns a pointer to a zero Config; the blueprint loader decodes the
// construct's YAML section into it (unknown fields are an error per strict yaml.v3).
type Config struct{}

// Construct is one ec2 instance covering all EKS worker nodes in a single Cluster.
type Construct struct {
	cloud    *fixture.Cloud
	clust    *fixture.Cluster
	st       *state.State
	maxNodes int // high-water mark of live node count; used for scale-down retirement
}

// New builds a Construct from the cfg pointer returned by NewConfig and the resolved
// fixture set. Build returns an error if fx.Cloud or fx.Cluster is nil (both are
// required to produce the EC2↔EKS correlation series).
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cloud == nil {
		return nil, errors.New("ec2: fixture.Cloud is required (nil)")
	}
	if fx.Cluster == nil {
		return nil, errors.New("ec2: fixture.Cluster is required (nil)")
	}
	return &Construct{
		cloud: fx.Cloud,
		clust: fx.Cluster,
		st:    state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — this construct emits metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one metric batch covering all nodes in the cluster. All series are
// per-period gauges (state.Set only — ARCHITECTURE I5).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)

	// ── Live node derivation ──────────────────────────────────────────────────────
	// Compute the live pod total across all cluster workloads, respecting any
	// control-plane scaling override (w.Scaling). Falls back to declared Replicas when
	// Scaling is nil (default / no control plane). Then re-derive the node set from
	// the same deterministic function the k8s construct uses — ensuring byte-identical
	// node identities at any pod total (ARCHITECTURE I12).
	total := 0
	for _, wl := range c.clust.Workloads {
		n := wl.Replicas
		if w.Scaling != nil {
			n = w.Scaling.Count(wl.Name, wl.Replicas)
		}
		total += n
	}
	nodes := fixture.DeriveNodes(c.clust.Seed, c.clust.Name, c.clust.NodeGroups, c.clust.Region, total)
	nodeCount := float64(len(nodes))

	cloud := c.cloud
	region := cloud.Region
	accountID := cloud.AccountID

	// ── Universal labels present on every series in this namespace ───────────────
	// cwBase returns a fresh map with the 5 universal CW labels. It is called per
	// series-set so each series carries its own label map (no aliasing).
	cwBase := func(name, job string, extraDims map[string]string) map[string]string {
		m := map[string]string{
			"account_id": accountID,
			"region":     region,
			"namespace":  "AWS/EC2",
			"job":        "cloud/aws/" + job,
			"name":       name,
		}
		for k, v := range extraDims {
			m[k] = v
		}
		return m
	}

	// ── ASG-level series (one set per cluster) ────────────────────────────────────
	// dimension_AutoScalingGroupName identifies the node group. The predecessor emits these
	// alongside the per-instance series; both coexist in real CloudWatch.
	asgName := c.clust.Name // synthkit uses the cluster name as the ASG name
	asgDims := map[string]string{"dimension_AutoScalingGroupName": asgName}

	// cpuutilization (%) — business-hours shape, cluster-aggregate
	cpuASG := 28.0 + factor*35 + w.Shape.NormFloat64()*5
	cpuASG = clamp(cpuASG, 1, 95)
	c.setStats("aws_ec2_cpuutilization", cwBase("global", "ec2", asgDims), cpuASG)

	// network_in / network_out (bytes per 60-s period)
	netIn := factor * nodeCount * 5e6 * 60 * (1 + w.Shape.NormFloat64()*0.2)
	if netIn < 0 {
		netIn = 0
	}
	c.setStats("aws_ec2_network_in", cwBase("global", "ec2", asgDims), netIn)
	c.setStats("aws_ec2_network_out", cwBase("global", "ec2", asgDims), netIn*0.3)

	// status_check_failed* — 0 at baseline
	c.setStats("aws_ec2_status_check_failed", cwBase("global", "ec2", asgDims), 0)
	c.setStats("aws_ec2_status_check_failed_instance", cwBase("global", "ec2", asgDims), 0)
	c.setStats("aws_ec2_status_check_failed_system", cwBase("global", "ec2", asgDims), 0)
	c.setStats("aws_ec2_status_check_failed_attached_ebs", cwBase("global", "ec2", asgDims), 0)

	// ebsread_bytes / ebswrite_bytes (⚠ EBS runs together — I6 / signals/cw.md [slug: cw-naming])
	ebsRdBytes := factor * nodeCount * 500e3 * 60 * (1 + w.Shape.NormFloat64()*0.3)
	if ebsRdBytes < 0 {
		ebsRdBytes = 0
	}
	ebsWrBytes := factor * nodeCount * 200e3 * 60 * (1 + w.Shape.NormFloat64()*0.3)
	if ebsWrBytes < 0 {
		ebsWrBytes = 0
	}
	c.setStats("aws_ec2_ebsread_bytes", cwBase("global", "ec2", asgDims), ebsRdBytes)
	c.setStats("aws_ec2_ebswrite_bytes", cwBase("global", "ec2", asgDims), ebsWrBytes)

	// ebsread_ops / ebswrite_ops
	ebsRdOps := factor * nodeCount * 150 * 60
	if ebsRdOps < 0 {
		ebsRdOps = 0
	}
	ebsWrOps := factor * nodeCount * 80 * 60
	if ebsWrOps < 0 {
		ebsWrOps = 0
	}
	c.setStats("aws_ec2_ebsread_ops", cwBase("global", "ec2", asgDims), ebsRdOps)
	c.setStats("aws_ec2_ebswrite_ops", cwBase("global", "ec2", asgDims), ebsWrOps)

	// cpucredit_balance (burstable instances: drains at high load)
	creditBalance := 350.0 - factor*250
	if creditBalance < 10 {
		creditBalance = 10
	}
	c.setStats("aws_ec2_cpucredit_balance", cwBase("global", "ec2", asgDims), creditBalance)

	// ── Per-instance series (one series-set per Node) ─────────────────────────────
	// THE CORRELATION SEAM (ARCHITECTURE I12): dimension_InstanceId == Node.InstanceID
	// byte-exact; the k8s construct emits kube_node_info.provider_id with the same id;
	// a label_replace/regex join closes node (k8s) → EC2 instance (CloudWatch).
	for i, n := range nodes {
		arn := fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", region, accountID, n.InstanceID)
		instDims := map[string]string{"dimension_InstanceId": n.InstanceID}
		instLbls := cwBase(arn, "ec2", instDims)

		// CPU: per-node model based on node index and factor; matches the k8s
		// node-exporter cpu_idle model so the two lanes track together.
		instCPU := nodeCPUPercent(i, factor) + w.Shape.NormFloat64()*1.5
		instCPU = clamp(instCPU, 1, 99)
		c.setStats("aws_ec2_cpuutilization", instLbls, instCPU)

		// Network: per-node share of the ASG aggregate with jitter
		instNetIn := netIn / nodeCount * (1 + w.Shape.NormFloat64()*0.15)
		if instNetIn < 0 {
			instNetIn = 0
		}
		c.setStats("aws_ec2_network_in", instLbls, instNetIn)
		c.setStats("aws_ec2_network_out", instLbls, instNetIn*0.3)

		// Status checks — 0 at baseline (same as ASG)
		c.setStats("aws_ec2_status_check_failed", instLbls, 0)
		c.setStats("aws_ec2_status_check_failed_instance", instLbls, 0)
		c.setStats("aws_ec2_status_check_failed_system", instLbls, 0)
	}

	// ── aws_ec2_info (metadata scraper) ──────────────────────────────────────────
	// One info series per node. Carries tag_VpcId per signals/cw.md [slug: cw-ec2].
	// No stat suffix — info series are plain gauge=1 (I13: absent dim = omitted).
	for _, n := range nodes {
		arn := fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", region, accountID, n.InstanceID)
		infoLbls := cwBase(arn, "ec2", map[string]string{
			"dimension_InstanceId": n.InstanceID,
			"tag_VpcId":            cloud.VpcID,
		})
		c.st.Set("aws_ec2_info", infoLbls, 1)
	}

	// ── Scale-down retirement ─────────────────────────────────────────────────────
	// If the live node count shrank below the high-water mark, retire series for
	// instances that are no longer part of the derived node set. This prevents stale
	// per-instance series from lingering in Prometheus at their last value.
	active := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		active[n.InstanceID] = true
	}
	if len(nodes) < c.maxNodes {
		c.st.DropWhere(func(_ string, lbls map[string]string) bool {
			id := lbls["dimension_InstanceId"]
			return id != "" && !active[id]
		})
	}
	if len(nodes) > c.maxNodes {
		c.maxNodes = len(nodes)
	}

	// Collect and write
	batch := c.st.Collect(now)
	return w.Metrics.Write(ctx, batch)
}

// setStats emits the five CW stat suffixes for one per-period EC2 metric. The value
// policy (_maximum = +5%, _minimum = −10%, _sum = value×60 per-period total, 60 samples
// at 1/s) is EC2-specific and lives here; the suffix mechanic + the per-period-gauge rule
// (I5) + per-suffix label isolation live in cw.EmitStats.
func (c *Construct) setStats(base string, lbls map[string]string, value float64) {
	cw.EmitStats(c.st, base, lbls, cw.StatSet{
		Average:     value,
		Maximum:     value * 1.05,
		Minimum:     value * 0.9,
		Sum:         value * 60,
		SampleCount: 60,
	})
}

// nodeCPUPercent is the per-node CPU model used to correlate with the k8s node-exporter
// cpu_idle model (mirrors the predecessor's canon.NodeCPUPercent: base 15% + 5%×idx, scaled
// by factor; floor 5%).
func nodeCPUPercent(idx int, factor float64) float64 {
	base := 15.0 + float64(idx)*5.0
	return clamp(base+factor*40, 5, 95)
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
