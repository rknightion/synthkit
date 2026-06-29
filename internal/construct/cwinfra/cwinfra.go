// SPDX-License-Identifier: AGPL-3.0-only

// Package cwinfra is the synthkit construct for the AWS CloudWatch infrastructure
// metric-stream families: ALB, NLB, EBS, NAT Gateway, S3, EKS control plane, and
// Firehose self-monitoring.
//
// Kind:     "cw_infra"
// Scope:    core.ScopeBlueprint  (blueprint label stamped by the runner's scoped writer)
// Signals:  []{core.Metrics}
// Interval: 60 s  (CloudWatch 1-min resolution floor)
//
// # Naming law (ARCHITECTURE I6 / signals/cw.md [slug: cw-naming])
//
//	series = "aws_" + lower(strip_slash(namespace)) + "_" + lower(metric) + "_" + stat
//	stat ∈ {_sum, _average, _maximum, _minimum, _sample_count}
//
// Acronym/casing traps (lab-verified — must not be regressed):
//
//	CPUUtilization              → cpuutilization           (NOT cpu_utilization)
//	HTTPCode_Target_5XX_Count   → httpcode_target_5_xx_count
//	UnHealthyHostCount          → un_healthy_host_count    (NOT unhealthy_…)
//	EBSReadBytes                → ebsread_bytes            (EBS token runs together)
//
// # Per-period gauge invariant (ARCHITECTURE I5)
//
// ALL CloudWatch _sum series are per-period gauges (not cumulative counters). Every
// metric series — including _sum-suffixed ones — is written with state.Set, NEVER
// state.Add. The five-stat model therefore pushes fresh point-in-time values each tick.
//
// # Fixture wiring
//
//   - fx.Cloud is REQUIRED (error if nil): AccountID / Region / VpcID / NATGatewayIDs.
//   - fx.Env  is REQUIRED (error if nil): traffic weight and non-prod flag.
//   - fx.Cluster is OPTIONAL: when present, EBS volumes and EKS CP metrics are derived
//     from the node list; when absent, static counts apply.
package cwinfra

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// ── Registration ──────────────────────────────────────────────────────────────

// Registration returns the core.ConstructReg for the "cw_infra" kind.
// The composition root's catalog wiring file calls this function; no init()
// self-registration occurs (ARCHITECTURE §2).
func Registration() core.ConstructReg {
	return core.ConstructReg{
		Kind:      "cw_infra",
		Doc:       "AWS CloudWatch infrastructure metric-stream families (ALB/NLB/EBS/NAT/S3/EKS/Firehose)",
		Scope:     core.ScopeBlueprint,
		NewConfig: func() any { return &Config{} },
		Build: func(cfg any, fx *fixture.Set) (core.Construct, error) {
			c := cfg.(*Config)
			if fx.Cloud == nil {
				return nil, fmt.Errorf("cw_infra: fx.Cloud is required")
			}
			if fx.Env == nil {
				return nil, fmt.Errorf("cw_infra: fx.Env is required")
			}
			return &construct{cfg: *c, fx: fx, st: state.NewState()}, nil
		},
	}
}

// ── Config ────────────────────────────────────────────────────────────────────

// Config is the YAML config struct for the cw_infra construct.
// Unknown fields are rejected by strict yaml.v3 decoding at blueprint load.
//
// # Every family is independently disableable (ALBs/S3Buckets via *int, the rest via *bool)
//
// ALBs and S3Buckets are *int COUNTS with full toggle parity to the *bool families:
//   - key omitted (nil)  → default count (1 ALB, 2 S3 buckets)
//   - explicit 0         → family fully DISABLED (no series, no _info) — a real account
//     can have zero ALBs or zero buckets
//   - explicit N         → N instances
//
// NLB, EBS, NATGateway, EKS, Firehose, and PrivateLink use *bool with the same omitted-vs-explicit
// distinction: nil → default true, an explicit "nlb: false" disables the family.
//
// The pointer types exist so the YAML decoder can tell "key omitted" (nil → default) from
// "explicit zero/false" (→ disabled); a plain int/bool cannot express that difference. (This
// is the construct's build-time config, decoded once from the blueprint — distinct from the
// control-plane State, which carries the I24 no-omitempty JSON round-trip guarantee.)
type Config struct {
	// ALBs is the number of Application Load Balancer instances to emit: nil/omitted →
	// default 1; explicit 0 disables the ALB family entirely; N emits N. See albCount.
	ALBs *int `yaml:"albs"`
	// S3Buckets is the number of S3 buckets to emit: nil/omitted → default 2; explicit 0
	// disables the S3 family entirely; N emits N. See s3Count.
	S3Buckets   *int  `yaml:"s3_buckets"`
	Firehose    *bool `yaml:"firehose"`     // emit Firehose pipeline-health metrics (default true)
	NLB         *bool `yaml:"nlb"`          // emit AWS/NetworkELB family (default true)
	EBS         *bool `yaml:"ebs"`          // emit AWS/EBS family (default true)
	NATGateway  *bool `yaml:"nat_gateway"`  // emit AWS/NATGateway family (default true)
	EKS         *bool `yaml:"eks"`          // emit AWS/EKS control-plane family (default true)
	PrivateLink *bool `yaml:"private_link"` // emit AWS/PrivateLink endpoints+services families (default true)
}

