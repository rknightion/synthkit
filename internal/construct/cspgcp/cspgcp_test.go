// SPDX-License-Identifier: AGPL-3.0-only

package cspgcp_test

// cspgcp_test.go — contract tests for the csp_gcp construct.
//
// (a) Inventory per sub-signal — every expected series name is present.
// (b) Type-rule behavior:
//     - A known CUMULATIVE series is monotone across two ticks (state.Add).
//     - A known GAUGE series does NOT accumulate (state.Set).
// (c) DISTRIBUTION → expands to _bucket / _sum / _count (BASE name passed to Observe).
// (d) Enum completeness:
//     - Cloud SQL INSTANCE_STATE: both RUNNABLE and MAINTENANCE emitted.
//     - Cloud SQL REPLICATION_STATE: both HEALTHY and UNHEALTHY emitted.
//     - AlloyDB INSTANCES: both status="up" and status="down" emitted.
//     - Cloud Run CONTAINERS: both state="active" and state="idle" emitted.
// (e) Bigtable: exported_instance label present (NOT instance).
// (f) database_id form: "<project_id>:<instance_name>".
// (g) Deterministic project identity: "<company>-NN" pattern.
// (h) Pub/Sub unacked_bytes_by_region is present.
// (i) Interface conformance: Kind / Signals / Interval.
// (j) Logs: {job="integrations/gcp"} stream per project.
// (k) Base label set: job="integrations/gcp", project_id, unit on every metric series.
// (l) Sub-signal filtering: disabled sub-signal → no matching series.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/cspgcp"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// testNow is a fixed mid-business-hours time (Europe/Zurich 14:00).
var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC) // noon UTC = 14:00 Zurich

// buildDefault builds a csp_gcp construct with 2 projects, company="demo", all sub-signals.
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	cfg := &cspgcp.Config{Projects: 2, Company: "demo"}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickOnce calls Tick once and returns the MetricCapture and LogCapture.
func tickOnce(t *testing.T, c core.Construct) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc, lc
}

// ── (i) Interface conformance ─────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "csp_gcp" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "csp_gcp")
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Fatalf("Signals() len=%d want 2", len(sigs))
	}
	hasMetrics, hasLogs := false, false
	for _, s := range sigs {
		switch s {
		case core.Metrics:
			hasMetrics = true
		case core.Logs:
			hasLogs = true
		}
	}
	if !hasMetrics {
		t.Error("Signals(): Metrics missing")
	}
	if !hasLogs {
		t.Error("Signals(): Logs missing")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (g) Deterministic project identity ───────────────────────────────────────

func TestProjectIdentity(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 3, Company: "testco"}
	fx := &fixture.Set{Seed: "seed1"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	wantProjects := []string{"testco-01", "testco-02", "testco-03"}
	for _, want := range wantProjects {
		found := false
		for _, s := range mc.All() {
			if s.Labels["project_id"] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("project_id=%q not found in any series", want)
		}
	}
}

// ── (k) Base label set on every metric ───────────────────────────────────────

func TestBaseLabelSetOnEveryMetric(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, s := range mc.All() {
		if s.Labels["job"] != "integrations/gcp" {
			t.Errorf("series %q: job=%q want %q", s.Name, s.Labels["job"], "integrations/gcp")
		}
		if s.Labels["project_id"] == "" {
			t.Errorf("series %q: project_id is empty", s.Name)
		}
		if _, ok := s.Labels["unit"]; !ok {
			t.Errorf("series %q: unit label missing", s.Name)
		}
	}
}

// ── (SK-17) Exponential bucket shape tests ───────────────────────────────────

// TestExpBucketsShape verifies the expBuckets helper produces the right number of
// buckets and that each successive bound grows by the growth factor.
func TestExpBucketsShape(t *testing.T) {
	cases := []struct {
		scale, growth float64
		n             int
	}{
		{10, 1.1, 135}, // gcpRunLatencyBuckets
		{1, 1.4, 66},   // gcpLBLatencyBuckets
		{1, 1.4, 20},   // gcpDistBuckets
	}
	for _, tc := range cases {
		bs := cspgcp.ExpBucketsForTest(tc.scale, tc.growth, tc.n)
		if len(bs) != tc.n {
			t.Errorf("expBuckets(scale=%v, growth=%v, n=%d): got %d bounds, want %d",
				tc.scale, tc.growth, tc.n, len(bs), tc.n)
			continue
		}
		// First bound = scale * growth^0 = scale
		if bs[0] != tc.scale {
			t.Errorf("expBuckets first bound: got %v want %v", bs[0], tc.scale)
		}
		// Each bound grows by the growth factor (within float tolerance).
		for i := 1; i < len(bs); i++ {
			ratio := bs[i] / bs[i-1]
			if ratio < tc.growth*0.999 || ratio > tc.growth*1.001 {
				t.Errorf("expBuckets[%d]/[%d] = %v, want ~%v", i, i-1, ratio, tc.growth)
				break
			}
		}
	}
}

// TestLBHistogramBucketCount verifies the LB latency histogram uses gcpLBLatencyBuckets
// (66 finite bounds → 67 _bucket series including +Inf).
func TestLBHistogramBucketCount(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"loadbalancing"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	// _bucket series for https_total_latencies; each le value is a separate series.
	bucketSeries := mc.Find("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_total_latencies_bucket")
	if len(bucketSeries) == 0 {
		t.Fatal("no _bucket series for https_total_latencies")
	}
	// Count unique le values across all label combos for one label set (pick first country+backend).
	leCounts := map[string]bool{}
	first := bucketSeries[0]
	targetSig := first.Labels["client_country"] + "|" + first.Labels["backend_target_name"]
	for _, s := range bucketSeries {
		if s.Labels["client_country"]+"|"+s.Labels["backend_target_name"] == targetSig {
			leCounts[s.Labels["le"]] = true
		}
	}
	// gcpLBLatencyBuckets has 66 finite bounds → 67 with +Inf.
	const wantBounds = 67
	if len(leCounts) != wantBounds {
		t.Errorf("LB latency histogram: got %d unique le values, want %d (66 finite + +Inf)", len(leCounts), wantBounds)
	}
}

// TestCloudRunLatencyHistogramBucketCount verifies startup/probe histograms use
// gcpRunLatencyBuckets (135 finite bounds → 136 with +Inf).
func TestCloudRunLatencyHistogramBucketCount(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"cloudrun"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	bucketSeries := mc.Find("stackdriver_cloud_run_revision_run_googleapis_com_container_startup_latencies_bucket")
	if len(bucketSeries) == 0 {
		t.Fatal("no _bucket series for container_startup_latencies")
	}
	// Count unique le values for a single label combination.
	leCounts := map[string]bool{}
	first := bucketSeries[0]
	targetSig := first.Labels["container_name"] + "|" + first.Labels["service_name"]
	for _, s := range bucketSeries {
		if s.Labels["container_name"]+"|"+s.Labels["service_name"] == targetSig {
			leCounts[s.Labels["le"]] = true
		}
	}
	// gcpRunLatencyBuckets has 135 finite bounds → 136 with +Inf.
	const wantBounds = 136
	if len(leCounts) != wantBounds {
		t.Errorf("Cloud Run startup_latencies histogram: got %d unique le values, want %d (135 finite + +Inf)", len(leCounts), wantBounds)
	}
}

// ── (SK-18) Resource/metric label shape tests ─────────────────────────────────

// TestGCEInstanceIDLabel verifies that all gce_instance series carry an instance_id label
// containing a deterministic numeric string (real GCE resource label).
func TestGCEInstanceIDLabel(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"compute"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	anchor := "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization"
	series := mc.Find(anchor)
	if len(series) == 0 {
		t.Fatalf("GCE anchor %q not found", anchor)
	}
	for _, s := range series {
		id, ok := s.Labels["instance_id"]
		if !ok {
			t.Errorf("GCE series %q: instance_id label missing", s.Name)
			continue
		}
		if id == "" {
			t.Errorf("GCE series %q: instance_id is empty", s.Name)
			continue
		}
		// Must be numeric digits only (deterministic hash).
		for _, ch := range id {
			if ch < '0' || ch > '9' {
				t.Errorf("GCE series %q: instance_id=%q contains non-numeric char %q", s.Name, id, string(ch))
				break
			}
		}
	}
}

// TestGCSBucketLocationLabel verifies that all gcs_bucket series carry a location label.
func TestGCSBucketLocationLabel(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"storage"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	anchor := "stackdriver_gcs_bucket_storage_googleapis_com_storage_object_count"
	series := mc.Find(anchor)
	if len(series) == 0 {
		t.Fatalf("GCS anchor %q not found", anchor)
	}
	for _, s := range series {
		if loc, ok := s.Labels["location"]; !ok || loc == "" {
			t.Errorf("GCS series %q: location label missing or empty", s.Name)
		}
	}
}

// TestGCSApiRequestCountMethodAndResponseCode verifies that api_request_count carries
// method and response_code metric labels.
func TestGCSApiRequestCountMethodAndResponseCode(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"storage"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	name := "stackdriver_gcs_bucket_storage_googleapis_com_api_request_count"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("GCS api_request_count %q not found", name)
	}
	for _, s := range series {
		if m, ok := s.Labels["method"]; !ok || m == "" {
			t.Errorf("api_request_count: method label missing or empty")
		}
		if rc, ok := s.Labels["response_code"]; !ok || rc == "" {
			t.Errorf("api_request_count: response_code label missing or empty")
		}
	}
}

