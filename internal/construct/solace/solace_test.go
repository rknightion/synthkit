// SPDX-License-Identifier: AGPL-3.0-only

package solace_test

// solace_test.go - contract tests for the solace construct.
//
// Every metric name and label asserted here is sourced from solacecommunity/solace-prometheus-exporter
// at release tag v1.21.0 (internal/semp/metricDesc.go and the per-target get*Semp1.go files). The
// per-family comments name the source file; signals/solace.md carries the line-level citations.
//
// (a) Interface conformance.
// (b) Build validation rejects ambiguous or unsafe declarations; no names are invented as defaults.
// (c) Only sourced family names are emitted, with exactly the sourced label sets.
// (d) Per-queue and per-VPN series exist for every declared broker/VPN/queue.
// (e) Counters are cumulative across ticks and tagged KindCounter.
// (f) Queue depth integrates publish minus consume; VPN, broker spool and rx/tx roll up from queues.
// (g) The targets list gates families; QueueRates (deprecated upstream) is opt-in.
// (h) Absent dimensions are omitted, never empty-valued.
// (i) A nil Metrics writer is safe.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/solace"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

func f64(v float64) *float64 { return &v }
func i64(v int) *int         { return &v }

// queueFamilies are the per-queue families (labels vpn_name, queue_name). Source: metricDesc.go
// QueueDetails (lines 616-622), QueueStats (56-71) and QueueRates (606-615) at v1.21.0.
var queueDetailsNames = []string{
	"solace_queue_spool_quota_bytes",
	"solace_queue_spool_usage_bytes",
	"solace_queue_spool_usage_msgs",
	"solace_queue_binds",
	"solace_queue_subscriptions",
}

var queueStatsNames = []string{
	"solace_queue_byte_spooled",
	"solace_queue_msg_spooled",
	"solace_queue_msg_redelivered",
	"solace_queue_msg_retransmitted",
	"solace_queue_msg_spool_usage_exceeded",
	"solace_queue_msg_max_msg_size_exceeded",
	"solace_queue_msg_total_deleted",
	"solace_queue_msg_shutdown_discarded",
	"solace_queue_msg_ttl_discarded",
	"solace_queue_msg_ttl_dmq",
	"solace_queue_msg_ttl_dmq_failed",
	"solace_queue_msg_max_redelivered_discarded",
	"solace_queue_msg_max_redelivered_dmq",
	"solace_queue_msg_max_redelivered_dmq_failed",
}

var queueRatesNames = []string{
	"solace_queue_rx_msg_rate",
	"solace_queue_tx_msg_rate",
	"solace_queue_rx_byte_rate",
	"solace_queue_tx_byte_rate",
	"solace_queue_rx_msg_rate_avg",
	"solace_queue_tx_msg_rate_avg",
	"solace_queue_rx_byte_rate_avg",
	"solace_queue_tx_byte_rate_avg",
}

// vpnFamilies carry the single label vpn_name. Source: metricDesc.go Vpn (393-403), VpnStats
// (550-571) and VpnSpool (473-487).
var vpnNames = []string{
	"solace_vpn_is_management_vpn",
	"solace_vpn_enabled",
	"solace_vpn_operational",
	"solace_vpn_locally_configured",
	"solace_vpn_local_status",
	"solace_vpn_unique_subscriptions",
	"solace_vpn_total_local_unique_subscriptions",
	"solace_vpn_total_remote_unique_subscriptions",
	"solace_vpn_total_unique_subscriptions",
}

var vpnStatsNames = []string{
	"solace_vpn_rx_msgs_total",
	"solace_vpn_tx_msgs_total",
	"solace_vpn_rx_bytes_total",
	"solace_vpn_tx_bytes_total",
	"solace_vpn_rx_discarded_msgs_total",
	"solace_vpn_tx_discarded_msgs_total",
	"solace_vpn_connections",
	"solace_vpn_connections_service_amqp",
	"solace_vpn_connections_service_mqtt",
	"solace_vpn_connections_service_smf",
	"solace_vpn_connections_service_web",
	"solace_vpn_connections_service_rest_in",
	"solace_vpn_connections_service_rest_out",
}

// vpnStatsQuotaNames are emitted only when the VPN declares max_connections.
var vpnStatsQuotaNames = []string{
	"solace_vpn_quota_connections",
}

var vpnSpoolNames = []string{
	"solace_vpn_spool_quota_bytes",
	"solace_vpn_spool_usage_bytes",
	"solace_vpn_spool_usage_pct",
	"solace_vpn_spool_usage_msgs",
	"solace_vpn_spool_current_endpoints",
	"solace_vpn_spool_current_egress_flows",
	"solace_vpn_spool_current_ingress_flows",
	"solace_vpn_spool_current_transacted_sessions",
	"solace_vpn_spool_current_transacted_msgs",
}

