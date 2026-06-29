// SPDX-License-Identifier: AGPL-3.0-only

package cwinfra_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/construct/cwinfra"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// ── Test: Registration metadata ──────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := cwinfra.Registration()
	if reg.Kind != "cw_infra" {
		t.Errorf("Kind = %q, want cw_infra", reg.Kind)
	}
	if reg.Scope != core.ScopeBlueprint {
		t.Errorf("Scope = %v, want ScopeBlueprint", reg.Scope)
	}
	if reg.NewConfig == nil || reg.Build == nil {
		t.Fatal("NewConfig or Build is nil")
	}
	cfg := reg.NewConfig()
	if _, ok := cfg.(*cwinfra.Config); !ok {
		t.Fatalf("NewConfig returned %T, want *cwinfra.Config", cfg)
	}
}

func TestConstructMetadata(t *testing.T) {
	reg := cwinfra.Registration()
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	con, err := reg.Build(reg.NewConfig(), fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if con.Kind() != "cw_infra" {
		t.Errorf("Kind = %q, want cw_infra", con.Kind())
	}
	if con.Interval() != 60*time.Second {
		t.Errorf("Interval = %v, want 60s", con.Interval())
	}
	sigs := con.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals = %v, want [Metrics]", sigs)
	}
}

// ── Test: Build errors ────────────────────────────────────────────────────────

func TestBuildRequiresCloud(t *testing.T) {
	reg := cwinfra.Registration()
	_, err := reg.Build(reg.NewConfig(), &fixture.Set{Env: coretest.Env()})
	if err == nil {
		t.Fatal("expected error when Cloud is nil")
	}
}

func TestBuildRequiresEnv(t *testing.T) {
	reg := cwinfra.Registration()
	_, err := reg.Build(reg.NewConfig(), &fixture.Set{Cloud: coretest.Cloud()})
	if err == nil {
		t.Fatal("expected error when Env is nil")
	}
}

// ── Test: Config defaults ─────────────────────────────────────────────────────

func TestConfigDefaults(t *testing.T) {
	reg := cwinfra.Registration()
	cfg := reg.NewConfig().(*cwinfra.Config)
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	_, err := reg.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// All toggle fields stay nil when unset — the effective count/enablement is resolved
	// lazily (no up-front defaulting pass), so an explicit 0/false is never floored away.
	if cfg.ALBs != nil {
		t.Errorf("ALBs field should stay nil when not set (default=1 resolved lazily), got %d", *cfg.ALBs)
	}
	if cfg.S3Buckets != nil {
		t.Errorf("S3Buckets field should stay nil when not set (default=2 resolved lazily), got %d", *cfg.S3Buckets)
	}
	if cfg.Firehose != nil {
		t.Errorf("Firehose field should stay nil when not set (default=true via firehoseEnabled)")
	}
}

// TestDefaultCounts pins the effective default instance counts (nil → 1 ALB, 2 S3 buckets)
// via the emitted _info series, since the count now resolves lazily rather than mutating cfg.
func TestDefaultCounts(t *testing.T) {
	cap := runTick(t, nil, nil) // nil cfg → Registration defaults
	if got := len(cap.Find("aws_applicationelb_info")); got != 1 {
		t.Errorf("default ALB _info series = %d, want 1", got)
	}
	if got := len(cap.Find("aws_s3_info")); got != 2 {
		t.Errorf("default S3 _info series = %d, want 2", got)
	}
}

// TestConfigDecode_NilVsExplicitZero locks the crux of the disable feature on the real
// blueprint path: yaml.v3 decodes an OMITTED key to a nil pointer (→ default) and an
// EXPLICIT "albs: 0" to a non-nil *int(0) (→ disabled). This is what lets a blueprint
// author actually turn the family off.
func TestConfigDecode_NilVsExplicitZero(t *testing.T) {
	t.Run("omitted → nil", func(t *testing.T) {
		var c cwinfra.Config
		if err := yaml.Unmarshal([]byte("nlb: false\n"), &c); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if c.ALBs != nil || c.S3Buckets != nil {
			t.Errorf("omitted albs/s3_buckets should decode to nil, got %v/%v", c.ALBs, c.S3Buckets)
		}
	})
	t.Run("explicit 0 → non-nil zero", func(t *testing.T) {
		var c cwinfra.Config
		if err := yaml.Unmarshal([]byte("albs: 0\ns3_buckets: 0\n"), &c); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if c.ALBs == nil || *c.ALBs != 0 {
			t.Errorf("albs: 0 should decode to non-nil *int(0), got %v", c.ALBs)
		}
		if c.S3Buckets == nil || *c.S3Buckets != 0 {
			t.Errorf("s3_buckets: 0 should decode to non-nil *int(0), got %v", c.S3Buckets)
		}
	})
}