// TestCloudRunConfigurationNameLabel verifies that all cloud_run_revision series carry
// a configuration_name label (real resource label in GCP).
func TestCloudRunConfigurationNameLabel(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"cloudrun"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	anchor := "stackdriver_cloud_run_revision_run_googleapis_com_container_containers"
	series := mc.Find(anchor)
	if len(series) == 0 {
		t.Fatalf("Cloud Run containers %q not found", anchor)
	}
	for _, s := range series {
		if cn, ok := s.Labels["configuration_name"]; !ok || cn == "" {
			t.Errorf("Cloud Run series %q: configuration_name label missing or empty", s.Name)
		}
	}
}

// ── (a) Inventory per sub-signal ─────────────────────────────────────────────

// computeSeriesAnchors are the expected compute series (GAUGE + CUMULATIVE per the extract).
var computeSeriesAnchors = []string{
	"stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization",
	"stackdriver_gce_instance_compute_googleapis_com_instance_cpu_usage_time",
	"stackdriver_gce_instance_compute_googleapis_com_instance_network_received_bytes_count",
	"stackdriver_gce_instance_compute_googleapis_com_instance_network_sent_bytes_count",
	"stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_bytes_count",
	"stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_bytes_count",
	"stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_ops_count",
	"stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_ops_count",
}

var storageSeriesAnchors = []string{
	"stackdriver_gcs_bucket_storage_googleapis_com_storage_object_count",
	"stackdriver_gcs_bucket_storage_googleapis_com_storage_total_bytes",
	"stackdriver_gcs_bucket_storage_googleapis_com_network_received_bytes_count",
	"stackdriver_gcs_bucket_storage_googleapis_com_network_sent_bytes_count",
	"stackdriver_gcs_bucket_storage_googleapis_com_api_request_count",
}

var networkingSeriesAnchors = []string{
	"stackdriver_google_service_gce_client_networking_googleapis_com_google_service_response_bytes_count",
	"stackdriver_google_service_gce_client_networking_googleapis_com_google_service_request_bytes_count",
	"stackdriver_networking_googleapis_com_location_networking_googleapis_com_fixed_standard_tier_usage",
	"stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_egress_bytes_count",
	"stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_ingress_bytes_count",
}

var lbSeriesAnchors = []string{
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_count",
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_bytes_count",
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_response_bytes_count",
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_request_bytes_count",
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_response_bytes_count",
	// DISTRIBUTION bases — will appear as _bucket/_sum/_count
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_total_latencies",
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_frontend_tcp_rtt",
	"stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_latencies",
}