// Default instance counts when the ALBs/S3Buckets key is omitted (nil pointer).
const (
	defaultALBs      = 1
	defaultS3Buckets = 2
)

// albCount returns the effective ALB instance count: nil → defaultALBs, an explicit value
// otherwise (0 disables the family). Negative values are clamped to 0 (also disabled).
func (c *Config) albCount() int {
	if c.ALBs == nil {
		return defaultALBs
	}
	if *c.ALBs < 0 {
		return 0
	}
	return *c.ALBs
}

// s3Count returns the effective S3 bucket count: nil → defaultS3Buckets, an explicit value
// otherwise (0 disables the family). Negative values are clamped to 0 (also disabled).
func (c *Config) s3Count() int {
	if c.S3Buckets == nil {
		return defaultS3Buckets
	}
	if *c.S3Buckets < 0 {
		return 0
	}
	return *c.S3Buckets
}

// firehoseEnabled returns true unless Config.Firehose is explicitly set to false.
func (c *Config) firehoseEnabled() bool {
	if c.Firehose == nil {
		return true
	}
	return *c.Firehose
}

// nlbEnabled returns true unless Config.NLB is explicitly set to false.
func (c *Config) nlbEnabled() bool {
	if c.NLB == nil {
		return true
	}
	return *c.NLB
}

// ebsEnabled returns true unless Config.EBS is explicitly set to false.
func (c *Config) ebsEnabled() bool {
	if c.EBS == nil {
		return true
	}
	return *c.EBS
}

// natGatewayEnabled returns true unless Config.NATGateway is explicitly set to false.
func (c *Config) natGatewayEnabled() bool {
	if c.NATGateway == nil {
		return true
	}
	return *c.NATGateway
}

// eksEnabled returns true unless Config.EKS is explicitly set to false.
func (c *Config) eksEnabled() bool {
	if c.EKS == nil {
		return true
	}
	return *c.EKS
}

// privateLinkEnabled returns true unless Config.PrivateLink is explicitly set to false.
func (c *Config) privateLinkEnabled() bool {
	if c.PrivateLink == nil {
		return true
	}
	return *c.PrivateLink
}

// All families resolve their effective count/enablement lazily via albCount/s3Count and the
// XxxEnabled helpers — nil pointer means "unset" which reads as the default. There is no
// up-front defaulting pass, so an explicit 0/false in the config is preserved (and disables
// the family) rather than being floored away.

// ── construct ─────────────────────────────────────────────────────────────────

type construct struct {
	cfg Config
	fx  *fixture.Set
	st  *state.State
}