// GlobalStats dispatches GetGlobalStatsSemp1, not GetGlobalSystemInfoSemp1, even though
// both use the GlobalStats descriptor group. Source: exporter.collect.go:68-71 and
// getGlobalStatsSemp1.go:118-124 at v1.21.0 / 710f4968c5914c1396a5ac9eb07c88465cce9916.
var globalNames = []string{
	"solace_system_total_clients_connected",
	"solace_system_rx_msgs_total",
	"solace_system_tx_msgs_total",
	"solace_system_rx_bytes_total",
	"solace_system_tx_bytes_total",
	"solace_system_total_rx_discards",
	"solace_system_total_tx_discards",
}

// GlobalSystemInfo dispatches GetGlobalSystemInfoSemp1 (getGlobalStatsSemp1.go:52-56).
// system_message_spool_quota remains intentionally unmodelled; capacities require declaration.
var systemInfoNames = []string{"solace_system_uptime_seconds"}

var globalOptionalNames = []string{
	"solace_system_total_clients_quota",
	"solace_system_cpu_cores",
	"solace_system_memory_bytes",
}

// spoolNames carry no labels beyond job/instance. Source: metricDesc.go Spool (164-209).
var spoolNames = []string{
	"solace_system_spool_usage_bytes",
	"solace_system_spool_usage_msgs",
	"solace_system_spool_ingress_flows_count",
	"solace_system_spool_egress_flows_count",
	"solace_system_spool_egress_flows_active",
	"solace_system_spool_egress_flows_inactive",
	"solace_system_spool_egress_flows_browser",
	"solace_system_spool_endpoints_queue",
	"solace_system_spool_endpoints_dte",
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// testConfig declares two brokers with neutral names: broker-a carries one VPN with two queues,
// broker-b one VPN with one queue.
func testConfig() *solace.Config {
	return &solace.Config{
		Brokers: []solace.BrokerConfig{
			{
				Name:           "broker-a",
				CPUCores:       4,
				MemoryGiB:      16,
				MaxConnections: 1000,
				VPNs: []solace.VPNConfig{{
					Name:           "vpn-1",
					Publishers:     i64(2),
					MaxConnections: 100,
					Queues: []solace.QueueConfig{
						{Name: "queue-1", PublishMsgsPerSec: f64(2), ConsumeMsgsPerSec: f64(6), Consumers: i64(2)},
						{Name: "queue-2", PublishMsgsPerSec: f64(1)},
					},
				}},
			},
			{
				Name: "broker-b",
				VPNs: []solace.VPNConfig{{
					Name:   "vpn-2",
					Queues: []solace.QueueConfig{{Name: "queue-3"}},
				}},
			},
		},
	}
}

func build(t *testing.T, cfg *solace.Config) core.Construct {
	t.Helper()
	c, err := solace.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickN ticks the construct n times 60s apart starting at testNow and returns the capture.
func tickN(t *testing.T, c core.Construct, n int) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	for i := range n {
		if err := c.Tick(context.Background(), testNow.Add(time.Duration(i)*time.Minute), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	return mc
}

// last returns the final captured sample of the series with the name and exact label subset.
func last(t *testing.T, mc *coretest.MetricCapture, name string, labels map[string]string) promrw.Series {
	t.Helper()
	var found promrw.Series
	ok := false
	for _, s := range mc.Find(name) {
		match := true
		for k, v := range labels {
			if s.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			found, ok = s, true
		}
	}
	if !ok {
		t.Fatalf("no series %s%v", name, labels)
	}
	return found
}

func TestInterfaceConformance(t *testing.T) {
	c := build(t, testConfig())
	if c.Kind() != "solace" {
		t.Errorf("Kind()=%q want solace", c.Kind())
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
	reg := solace.Registration()
	if reg.Kind != "solace" || reg.Scope != core.ScopeSubstrate || reg.Group != core.GroupIntegration {
		t.Errorf("registration = %+v, want kind solace, substrate scope, integration group", reg)
	}
}

func TestBuildValidation(t *testing.T) {
	cases := map[string]func(*solace.Config){
		"no brokers":           func(c *solace.Config) { c.Brokers = nil },
		"empty broker name":    func(c *solace.Config) { c.Brokers[0].Name = "" },
		"duplicate broker":     func(c *solace.Config) { c.Brokers[1].Name = c.Brokers[0].Name },
		"duplicate instance":   func(c *solace.Config) { c.Brokers[0].Instance = "x:1"; c.Brokers[1].Instance = "x:1" },
		"broker without vpn":   func(c *solace.Config) { c.Brokers[1].VPNs = nil },
		"empty vpn name":       func(c *solace.Config) { c.Brokers[0].VPNs[0].Name = "" },
		"duplicate vpn":        func(c *solace.Config) { c.Brokers[0].VPNs = append(c.Brokers[0].VPNs, c.Brokers[0].VPNs[0]) },
		"empty queue name":     func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[0].Name = "" },
		"duplicate queue":      func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[1].Name = "queue-1" },
		"negative publish":     func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[0].PublishMsgsPerSec = f64(-1) },
		"negative consume":     func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[0].ConsumeMsgsPerSec = f64(-1) },
		"negative consumers":   func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[0].Consumers = i64(-1) },
		"negative msg bytes":   func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[0].AvgMsgBytes = -1 },
		"negative quota":       func(c *solace.Config) { c.Brokers[0].VPNs[0].Queues[0].SpoolQuotaMB = -1 },
		"unknown target":       func(c *solace.Config) { c.Targets = []string{"NoSuchTarget"} },
		"duplicate target":     func(c *solace.Config) { c.Targets = []string{"VpnStats", "VpnStats"} },
		"negative cpu cores":   func(c *solace.Config) { c.Brokers[0].CPUCores = -1 },
		"negative memory":      func(c *solace.Config) { c.Brokers[0].MemoryGiB = -1 },
		"negative publishers":  func(c *solace.Config) { c.Brokers[0].VPNs[0].Publishers = i64(-1) },
		"negative vpn conns":   func(c *solace.Config) { c.Brokers[0].VPNs[0].MaxConnections = -1 },
		"negative broker conn": func(c *solace.Config) { c.Brokers[0].MaxConnections = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			mutate(cfg)
			if _, err := solace.Build(cfg, nil); err == nil {
				t.Fatal("Build accepted an invalid declaration")
			}
		})
	}
	if _, err := solace.Build("not a config", nil); err == nil {
		t.Error("Build accepted a foreign config type")
	}
}

