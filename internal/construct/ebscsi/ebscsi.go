// SPDX-License-Identifier: AGPL-3.0-only

// Package ebscsi implements the "ebs_csi" construct (ARCHITECTURE §2, kind="ebs_csi",
// Scope=ScopeSubstrate). It emits AWS EBS CSI Driver controller metrics scraped from
// the controller's :3301 metrics endpoint (EKS-managed addon).
//
// Kind:     "ebs_csi"
// Scope:    core.ScopeSubstrate (no blueprint label; cluster disambiguates)
// Signals:  []{core.Metrics}
// Interval: 60 s
//
// Families emitted (controller endpoint only — no per-volume series):
//   - aws_ebs_csi_api_request_duration_seconds (H; request = EC2 API request type)
//   - aws_ebs_csi_ec2_collector_duration_seconds (H; controller-level, no per-volume label)
//   - aws_ebs_csi_ec2_collector_scrapes_total (C; controller-level, no per-volume label)
//
// NOT emitted:
//   - Per-volume / per-node NVMe series (volume_id-labelled): the live controller
//     endpoint emits NO volume_id-labelled series at all. Per-volume identity lives in
//     CloudWatch AWS/EBS and kubelet_volume_stats_* — NOT in this driver (SK-12).
//   - aws_ebs_csi_api_request_errors_total / aws_ebs_csi_api_request_throttles_total:
//     absent in healthy steady state; only appear under actual errors/throttles (SK-12).
//   - csi_sidecar_operations_seconds: a separate sidecar endpoint, not the controller.
//   - deprecated cloudprovider_aws_* family (signals/k8s-addons.md [slug: k8s-ebs-csi] traps).
//
// Job label: "integrations/aws-ebs-csi-driver" is a deployment-defined default.
// k8s-monitoring does not scrape EBS-CSI by default; the real job label is whatever
// the operator's scrape config sets (SK-10).
package ebscsi

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "ebs_csi"
	interval = 60 * time.Second
	// job is the deployment-defined default scrape job label (SK-10: k8s-monitoring does
	// not scrape EBS-CSI by default; operators set whatever their scrape config uses).
	job = "integrations/aws-ebs-csi-driver"
)

// ebsCSIRequests are the EC2 API request type values for the request label on
// aws_ebs_csi_api_request_duration_seconds.
var ebsCSIRequests = []string{
	"CreateVolume", "AttachVolume", "DetachVolume", "DescribeVolumes",
	"DeleteVolume", "ListVolumes", "DescribeSnapshots",
}

// defaultSecondsBuckets mirrors the Prometheus client default seconds bucket set used
// for the API request duration and EC2 collector duration histograms.
var defaultSecondsBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Config is the construct config struct (empty — all identity comes from fixtures).
type Config struct{}

// Construct is one ebs_csi instance covering the EBS CSI controller in a single Cluster.
type Construct struct {
	cluster *fixture.Cluster
	st      *state.State
}

// New builds a Construct from the cfg pointer and the resolved fixture set.
// Returns an error if fx.Cluster is nil (required for cluster identity labels).
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("ebs_csi: fixture.Cluster is required (nil)")
	}
	return &Construct{
		cluster: fx.Cluster,
		st:      state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics only.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one metric batch covering the EBS CSI controller metrics.
// Counters/histograms accumulate with state.Add/Observe (ARCHITECTURE I3).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	factor := w.Shape.Factor(now, c.cluster.Env.Weight, c.cluster.Env.NonProd)
	if factor < 0 {
		factor = 0
	}
	scale := interval.Seconds() / 30.0

	clusterName := c.cluster.Name

	base := map[string]string{
		"cluster":          clusterName,
		"k8s_cluster_name": clusterName,
		"job":              job,
	}

	withExtra := func(extra map[string]string) map[string]string {
		m := make(map[string]string, len(base)+len(extra))
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// ── aws_ebs_csi_api_request_duration_seconds (H; request) ────────────────
	// One histogram per EC2 API request type; a few observations per tick.

	for _, req := range ebsCSIRequests {
		samples := 1 + w.Shape.IntN(3)
		for range samples {
			c.st.Observe("aws_ebs_csi_api_request_duration_seconds",
				withExtra(map[string]string{"request": req}),
				defaultSecondsBuckets, state.LEBare,
				0.05+factor*0.3*w.Shape.Noise(0.4))
		}
	}

	// ── aws_ebs_csi_ec2_collector_duration_seconds (H; controller-level) ─────
	// A few observations per tick at small durations.

	collectorObs := 2 + w.Shape.IntN(3)
	for range collectorObs {
		c.st.Observe("aws_ebs_csi_ec2_collector_duration_seconds",
			base,
			defaultSecondsBuckets, state.LEBare,
			0.01+factor*0.05*w.Shape.Noise(0.3))
	}

	// ── aws_ebs_csi_ec2_collector_scrapes_total (C; controller-level) ────────
	// Small positive delta per tick, accumulated cumulatively via state.Add.

	scrapesDelta := (1 + factor*2) * scale
	c.st.Add("aws_ebs_csi_ec2_collector_scrapes_total", base, scrapesDelta)

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// Registration returns the core.ConstructReg for the "ebs_csi" kind.
// The composition root's catalog wiring file calls this; no init() self-registration
// (ARCHITECTURE §2).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      kind,
		Doc:       "AWS EBS CSI Driver controller metrics (aws_ebs_csi_api_request_duration_seconds, aws_ebs_csi_ec2_collector_duration_seconds, aws_ebs_csi_ec2_collector_scrapes_total); no per-volume series (SK-12)",
		Scope:     core.ScopeSubstrate,
		NewConfig: func() any { return &Config{} },
		Build:     New,
	}
}