func (c *construct) Kind() string                { return "cw_infra" }
func (c *construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one complete CloudWatch infrastructure tick into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	fx := c.fx
	factor := w.Shape.Factor(now, fx.Env.Weight, fx.Env.NonProd)
	if factor < 0 {
		factor = 0
	}

	azs := azsForRegion(fx.Cloud.Region)

	c.emitInfoSeries(fx)
	c.emitALB(factor, fx, azs, w)
	if c.cfg.nlbEnabled() {
		c.emitNLB(factor, fx, azs, w)
	}
	if c.cfg.ebsEnabled() {
		c.emitEBS(factor, fx, w)
	}
	if c.cfg.natGatewayEnabled() {
		c.emitNATGW(factor, fx, w)
	}
	c.emitS3(factor, fx, w)
	if c.cfg.eksEnabled() && fx.Cluster != nil {
		c.emitEKS(factor, fx, w)
	}
	if c.cfg.firehoseEnabled() {
		c.emitFirehose(factor, fx, w)
	}
	if c.cfg.privateLinkEnabled() {
		c.emitPrivateLink(factor, fx, w)
	}

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// ── _info series (one per namespace per account, value=1) ────────────────────

func (c *construct) emitInfoSeries(fx *fixture.Set) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region
	vpcID := fx.Cloud.VpcID
	tags := map[string]string{"tag_VpcId": vpcID}

	// ALB info — one per configured ALB
	for i := range c.cfg.albCount() {
		st.Set("aws_applicationelb_info", cwInfoLabels(accountID, region, "AWS/ApplicationELB", "applicationelb",
			albARN(fx.Seed, accountID, region, i), tags), 1)
	}
	// NLB info — one instance
	if c.cfg.nlbEnabled() {
		st.Set("aws_networkelb_info", cwInfoLabels(accountID, region, "AWS/NetworkELB", "networkelb",
			nlbARN(fx.Seed, accountID, region), tags), 1)
	}
	// EBS info — one per volume
	if c.cfg.ebsEnabled() {
		for _, volID := range ebsVolumeIDs(fx) {
			ebsTags := map[string]string{"tag_VpcId": vpcID, "dimension_VolumeId": volID}
			st.Set("aws_ebs_info", cwInfoLabels(accountID, region, "AWS/EBS", "ebs",
				ebsVolumeARN(accountID, region, volID), ebsTags), 1)
		}
	}
	// NAT GW info — one per NATGatewayID entry in fx.Cloud
	if c.cfg.natGatewayEnabled() {
		for _, natID := range fx.Cloud.NATGatewayIDs {
			st.Set("aws_natgateway_info", cwInfoLabels(accountID, region, "AWS/NATGateway", "natgateway",
				natGWARN(accountID, region, natID), tags), 1)
		}
	}
	// S3 info — one per configured bucket
	for i := range c.cfg.s3Count() {
		bucketName := s3BucketName(fx.Seed, i)
		st.Set("aws_s3_info", cwInfoLabels(accountID, region, "AWS/S3", "s3",
			bucketName, tags), 1)
	}
	// EKS info — only when cluster is present AND EKS toggle is enabled
	if c.cfg.eksEnabled() && fx.Cluster != nil {
		st.Set("aws_eks_info", cwInfoLabels(accountID, region, "AWS/EKS", "eks",
			"global", tags), 1)
	}
	// Firehose info
	if c.cfg.firehoseEnabled() {
		st.Set("aws_firehose_info", cwInfoLabels(accountID, region, "AWS/Firehose", "firehose",
			firehoseARN(fx.Seed, accountID, region), tags), 1)
	}
	// PrivateLink info — one per namespace
	if c.cfg.privateLinkEnabled() {
		st.Set("aws_privatelinkendpoints_info", cwInfoLabels(accountID, region,
			"AWS/PrivateLinkEndpoints", "privatelinkendpoints", "global", tags), 1)
		st.Set("aws_privatelinkservices_info", cwInfoLabels(accountID, region,
			"AWS/PrivateLinkServices", "privatelinkservices", "global", tags), 1)
	}
}

// ── ALB — AWS/ApplicationELB ─────────────────────────────────────────────────