// TestOnlySourcedFamiliesWithSourcedLabels is the signal-contract gate: every emitted name is one
// of the sourced families and carries exactly its sourced label keys plus the scrape identity.
func TestOnlySourcedFamiliesWithSourcedLabels(t *testing.T) {
	cfg := testConfig()
	cfg.Targets = []string{"Vpn", "VpnStats", "VpnSpool", "QueueDetails", "QueueStats", "QueueRates", "GlobalStats", "GlobalSystemInfo", "Spool"}
	mc := tickN(t, build(t, cfg), 2)

	queueLabels := []string{"instance", "job", "queue_name", "vpn_name"}
	vpnLabels := []string{"instance", "job", "vpn_name"}
	sysLabels := []string{"instance", "job"}
	want := map[string][]string{}
	for _, n := range concat(queueDetailsNames, queueStatsNames, queueRatesNames) {
		want[n] = queueLabels
	}
	for _, n := range concat(vpnNames, vpnStatsNames, vpnStatsQuotaNames, vpnSpoolNames) {
		want[n] = vpnLabels
	}
	for _, n := range concat(globalNames, systemInfoNames, globalOptionalNames, spoolNames) {
		want[n] = sysLabels
	}
	want["solace_up"] = []string{"endpoint", "instance", "job"}

	for _, name := range mc.Names() {
		keys, ok := want[name]
		if !ok {
			t.Errorf("emitted unsourced family %q", name)
			continue
		}
		if got := mc.LabelKeys(name); strings.Join(got, ",") != strings.Join(keys, ",") {
			t.Errorf("%s label keys = %v, want %v", name, got, keys)
		}
	}
	for name := range want {
		if len(mc.Find(name)) == 0 {
			t.Errorf("sourced family %q never emitted", name)
		}
	}
	for _, s := range mc.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Fatalf("%s carries a blueprint label; the construct is substrate scoped", s.Name)
		}
	}
}

func TestPerQueueAndPerVPNSeries(t *testing.T) {
	mc := tickN(t, build(t, testConfig()), 1)

	type id struct{ instance, vpn, queue string }
	wantQueues := []id{
		{"broker-a:9628", "vpn-1", "queue-1"},
		{"broker-a:9628", "vpn-1", "queue-2"},
		{"broker-b:9628", "vpn-2", "queue-3"},
	}
	for _, q := range wantQueues {
		for _, name := range concat(queueDetailsNames, queueStatsNames) {
			last(t, mc, name, map[string]string{"instance": q.instance, "vpn_name": q.vpn, "queue_name": q.queue})
		}
	}
	for _, v := range []id{{"broker-a:9628", "vpn-1", ""}, {"broker-b:9628", "vpn-2", ""}} {
		for _, name := range concat(vpnNames, vpnStatsNames, vpnSpoolNames) {
			last(t, mc, name, map[string]string{"instance": v.instance, "vpn_name": v.vpn})
		}
	}
	// One series per declared queue per family, no cross-broker leakage.
	if got := len(mc.Find("solace_queue_binds")); got != 3 {
		t.Errorf("solace_queue_binds series = %d, want 3", got)
	}
	if got := len(mc.Find("solace_vpn_enabled")); got != 2 {
		t.Errorf("solace_vpn_enabled series = %d, want 2", got)
	}
	// Default job when none is declared.
	for _, s := range mc.Find("solace_vpn_enabled") {
		if s.Labels["job"] != "solace-prometheus-exporter" {
			t.Errorf("job = %q, want solace-prometheus-exporter", s.Labels["job"])
		}
	}
}