// ── Test: series inventory (exact names per extract) ─────────────────────────

func TestSeriesInventory_ALB(t *testing.T) {
	wantRoots := []string{
		"aws_applicationelb_request_count",
		"aws_applicationelb_target_response_time",
		"aws_applicationelb_httpcode_target_2_xx_count",
		"aws_applicationelb_httpcode_target_4_xx_count",
		"aws_applicationelb_httpcode_target_5_xx_count", // ⚠ 5_xx not 5xx
		"aws_applicationelb_httpcode_elb_5_xx_count",
		"aws_applicationelb_healthy_host_count",
		"aws_applicationelb_un_healthy_host_count", // ⚠ un_healthy not unhealthy
		"aws_applicationelb_active_connection_count",
		"aws_applicationelb_new_connection_count",
		"aws_applicationelb_processed_bytes",
		"aws_applicationelb_target_connection_error_count",
	}
	cap := runTick(t, nil, nil)
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_applicationelb_info")
}

func TestSeriesInventory_NLB(t *testing.T) {
	wantRoots := []string{
		"aws_networkelb_active_flow_count",
		"aws_networkelb_new_flow_count",
		"aws_networkelb_processed_bytes",
		"aws_networkelb_healthy_host_count",
		"aws_networkelb_un_healthy_host_count", // ⚠ un_healthy
		"aws_networkelb_port_allocation_error_count",
		"aws_networkelb_tcp_client_reset_count",
		"aws_networkelb_tcp_elb_reset_count",
		"aws_networkelb_tcp_target_reset_count",
		"aws_networkelb_peak_bytes_per_second",
		"aws_networkelb_peak_packets_per_second",
	}
	cap := runTick(t, nil, nil)
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_networkelb_info")
}

func TestSeriesInventory_EBS_NoCluster(t *testing.T) {
	wantRoots := []string{
		"aws_ebs_volume_read_bytes",
		"aws_ebs_volume_write_bytes",
		"aws_ebs_volume_read_ops",
		"aws_ebs_volume_write_ops",
		"aws_ebs_volume_queue_length",
		"aws_ebs_burst_balance",
		"aws_ebs_volume_avg_read_latency",
		"aws_ebs_volume_avg_write_latency",
		"aws_ebs_volume_total_read_time",
		"aws_ebs_volume_total_write_time",
	}
	cap := runTick(t, nil, nil)
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_ebs_info")
}

func TestSeriesInventory_NATGW(t *testing.T) {
	wantRoots := []string{
		"aws_natgateway_bytes_out_to_destination",
		"aws_natgateway_error_port_allocation",
		"aws_natgateway_packets_drop_count",
		"aws_natgateway_active_connection_count",
		"aws_natgateway_connection_attempt_count",
		"aws_natgateway_connection_established_count",
	}
	cap := runTick(t, nil, nil)
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_natgateway_info")
}

func TestSeriesInventory_S3(t *testing.T) {
	wantRoots := []string{
		"aws_s3_bucket_size_bytes",
		"aws_s3_number_of_objects",
	}
	cap := runTick(t, nil, nil)
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_s3_info")
}

func TestSeriesInventory_EKS_WithCluster(t *testing.T) {
	wantRoots := []string{
		"aws_eks_apiserver_request_total",
		"aws_eks_apiserver_request_duration_seconds_get_p99",
		"aws_eks_scheduler_pending_pods",
		"aws_eks_apiserver_request_total_5_xx", // ⚠ 5_xx
		"aws_eks_apiserver_request_total_4_xx", // ⚠ 4_xx
		"aws_eks_etcd_mvcc_db_total_size_in_bytes",
	}
	cl := coretest.Cluster()
	cap := runTick(t, nil, cl)
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_eks_info")
}

func TestSeriesInventory_EKS_AbsentWithoutCluster(t *testing.T) {
	cap := runTick(t, nil, nil)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_eks_") {
			t.Errorf("unexpected EKS series %q emitted without cluster", n)
		}
	}
}