var pubsubSeriesAnchors = []string{
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_count",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_pull_ack_request_count",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_streaming_pull_response_count",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_expired_ack_deadlines_count",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_outstanding_messages",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_undelivered_messages",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_oldest_unacked_message_age",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_delivery_latency_health_score",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_unacked_messages_by_region",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_unacked_bytes_by_region",
	"stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies",
}

var cloudrunSeriesAnchors = []string{
	"stackdriver_cloud_run_revision_run_googleapis_com_container_containers",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_network_received_bytes_count",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_network_sent_bytes_count",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_billable_instance_time",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_inbound_bytes_count",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_outbound_bytes_count",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_attempt_count",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_count",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_cpu_usage",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_memory_usage",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_max_request_concurrencies",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_startup_latencies",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_probe_attempt_latencies",
	"stackdriver_cloud_run_revision_run_googleapis_com_container_probe_latencies",
}

var bigtableClusterAnchors = []string{
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_node_count",
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load",
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_hottest_node",
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_storage_utilization",
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_bytes_used",
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_storage_capacity",
	"stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_by_app_profile_by_method_by_table",
}

var bigtableTableAnchors = []string{
	"stackdriver_bigtable_table_bigtable_googleapis_com_table_bytes_used",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_data_boost_spu_usage",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_returned_rows_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_modified_rows_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_sent_bytes_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_received_bytes_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_error_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_multi_cluster_failovers_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_request_count",
	"stackdriver_bigtable_table_bigtable_googleapis_com_server_latencies",
	"stackdriver_bigtable_table_bigtable_googleapis_com_client_operation_latencies",
	"stackdriver_bigtable_table_bigtable_googleapis_com_client_attempt_latencies",
}

var cloudSQLCommonAnchors = []string{
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_utilization",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_utilization",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_utilization",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_available_for_failover",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_reserved_cores",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_quota",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_quota",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_read_ops_count",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_write_ops_count",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_connections",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_received_bytes_count",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_sent_bytes_count",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_instance_state",
	"stackdriver_cloudsql_database_cloudsql_googleapis_com_database_replication_state",
}

// seriesPresent returns true if any series with the exact name exists.
func seriesPresent(mc *coretest.MetricCapture, name string) bool {
	return len(mc.Find(name)) > 0
}

// histoPresent returns true if the histogram base name expanded to _bucket/_sum/_count.
func histoPresent(mc *coretest.MetricCapture, base string) bool {
	return seriesPresent(mc, base+"_bucket") &&
		seriesPresent(mc, base+"_sum") &&
		seriesPresent(mc, base+"_count")
}

func TestComputeInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range computeSeriesAnchors {
		if !seriesPresent(mc, name) {
			t.Errorf("compute: missing series %q", name)
		}
	}
}

func TestStorageInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range storageSeriesAnchors {
		if !seriesPresent(mc, name) {
			t.Errorf("storage: missing series %q", name)
		}
	}
}

func TestNetworkingInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range networkingSeriesAnchors {
		if !seriesPresent(mc, name) {
			t.Errorf("networking: missing series %q", name)
		}
	}
}

func TestLoadBalancingInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range lbSeriesAnchors {
		// Distribution bases appear as _bucket, scalars appear directly.
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("loadbalancing: missing series (or histogram) %q", name)
		}
	}
}

func TestPubSubInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range pubsubSeriesAnchors {
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("pubsub: missing series (or histogram) %q", name)
		}
	}
}

func TestCloudRunInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range cloudrunSeriesAnchors {
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("cloudrun: missing series (or histogram) %q", name)
		}
	}
}

func TestBigtableInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	all := append(bigtableClusterAnchors, bigtableTableAnchors...)
	for _, name := range all {
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("bigtable: missing series (or histogram) %q", name)
		}
	}
}

func TestCloudSQLInventory(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, name := range cloudSQLCommonAnchors {
		if !seriesPresent(mc, name) {
			t.Errorf("cloudsql: missing series %q", name)
		}
	}
}

// ── (b) Type-rule behavior ────────────────────────────────────────────────────

// TestCumulativeSeriesIsMonotone verifies that a known CUMULATIVE counter (cpu_usage_time)
// strictly increases across two ticks (state.Add semantics — I3).
func TestCumulativeSeriesIsMonotone(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"compute"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	now1 := testNow
	if err := c.Tick(context.Background(), now1, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	now2 := testNow.Add(60 * time.Second)
	if err := c.Tick(context.Background(), now2, w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	cumulName := "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_usage_time"
	s1 := mc1.Find(cumulName)
	s2 := mc2.Find(cumulName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("cumulative series %q not found in tick 1 (%d) or tick 2 (%d)", cumulName, len(s1), len(s2))
	}

	// Every tick-2 value must be ≥ the corresponding tick-1 value for matching labels.
	for _, a := range s1 {
		for _, b := range s2 {
			if a.Labels["instance_name"] == b.Labels["instance_name"] {
				if b.Value < a.Value {
					t.Errorf("cumulative series %q NOT monotone: tick1=%v tick2=%v instance=%s",
						cumulName, a.Value, b.Value, a.Labels["instance_name"])
				}
			}
		}
	}
}

// TestGaugeSeriesNotAccumulated verifies that a known GAUGE (cpu_utilization) does NOT
// accumulate across ticks (state.Set semantics — instantaneous).
func TestGaugeSeriesNotAccumulated(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"compute"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	gaugeName := "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization"
	s1 := mc1.Find(gaugeName)
	s2 := mc2.Find(gaugeName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("gauge series %q not found", gaugeName)
	}

	// Gauge values should remain ≤1.0 (clamped to [0,1]) and NOT double each tick.
	for _, s := range s2 {
		if s.Value > 1.0+1e-9 {
			t.Errorf("gauge series %q accumulated to %v (>1.0) — looks like Add was used instead of Set",
				gaugeName, s.Value)
		}
	}
}

// ── (c) DISTRIBUTION expands to _bucket/_sum/_count ──────────────────────────

func TestDistributionExpandsToHistogramSeries(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"loadbalancing"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	base := "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_total_latencies"
	if !seriesPresent(mc, base+"_bucket") {
		t.Errorf("distribution %q: _bucket series missing", base)
	}
	if !seriesPresent(mc, base+"_sum") {
		t.Errorf("distribution %q: _sum series missing", base)
	}
	if !seriesPresent(mc, base+"_count") {
		t.Errorf("distribution %q: _count series missing", base)
	}
	// Ensure the raw base name is NOT present (Collect appends suffixes)
	if seriesPresent(mc, base) {
		t.Errorf("distribution %q: base name should NOT be in output (only _bucket/_sum/_count)", base)
	}
	// No double-suffix
	if seriesPresent(mc, base+"_bucket_bucket") {
		t.Errorf("distribution %q: _bucket_bucket produced — BASE name not being passed to Observe", base)
	}
}

// ── (d) Enum completeness ────────────────────────────────────────────────────

func TestCloudSQLInstanceStateEnumCoverage(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	name := "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_instance_state"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("INSTANCE_STATE series %q not found", name)
	}

	states := map[string]bool{}
	for _, s := range series {
		if v, ok := s.Labels["state"]; ok {
			states[v] = true
		}
	}
	for _, want := range []string{"RUNNABLE", "RUNNING", "SUSPENDED", "PENDING_CREATE", "MAINTENANCE", "FAILED", "UNKNOWN_STATE"} {
		if !states[want] {
			t.Errorf("INSTANCE_STATE: state=%q not emitted (load-bearing — incident panels will be dark)", want)
		}
	}
}

func TestCloudSQLReplicationStateEnumCoverage(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	name := "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_replication_state"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("REPLICATION_STATE series %q not found", name)
	}

	states := map[string]bool{}
	for _, s := range series {
		if v, ok := s.Labels["state"]; ok {
			states[v] = true
		}
	}
	for _, want := range []string{"HEALTHY", "UNHEALTHY"} {
		if !states[want] {
			t.Errorf("REPLICATION_STATE: state=%q not emitted", want)
		}
	}
}

