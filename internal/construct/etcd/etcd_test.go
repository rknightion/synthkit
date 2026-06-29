// SPDX-License-Identifier: AGPL-3.0-only

package etcd_test

// etcd_test.go — construct invariant tests for the etcd construct.
//
// Test inventory:
//   (a) Key series present: etcd_server_has_leader, etcd_mvcc_db_total_size_in_bytes,
//       grpc_server_handled_total.
//   (b) etcd_server_has_leader: value=1, job="integrations/etcd".
//   (c) cluster + k8s_cluster_name on every series; NO blueprint label.
//   (d) instance label looks like <ip>:2381.
//   (e) Counters are monotone across two ticks.
//   (f) Build error on nil Cluster.
//   (g) Kind/Signals/Interval metadata.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/etcd"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := etcd.New(&etcd.Config{}, &fixture.Set{
		Seed:    "test",
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("etcd.New: %v", err)
	}
	return c
}

var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// ─── (a) Key series present ───────────────────────────────────────────────────

func TestKeySeriesPresent(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	required := []string{
		"etcd_server_has_leader",
		"etcd_mvcc_db_total_size_in_bytes",
		"grpc_server_handled_total",
	}

	names := map[string]bool{}
	for _, n := range cap.Names() {
		names[n] = true
	}
	for _, want := range required {
		if !names[want] {
			t.Errorf("MISSING series: %s", want)
		}
	}
}

// ─── (b) etcd_server_has_leader: value=1, job="integrations/etcd" ────────────

func TestServerHasLeaderValue(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	series := cap.Find("etcd_server_has_leader")
	if len(series) == 0 {
		t.Fatal("etcd_server_has_leader: no series found")
	}
	for _, s := range series {
		if s.Value != 1.0 {
			t.Errorf("etcd_server_has_leader: value=%v, want 1.0 (instance=%s)", s.Value, s.Labels["instance"])
		}
		if s.Labels["job"] != "integrations/etcd" {
			t.Errorf("etcd_server_has_leader: job=%q, want integrations/etcd", s.Labels["job"])
		}
	}
}

// ─── (c) cluster + k8s_cluster_name on every series; NO blueprint label ───────

func TestBaseLabels(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	clust := coretest.Cluster()

	for _, s := range cap.All() {
		if s.Labels["cluster"] != clust.Name {
			t.Errorf("series %q: cluster=%q want %q", s.Name, s.Labels["cluster"], clust.Name)
		}
		if s.Labels["k8s_cluster_name"] != clust.Name {
			t.Errorf("series %q: k8s_cluster_name=%q want %q", s.Name, s.Labels["k8s_cluster_name"], clust.Name)
		}
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q must NOT carry blueprint label (ScopeSubstrate)", s.Name)
		}
	}
}

// ─── (d) instance label looks like <ip>:2381 ─────────────────────────────────

func TestInstanceLabel(t *testing.T) {
	c := buildDefault(t)
	cap := tickOnce(t, c)

	series := cap.Find("etcd_server_has_leader")
	if len(series) == 0 {
		t.Fatal("no etcd_server_has_leader series to check instance label")
	}

	for _, s := range series {
		inst := s.Labels["instance"]
		if inst == "" {
			t.Errorf("series etcd_server_has_leader: missing instance label")
			continue
		}
		if !strings.HasSuffix(inst, ":2381") {
			t.Errorf("instance=%q does not end with :2381", inst)
		}
		// Should be <ip>:2381 — ip part should have dots
		ip := strings.TrimSuffix(inst, ":2381")
		if !strings.Contains(ip, ".") {
			t.Errorf("instance=%q — ip part %q does not look like an IPv4 address", inst, ip)
		}
	}
}

// ─── (e) Counters are monotone across two ticks ───────────────────────────────

func TestCountersMonotone(t *testing.T) {
	c := buildDefault(t)

	cap1 := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), testNow, coretest.World(cap1, nil, nil)); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	cap2 := &coretest.MetricCapture{}
	t2 := testNow.Add(60 * time.Second)
	if err := c.Tick(context.Background(), t2, coretest.World(cap2, nil, nil)); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	vals1 := seriesVals(cap1)
	vals2 := seriesVals(cap2)

	counters := []string{
		"etcd_server_leader_changes_seen_total",
		"etcd_network_client_grpc_received_bytes_total",
		"etcd_network_client_grpc_sent_bytes_total",
		"grpc_server_handled_total",
		"grpc_server_started_total",
	}
	for _, name := range counters {
		for _, s := range cap2.Find(name) {
			sig := labelSig(s.Labels)
			v1 := vals1[name+"\x00"+sig]
			v2 := vals2[name+"\x00"+sig]
			if v2 < v1 {
				t.Errorf("counter %q decreased: tick1=%.2f tick2=%.2f (not monotone)", name, v1, v2)
			}
		}
	}
}

// ─── (f) Build error on nil Cluster ──────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := etcd.New(&etcd.Config{}, &fixture.Set{Seed: "test"})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ─── (g) Kind/Signals/Interval metadata ──────────────────────────────────────

func TestKindAndSignals(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "etcd" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "etcd")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func seriesVals(cap *coretest.MetricCapture) map[string]float64 {
	m := map[string]float64{}
	for _, s := range cap.All() {
		m[s.Name+"\x00"+labelSig(s.Labels)] = s.Value
	}
	return m
}

func labelSig(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb []byte
	for _, k := range keys {
		sb = append(sb, []byte(k+"="+labels[k]+";")...)
	}
	return string(sb)
}