func TestJobAndInstanceOverrides(t *testing.T) {
	cfg := testConfig()
	cfg.Job = "custom-job"
	cfg.Brokers[0].Instance = "exporter-1:9628"
	mc := tickN(t, build(t, cfg), 1)
	s := last(t, mc, "solace_vpn_enabled", map[string]string{"vpn_name": "vpn-1"})
	if s.Labels["job"] != "custom-job" || s.Labels["instance"] != "exporter-1:9628" {
		t.Errorf("labels = %v", s.Labels)
	}
}

func TestCountersAreCumulative(t *testing.T) {
	mc := tickN(t, build(t, testConfig()), 4)
	counters := concat(queueStatsNames,
		[]string{"solace_vpn_rx_msgs_total", "solace_vpn_tx_msgs_total", "solace_vpn_rx_bytes_total",
			"solace_vpn_tx_bytes_total", "solace_vpn_rx_discarded_msgs_total", "solace_vpn_tx_discarded_msgs_total",
			"solace_system_rx_msgs_total", "solace_system_tx_msgs_total", "solace_system_rx_bytes_total",
			"solace_system_tx_bytes_total", "solace_system_total_rx_discards", "solace_system_total_tx_discards",
			"solace_system_uptime_seconds"})
	for _, name := range counters {
		samples := mc.Find(name)
		if len(samples) == 0 {
			t.Errorf("%s missing", name)
			continue
		}
		prev := map[string]float64{}
		for _, s := range samples {
			if s.Kind != promrw.KindCounter {
				t.Errorf("%s kind = %v, want counter", name, s.Kind)
			}
			key := s.Labels["instance"] + "|" + s.Labels["vpn_name"] + "|" + s.Labels["queue_name"]
			if s.Value < prev[key] {
				t.Errorf("%s decreased for %s: %v -> %v", name, key, prev[key], s.Value)
			}
			prev[key] = s.Value
		}
	}
	// A publishing queue strictly advances its spooled counter every tick.
	var vals []float64
	for _, s := range mc.Find("solace_queue_msg_spooled") {
		if s.Labels["queue_name"] == "queue-1" {
			vals = append(vals, s.Value)
		}
	}
	for i := 1; i < len(vals); i++ {
		if vals[i] <= vals[i-1] {
			t.Errorf("queue-1 spooled did not advance: %v", vals)
		}
	}
	// Uptime advances by the tick spacing (60s).
	var up []float64
	for _, s := range mc.Find("solace_system_uptime_seconds") {
		if s.Labels["instance"] == "broker-a:9628" {
			up = append(up, s.Value)
		}
	}
	for i := 1; i < len(up); i++ {
		if d := up[i] - up[i-1]; d != 60 {
			t.Errorf("uptime step = %v, want 60", d)
		}
	}
}

// TestQueueDepthIntegrates proves the red/green signal that matters operationally: a queue whose
// consumers drain slower than publishers fill it accumulates depth, and a queue with headroom does not.
func TestQueueDepthIntegrates(t *testing.T) {
	cfg := &solace.Config{Brokers: []solace.BrokerConfig{{
		Name: "broker-a",
		VPNs: []solace.VPNConfig{{
			Name: "vpn-1",
			Queues: []solace.QueueConfig{
				{Name: "stalled", PublishMsgsPerSec: f64(2), ConsumeMsgsPerSec: f64(0), AvgMsgBytes: 1000},
				{Name: "healthy", PublishMsgsPerSec: f64(2), ConsumeMsgsPerSec: f64(10), AvgMsgBytes: 1000},
			},
		}},
	}}}
	mc := tickN(t, build(t, cfg), 5)

	depth := func(queue string) []float64 {
		var out []float64
		for _, s := range mc.Find("solace_queue_spool_usage_msgs") {
			if s.Labels["queue_name"] == queue {
				out = append(out, s.Value)
			}
		}
		return out
	}
	stalled, healthy := depth("stalled"), depth("healthy")
	for i := 1; i < len(stalled); i++ {
		if stalled[i] <= stalled[i-1] {
			t.Errorf("stalled queue depth did not grow: %v", stalled)
		}
	}
	for _, d := range healthy {
		if d != 0 {
			t.Errorf("healthy queue accumulated depth: %v", healthy)
			break
		}
	}
	// Spool bytes = depth * avg message size; a stalled queue delivers nothing.
	qs := map[string]string{"queue_name": "stalled"}
	if b, m := last(t, mc, "solace_queue_spool_usage_bytes", qs).Value, last(t, mc, "solace_queue_spool_usage_msgs", qs).Value; b != m*1000 {
		t.Errorf("spool bytes %v != msgs %v * 1000", b, m)
	}
	if v := last(t, mc, "solace_queue_msg_total_deleted", qs).Value; v != 0 {
		t.Errorf("stalled queue deleted %v messages, want 0", v)
	}
}

