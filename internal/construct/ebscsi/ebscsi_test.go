// SPDX-License-Identifier: AGPL-3.0-only

package ebscsi_test

// ebscsi_test.go — construct invariant tests for the ebs_csi construct.
//
// Test inventory:
//   (a) Exact series name inventory — all expected names present, no extras.
//   (b) cloudprovider_aws_* is ABSENT (deprecated family must not be emitted).
//   (c) Counters are monotone: two successive ticks produce non-decreasing values
//       (state.Add semantics — ARCHITECTURE I3).
//   (d) Build returns an error when Cluster fixture is nil.
//   (e) Kind / Signals / Interval metadata.
//   (f) Scope is ScopeSubstrate (no blueprint label).

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/ebscsi"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ── helpers ───────────────────────────────────────────────────────────────────

const testSeed = "test"

func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	c, err := ebscsi.New(&ebscsi.Config{}, &fixture.Set{
		Seed:    testSeed,
		Cluster: coretest.Cluster(),
	})
	if err != nil {
		t.Fatalf("ebscsi.New: %v", err)
	}
	return c
}

func tickOnce(t *testing.T, c core.Construct) ([]promrw.Series, []string) {
	t.Helper()
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) // business hours
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap.All(), cap.Names()
}

// ── (a) Exact series inventory ────────────────────────────────────────────────

// expectedNames is the canonical series name inventory for the controller metrics.
// Per-request series (request dimension) deduplicate to one name each.
var expectedNames = func() []string {
	names := []string{
		// Controller API request duration (per request label, deduped to single name)
		"aws_ebs_csi_api_request_duration_seconds_bucket",
		"aws_ebs_csi_api_request_duration_seconds_sum",
		"aws_ebs_csi_api_request_duration_seconds_count",
		// EC2 collector duration (controller-level, no per-volume label)
		"aws_ebs_csi_ec2_collector_duration_seconds_bucket",
		"aws_ebs_csi_ec2_collector_duration_seconds_sum",
		"aws_ebs_csi_ec2_collector_duration_seconds_count",
		// EC2 collector scrapes (counter, controller-level)
		"aws_ebs_csi_ec2_collector_scrapes_total",
	}
	sort.Strings(names)
	return names
}()

func TestSeriesInventory(t *testing.T) {
	c := buildDefault(t)
	_, got := tickOnce(t, c)

	want := map[string]bool{}
	for _, n := range expectedNames {
		want[n] = true
	}
	got_ := map[string]bool{}
	for _, n := range got {
		got_[n] = true
	}

	for n := range want {
		if !got_[n] {
			t.Errorf("MISSING series: %s", n)
		}
	}
	for n := range got_ {
		if !want[n] {
			t.Errorf("UNEXPECTED series: %s", n)
		}
	}
}

// ── (b) cloudprovider_aws_* absent ────────────────────────────────────────────

// TestCloudProviderAwsAbsent asserts that the deprecated cloudprovider_aws_* metric
// family is never emitted (signals/k8s-addons.md [slug: k8s-ebs-csi] traps).
func TestCloudProviderAwsAbsent(t *testing.T) {
	c := buildDefault(t)
	all, _ := tickOnce(t, c)

	for _, s := range all {
		if len(s.Name) >= 18 && s.Name[:18] == "cloudprovider_aws_" {
			t.Errorf("deprecated cloudprovider_aws_* series must not be emitted: %s", s.Name)
		}
	}
}

// ── (c) Counters are monotone across ticks ────────────────────────────────────

// TestCountersMonotone verifies that cumulative counter series never decrease across
// successive ticks (state.Add semantics — ARCHITECTURE I3).
// Uses aws_ebs_csi_ec2_collector_scrapes_total (controller-level counter).
func TestCountersMonotone(t *testing.T) {
	c := buildDefault(t)

	cap1 := &coretest.MetricCapture{}
	w1 := coretest.World(cap1, nil, nil)
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	cap2 := &coretest.MetricCapture{}
	w2 := coretest.World(cap2, nil, nil)
	now2 := now.Add(60 * time.Second)
	if err := c.Tick(context.Background(), now2, w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// aws_ebs_csi_ec2_collector_scrapes_total is a controller-level counter (no label key).
	// It should be non-decreasing between ticks.
	var v1, v2 float64
	found1, found2 := false, false
	for _, s := range cap1.All() {
		if s.Name == "aws_ebs_csi_ec2_collector_scrapes_total" {
			v1 = s.Value
			found1 = true
			break
		}
	}
	for _, s := range cap2.All() {
		if s.Name == "aws_ebs_csi_ec2_collector_scrapes_total" {
			v2 = s.Value
			found2 = true
			break
		}
	}

	if !found1 {
		t.Fatal("aws_ebs_csi_ec2_collector_scrapes_total absent in tick 1")
	}
	if !found2 {
		t.Fatal("aws_ebs_csi_ec2_collector_scrapes_total absent in tick 2")
	}
	if v2 < v1 {
		t.Errorf("ec2_collector_scrapes_total decreased: tick1=%.0f tick2=%.0f (must be monotone)", v1, v2)
	}
}

// ── (d) Build error on nil Cluster ────────────────────────────────────────────

func TestBuildErrorOnNilCluster(t *testing.T) {
	_, err := ebscsi.New(&ebscsi.Config{}, &fixture.Set{
		Seed:    testSeed,
		Cluster: nil,
	})
	if err == nil {
		t.Fatal("expected error when Cluster is nil, got nil")
	}
}

// ── (e) Kind / Signals / Interval metadata ───────────────────────────────────

func TestMetadata(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "ebs_csi" {
		t.Errorf("Kind() = %q, want %q", c.Kind(), "ebs_csi")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals() = %v, want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// ── (f) Scope is ScopeSubstrate (no blueprint label) ─────────────────────────

func TestRegistrationScope(t *testing.T) {
	reg := ebscsi.Registration()
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration().Scope = %v, want ScopeSubstrate", reg.Scope)
	}
	if reg.Kind != "ebs_csi" {
		t.Errorf("Registration().Kind = %q, want %q", reg.Kind, "ebs_csi")
	}
}