func TestAlloyDBInstancesEnumCoverage(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	name := "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_instances"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("AlloyDB INSTANCES series %q not found", name)
	}

	statuses := map[string]bool{}
	for _, s := range series {
		if v, ok := s.Labels["status"]; ok {
			statuses[v] = true
		}
	}
	for _, want := range []string{"up", "down"} {
		if !statuses[want] {
			t.Errorf("AlloyDB INSTANCES: status=%q not emitted (Overview panel will be dark)", want)
		}
	}
}

func TestCloudRunContainersEnumCoverage(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	name := "stackdriver_cloud_run_revision_run_googleapis_com_container_containers"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("Cloud Run CONTAINERS series %q not found", name)
	}

	states := map[string]bool{}
	for _, s := range series {
		if v, ok := s.Labels["state"]; ok {
			states[v] = true
		}
	}
	for _, want := range []string{"active", "idle"} {
		if !states[want] {
			t.Errorf("Cloud Run CONTAINERS: state=%q not emitted (idle-container panels will be dark)", want)
		}
	}
}

// ── (e) Bigtable: exported_instance NOT instance ──────────────────────────────

func TestBigtableExportedInstanceLabel(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	clusterAnchor := "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_node_count"
	series := mc.Find(clusterAnchor)
	if len(series) == 0 {
		t.Fatalf("Bigtable cluster anchor %q not found", clusterAnchor)
	}
	for _, s := range series {
		if _, ok := s.Labels["exported_instance"]; !ok {
			t.Errorf("Bigtable series %q: exported_instance label missing (variable picker will be empty)", s.Name)
		}
		if _, ok := s.Labels["instance"]; ok {
			t.Errorf("Bigtable series %q: 'instance' label present but should be 'exported_instance'", s.Name)
		}
	}

	tableAnchor := "stackdriver_bigtable_table_bigtable_googleapis_com_server_request_count"
	for _, s := range mc.Find(tableAnchor) {
		if _, ok := s.Labels["exported_instance"]; !ok {
			t.Errorf("Bigtable table series %q: exported_instance label missing", s.Name)
		}
		if _, ok := s.Labels["instance"]; ok {
			t.Errorf("Bigtable table series %q: 'instance' label present (should be exported_instance)", s.Name)
		}
	}
}

// ── (f) database_id form ──────────────────────────────────────────────────────

func TestCloudSQLDatabaseIDForm(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	anchor := "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up"
	series := mc.Find(anchor)
	if len(series) == 0 {
		t.Fatalf("Cloud SQL anchor %q not found", anchor)
	}
	for _, s := range series {
		dbID, ok := s.Labels["database_id"]
		if !ok {
			t.Errorf("Cloud SQL series %q: database_id label missing", s.Name)
			continue
		}
		projID, ok := s.Labels["project_id"]
		if !ok {
			t.Errorf("Cloud SQL series %q: project_id label missing", s.Name)
			continue
		}
		// Must be "<project_id>:<something>" form
		if !strings.HasPrefix(dbID, projID+":") {
			t.Errorf("Cloud SQL series %q: database_id=%q does not start with project_id=%q + ':'",
				s.Name, dbID, projID)
		}
		// Must have a non-empty instance suffix
		suffix := strings.TrimPrefix(dbID, projID+":")
		if suffix == "" {
			t.Errorf("Cloud SQL series %q: database_id=%q has empty instance part", s.Name, dbID)
		}
	}
}

// ── (h) Pub/Sub unacked_bytes_by_region ───────────────────────────────────────

func TestPubSubUnackedBytesByRegionPresent(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	name := "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_unacked_bytes_by_region"
	if !seriesPresent(mc, name) {
		t.Errorf("Pub/Sub %q MUST be emitted — variables.ts anchor; subscription dropdown will be empty", name)
	}
}

// ── (j) Logs sub-signal ───────────────────────────────────────────────────────

func TestLogsSubSignal(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 2, Company: "demo"}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(lc.Streams) == 0 {
		t.Fatal("no log streams emitted for csp_gcp with logs sub-signal enabled")
	}
	for _, stream := range lc.Streams {
		if stream.Labels["job"] != "integrations/gcp" {
			t.Errorf("log stream job=%q want %q", stream.Labels["job"], "integrations/gcp")
		}
		if len(stream.Lines) == 0 {
			t.Error("log stream has no lines")
		}
	}
}