func (c *construct) emitALB(factor float64, fx *fixture.Set, azs []string, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region

	// healthy target count: from cluster nodes when present, else 2 static targets
	var healthyHosts float64
	if fx.Cluster != nil {
		healthyHosts = float64(len(fx.Cluster.Nodes))
	} else {
		healthyHosts = 2
	}

	for i := range c.cfg.albCount() {
		arn := albARN(fx.Seed, accountID, region, i)
		albDimLB := albDimName(fx.Seed, i)
		albDimTG := albTGDimName(fx.Seed, i)

		baseRPS := 30.0 * factor * w.Shape.Noise(0.1)
		if baseRPS < 0 {
			baseRPS = 0
		}
		rpsPerAZ := baseRPS / float64(len(azs))

		for _, az := range azs {
			dims := map[string]string{
				"dimension_LoadBalancer":     albDimLB,
				"dimension_TargetGroup":      albDimTG,
				"dimension_AvailabilityZone": az,
			}
			lbls := cwLabels(accountID, region, "AWS/ApplicationELB", "applicationelb", arn, dims)

			reqCount := rpsPerAZ * 60
			setGaugeStats(st, "aws_applicationelb_request_count", lbls, reqCount, w)

			rtMean := 0.08 * (1 + w.Shape.NormFloat64()*0.15)
			if rtMean < 0.005 {
				rtMean = 0.005
			}
			setGaugeStats(st, "aws_applicationelb_target_response_time", lbls, rtMean, w)

			// ⚠ 2_xx, 4_xx, 5_xx (NOT 2xx/4xx/5xx — LAW)
			setGaugeStats(st, "aws_applicationelb_httpcode_target_2_xx_count", lbls, reqCount*0.98, w)
			setGaugeStats(st, "aws_applicationelb_httpcode_target_4_xx_count", lbls, reqCount*0.015, w)
			setGaugeStats(st, "aws_applicationelb_httpcode_target_5_xx_count", lbls, reqCount*0.002, w)
			setGaugeStats(st, "aws_applicationelb_httpcode_elb_5_xx_count", lbls, reqCount*0.0005, w)

			// ⚠ un_healthy_host_count (NOT unhealthy_host_count — LAW)
			setGaugeStats(st, "aws_applicationelb_healthy_host_count", lbls, healthyHosts, w)
			setGaugeStats(st, "aws_applicationelb_un_healthy_host_count", lbls, 0, w)

			activeCx := rpsPerAZ * 8 * w.Shape.Noise(0.2)
			if activeCx < 0 {
				activeCx = 0
			}
			setGaugeStats(st, "aws_applicationelb_active_connection_count", lbls, activeCx, w)
			setGaugeStats(st, "aws_applicationelb_new_connection_count", lbls, rpsPerAZ*2, w)
			setGaugeStats(st, "aws_applicationelb_processed_bytes", lbls, reqCount*8192, w)
			setGaugeStats(st, "aws_applicationelb_target_connection_error_count", lbls, reqCount*0.0002, w)
		}
	}
}

// ── NLB — AWS/NetworkELB ─────────────────────────────────────────────────────

func (c *construct) emitNLB(factor float64, fx *fixture.Set, azs []string, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region
	arn := nlbARN(fx.Seed, accountID, region)

	var healthyHosts float64
	if fx.Cluster != nil {
		healthyHosts = float64(len(fx.Cluster.Nodes))
	} else {
		healthyHosts = 2
	}

	baseFlowRate := 50.0 * factor * w.Shape.Noise(0.1)
	if baseFlowRate < 0 {
		baseFlowRate = 0
	}
	flowsPerAZ := baseFlowRate / float64(len(azs))

	for _, az := range azs {
		dims := map[string]string{
			"dimension_LoadBalancer":     nlbDimName(fx.Seed),
			"dimension_TargetGroup":      nlbTGDimName(fx.Seed),
			"dimension_AvailabilityZone": az,
		}
		lbls := cwLabels(accountID, region, "AWS/NetworkELB", "networkelb", arn, dims)

		procBytes := flowsPerAZ * 60 * 4096
		newFlows := flowsPerAZ * 10

		setGaugeStats(st, "aws_networkelb_active_flow_count", lbls, flowsPerAZ*60, w)
		setGaugeStats(st, "aws_networkelb_new_flow_count", lbls, newFlows, w)
		setGaugeStats(st, "aws_networkelb_processed_bytes", lbls, procBytes, w)
		setGaugeStats(st, "aws_networkelb_healthy_host_count", lbls, healthyHosts, w)
		setGaugeStats(st, "aws_networkelb_un_healthy_host_count", lbls, 0, w) // ⚠ un_healthy
		setGaugeStats(st, "aws_networkelb_port_allocation_error_count", lbls, 0, w)
		setGaugeStats(st, "aws_networkelb_tcp_client_reset_count", lbls, newFlows*0.002, w)
		setGaugeStats(st, "aws_networkelb_tcp_elb_reset_count", lbls, newFlows*0.001, w)
		setGaugeStats(st, "aws_networkelb_tcp_target_reset_count", lbls, newFlows*0.001, w)
		setGaugeStats(st, "aws_networkelb_peak_bytes_per_second", lbls, procBytes/60*3, w)
		setGaugeStats(st, "aws_networkelb_peak_packets_per_second", lbls, flowsPerAZ*3*5, w)
	}
}

// ── EBS — AWS/EBS ─────────────────────────────────────────────────────────────