func TestSeriesInventory_Firehose(t *testing.T) {
	wantRoots := []string{
		"aws_firehose_delivery_to_http_endpoint_success",
		"aws_firehose_delivery_to_http_endpoint_data_freshness",
	}
	cap := runTick(t, nil, nil) // Firehose default = enabled
	assertRootsWithFiveStats(t, cap, wantRoots)
	assertHasName(t, cap, "aws_firehose_info")
}

func TestFirehoseDisabled(t *testing.T) {
	cfg := &cwinfra.Config{ALBs: intPtr(1), S3Buckets: intPtr(1), Firehose: boolPtr(false)}
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_firehose_") {
			t.Errorf("unexpected Firehose series %q when firehose=false", n)
		}
	}
}

// ── Test: per-family *bool toggles ───────────────────────────────────────────

// TestNLBToggle verifies that NLB: false suppresses all aws_networkelb_* and
// aws_nlb_info series, while the default config (nil) emits them.
func TestNLBToggle(t *testing.T) {
	// disabled: no aws_networkelb_* or aws_nlb_info
	cfg := &cwinfra.Config{ALBs: intPtr(1), S3Buckets: intPtr(1), NLB: boolPtr(false)}
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_networkelb_") || strings.HasPrefix(n, "aws_nlb_") {
			t.Errorf("NLB=false: unexpected series %q", n)
		}
	}

	// enabled (default nil): aws_networkelb_* present
	capDefault := runTick(t, nil, nil)
	found := false
	for _, n := range capDefault.Names() {
		if strings.HasPrefix(n, "aws_networkelb_") {
			found = true
			break
		}
	}
	if !found {
		t.Error("default config: no aws_networkelb_* series found")
	}
	if len(capDefault.Find("aws_networkelb_info")) == 0 {
		t.Error("default config: aws_networkelb_info not found")
	}
}

// TestEBSToggle verifies that EBS: false suppresses all aws_ebs_* and aws_ebs_info series.
func TestEBSToggle(t *testing.T) {
	// disabled
	cfg := &cwinfra.Config{ALBs: intPtr(1), S3Buckets: intPtr(1), EBS: boolPtr(false)}
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_ebs_") {
			t.Errorf("EBS=false: unexpected series %q", n)
		}
	}

	// enabled (default nil): aws_ebs_* present
	capDefault := runTick(t, nil, nil)
	found := false
	for _, n := range capDefault.Names() {
		if strings.HasPrefix(n, "aws_ebs_") {
			found = true
			break
		}
	}
	if !found {
		t.Error("default config: no aws_ebs_* series found")
	}
	if len(capDefault.Find("aws_ebs_info")) == 0 {
		t.Error("default config: aws_ebs_info not found")
	}
}

// TestNATGatewayToggle verifies that NATGateway: false suppresses all aws_natgateway_* series.
func TestNATGatewayToggle(t *testing.T) {
	// disabled
	cfg := &cwinfra.Config{ALBs: intPtr(1), S3Buckets: intPtr(1), NATGateway: boolPtr(false)}
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_natgateway_") {
			t.Errorf("NATGateway=false: unexpected series %q", n)
		}
	}

	// enabled (default nil): aws_natgateway_* present
	capDefault := runTick(t, nil, nil)
	found := false
	for _, n := range capDefault.Names() {
		if strings.HasPrefix(n, "aws_natgateway_") {
			found = true
			break
		}
	}
	if !found {
		t.Error("default config: no aws_natgateway_* series found")
	}
	if len(capDefault.Find("aws_natgateway_info")) == 0 {
		t.Error("default config: aws_natgateway_info not found")
	}
}

// TestEKSToggle verifies that EKS: false suppresses all aws_eks_* series even when
// a cluster is present, and that with default config + cluster they are emitted.
func TestEKSToggle(t *testing.T) {
	// disabled (with cluster present — toggle takes priority)
	cl := coretest.Cluster()
	cfg := &cwinfra.Config{ALBs: intPtr(1), S3Buckets: intPtr(1), EKS: boolPtr(false)}
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud(), Cluster: cl}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_eks_") {
			t.Errorf("EKS=false: unexpected series %q", n)
		}
	}

	// enabled (default nil) with cluster: aws_eks_* present
	capDefault := runTick(t, nil, cl)
	found := false
	for _, n := range capDefault.Names() {
		if strings.HasPrefix(n, "aws_eks_") {
			found = true
			break
		}
	}
	if !found {
		t.Error("default config with cluster: no aws_eks_* series found")
	}
	if len(capDefault.Find("aws_eks_info")) == 0 {
		t.Error("default config with cluster: aws_eks_info not found")
	}
}