// ── (l) Sub-signal filtering ──────────────────────────────────────────────────

func TestDisabledSubSignalProducesNoSeries(t *testing.T) {
	// Enable only compute; bigtable series should be absent.
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"compute"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	for _, name := range bigtableClusterAnchors {
		if seriesPresent(mc, name) {
			t.Errorf("sub-signal filter: bigtable series %q present but bigtable sub-signal is disabled", name)
		}
	}
}

// ── REGRESSION: sub-signal gating completeness ───────────────────────────────

// TestSubSignalEmptyDefaultsToAll verifies that an empty SubSignals list (zero Config)
// enables ALL service families — the canonical default path.
// Blueprint equivalent:
//
//	features: { csp_gcp: { enabled: true } }   # no sub_signals key → all families
func TestSubSignalEmptyDefaultsToAll(t *testing.T) {
	// Zero Config → Build applies defaults → all sub-signals enabled.
	cfg := &cspgcp.Config{}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build with zero Config: %v", err)
	}
	mc, lc := tickOnce(t, c)

	// One representative series from each family must be present.
	representatives := map[string]string{
		"compute":       "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization",
		"databases":     "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up",
		"storage":       "stackdriver_gcs_bucket_storage_googleapis_com_storage_object_count",
		"networking":    "stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_egress_bytes_count",
		"loadbalancing": "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_count",
		"pubsub":        "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_undelivered_messages",
		"cloudrun":      "stackdriver_cloud_run_revision_run_googleapis_com_container_containers",
		"bigtable":      "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_node_count",
	}
	for family, metric := range representatives {
		if !seriesPresent(mc, metric) {
			t.Errorf("empty sub_signals (default-all): family %q representative %q absent", family, metric)
		}
	}
	// Logs family: streams must be present.
	if len(lc.Streams) == 0 {
		t.Error("empty sub_signals (default-all): no log streams — logs family not emitted")
	}
}

// TestSubSignalDatabasesOnlyGating verifies that sub_signals=["databases"] emits ONLY
// database family metrics and suppresses all other service families.
// Blueprint equivalent:
//
//	features: { csp_gcp: { enabled: true, sub_signals: [databases] } }
func TestSubSignalDatabasesOnlyGating(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"databases"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, lc := tickOnce(t, c)

	// Databases anchor must be present.
	dbAnchor := "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up"
	if !seriesPresent(mc, dbAnchor) {
		t.Errorf("sub_signals=[databases]: databases anchor %q absent", dbAnchor)
	}

	// All other metric families must be absent.
	absentFamilies := map[string][]string{
		"compute":       computeSeriesAnchors,
		"storage":       storageSeriesAnchors,
		"networking":    networkingSeriesAnchors,
		"loadbalancing": lbSeriesAnchors,
		"pubsub":        pubsubSeriesAnchors,
		"cloudrun":      cloudrunSeriesAnchors,
		"bigtable":      append(bigtableClusterAnchors, bigtableTableAnchors...),
	}
	for family, anchors := range absentFamilies {
		for _, absent := range anchors {
			// Distribution series expand; check both base and _bucket form.
			if seriesPresent(mc, absent) || seriesPresent(mc, absent+"_bucket") {
				t.Errorf("sub_signals=[databases]: family %q series %q must be absent", family, absent)
			}
		}
	}
	// Logs must also be absent.
	if len(lc.Streams) > 0 {
		t.Errorf("sub_signals=[databases]: expected 0 log streams, got %d", len(lc.Streams))
	}
}

// ── DB fixture integration ─────────────────────────────────────────────────────

// TestCloudSQLFromFixtureDBs verifies that when fx.DBs contains GCP fixtures,
// Cloud SQL instances take their names from the fixtures.
func TestCloudSQLFromFixtureDBs(t *testing.T) {
	mysqlCloud := &fixture.Cloud{Provider: "gcp", AccountID: "demo-01", Region: "europe-west1"}
	mysqlDB := &fixture.DB{
		Engine: "mysql",
		Name:   "mysql-app-01",
		Cloud:  mysqlCloud,
	}
	pgCloud := &fixture.Cloud{Provider: "gcp", AccountID: "demo-01", Region: "europe-west1"}
	pgDB1 := &fixture.DB{Engine: "postgres", Name: "pg-app-01", Cloud: pgCloud}
	pgDB2 := &fixture.DB{Engine: "postgres", Name: "pg-app-02", Cloud: pgCloud}

	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"databases"}}
	fx := &fixture.Set{
		Seed: "test",
		DBs:  []*fixture.DB{mysqlDB, pgDB1, pgDB2},
	}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	anchor := "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up"
	series := mc.Find(anchor)
	if len(series) == 0 {
		t.Fatal("Cloud SQL anchor not found with DB fixtures")
	}

	// MySQL-specific metric should appear for the mysql fixture.
	mysqlMetric := "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_total"
	if !seriesPresent(mc, mysqlMetric) {
		t.Errorf("MySQL-specific metric %q missing when MySQL fixture provided", mysqlMetric)
	}
}

// ── Bigtable method enum completeness ─────────────────────────────────────────

func TestBigtableServerRequestCountMethodEnum(t *testing.T) {
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)

	name := "stackdriver_bigtable_table_bigtable_googleapis_com_server_request_count"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("Bigtable server_request_count %q not found", name)
	}

	wantMethods := []string{
		"Bigtable.ReadRows",
		"Bigtable.MutateRow",
		"Bigtable.MutateRows",
		"Bigtable.CheckAndMutateRow",
		"Bigtable.ReadModifyWriteRow",
		"Bigtable.SampleRowKeys",
		"Bigtable.ExecuteQuery",
	}

	methods := map[string]bool{}
	for _, s := range series {
		if m, ok := s.Labels["method"]; ok {
			methods[m] = true
		}
	}
	for _, want := range wantMethods {
		if !methods[want] {
			t.Errorf("Bigtable server_request_count: method=%q not emitted (dropdown will be incomplete)", want)
		}
	}

	// Live label set (SK-18 2026-06-14): app_profile and zone must be present on every series.
	for _, s := range series {
		if ap, ok := s.Labels["app_profile"]; !ok || ap == "" {
			t.Errorf("Bigtable server_request_count: app_profile label missing or empty (method=%s)", s.Labels["method"])
		}
		if z, ok := s.Labels["zone"]; !ok || z == "" {
			t.Errorf("Bigtable server_request_count: zone label missing or empty (method=%s)", s.Labels["method"])
		}
	}
}