func (c *construct) emitEBS(factor float64, fx *fixture.Set, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region

	for vi, volID := range ebsVolumeIDs(fx) {
		dims := map[string]string{"dimension_VolumeId": volID}
		lbls := cwLabels(accountID, region, "AWS/EBS", "ebs",
			ebsVolumeARN(accountID, region, volID), dims)

		// vol 0 = data (heavier), vol 1+ = log (lighter) — scale by index
		volFactor := factor * (1.0 - float64(vi)*0.4) * w.Shape.Noise(0.2)
		if volFactor < 0 {
			volFactor = 0
		}
		rdBytes := volFactor * 300e3 * 60
		wrBytes := volFactor * 150e3 * 60
		rdOps := volFactor * 120 * 60
		wrOps := volFactor * 60 * 60
		qLen := volFactor * 0.5 * w.Shape.Noise(0.5)
		if qLen < 0 {
			qLen = 0
		}
		burstBal := 3000.0 - rdOps*0.001
		if burstBal < 100 {
			burstBal = 100
		}
		rdLatMs := 0.5 + w.Shape.NormFloat64()*0.2
		if rdLatMs < 0.1 {
			rdLatMs = 0.1
		}
		wrLatMs := 0.3 + w.Shape.NormFloat64()*0.15
		if wrLatMs < 0.1 {
			wrLatMs = 0.1
		}

		setGaugeStats(st, "aws_ebs_volume_read_bytes", lbls, rdBytes, w)
		setGaugeStats(st, "aws_ebs_volume_write_bytes", lbls, wrBytes, w)
		setGaugeStats(st, "aws_ebs_volume_read_ops", lbls, rdOps, w)
		setGaugeStats(st, "aws_ebs_volume_write_ops", lbls, wrOps, w)
		setGaugeStats(st, "aws_ebs_volume_queue_length", lbls, qLen, w)
		setGaugeStats(st, "aws_ebs_burst_balance", lbls, burstBal, w)
		setGaugeStats(st, "aws_ebs_volume_avg_read_latency", lbls, rdLatMs, w)
		setGaugeStats(st, "aws_ebs_volume_avg_write_latency", lbls, wrLatMs, w)
		setGaugeStats(st, "aws_ebs_volume_total_read_time", lbls, rdOps*rdLatMs/1000, w)
		setGaugeStats(st, "aws_ebs_volume_total_write_time", lbls, wrOps*wrLatMs/1000, w)
	}
}

// ── NAT Gateway — AWS/NATGateway ─────────────────────────────────────────────

func (c *construct) emitNATGW(factor float64, fx *fixture.Set, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region

	for _, natID := range fx.Cloud.NATGatewayIDs {
		dims := map[string]string{"dimension_NatGatewayId": natID}
		lbls := cwLabels(accountID, region, "AWS/NATGateway", "natgateway",
			natGWARN(accountID, region, natID), dims)

		natEgressBytes := factor * 50 * 1e6 * w.Shape.Noise(0.3)
		if natEgressBytes < 0 {
			natEgressBytes = 0
		}
		setGaugeStats(st, "aws_natgateway_bytes_out_to_destination", lbls, natEgressBytes, w)
		setGaugeStats(st, "aws_natgateway_error_port_allocation", lbls, 0, w)
		setGaugeStats(st, "aws_natgateway_packets_drop_count", lbls, 0, w)
		activeCx := factor * 200 * w.Shape.Noise(0.2)
		if activeCx < 0 {
			activeCx = 0
		}
		setGaugeStats(st, "aws_natgateway_active_connection_count", lbls, activeCx, w)
		natAttempts := factor * 120 * 60
		setGaugeStats(st, "aws_natgateway_connection_attempt_count", lbls, natAttempts, w)
		setGaugeStats(st, "aws_natgateway_connection_established_count", lbls, natAttempts*0.98, w)
	}
}

// ── S3 — AWS/S3 (daily-ish storage gauges) ───────────────────────────────────

func (c *construct) emitS3(factor float64, fx *fixture.Set, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region

	for i := range c.cfg.s3Count() {
		bucketName := s3BucketName(fx.Seed, i)

		// BucketSizeBytes — daily gauge per storage type
		for _, storageType := range []string{"StandardStorage", "AllStorageTypes"} {
			dims := map[string]string{
				"dimension_BucketName":  bucketName,
				"dimension_StorageType": storageType,
			}
			lbls := cwLabels(accountID, region, "AWS/S3", "s3", bucketName, dims)
			bucketSizeBytes := 100e9 * float64(i+1) * (1 + w.Shape.NormFloat64()*0.05)
			if bucketSizeBytes < 0 {
				bucketSizeBytes = 0
			}
			setGaugeStats(st, "aws_s3_bucket_size_bytes", lbls, bucketSizeBytes, w)
		}

		// NumberOfObjects — daily gauge (AllStorageTypes only)
		dims := map[string]string{
			"dimension_BucketName":  bucketName,
			"dimension_StorageType": "AllStorageTypes",
		}
		lbls := cwLabels(accountID, region, "AWS/S3", "s3", bucketName, dims)
		numObjects := 1e6 * float64(i+1) * (1 + w.Shape.NormFloat64()*0.05)
		if numObjects < 0 {
			numObjects = 0
		}
		setGaugeStats(st, "aws_s3_number_of_objects", lbls, numObjects, w)
	}
}