// ── Test: per-series label keys (dimension_* casing) ─────────────────────────

func TestLabelKeys_ALB(t *testing.T) {
	cap := runTick(t, nil, nil)
	keys := cap.LabelKeys("aws_applicationelb_request_count_average")
	wantKeys := []string{
		"account_id", "dimension_AvailabilityZone", "dimension_LoadBalancer",
		"dimension_TargetGroup", "job", "name", "namespace", "region",
	}
	assertContainsKeys(t, "aws_applicationelb_request_count_average", keys, wantKeys)
}

func TestLabelKeys_NLB(t *testing.T) {
	cap := runTick(t, nil, nil)
	keys := cap.LabelKeys("aws_networkelb_active_flow_count_average")
	wantKeys := []string{
		"account_id", "dimension_AvailabilityZone", "dimension_LoadBalancer",
		"dimension_TargetGroup", "job", "name", "namespace", "region",
	}
	assertContainsKeys(t, "aws_networkelb_active_flow_count_average", keys, wantKeys)
}

func TestLabelKeys_EBS(t *testing.T) {
	cap := runTick(t, nil, nil)
	keys := cap.LabelKeys("aws_ebs_volume_read_bytes_average")
	wantKeys := []string{
		"account_id", "dimension_VolumeId", "job", "name", "namespace", "region",
	}
	assertContainsKeys(t, "aws_ebs_volume_read_bytes_average", keys, wantKeys)
}

func TestLabelKeys_NATGW(t *testing.T) {
	cap := runTick(t, nil, nil)
	keys := cap.LabelKeys("aws_natgateway_bytes_out_to_destination_average")
	wantKeys := []string{
		"account_id", "dimension_NatGatewayId", "job", "name", "namespace", "region",
	}
	assertContainsKeys(t, "aws_natgateway_bytes_out_to_destination_average", keys, wantKeys)
}

func TestLabelKeys_EKS(t *testing.T) {
	cl := coretest.Cluster()
	cap := runTick(t, nil, cl)
	keys := cap.LabelKeys("aws_eks_apiserver_request_total_average")
	wantKeys := []string{
		"account_id", "dimension_ClusterName", "job", "name", "namespace", "region",
	}
	assertContainsKeys(t, "aws_eks_apiserver_request_total_average", keys, wantKeys)
}

func TestLabelKeys_Firehose(t *testing.T) {
	cap := runTick(t, nil, nil)
	keys := cap.LabelKeys("aws_firehose_delivery_to_http_endpoint_success_average")
	wantKeys := []string{
		"account_id", "dimension_DeliveryStreamName", "job", "name", "namespace", "region",
	}
	assertContainsKeys(t, "aws_firehose_delivery_to_http_endpoint_success_average", keys, wantKeys)
}

// ── Test: dimension_NatGatewayId values == fixture NAT IDs ───────────────────

func TestNATGWIDValues(t *testing.T) {
	cloud := coretest.Cloud()
	wantIDs := cloud.NATGatewayIDs

	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: cloud}
	cap := tickWith(t, &cwinfra.Config{ALBs: intPtr(1), S3Buckets: intPtr(1)}, fx)

	series := cap.Find("aws_natgateway_bytes_out_to_destination_average")
	gotIDs := make(map[string]bool)
	for _, s := range series {
		gotIDs[s.Labels["dimension_NatGatewayId"]] = true
	}
	for _, wantID := range wantIDs {
		if !gotIDs[wantID] {
			t.Errorf("dimension_NatGatewayId %q not found; got %v", wantID, gotIDs)
		}
	}
	if len(gotIDs) != len(wantIDs) {
		t.Errorf("got %d distinct NatGatewayId values, want %d", len(gotIDs), len(wantIDs))
	}
}

// ── Test: EBS volume count == node count when cluster present ─────────────────