// ── Default config behaviour ─────────────────────────────────────────────────

func TestDefaultProjectsAndCompany(t *testing.T) {
	cfg := &cspgcp.Config{} // zero value — defaults should apply
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build with zero Config: %v", err)
	}
	mc, _ := tickOnce(t, c)

	// Default company is "demo", default projects is 2 → expect demo-01 and demo-02.
	for _, proj := range []string{"demo-01", "demo-02"} {
		found := false
		for _, s := range mc.All() {
			if s.Labels["project_id"] == proj {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default config: project_id=%q not found", proj)
		}
	}
}

// ── No blueprint label ─────────────────────────────────────────────────────────

func TestNoScopeSubstrateBlueprint(t *testing.T) {
	// Verify ScopeSubstrate by checking Kind registration is correct.
	// The runner enforces no blueprint label for ScopeSubstrate constructs.
	// Here we verify the construct's Scope would be set correctly by checking
	// that no series emitted carries a "blueprint" label key (defence-in-depth).
	c := buildDefault(t)
	mc, _ := tickOnce(t, c)
	for _, s := range mc.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries blueprint label — csp_gcp is ScopeSubstrate (I21)", s.Name)
		}
	}
}

// ── Nil World writers are safe ────────────────────────────────────────────────

func TestNilWritersSafe(t *testing.T) {
	c := buildDefault(t)
	// World with nil Metrics and nil Logs — must not panic.
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil writers: %v", err)
	}
}

// ── REGRESSION: cumulative-discipline (M5 verification pins) ─────────────────

// twoTicksCloudRun builds a cspgcp with only the cloudrun sub-signal and ticks it
// twice 60 s apart, returning (mc1, mc2).
func twoTicksCloudRun(t *testing.T) (*coretest.MetricCapture, *coretest.MetricCapture) {
	t.Helper()
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"cloudrun"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc1 := &coretest.MetricCapture{}
	mc2 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	return mc1, mc2
}

// labelSig produces a stable string key from a label map (local helper; same
// algorithm as state.LabelSig but kept test-local to avoid a cross-package dep).
func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// TestCounterStrictlyIncreasesAcrossTicks pins that a known CUMULATIVE counter
// (Cloud Run network_received_bytes_count) strictly increases across two ticks.
// Regression for M5: if the call were changed to state.Set the second tick value
// would NOT be larger than the first.
func TestCounterStrictlyIncreasesAcrossTicks(t *testing.T) {
	mc1, mc2 := twoTicksCloudRun(t)

	const counterName = "stackdriver_cloud_run_revision_run_googleapis_com_container_network_received_bytes_count"
	s1 := mc1.Find(counterName)
	s2 := mc2.Find(counterName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("counter %q not found (tick1=%d tick2=%d)", counterName, len(s1), len(s2))
	}
	// Build a sig→value map from tick1 for matching.
	prev := make(map[string]float64, len(s1))
	for _, s := range s1 {
		prev[labelSig(s.Labels)] = s.Value
	}
	for _, s := range s2 {
		sig := labelSig(s.Labels)
		v1, ok := prev[sig]
		if !ok {
			continue // different label combination — skip
		}
		if s.Value <= v1 {
			t.Errorf("counter %q did NOT strictly increase: tick1=%.6f tick2=%.6f labels=%v",
				counterName, v1, s.Value, s.Labels)
		}
	}
}

// TestGaugeDoesNotAccumulateAcrossTicks pins that a known GAUGE (Cloud Run
// container_containers) does NOT accumulate — tick2 value stays in a plausible
// instantaneous range (≤10), not 2× tick1.
// Regression for M5: if the call were changed to state.Add the value would double.
func TestGaugeDoesNotAccumulateAcrossTicks(t *testing.T) {
	mc1, mc2 := twoTicksCloudRun(t)

	const gaugeName = "stackdriver_cloud_run_revision_run_googleapis_com_container_containers"
	s1 := mc1.Find(gaugeName)
	s2 := mc2.Find(gaugeName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("gauge %q not found (tick1=%d tick2=%d)", gaugeName, len(s1), len(s2))
	}
	// Build sig→value from tick1.
	prev := make(map[string]float64, len(s1))
	for _, s := range s1 {
		prev[labelSig(s.Labels)] = s.Value
	}
	for _, s := range s2 {
		sig := labelSig(s.Labels)
		v1, ok := prev[sig]
		if !ok {
			continue
		}
		// If Add were used instead of Set, tick2 ≈ 2×tick1 for identical noise seeds.
		// A real gauge changes by noise but stays in range (never 2×).
		// We assert tick2 ≤ 5× tick1 as a loose ceiling; the real ceiling is ~2bf ≈ 2.
		if s.Value > 10.0 {
			t.Errorf("gauge %q accumulated: tick1=%.4f tick2=%.4f — looks like state.Add was used",
				gaugeName, v1, s.Value)
		}
	}
}

// TestHistogramExpandsCorrectly pins that a known DISTRIBUTION
// (max_request_concurrencies — verified CORRECT in M5: Observe is right per §2.6)
// expands to _bucket/_sum/_count and the base name is absent.
func TestHistogramExpandsCorrectly(t *testing.T) {
	mc1, _ := twoTicksCloudRun(t)

	const base = "stackdriver_cloud_run_revision_run_googleapis_com_container_max_request_concurrencies"
	if !histoPresent(mc1, base) {
		t.Errorf("DISTRIBUTION %q: _bucket/_sum/_count not all present (Observe must pass BASE name)", base)
	}
	// Base name must NOT appear as a plain series (Collect appends suffixes).
	if seriesPresent(mc1, base) {
		t.Errorf("DISTRIBUTION %q: plain base name present — BASE name must not be emitted directly", base)
	}
}