// ── EKS control plane — AWS/EKS ──────────────────────────────────────────────

func (c *construct) emitEKS(factor float64, fx *fixture.Set, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region
	cluster := fx.Cluster.Name

	dims := map[string]string{"dimension_ClusterName": cluster}
	lbls := cwLabels(accountID, region, "AWS/EKS", "eks", "global", dims)

	apiReqs := 500.0*factor*(1+w.Shape.NormFloat64()*0.15) + 50
	if apiReqs < 10 {
		apiReqs = 10
	}
	setGaugeStats(st, "aws_eks_apiserver_request_total", lbls, apiReqs, w)

	// ⚠ percentile is EMBEDDED in the CW metric name; _average is the stat applied on top
	p99GetSec := 0.12 + w.Shape.NormFloat64()*0.04
	if p99GetSec < 0.01 {
		p99GetSec = 0.01
	}
	setGaugeStats(st, "aws_eks_apiserver_request_duration_seconds_get_p99", lbls, p99GetSec, w)
	setGaugeStats(st, "aws_eks_scheduler_pending_pods", lbls, 0, w)
	setGaugeStats(st, "aws_eks_apiserver_request_total_5_xx", lbls, apiReqs*0.001, w) // ⚠ 5_xx
	setGaugeStats(st, "aws_eks_apiserver_request_total_4_xx", lbls, apiReqs*0.001, w) // ⚠ 4_xx
	etcdDBSize := 100e6 + factor*20e6 + w.Shape.NormFloat64()*2e6
	if etcdDBSize < 10e6 {
		etcdDBSize = 10e6
	}
	setGaugeStats(st, "aws_eks_etcd_mvcc_db_total_size_in_bytes", lbls, etcdDBSize, w)
}

// ── Firehose — AWS/Firehose ───────────────────────────────────────────────────

func (c *construct) emitFirehose(factor float64, fx *fixture.Set, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region

	dims := map[string]string{
		"dimension_DeliveryStreamName": firehoseStreamName(fx.Seed),
	}
	lbls := cwLabels(accountID, region, "AWS/Firehose", "firehose",
		firehoseARN(fx.Seed, accountID, region), dims)

	fhSuccess := 0.998 + w.Shape.NormFloat64()*0.001
	if fhSuccess > 1.0 {
		fhSuccess = 1.0
	}
	if fhSuccess < 0 {
		fhSuccess = 0
	}
	setGaugeStats(st, "aws_firehose_delivery_to_http_endpoint_success", lbls, fhSuccess, w)

	fhFreshness := 15.0 + w.Shape.NormFloat64()*5
	if fhFreshness < 5 {
		fhFreshness = 5
	}
	setGaugeStats(st, "aws_firehose_delivery_to_http_endpoint_data_freshness", lbls, fhFreshness, w)
}

// ── PrivateLink — AWS/PrivateLinkEndpoints + AWS/PrivateLinkServices ──────────