func TestEBSVolumeCount_WithCluster(t *testing.T) {
	cl := coretest.Cluster()
	wantCount := len(cl.Nodes)
	cap := runTick(t, nil, cl)

	series := cap.Find("aws_ebs_volume_read_bytes_average")
	volIDs := make(map[string]bool)
	for _, s := range series {
		volIDs[s.Labels["dimension_VolumeId"]] = true
	}
	if len(volIDs) != wantCount {
		t.Errorf("EBS volume count = %d, want %d (node count)", len(volIDs), wantCount)
	}
}

func TestEBSVolumeCount_WithoutCluster(t *testing.T) {
	cap := runTick(t, nil, nil)
	series := cap.Find("aws_ebs_volume_read_bytes_average")
	volIDs := make(map[string]bool)
	for _, s := range series {
		volIDs[s.Labels["dimension_VolumeId"]] = true
	}
	if len(volIDs) != 2 {
		t.Errorf("EBS volume count without cluster = %d, want 2", len(volIDs))
	}
}

// ── Test: account_id / region / job on every series ──────────────────────────

func TestUniversalLabels(t *testing.T) {
	cloud := coretest.Cloud()
	cap := runTick(t, nil, nil)
	for _, s := range cap.All() {
		if s.Labels["account_id"] != cloud.AccountID {
			t.Errorf("series %q: account_id = %q, want %q", s.Name, s.Labels["account_id"], cloud.AccountID)
		}
		if s.Labels["region"] != cloud.Region {
			t.Errorf("series %q: region = %q, want %q", s.Name, s.Labels["region"], cloud.Region)
		}
		if !strings.HasPrefix(s.Labels["job"], "cloud/aws/") {
			t.Errorf("series %q: job = %q, want cloud/aws/... prefix", s.Name, s.Labels["job"])
		}
		if s.Labels["namespace"] == "" {
			t.Errorf("series %q: namespace label missing", s.Name)
		}
	}
}

// ── Test: per-period gauge — no monotonic accumulation across ticks ───────────

func TestPerPeriodGauge_NoMonotonicAccumulation(t *testing.T) {
	// Run N ticks on the same construct instance. Each tick the _sum values are
	// per-period gauges (Set, not Add). Verify that by tick N the sum value has NOT
	// grown to N × the first-tick value — which it would if Add were used.
	reg := cwinfra.Registration()
	cfg := reg.NewConfig()
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	con, err := reg.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// A per-period gauge (Set) FLUCTUATES tick-to-tick via the shape engine's Noise, so across N
	// ticks at least one tick is below a previous one; an Add counter would be monotonically
	// non-decreasing. We assert a decrease is observed rather than a (variance-fragile) magnitude
	// threshold. (The Set-not-Add semantics of EmitStats itself are proven deterministically by
	// cw.TestEmitStatsSeriesAreGauges + state.TestGaugeInstantaneous; THIS is the integration check
	// that the cwinfra construct routes through that Set path, not a raw st.Add.)
	//
	// The shape Engine is created ONCE and reused across all ticks — matching production, where the
	// per-blueprint Engine persists so its (deterministically-seeded, I12) RNG advances tick-to-tick
	// and Noise differs each tick. A fresh Engine per tick would re-seed to the same draw every tick,
	// making the gauge byte-identical and this check spuriously fail.
	eng := shape.New("", nil)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	const N = 12
	vals := make([]float64, 0, N)
	for range N {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		w.Shape = eng
		if err := con.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("tick: %v", err)
		}
		series := mc.Find("aws_natgateway_bytes_out_to_destination_sum")
		if len(series) == 0 {
			t.Fatal("no aws_natgateway_bytes_out_to_destination_sum series")
		}
		vals = append(vals, series[0].Value)
		now = now.Add(60 * time.Second)
	}
	decreased := false
	for i := 1; i < len(vals); i++ {
		if vals[i] < vals[i-1] {
			decreased = true
			break
		}
	}
	if !decreased {
		t.Errorf("per-period _sum never decreased across %d ticks (%v) — monotonic, looks like Add accumulation not a Set gauge", N, vals)
	}
}

// ── Test: two Ticks → per-period gauges may go up or down ────────────────────