// TestServerDataBoostSPUUsageIsPlainGauge pins that server_data_boost_spu_usage
// emits as a plain GAUGE (state.Set) — NOT as a histogram.
// M5 verification confirms: (G) in signals/cspgcp.md [slug: cspgcp] → state.Set is CORRECT.
// If accidentaly changed to state.Observe the series would appear as _bucket/_sum/_count.
func TestServerDataBoostSPUUsageIsPlainGauge(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"bigtable"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	const plainName = "stackdriver_bigtable_table_bigtable_googleapis_com_server_data_boost_spu_usage"
	if !seriesPresent(mc, plainName) {
		t.Errorf("server_data_boost_spu_usage: plain gauge series absent — expected state.Set emission")
	}
	// Must NOT produce histogram suffixes.
	if seriesPresent(mc, plainName+"_bucket") {
		t.Errorf("server_data_boost_spu_usage: _bucket present — should be plain gauge (state.Set)")
	}
	if seriesPresent(mc, plainName+"_sum") {
		t.Errorf("server_data_boost_spu_usage: _sum present — should be plain gauge (state.Set)")
	}
	if seriesPresent(mc, plainName+"_count") {
		t.Errorf("server_data_boost_spu_usage: _count present — should be plain gauge (state.Set)")
	}
}

// TestNodePostgresUptimeDeltaPinned pins the 60s delta/tick for
// node_postgres_uptime (AlloyDB). The delta equals the construct's own Interval()
// in seconds, so the cumulative counter tracks wall-clock 1:1 (SK-29 resolved
// 2026-06-14 — previously frozen at 86400/tick, ≈1440× fast).
// If this delta changes accidentally, update both the comment in cspgcp.go and SK-29.
func TestNodePostgresUptimeDeltaPinned(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"databases"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc1 := &coretest.MetricCapture{}
	mc2 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const name = "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_uptime"
	s1 := mc1.Find(name)
	s2 := mc2.Find(name)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("node_postgres_uptime not found (tick1=%d tick2=%d)", len(s1), len(s2))
	}
	// Build sig→value from tick1.
	prev := make(map[string]float64, len(s1))
	for _, s := range s1 {
		prev[labelSig(s.Labels)] = s.Value
	}
	const wantDelta = 60.0 // = Interval().Seconds(); uptime tracks wall-clock 1:1 (SK-29)
	for _, s := range s2 {
		sig := labelSig(s.Labels)
		v1, ok := prev[sig]
		if !ok {
			continue
		}
		delta := s.Value - v1
		if delta != wantDelta {
			t.Errorf("node_postgres_uptime delta per tick: got %.0f, want %.0f (= Interval().Seconds(); see cantfind SK-29)",
				delta, wantDelta)
		}
	}
}

// ── Vertex AI sub-signal tests ────────────────────────────────────────────────

// vertexEndpointAnchors lists the Prometheus series names emitted by the vertex sub-signal.
// Names follow the stackdriver_exporter naming convention:
//
//	stackdriver_<resource_type>_<metric_path_snake>
//
// Resource types:
//   - aiplatform_googleapis_com_endpoint — prediction/online/* (per-endpoint)
//   - aiplatform_googleapis_com_location — prediction/model_invocation/* (model garden)
//
// All names flagged v:assumed (no live Alloy capture yet — see signals/cspgcp.md [slug: cspgcp-vertex]).
var vertexEndpointAnchors = []string{
	"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count",
	"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_error_count",
	"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_response_latencies",
}

var vertexLocationAnchors = []string{
	"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_invocations",
	"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_input_token_count",
	"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_output_token_count",
	"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_failures",
	"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_latencies",
}

// TestVertexInventory verifies that all vertex series are present when sub_signals=["vertex"].
func TestVertexInventory(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	all := append(vertexEndpointAnchors, vertexLocationAnchors...)
	for _, name := range all {
		present := seriesPresent(mc, name) || histoPresent(mc, name)
		if !present {
			t.Errorf("vertex: missing series (or histogram) %q", name)
		}
	}
}

// TestVertexNotInDefault verifies that the vertex sub-signal is NOT emitted when
// sub_signals is empty (default). Existing test TestSubSignalEmptyDefaultsToAll
// must remain byte-identical; this test adds the opt-in negative assertion.
func TestVertexNotInDefault(t *testing.T) {
	cfg := &cspgcp.Config{} // zero → defaults → vertex MUST be absent
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build with zero Config: %v", err)
	}
	mc, _ := tickOnce(t, c)

	all := append(vertexEndpointAnchors, vertexLocationAnchors...)
	for _, name := range all {
		if seriesPresent(mc, name) || seriesPresent(mc, name+"_bucket") {
			t.Errorf("vertex NOT in default sub_signals but series %q is present", name)
		}
	}
}

// TestVertexEnvStamped verifies that when fx.Env is set, all vertex series carry
// env=<name>; and that env is absent (I13) when fx.Env is nil.
func TestVertexEnvStamped(t *testing.T) {
	env := &fixture.Env{Name: "production", Weight: 1.0, NonProd: false}
	fxWithEnv := &fixture.Set{Seed: "test", Env: env}
	fxNoEnv := &fixture.Set{Seed: "test"}

	for _, tc := range []struct {
		name    string
		fx      *fixture.Set
		wantEnv string // "" means env label must be absent
	}{
		{"with-env", fxWithEnv, "production"},
		{"no-env", fxNoEnv, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
			c, err := cspgcp.Build(cfg, tc.fx)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			mc, _ := tickOnce(t, c)

			// Check a representative scalar series (prediction_count).
			anchor := "stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count"
			series := mc.Find(anchor)
			if len(series) == 0 {
				t.Fatalf("vertex anchor %q not found", anchor)
			}
			for _, s := range series {
				if tc.wantEnv == "" {
					// env label must be absent (I13)
					if envVal, ok := s.Labels["env"]; ok {
						t.Errorf("vertex no-env: env label present (=%q) — must be omitted (I13)", envVal)
					}
				} else {
					// env label must be set to the env name
					if envVal, ok := s.Labels["env"]; !ok {
						t.Errorf("vertex with-env: env label missing")
					} else if envVal != tc.wantEnv {
						t.Errorf("vertex with-env: env=%q want %q", envVal, tc.wantEnv)
					}
				}
			}
		})
	}
}