// emitPrivateLink emits the PrivateLink endpoints and services CW families.
// Base names sourced VERBATIM from signals/cw.md [slug: cw-privatelink].
// ⚠ Space→underscore in dimension names (SK-3 live-confirmed): "Endpoint Type" →
// dimension_Endpoint_Type, "VPC Endpoint Id" → dimension_VPC_Endpoint_Id, etc.
func (c *construct) emitPrivateLink(factor float64, fx *fixture.Set, w *core.World) {
	st := c.st
	accountID := fx.Cloud.AccountID
	region := fx.Cloud.Region

	// Synthesise one endpoint identity from seed (representative, not exhaustive).
	epID := "vpce-" + fixture.HexID(fx.Seed, 17, "pl-ep")
	svcID := "vpce-svc-" + fixture.HexID(fx.Seed, 17, "pl-svc")
	vpcID := fx.Cloud.VpcID
	subnetID := "subnet-" + fixture.HexID(fx.Seed, 8, "pl-subnet")
	az := region + "a"

	// ── Endpoints family (AWS/PrivateLinkEndpoints) ────────────────────────────
	epDims := map[string]string{
		"dimension_Endpoint_Type":   "Interface",
		"dimension_Service_Name":    "com.amazonaws." + region + ".execute-api",
		"dimension_Subnet_Id":       subnetID,
		"dimension_VPC_Endpoint_Id": epID,
		"dimension_VPC_Id":          vpcID,
	}
	epLbls := cwLabels(accountID, region, "AWS/PrivateLinkEndpoints", "privatelinkendpoints", "global", epDims)
	setGaugeStats(st, "aws_privatelinkendpoints_active_connections", epLbls, factor*5, w)
	setGaugeStats(st, "aws_privatelinkendpoints_bytes_processed", epLbls, factor*1e6, w)
	setGaugeStats(st, "aws_privatelinkendpoints_new_connections", epLbls, factor*2, w)
	setGaugeStats(st, "aws_privatelinkendpoints_packets_dropped", epLbls, 0, w)
	setGaugeStats(st, "aws_privatelinkendpoints_rst_packets_received", epLbls, factor*0.1, w)

	// ── Services family (AWS/PrivateLinkServices) ──────────────────────────────
	lbARN := albARN(fx.Seed, accountID, region, 0)
	svcDims := map[string]string{
		"dimension_Az":                az,
		"dimension_Load_Balancer_Arn": lbARN,
		"dimension_Service_Id":        svcID,
		"dimension_VPC_Endpoint_Id":   epID,
	}
	svcLbls := cwLabels(accountID, region, "AWS/PrivateLinkServices", "privatelinkservices", "global", svcDims)
	setGaugeStats(st, "aws_privatelinkservices_active_connections", svcLbls, factor*5, w)
	setGaugeStats(st, "aws_privatelinkservices_bytes_processed", svcLbls, factor*1e6, w)
	setGaugeStats(st, "aws_privatelinkservices_endpoints_count", svcLbls, 1, w)
	setGaugeStats(st, "aws_privatelinkservices_new_connections", svcLbls, factor*2, w)
	setGaugeStats(st, "aws_privatelinkservices_rst_packets_sent", svcLbls, factor*0.1, w)
}

// ── Label helpers ─────────────────────────────────────────────────────────────

// cwBaseLabels returns the universal CW label set required on every series
// (signals/cw.md [slug: cw-naming] / ARCHITECTURE I6).
func cwBaseLabels(accountID, region, namespace, service, name string) map[string]string {
	return map[string]string{
		"account_id": accountID,
		"region":     region,
		"namespace":  namespace,
		"job":        "cloud/aws/" + service,
		"name":       name,
	}
}

// cwLabels merges the universal base CW labels with dimension_* labels.
// Absent dimensions are not added (ARCHITECTURE I13: never "" or "NA").
func cwLabels(accountID, region, namespace, service, name string, dims map[string]string) map[string]string {
	out := cwBaseLabels(accountID, region, namespace, service, name)
	for k, v := range dims {
		if v != "" { // I13: omit absent dimensions
			out[k] = v
		}
	}
	return out
}