func TestTwoTicks_PerPeriodGaugesFluctuate(t *testing.T) {
	// Both ticks must emit the same set of series names (no series appear/disappear).
	reg := cwinfra.Registration()
	cfg := reg.NewConfig()
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	con, err := reg.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	mc1 := &coretest.MetricCapture{}
	if err := con.Tick(context.Background(), now, coretest.World(mc1, nil, nil)); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	mc2 := &coretest.MetricCapture{}
	if err := con.Tick(context.Background(), now.Add(60*time.Second), coretest.World(mc2, nil, nil)); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	names1 := mc1.Names()
	names2 := mc2.Names()
	if len(names1) != len(names2) {
		t.Errorf("tick1 series count %d != tick2 series count %d", len(names1), len(names2))
	}
	for _, n := range names1 {
		if !slices.Contains(names2, n) {
			t.Errorf("series %q present in tick1 but missing from tick2", n)
		}
	}
}

// ── Test: EKS dimension_ClusterName == cluster name ──────────────────────────

func TestEKSClusterNameDimension(t *testing.T) {
	cl := coretest.Cluster()
	cap := runTick(t, nil, cl)
	series := cap.Find("aws_eks_apiserver_request_total_average")
	if len(series) == 0 {
		t.Fatal("no aws_eks_apiserver_request_total_average series")
	}
	for _, s := range series {
		if s.Labels["dimension_ClusterName"] != cl.Name {
			t.Errorf("dimension_ClusterName = %q, want %q", s.Labels["dimension_ClusterName"], cl.Name)
		}
	}
}

// ── Test: tag_VpcId on _info series ──────────────────────────────────────────

func TestInfoSeries_TagVpcId(t *testing.T) {
	cloud := coretest.Cloud()
	cap := runTick(t, nil, nil)
	for _, infoName := range []string{
		"aws_applicationelb_info",
		"aws_networkelb_info",
		"aws_natgateway_info",
		"aws_firehose_info",
	} {
		series := cap.Find(infoName)
		if len(series) == 0 {
			t.Errorf("no %q series found", infoName)
			continue
		}
		for _, s := range series {
			if s.Labels["tag_VpcId"] != cloud.VpcID {
				t.Errorf("%q: tag_VpcId = %q, want %q", infoName, s.Labels["tag_VpcId"], cloud.VpcID)
			}
		}
	}
}

// ── Test: namespace label values ─────────────────────────────────────────────

func TestNamespaceLabels(t *testing.T) {
	cl := coretest.Cluster()
	cap := runTick(t, nil, cl)

	want := map[string]string{
		"aws_applicationelb_request_count_average":               "AWS/ApplicationELB",
		"aws_networkelb_active_flow_count_average":               "AWS/NetworkELB",
		"aws_ebs_volume_read_bytes_average":                      "AWS/EBS",
		"aws_natgateway_bytes_out_to_destination_average":        "AWS/NATGateway",
		"aws_s3_bucket_size_bytes_average":                       "AWS/S3",
		"aws_eks_apiserver_request_total_average":                "AWS/EKS",
		"aws_firehose_delivery_to_http_endpoint_success_average": "AWS/Firehose",
	}
	for seriesName, wantNS := range want {
		series := cap.Find(seriesName)
		if len(series) == 0 {
			t.Errorf("series %q not found", seriesName)
			continue
		}
		if got := series[0].Labels["namespace"]; got != wantNS {
			t.Errorf("%q: namespace = %q, want %q", seriesName, got, wantNS)
		}
	}
}

// ── Test: job label values ────────────────────────────────────────────────────

func TestJobLabels(t *testing.T) {
	cl := coretest.Cluster()
	cap := runTick(t, nil, cl)

	want := map[string]string{
		"aws_applicationelb_request_count_average":               "cloud/aws/applicationelb",
		"aws_networkelb_active_flow_count_average":               "cloud/aws/networkelb",
		"aws_ebs_volume_read_bytes_average":                      "cloud/aws/ebs",
		"aws_natgateway_bytes_out_to_destination_average":        "cloud/aws/natgateway",
		"aws_s3_bucket_size_bytes_average":                       "cloud/aws/s3",
		"aws_eks_apiserver_request_total_average":                "cloud/aws/eks",
		"aws_firehose_delivery_to_http_endpoint_success_average": "cloud/aws/firehose",
	}
	for seriesName, wantJob := range want {
		series := cap.Find(seriesName)
		if len(series) == 0 {
			t.Errorf("series %q not found", seriesName)
			continue
		}
		if got := series[0].Labels["job"]; got != wantJob {
			t.Errorf("%q: job = %q, want %q", seriesName, got, wantJob)
		}
	}
}

// ── Test: ALB dimension_LoadBalancer format "app/<name>/<16hex>" ──────────────