func TestRollups(t *testing.T) {
	mc := tickN(t, build(t, testConfig()), 3)
	v1 := map[string]string{"instance": "broker-a:9628", "vpn_name": "vpn-1"}
	sys := map[string]string{"instance": "broker-a:9628"}
	q1 := map[string]string{"instance": "broker-a:9628", "queue_name": "queue-1"}
	q2 := map[string]string{"instance": "broker-a:9628", "queue_name": "queue-2"}

	sum := func(name string) float64 {
		return last(t, mc, name, q1).Value + last(t, mc, name, q2).Value
	}
	if got, want := last(t, mc, "solace_vpn_rx_msgs_total", v1).Value, sum("solace_queue_msg_spooled"); got != want {
		t.Errorf("vpn rx msgs %v != sum of queue spooled %v", got, want)
	}
	if got, want := last(t, mc, "solace_vpn_tx_msgs_total", v1).Value, sum("solace_queue_msg_total_deleted"); got != want {
		t.Errorf("vpn tx msgs %v != sum of queue deleted %v", got, want)
	}
	if got, want := last(t, mc, "solace_vpn_rx_bytes_total", v1).Value, sum("solace_queue_byte_spooled"); got != want {
		t.Errorf("vpn rx bytes %v != sum of queue spooled bytes %v", got, want)
	}
	if got, want := last(t, mc, "solace_vpn_spool_usage_msgs", v1).Value, sum("solace_queue_spool_usage_msgs"); got != want {
		t.Errorf("vpn spool msgs %v != sum of queue depth %v", got, want)
	}
	if got, want := last(t, mc, "solace_vpn_spool_usage_bytes", v1).Value, sum("solace_queue_spool_usage_bytes"); got != want {
		t.Errorf("vpn spool bytes %v != sum of queue spool bytes %v", got, want)
	}
	if got := last(t, mc, "solace_vpn_spool_current_endpoints", v1).Value; got != 2 {
		t.Errorf("vpn endpoints = %v, want 2", got)
	}
	// Flows: 2 publisher flows in; consumer flows out = sum of queue consumers (2 + default 1).
	if got := last(t, mc, "solace_vpn_spool_current_ingress_flows", v1).Value; got != 2 {
		t.Errorf("vpn ingress flows = %v, want 2", got)
	}
	if got := last(t, mc, "solace_vpn_spool_current_egress_flows", v1).Value; got != 3 {
		t.Errorf("vpn egress flows = %v, want 3", got)
	}
	// Connections: publishers + consumers on the SMF service; total and per-service agree.
	if got := last(t, mc, "solace_vpn_connections_service_smf", v1).Value; got != 5 {
		t.Errorf("vpn smf connections = %v, want 5", got)
	}
	if got := last(t, mc, "solace_vpn_connections", v1).Value; got != 5 {
		t.Errorf("vpn connections = %v, want 5", got)
	}
	if got := last(t, mc, "solace_vpn_quota_connections", v1).Value; got != 100 {
		t.Errorf("vpn quota connections = %v, want 100", got)
	}
	if got := last(t, mc, "solace_system_total_clients_connected", sys).Value; got != 5 {
		t.Errorf("clients connected = %v, want 5", got)
	}
	if got, want := last(t, mc, "solace_system_rx_msgs_total", sys).Value, last(t, mc, "solace_vpn_rx_msgs_total", v1).Value; got != want {
		t.Errorf("system rx msgs %v != vpn rx msgs %v (single VPN broker)", got, want)
	}
	if got, want := last(t, mc, "solace_system_spool_usage_msgs", sys).Value, last(t, mc, "solace_vpn_spool_usage_msgs", v1).Value; got != want {
		t.Errorf("system spool msgs %v != vpn spool msgs %v", got, want)
	}
	if got := last(t, mc, "solace_system_spool_endpoints_queue", sys).Value; got != 2 {
		t.Errorf("system queue endpoints = %v, want 2", got)
	}
	if got := last(t, mc, "solace_system_spool_egress_flows_count", sys).Value; got != 3 {
		t.Errorf("system egress flows = %v, want 3", got)
	}
	// Spool percentage = round(usage/quota*100); the default quota is wide, so a healthy VPN rounds to 0.
	if got := last(t, mc, "solace_vpn_spool_usage_pct", v1).Value; got != 0 {
		t.Errorf("vpn spool pct = %v, want 0", got)
	}
}