// cwInfoLabels builds the label set for a per-namespace _info series (base + tag_*).
func cwInfoLabels(accountID, region, namespace, service, name string, tags map[string]string) map[string]string {
	out := cwBaseLabels(accountID, region, namespace, service, name)
	for k, v := range tags {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// ── 5-stat gauge model ────────────────────────────────────────────────────────
//
// CloudWatch metric-stream series carry exactly 5 statistics per metric per period:
// _sum, _average, _maximum, _minimum, _sample_count. All are per-period gauges
// (ARCHITECTURE I5 — NEVER Add; ALWAYS Set).

// setGaugeStats emits all 5 CW statistics for root as instantaneous gauges.
// mean is the typical average value; statistics are modelled as:
//
//	_sum          = mean × sampleCount      (total in the 60 s window)
//	_average      = mean                    (arithmetic mean)
//	_maximum      = mean × 1.2              (peak within the window)
//	_minimum      = mean × 0.8              (trough)
//	_sample_count = 60                      (one data point per second)
func setGaugeStats(st *state.State, root string, labels map[string]string, mean float64, w *core.World) {
	const sampleCount = 60
	// Apply a small per-call jitter to prevent all stats of the same series from
	// landing on perfectly round numbers, while keeping them coherent. The single
	// NormFloat64 draw is part of the deterministic output — it must happen exactly once
	// here, before the StatSet is built. The suffix mechanic + per-period-gauge rule (I5)
	// + per-suffix label isolation live in cw.EmitStats.
	jitter := 1 + w.Shape.NormFloat64()*0.05
	if jitter < 0.5 {
		jitter = 0.5
	}
	avg := mean * jitter
	mn := avg * 0.8
	if mn < 0 {
		mn = 0
	}
	cw.EmitStats(st, root, labels, cw.StatSet{
		Sum:         avg * sampleCount,
		Average:     avg,
		Maximum:     avg * 1.2,
		Minimum:     mn,
		SampleCount: sampleCount,
	})
}

// ── Deterministic resource identifiers (seed-derived per ARCHITECTURE I12) ───

// azsForRegion returns two AZ names for a region (≤2 AZs keeps ALB/NLB cardinality bounded).
func azsForRegion(region string) []string {
	return []string{region + "a", region + "b"}
}

// albDimName returns the dimension_LoadBalancer value for ALB index i:
// "app/<name>/<16-hex>" (signals/cw.md [slug: cw-alb]).
func albDimName(seed string, i int) string {
	name := fmt.Sprintf("alb-%02d", i+1)
	id := fixture.HexID(seed, 16, "alb", fmt.Sprintf("%d", i))
	return "app/" + name + "/" + id
}

// albTGDimName returns the dimension_TargetGroup value for ALB index i:
// "targetgroup/<name>/<16-hex>".
func albTGDimName(seed string, i int) string {
	name := fmt.Sprintf("alb-tg-%02d", i+1)
	id := fixture.HexID(seed, 16, "alb-tg", fmt.Sprintf("%d", i))
	return "targetgroup/" + name + "/" + id
}

// albARN returns a synthetic Application Load Balancer ARN.
func albARN(seed, accountID, region string, i int) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s",
		region, accountID, albDimName(seed, i))
}

// nlbDimName returns the dimension_LoadBalancer value for the (single) NLB:
// "net/<name>/<16-hex>" (signals/cw.md [slug: cw-nlb]).
func nlbDimName(seed string) string {
	id := fixture.HexID(seed, 16, "nlb")
	return "net/nlb-01/" + id
}

// nlbTGDimName returns the dimension_TargetGroup value for the NLB.
func nlbTGDimName(seed string) string {
	id := fixture.HexID(seed, 16, "nlb-tg")
	return "targetgroup/nlb-tg-01/" + id
}

// nlbARN returns a synthetic Network Load Balancer ARN.
func nlbARN(seed, accountID, region string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s",
		region, accountID, nlbDimName(seed))
}

// ebsVolumeIDs returns the deterministic list of EBS volume IDs:
//   - one per cluster node (fixture.VolumeID(seed, node.InstanceID)) when Cluster present;
//   - two static volumes otherwise.
func ebsVolumeIDs(fx *fixture.Set) []string {
	if fx.Cluster != nil && len(fx.Cluster.Nodes) > 0 {
		ids := make([]string, len(fx.Cluster.Nodes))
		for i, n := range fx.Cluster.Nodes {
			ids[i] = fixture.VolumeID(fx.Seed, n.InstanceID)
		}
		return ids
	}
	return []string{
		fixture.VolumeID(fx.Seed, "vol", "0"),
		fixture.VolumeID(fx.Seed, "vol", "1"),
	}
}

// ebsVolumeARN returns a synthetic EBS volume ARN.
func ebsVolumeARN(accountID, region, volID string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:volume/%s", region, accountID, volID)
}

// natGWARN returns a synthetic NAT Gateway ARN.
func natGWARN(accountID, region, natID string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:natgateway/%s", region, accountID, natID)
}

// s3BucketName returns a deterministic S3 bucket name for index i:
// "<6-hex-seed>-data-<NN>".
func s3BucketName(seed string, i int) string {
	prefix := fixture.HexID(seed, 6, "s3")
	return fmt.Sprintf("%s-data-%02d", prefix, i+1)
}

// firehoseStreamName returns the metric-stream Firehose delivery stream name.
func firehoseStreamName(seed string) string {
	prefix := fixture.HexID(seed, 6, "firehose")
	return prefix + "-cw-metric-stream-firehose"
}

// firehoseARN returns a synthetic Firehose delivery stream ARN.
func firehoseARN(seed, accountID, region string) string {
	return fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s",
		region, accountID, firehoseStreamName(seed))
}