func TestALBDimLoadBalancerFormat(t *testing.T) {
	cap := runTick(t, nil, nil)
	series := cap.Find("aws_applicationelb_request_count_average")
	if len(series) == 0 {
		t.Fatal("no aws_applicationelb_request_count_average series")
	}
	lb := series[0].Labels["dimension_LoadBalancer"]
	if !strings.HasPrefix(lb, "app/") {
		t.Errorf("dimension_LoadBalancer = %q, want app/<name>/<hex> format", lb)
	}
	parts := strings.Split(lb, "/")
	if len(parts) != 3 {
		t.Errorf("dimension_LoadBalancer = %q, expected 3 slash-parts (got %d)", lb, len(parts))
	} else if len(parts[2]) != 16 {
		t.Errorf("dimension_LoadBalancer hex segment = %q (len %d), want 16 hex chars", parts[2], len(parts[2]))
	}
}

// ── Test: NLB dimension_LoadBalancer format "net/<name>/<16hex>" ─────────────

func TestNLBDimLoadBalancerFormat(t *testing.T) {
	cap := runTick(t, nil, nil)
	series := cap.Find("aws_networkelb_active_flow_count_average")
	if len(series) == 0 {
		t.Fatal("no aws_networkelb_active_flow_count_average series")
	}
	lb := series[0].Labels["dimension_LoadBalancer"]
	if !strings.HasPrefix(lb, "net/") {
		t.Errorf("dimension_LoadBalancer = %q, want net/<name>/<hex> format", lb)
	}
}

// ── Test: multiple ALBs ───────────────────────────────────────────────────────

func TestMultipleALBs(t *testing.T) {
	cfg := &cwinfra.Config{ALBs: intPtr(3), S3Buckets: intPtr(1)}
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)

	series := cap.Find("aws_applicationelb_request_count_average")
	lbIDs := make(map[string]bool)
	for _, s := range series {
		lbIDs[s.Labels["dimension_LoadBalancer"]] = true
	}
	if len(lbIDs) != 3 {
		t.Errorf("with ALBs=3 got %d distinct dimension_LoadBalancer values, want 3", len(lbIDs))
	}
}

// ── Test: ALB/S3 disable parity (explicit 0 → family fully off) ───────────────

// TestALBDisabled proves albs:0 suppresses every aws_applicationelb_* series including
// the _info series — full parity with the *bool family toggles. A real AWS account can
// have zero ALBs; this is the regression guard for that.
func TestALBDisabled(t *testing.T) {
	cfg := &cwinfra.Config{ALBs: intPtr(0)} // S3 left nil → still default 2
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_applicationelb_") {
			t.Errorf("unexpected ALB series %q when albs=0 (family must be fully disabled)", n)
		}
	}
	// Sibling families unaffected: S3 still defaults to 2 buckets.
	if got := len(cap.Find("aws_s3_info")); got != 2 {
		t.Errorf("S3 should be unaffected by albs=0; aws_s3_info = %d, want 2", got)
	}
}

// TestS3Disabled proves s3_buckets:0 suppresses every aws_s3_* series including _info.
func TestS3Disabled(t *testing.T) {
	cfg := &cwinfra.Config{S3Buckets: intPtr(0)} // ALB left nil → still default 1
	fx := &fixture.Set{Seed: "test", Env: coretest.Env(), Cloud: coretest.Cloud()}
	cap := tickWith(t, cfg, fx)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_s3_") {
			t.Errorf("unexpected S3 series %q when s3_buckets=0 (family must be fully disabled)", n)
		}
	}
	if got := len(cap.Find("aws_applicationelb_info")); got != 1 {
		t.Errorf("ALB should be unaffected by s3_buckets=0; aws_applicationelb_info = %d, want 1", got)
	}
}

// ── run helpers ───────────────────────────────────────────────────────────────

// runTick builds with default config plus optional cluster and runs one tick.
func runTick(t *testing.T, cfg *cwinfra.Config, cluster *fixture.Cluster) *coretest.MetricCapture {
	t.Helper()
	fx := &fixture.Set{
		Seed:    "test",
		Env:     coretest.Env(),
		Cloud:   coretest.Cloud(),
		Cluster: cluster,
	}
	return tickWith(t, cfg, fx)
}