func TestTargetsGateFamilies(t *testing.T) {
	// Follow exporter.collect.go dispatch, not metricDesc.go group names. Each expected
	// family comes from that target's Get*Semp1 function, less the documented omissions.
	matrix := map[string][]string{
		"Vpn":              vpnNames,
		"VpnStats":         concat(vpnStatsNames, vpnStatsQuotaNames),
		"VpnSpool":         vpnSpoolNames,
		"QueueDetails":     queueDetailsNames,
		"QueueStats":       queueStatsNames,
		"QueueRates":       queueRatesNames,
		"GlobalStats":      globalNames,
		"GlobalSystemInfo": concat(systemInfoNames, globalOptionalNames),
		"Spool":            spoolNames,
	}
	selections := [][]string{{"VpnStats", "QueueRates"}, {"GlobalStats", "GlobalSystemInfo"}}
	for target := range matrix {
		selections = append(selections, []string{target})
	}
	for _, selected := range selections {
		t.Run(strings.Join(selected, "+"), func(t *testing.T) {
			cfg := testConfig()
			cfg.Targets = selected
			mc := tickN(t, build(t, cfg), 2)
			want := []string{"solace_up"}
			for _, target := range selected {
				want = append(want, matrix[target]...)
			}
			sort.Strings(want)
			if got := mc.Names(); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("target inventory = %v, want %v", got, want)
			}
			// Exact health endpoint membership and one sample per broker per tick.
			counts := map[string]int{}
			for _, s := range mc.Find("solace_up") {
				if s.Value != 1 || s.Kind != promrw.KindGauge || s.Labels["error"] != "" {
					t.Errorf("invalid healthy scrape: %+v", s)
				}
				counts[s.Labels["instance"]+"/"+s.Labels["endpoint"]]++
			}
			for _, b := range cfg.Brokers {
				for _, target := range selected {
					key := b.Name + ":9628/" + target
					if counts[key] != 2 {
						t.Errorf("health count %s = %d, want 2", key, counts[key])
					}
					delete(counts, key)
				}
			}
			if len(counts) != 0 {
				t.Errorf("unselected health endpoints: %v", counts)
			}
		})
	}
}

func TestDefaultTargetsExcludeDeprecatedQueueRates(t *testing.T) {
	mc := tickN(t, build(t, testConfig()), 1)
	var endpoints []string
	for _, s := range mc.Find("solace_up") {
		if s.Labels["instance"] == "broker-a:9628" {
			endpoints = append(endpoints, s.Labels["endpoint"])
		}
	}
	sort.Strings(endpoints)
	if got := strings.Join(endpoints, ","); got != "GlobalStats,GlobalSystemInfo,QueueDetails,QueueStats,Spool,Vpn,VpnSpool,VpnStats" {
		t.Errorf("default health endpoints = %s", got)
	}
	for _, name := range queueRatesNames {
		if len(mc.Find(name)) != 0 {
			t.Errorf("deprecated QueueRates family %s emitted by default", name)
		}
	}
	for _, name := range concat(queueDetailsNames, queueStatsNames, vpnNames, vpnStatsNames, vpnSpoolNames, globalNames, systemInfoNames, globalOptionalNames, spoolNames) {
		if len(mc.Find(name)) == 0 {
			t.Errorf("default target family %s missing", name)
		}
	}
}

func TestQueueRatesReflectTraffic(t *testing.T) {
	cfg := &solace.Config{
		Targets: []string{"QueueRates"},
		Brokers: []solace.BrokerConfig{{
			Name: "broker-a",
			VPNs: []solace.VPNConfig{{Name: "vpn-1", Queues: []solace.QueueConfig{
				{Name: "queue-1", PublishMsgsPerSec: f64(2), ConsumeMsgsPerSec: f64(20), AvgMsgBytes: 500},
			}}},
		}},
	}
	mc := tickN(t, build(t, cfg), 3)
	q := map[string]string{"queue_name": "queue-1"}
	rx := last(t, mc, "solace_queue_rx_msg_rate", q).Value
	tx := last(t, mc, "solace_queue_tx_msg_rate", q).Value
	if rx <= 0 || rx > 3.5 {
		t.Errorf("rx msg rate = %v, want a positive rate near the declared 2/s", rx)
	}
	if diff := rx - tx; diff < -0.5 || diff > 0.5 {
		t.Errorf("a healthy queue's tx rate %v should track rx %v", tx, rx)
	}
	if got, want := last(t, mc, "solace_queue_rx_byte_rate", q).Value, rx*500; got < want*0.99 || got > want*1.01 {
		t.Errorf("rx byte rate %v, want about msg rate * 500 = %v", got, want)
	}
}

func TestAbsentDimensionsOmitted(t *testing.T) {
	cfg := &solace.Config{Brokers: []solace.BrokerConfig{{
		Name: "broker-b",
		VPNs: []solace.VPNConfig{{Name: "vpn-2", Queues: []solace.QueueConfig{{Name: "queue-3"}}}},
	}}}
	mc := tickN(t, build(t, cfg), 1)
	for _, name := range append(append([]string{}, globalOptionalNames...), vpnStatsQuotaNames...) {
		if len(mc.Find(name)) != 0 {
			t.Errorf("%s emitted though its declaration is absent", name)
		}
	}
	for _, s := range mc.All() {
		for k, v := range s.Labels {
			if v == "" || v == "NA" {
				t.Errorf("%s carries empty-valued label %s", s.Name, k)
			}
		}
	}
	// A healthy exporter reports solace_up without an error label.
	for _, s := range mc.Find("solace_up") {
		if _, ok := s.Labels["error"]; ok {
			t.Errorf("solace_up carries an error label on a healthy scrape: %v", s.Labels)
		}
	}
}