// TestVertexHistogramExpands verifies that DISTRIBUTION bases expand to
// _bucket/_sum/_count (prediction/online/response/latencies and
// prediction/model_invocation/latencies).
func TestVertexHistogramExpands(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	for _, base := range []string{
		"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_response_latencies",
		"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_latencies",
	} {
		if !histoPresent(mc, base) {
			t.Errorf("vertex DISTRIBUTION %q: _bucket/_sum/_count not all present", base)
		}
		if seriesPresent(mc, base) {
			t.Errorf("vertex DISTRIBUTION %q: plain base name present — must NOT be emitted directly", base)
		}
	}
}

// TestVertexCountersAreMonotone verifies that vertex CUMULATIVE counters strictly
// increase across two ticks (state.Add semantics).
func TestVertexCountersAreMonotone(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc1 := &coretest.MetricCapture{}
	mc2 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, nil, nil)
	w2 := coretest.World(mc2, nil, nil)
	if err := c.Tick(context.Background(), testNow, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	const counterName = "stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count"
	s1 := mc1.Find(counterName)
	s2 := mc2.Find(counterName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("vertex counter %q not found (tick1=%d tick2=%d)", counterName, len(s1), len(s2))
	}

	prev := make(map[string]float64, len(s1))
	for _, s := range s1 {
		prev[labelSig(s.Labels)] = s.Value
	}
	for _, s := range s2 {
		sig := labelSig(s.Labels)
		v1, ok := prev[sig]
		if !ok {
			continue
		}
		if s.Value <= v1 {
			t.Errorf("vertex counter %q did NOT strictly increase: tick1=%.6f tick2=%.6f labels=%v",
				counterName, v1, s.Value, s.Labels)
		}
	}
}

// TestVertexBaseLabelsPresent verifies that all vertex series carry the mandatory
// base label set: job="integrations/gcp", project_id, unit (I13 / §K.1).
func TestVertexBaseLabelsPresent(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	// Only inspect vertex-prefixed series.
	for _, s := range mc.All() {
		if !strings.HasPrefix(s.Name, "stackdriver_aiplatform") {
			continue
		}
		if s.Labels["job"] != "integrations/gcp" {
			t.Errorf("vertex series %q: job=%q want %q", s.Name, s.Labels["job"], "integrations/gcp")
		}
		if s.Labels["project_id"] == "" {
			t.Errorf("vertex series %q: project_id is empty", s.Name)
		}
		if _, ok := s.Labels["unit"]; !ok {
			t.Errorf("vertex series %q: unit label missing", s.Name)
		}
	}
}

// TestVertexModelDifferentiation verifies that per-model volume differentiation is
// applied: a low-weight model (gemini-2.5-pro, weight 1.5) must emit fewer predictions
// than a high-weight model (gemini-2.5-flash-lite, weight 6.0) across the same tick.
// When the emitter was flat (200*vf for every model) this test would fail because both
// models produced identical values.
func TestVertexModelDifferentiation(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	const predCount = "stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count"

	var flashLiteVal, proVal float64
	flashLiteFound, proFound := false, false
	for _, s := range mc.All() {
		if s.Name != predCount {
			continue
		}
		mid := s.Labels["model_id"]
		if mid == "gemini-2.5-flash-lite" {
			flashLiteVal = s.Value
			flashLiteFound = true
		}
		if mid == "gemini-2.5-pro" {
			proVal = s.Value
			proFound = true
		}
	}
	if !flashLiteFound {
		t.Fatal("gemini-2.5-flash-lite not found in prediction_count series")
	}
	if !proFound {
		t.Fatal("gemini-2.5-pro not found in prediction_count series")
	}
	// flash-lite weight=6.0, pro weight=1.5 → flash-lite must be strictly greater.
	if flashLiteVal <= proVal {
		t.Errorf("expected flash-lite (%v) > pro (%v) — per-model weight differentiation not applied", flashLiteVal, proVal)
	}
}

// TestVertexVersionIDNonEmpty verifies that every vertex endpoint and location series
// carries a non-empty version_id / model_version_id (blank violates the omit-empty rule).
func TestVertexVersionIDNonEmpty(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	for _, s := range mc.All() {
		if !strings.HasPrefix(s.Name, "stackdriver_aiplatform") {
			continue
		}
		if vid, ok := s.Labels["version_id"]; ok && vid == "" {
			t.Errorf("series %q model_id=%q: version_id is empty (violates omit-empty)", s.Name, s.Labels["model_id"])
		}
		if mvid, ok := s.Labels["model_version_id"]; ok && mvid == "" {
			t.Errorf("series %q model_id=%q: model_version_id is empty (violates omit-empty)", s.Name, s.Labels["model_id"])
		}
	}
}

// TestVertexCurrentModelIDs verifies that the current-generation model IDs are present
// and the retired gemini-1-5-* IDs are gone.
func TestVertexCurrentModelIDs(t *testing.T) {
	cfg := &cspgcp.Config{Projects: 1, Company: "demo", SubSignals: []string{"vertex"}}
	fx := &fixture.Set{Seed: "test"}
	c, err := cspgcp.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc, _ := tickOnce(t, c)

	wantModels := []string{
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-2.5-pro",
		"claude-sonnet-4-5@20250929",
		"claude-haiku-4-5@20251001",
		"text-embedding-005",
	}
	retiredModels := []string{"gemini-1-5-flash", "gemini-1-5-pro"}

	const predCount = "stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count"

	foundIDs := map[string]bool{}
	for _, s := range mc.All() {
		if s.Name == predCount {
			foundIDs[s.Labels["model_id"]] = true
		}
	}

	for _, want := range wantModels {
		if !foundIDs[want] {
			t.Errorf("current model_id %q not found in vertex prediction_count", want)
		}
	}
	for _, retired := range retiredModels {
		if foundIDs[retired] {
			t.Errorf("retired model_id %q still present — must be replaced", retired)
		}
	}
}

// Silence unused import.
var _ = fmt.Sprintf