// tickWith builds from explicit cfg and fx, runs one tick, returns the captured metrics.
// If cfg is nil, the Registration default (NewConfig()) is used.
func tickWith(t *testing.T, cfg *cwinfra.Config, fx *fixture.Set) *coretest.MetricCapture {
	t.Helper()
	reg := cwinfra.Registration()
	var rawCfg any
	if cfg != nil {
		rawCfg = cfg
	} else {
		rawCfg = reg.NewConfig()
	}
	con, err := reg.Build(rawCfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) // midday Wednesday — business hours
	if err := con.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// ── assert helpers ────────────────────────────────────────────────────────────

// assertRootsWithFiveStats verifies that each root name has all five stat suffixes present.
func assertRootsWithFiveStats(t *testing.T, cap *coretest.MetricCapture, roots []string) {
	t.Helper()
	suffixes := []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}
	nameSet := make(map[string]bool)
	for _, n := range cap.Names() {
		nameSet[n] = true
	}
	for _, root := range roots {
		for _, suf := range suffixes {
			full := root + suf
			if !nameSet[full] {
				t.Errorf("missing series %q (root=%q, suffix=%q)", full, root, suf)
			}
		}
	}
}

// assertHasName verifies that at least one series with the given name exists.
func assertHasName(t *testing.T, cap *coretest.MetricCapture, name string) {
	t.Helper()
	if len(cap.Find(name)) == 0 {
		t.Errorf("series %q not found", name)
	}
}

// ── Test: PrivateLink sub-family toggle ──────────────────────────────────────

// TestPrivateLink_EnabledByDefault asserts that with nil PrivateLink config (default),
// the aws_privatelinkendpoints_* and aws_privatelinkservices_* series are emitted with
// the correct namespace labels and space→underscore dimension names.
func TestPrivateLink_EnabledByDefault(t *testing.T) {
	cap := runTick(t, nil, nil)
	// Endpoints family present.
	if got := cap.Find("aws_privatelinkendpoints_active_connections_average"); len(got) == 0 {
		t.Fatal("missing aws_privatelinkendpoints_active_connections_average")
	}
	// Services family present.
	if got := cap.Find("aws_privatelinkservices_active_connections_average"); len(got) == 0 {
		t.Fatal("missing aws_privatelinkservices_active_connections_average")
	}
	// Namespace correct on endpoints family.
	for _, s := range cap.Find("aws_privatelinkendpoints_active_connections_average") {
		if s.Labels["namespace"] != "AWS/PrivateLinkEndpoints" {
			t.Errorf("endpoints namespace = %q, want AWS/PrivateLinkEndpoints", s.Labels["namespace"])
		}
		// Space→underscore dimension names (SK-3 live-confirmed).
		if _, ok := s.Labels["dimension_VPC_Endpoint_Id"]; !ok {
			t.Error("missing dimension_VPC_Endpoint_Id on endpoints series")
		}
	}
	// Namespace correct on services family.
	for _, s := range cap.Find("aws_privatelinkservices_active_connections_average") {
		if s.Labels["namespace"] != "AWS/PrivateLinkServices" {
			t.Errorf("services namespace = %q, want AWS/PrivateLinkServices", s.Labels["namespace"])
		}
	}
	// Info series present.
	if got := cap.Find("aws_privatelinkendpoints_info"); len(got) == 0 {
		t.Fatal("missing aws_privatelinkendpoints_info")
	}
	if got := cap.Find("aws_privatelinkservices_info"); len(got) == 0 {
		t.Fatal("missing aws_privatelinkservices_info")
	}
}

// TestPrivateLink_Disabled asserts that with PrivateLink: false, no aws_privatelink* series are emitted.
func TestPrivateLink_Disabled(t *testing.T) {
	f := false
	cfg := &cwinfra.Config{PrivateLink: &f}
	cap := runTick(t, cfg, nil)
	for _, n := range cap.Names() {
		if strings.HasPrefix(n, "aws_privatelink") {
			t.Errorf("PrivateLink=false: unexpected series %q", n)
		}
	}
}

// assertContainsKeys verifies that every key in wantKeys appears in gotKeys.
func assertContainsKeys(t *testing.T, ctx string, gotKeys, wantKeys []string) {
	t.Helper()
	got := make(map[string]bool, len(gotKeys))
	for _, k := range gotKeys {
		got[k] = true
	}
	for _, k := range wantKeys {
		if !got[k] {
			t.Errorf("%s: label key %q missing; got keys: %v", ctx, k, gotKeys)
		}
	}
}