func TestDeclaredCapacityFieldsSurface(t *testing.T) {
	mc := tickN(t, build(t, testConfig()), 1)
	sys := map[string]string{"instance": "broker-a:9628"}
	if got := last(t, mc, "solace_system_cpu_cores", sys).Value; got != 4 {
		t.Errorf("cpu cores = %v, want 4", got)
	}
	if got := last(t, mc, "solace_system_memory_bytes", sys).Value; got != 16*1073741824 {
		t.Errorf("memory bytes = %v, want 16 GiB", got)
	}
	if got := last(t, mc, "solace_system_total_clients_quota", sys).Value; got != 1000 {
		t.Errorf("clients quota = %v, want 1000", got)
	}
}

func TestDeterministicAcrossInstances(t *testing.T) {
	a := tickN(t, build(t, testConfig()), 3).All()
	b := tickN(t, build(t, testConfig()), 3).All()
	if len(a) != len(b) {
		t.Fatalf("series counts differ: %d vs %d", len(a), len(b))
	}
	index := func(in []promrw.Series) map[string]float64 {
		m := map[string]float64{}
		for _, s := range in {
			m[s.Name+"|"+s.T.String()+"|"+s.Labels["instance"]+"|"+s.Labels["vpn_name"]+"|"+s.Labels["queue_name"]+"|"+s.Labels["endpoint"]] = s.Value
		}
		return m
	}
	ia, ib := index(a), index(b)
	for k, v := range ia {
		if ib[k] != v {
			t.Errorf("%s: %v vs %v", k, v, ib[k])
		}
	}
}

func TestNilMetricsWriterIsSafe(t *testing.T) {
	c := build(t, testConfig())
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Metrics: %v", err)
	}
}

// TestSpoolQuotaCapsDepthAndCountsDiscards: a stalled queue fills to its quota, then arrivals are
// discarded and counted rather than the depth growing past the quota.
func TestSpoolQuotaCapsDepthAndCountsDiscards(t *testing.T) {
	cfg := &solace.Config{Brokers: []solace.BrokerConfig{{
		Name: "broker-a",
		VPNs: []solace.VPNConfig{{Name: "vpn-1", Queues: []solace.QueueConfig{
			// 1 MB quota at 1000 bytes per message holds 1048 messages; 2/s fills it in about 9 ticks.
			{Name: "queue-1", PublishMsgsPerSec: f64(2), ConsumeMsgsPerSec: f64(0), AvgMsgBytes: 1000, SpoolQuotaMB: 1},
		}}},
	}}}
	mc := tickN(t, build(t, cfg), 20)
	q := map[string]string{"queue_name": "queue-1"}
	if got := last(t, mc, "solace_queue_spool_usage_bytes", q).Value; got > 1048576 {
		t.Errorf("spool usage %v exceeds the 1 MB quota", got)
	}
	if got := last(t, mc, "solace_queue_msg_spool_usage_exceeded", q).Value; got <= 0 {
		t.Errorf("no discards counted after the quota filled: %v", got)
	}
	v := map[string]string{"vpn_name": "vpn-1"}
	if got, want := last(t, mc, "solace_vpn_rx_discarded_msgs_total", v).Value, last(t, mc, "solace_queue_msg_spool_usage_exceeded", q).Value; got != want {
		t.Errorf("vpn rx discards %v != queue spool-exceeded %v", got, want)
	}
	if got := last(t, mc, "solace_vpn_spool_usage_pct", v).Value; got != 100 {
		t.Errorf("a full single-queue VPN spool reads %v%%, want 100", got)
	}
}

// TestExporterCounterTypedLevels: the exporter registers these two levels as Counters, so the
// series carry counter type metadata with the level as the value, and the value falls when the
// level falls (upstream semantics are not corrected).
func TestExporterCounterTypedLevels(t *testing.T) {
	cfg := &solace.Config{Brokers: []solace.BrokerConfig{{
		Name: "broker-a", MaxConnections: 500,
		VPNs: []solace.VPNConfig{{Name: "vpn-1", Queues: []solace.QueueConfig{
			{Name: "queue-1", PublishMsgsPerSec: f64(2), ConsumeMsgsPerSec: f64(1), AvgMsgBytes: 1000},
		}}},
	}}}
	c := build(t, cfg)
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	// Local noon, then 11 hours later: the long second window lets the slow consumers
	// drain what the first window left behind.
	for _, at := range []time.Time{testNow, testNow.Add(11 * time.Hour)} {
		if err := c.Tick(context.Background(), at, w); err != nil {
			t.Fatal(err)
		}
	}
	var bytes, msgs []float64
	for _, s := range mc.Find("solace_queue_spool_usage_bytes") {
		if s.Kind != promrw.KindCounter {
			t.Errorf("solace_queue_spool_usage_bytes kind = %v, want counter", s.Kind)
		}
		bytes = append(bytes, s.Value)
	}
	for _, s := range mc.Find("solace_queue_spool_usage_msgs") {
		msgs = append(msgs, s.Value)
	}
	if len(bytes) != 2 || len(msgs) != 2 {
		t.Fatalf("samples: bytes=%v msgs=%v", bytes, msgs)
	}
	if !(msgs[0] > 0 && msgs[1] < msgs[0]) {
		t.Fatalf("setup did not build a backlog that then drains: depth %v", msgs)
	}
	for i := range bytes {
		if bytes[i] != msgs[i]*1000 {
			t.Errorf("sample %d: counter-typed value %v is not the level %v", i, bytes[i], msgs[i]*1000)
		}
	}
	quota := last(t, mc, "solace_system_total_clients_quota", map[string]string{"instance": "broker-a:9628"})
	if quota.Kind != promrw.KindCounter || quota.Value != 500 {
		t.Errorf("clients quota = %v kind %v, want counter holding the level 500 after two ticks", quota.Value, quota.Kind)
	}
}

func TestBuildRejectsPerEnvironmentFanOut(t *testing.T) {
	fanned := &fixture.Set{Env: &fixture.Env{Name: "prod", Weight: 1}}
	if _, err := solace.Build(testConfig(), fanned); err == nil || !strings.Contains(err.Error(), "fan out") {
		t.Fatalf("fan-out instance accepted: %v", err)
	}
	// A non-fanned declaration carries no environment, with or without a fixture set.
	if _, err := solace.Build(testConfig(), &fixture.Set{Seed: "s"}); err != nil {
		t.Fatalf("non-fanned fixture set rejected: %v", err)
	}
	if _, err := solace.Build(testConfig(), nil); err != nil {
		t.Fatalf("nil fixture set rejected: %v", err)
	}
}

// A queue declared with consumers: 0 drains nothing unless a drain rate is declared.
func TestZeroConsumersStallsUnlessDrainDeclared(t *testing.T) {
	cfg := &solace.Config{Brokers: []solace.BrokerConfig{{
		Name: "broker-a",
		VPNs: []solace.VPNConfig{{Name: "vpn-1", Queues: []solace.QueueConfig{
			{Name: "no-consumers", PublishMsgsPerSec: f64(2), Consumers: i64(0)},
			{Name: "declared-drain", PublishMsgsPerSec: f64(2), Consumers: i64(0), ConsumeMsgsPerSec: f64(10)},
		}}},
	}}}
	mc := tickN(t, build(t, cfg), 3)
	if got := last(t, mc, "solace_queue_binds", map[string]string{"queue_name": "no-consumers"}).Value; got != 0 {
		t.Errorf("binds = %v, want 0", got)
	}
	if got := last(t, mc, "solace_queue_spool_usage_msgs", map[string]string{"queue_name": "no-consumers"}).Value; got <= 0 {
		t.Errorf("a queue with no consumers kept no backlog: %v", got)
	}
	if got := last(t, mc, "solace_queue_msg_total_deleted", map[string]string{"queue_name": "no-consumers"}).Value; got != 0 {
		t.Errorf("a queue with no consumers delivered %v messages", got)
	}
	if got := last(t, mc, "solace_queue_spool_usage_msgs", map[string]string{"queue_name": "declared-drain"}).Value; got != 0 {
		t.Errorf("an explicit drain rate was ignored: depth %v", got)
	}
}

func TestIdentityClaims(t *testing.T) {
	cfg := &solace.Config{Brokers: []solace.BrokerConfig{
		{Name: "broker-a"},
		{Name: "broker-b", Instance: "host-b:1"},
	}}
	got := cfg.IdentityClaims()
	if len(got) != 2 {
		t.Fatalf("claims = %v, want one per broker", got)
	}
	for _, c := range got {
		if c[0] != "solace broker" {
			t.Errorf("claim class = %q", c[0])
		}
	}
	if !strings.Contains(got[0][1], "broker-a:9628") || !strings.Contains(got[0][1], "solace-prometheus-exporter") {
		t.Errorf("default job/instance not in claim: %q", got[0][1])
	}
	if !strings.Contains(got[1][1], "host-b:1") || strings.Contains(got[1][1], "broker-b") {
		t.Errorf("instance override not the identity: %q", got[1][1])
	}

	// Job changes identity; the key cannot be made ambiguous by a separator inside job or instance.
	a := (&solace.Config{Job: "a b", Brokers: []solace.BrokerConfig{{Name: "x", Instance: "c"}}}).IdentityClaims()[0][1]
	b := (&solace.Config{Job: "a", Brokers: []solace.BrokerConfig{{Name: "x", Instance: "b c"}}}).IdentityClaims()[0][1]
	if a == b {
		t.Errorf("distinct (job, instance) pairs produced the same claim %q", a)
	}
	c := (&solace.Config{Job: "other", Brokers: []solace.BrokerConfig{{Name: "broker-a"}}}).IdentityClaims()[0][1]
	if c == got[0][1] {
		t.Error("a different job must be a different identity")
	}
}
