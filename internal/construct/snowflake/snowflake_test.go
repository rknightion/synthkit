// SPDX-License-Identifier: AGPL-3.0-only

package snowflake_test

// snowflake_test.go — contract tests for the snowflake construct.
//
// (a) All 27 metric names are present in the emitted series.
// (b) snowflake_up is present (no group labels beyond job+instance).
// (c) snowflake_database_bytes carries name+id labels.
// (d) snowflake_login_rate carries client_type+client_version labels.
// (e) snowflake_storage_bytes has no db/warehouse labels (account-level only).
// (f) Every emitted series is a plain gauge — no _bucket/_sum/_count suffixes.
// (g) Interface conformance: Kind / Signals / Interval.
// (h) Base labels: job="integrations/snowflake" + instance="<account>:9975" on every series.
// (i) No blueprint label (ScopeSubstrate — I21).
// (j) Nil Metrics writer is safe (no panic).
// (k) Default config (zero Config) applies generic defaults.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/snowflake"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// testNow is a fixed mid-business-hours time (noon UTC).
var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

// all27Names is the canonical list of all 27 snowflake metric names per signals/snowflake.md.
var all27Names = []string{
	// account-level storage (no group labels)
	"snowflake_storage_bytes",
	"snowflake_stage_bytes",
	"snowflake_failsafe_bytes",
	// database storage (name,id)
	"snowflake_database_bytes",
	"snowflake_database_failsafe_bytes",
	// credit usage (service_type,service)
	"snowflake_used_compute_credits",
	"snowflake_used_cloud_services_credits",
	// warehouse credits (name,id)
	"snowflake_warehouse_used_compute_credits",
	"snowflake_warehouse_used_cloud_service_credits",
	// login rates (client_type,client_version)
	"snowflake_login_rate",
	"snowflake_successful_login_rate",
	"snowflake_failed_login_rate",
	// warehouse query load (name,id)
	"snowflake_warehouse_executed_queries",
	"snowflake_warehouse_overloaded_queue_size",
	"snowflake_warehouse_provisioning_queue_size",
	"snowflake_warehouse_blocked_queries",
	// auto-clustering (6-label group)
	"snowflake_auto_clustering_credits",
	"snowflake_auto_clustering_bytes",
	"snowflake_auto_clustering_rows",
	// table storage (6-label group first four; deleted_tables no labels)
	"snowflake_table_active_bytes",
	"snowflake_table_time_travel_bytes",
	"snowflake_table_failsafe_bytes",
	"snowflake_table_clone_bytes",
	"snowflake_table_deleted_tables",
	// db replication (database_name,database_id)
	"snowflake_db_replication_used_credits",
	"snowflake_db_replication_transferred_bytes",
	// exporter health
	"snowflake_up",
}

// buildDefault builds a snowflake construct with zero config (all defaults).
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	cfg := &snowflake.Config{}
	c, err := snowflake.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// tickOnce calls Tick once and returns the MetricCapture.
func tickOnce(t *testing.T, c core.Construct) *coretest.MetricCapture {
	t.Helper()
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc
}

// seriesPresent returns true if any series with the exact name exists in mc.
func seriesPresent(mc *coretest.MetricCapture, name string) bool {
	return len(mc.Find(name)) > 0
}

// ── (g) Interface conformance ──────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "snowflake" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "snowflake")
	}
	sigs := c.Signals()
	if len(sigs) != 1 {
		t.Fatalf("Signals() len=%d want 1", len(sigs))
	}
	if sigs[0] != core.Metrics {
		t.Errorf("Signals()[0]=%v want Metrics", sigs[0])
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (a) All 27 metric names present ────────────────────────────────────────────

func TestAll27NamesPresent(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	// Collect distinct emitted names.
	emitted := map[string]bool{}
	for _, s := range mc.All() {
		emitted[s.Name] = true
	}

	if len(emitted) != 27 {
		t.Errorf("distinct metric names emitted = %d, want 27; got: %v", len(emitted), sortedKeys(emitted))
	}

	for _, name := range all27Names {
		if !emitted[name] {
			t.Errorf("missing metric name: %q", name)
		}
	}
}

// ── (b) snowflake_up present ────────────────────────────────────────────────────

func TestSnowflakeUpPresent(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	series := mc.Find("snowflake_up")
	if len(series) == 0 {
		t.Fatal("snowflake_up: not found")
	}
	// Value must be 1.
	for _, s := range series {
		if s.Value != 1.0 {
			t.Errorf("snowflake_up: value=%v want 1.0", s.Value)
		}
	}
}

// ── (c) snowflake_database_bytes carries name+id ───────────────────────────────

func TestDatabaseBytesHasNameAndID(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	series := mc.Find("snowflake_database_bytes")
	if len(series) == 0 {
		t.Fatal("snowflake_database_bytes: not found")
	}
	for _, s := range series {
		if name, ok := s.Labels["name"]; !ok || name == "" {
			t.Errorf("snowflake_database_bytes: name label missing or empty (labels=%v)", s.Labels)
		}
		if id, ok := s.Labels["id"]; !ok || id == "" {
			t.Errorf("snowflake_database_bytes: id label missing or empty (labels=%v)", s.Labels)
		}
	}
}

// ── (d) snowflake_login_rate carries client_type+client_version ────────────────

func TestLoginRateHasClientLabels(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	series := mc.Find("snowflake_login_rate")
	if len(series) == 0 {
		t.Fatal("snowflake_login_rate: not found")
	}
	for _, s := range series {
		if ct, ok := s.Labels["client_type"]; !ok || ct == "" {
			t.Errorf("snowflake_login_rate: client_type label missing or empty")
		}
		if cv, ok := s.Labels["client_version"]; !ok || cv == "" {
			t.Errorf("snowflake_login_rate: client_version label missing or empty")
		}
	}
}

// ── (e) snowflake_storage_bytes has no db/warehouse labels ─────────────────────

func TestStorageBytesIsAccountLevel(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	series := mc.Find("snowflake_storage_bytes")
	if len(series) == 0 {
		t.Fatal("snowflake_storage_bytes: not found")
	}
	for _, s := range series {
		for _, forbidden := range []string{"name", "id", "database_name", "database_id", "service_type", "service"} {
			if _, ok := s.Labels[forbidden]; ok {
				t.Errorf("snowflake_storage_bytes: unexpected label %q (account-level metric must have no group labels)", forbidden)
			}
		}
	}
}

// ── (f) No histogram suffixes — every series is a plain gauge ─────────────────

func TestNoHistogramSuffixes(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if strings.HasSuffix(s.Name, "_bucket") {
			t.Errorf("series %q: _bucket suffix present — all snowflake metrics must be plain gauges", s.Name)
		}
		if strings.HasSuffix(s.Name, "_sum") {
			t.Errorf("series %q: _sum suffix present — all snowflake metrics must be plain gauges", s.Name)
		}
		if strings.HasSuffix(s.Name, "_count") {
			t.Errorf("series %q: _count suffix present — all snowflake metrics must be plain gauges", s.Name)
		}
	}
}

// ── (h) Base labels on every series ────────────────────────────────────────────

func TestBaseLabelSetOnEveryMetric(t *testing.T) {
	cfg := &snowflake.Config{Account: "test-acct"}
	c, err := snowflake.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if s.Labels["job"] != "integrations/snowflake" {
			t.Errorf("series %q: job=%q want %q", s.Name, s.Labels["job"], "integrations/snowflake")
		}
		if s.Labels["instance"] != "test-acct:9975" {
			t.Errorf("series %q: instance=%q want %q", s.Name, s.Labels["instance"], "test-acct:9975")
		}
	}
}

// ── (i) No blueprint label (ScopeSubstrate — I21) ─────────────────────────────

func TestNoBlueprintLabel(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries blueprint label — snowflake is ScopeSubstrate (I21)", s.Name)
		}
	}
}

// ── (j) Nil Metrics writer is safe ────────────────────────────────────────────

func TestNilMetricsWriterSafe(t *testing.T) {
	c := buildDefault(t)
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Metrics: %v", err)
	}
}

// ── (k) Default config applies generic defaults ────────────────────────────────

func TestDefaultConfigAppliesDefaults(t *testing.T) {
	cfg := &snowflake.Config{} // zero value
	c, err := snowflake.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build with zero Config: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// instance must be "demo-acct:9975" (default account)
	series := mc.Find("snowflake_up")
	if len(series) == 0 {
		t.Fatal("snowflake_up not found with default config")
	}
	if series[0].Labels["instance"] != "demo-acct:9975" {
		t.Errorf("default account: instance=%q want %q", series[0].Labels["instance"], "demo-acct:9975")
	}

	// Warehouse metrics must exist (defaults: wh_compute, wh_etl)
	whSeries := mc.Find("snowflake_warehouse_executed_queries")
	if len(whSeries) == 0 {
		t.Fatal("snowflake_warehouse_executed_queries not found with default config")
	}
	whNames := map[string]bool{}
	for _, s := range whSeries {
		whNames[s.Labels["name"]] = true
	}
	for _, want := range []string{"wh_compute", "wh_etl"} {
		if !whNames[want] {
			t.Errorf("default warehouses: warehouse name=%q not found in warehouse_executed_queries", want)
		}
	}

	// Database metrics must exist (defaults: analytics, raw)
	dbSeries := mc.Find("snowflake_database_bytes")
	if len(dbSeries) == 0 {
		t.Fatal("snowflake_database_bytes not found with default config")
	}
	dbNames := map[string]bool{}
	for _, s := range dbSeries {
		dbNames[s.Labels["name"]] = true
	}
	for _, want := range []string{"analytics", "raw"} {
		if !dbNames[want] {
			t.Errorf("default databases: database name=%q not found in database_bytes", want)
		}
	}
}

// ── Label group spot-checks ────────────────────────────────────────────────────

// TestWarehouseCreditsHasNameAndID verifies warehouse credit metrics carry name+id.
func TestWarehouseCreditsHasNameAndID(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_warehouse_used_compute_credits",
		"snowflake_warehouse_used_cloud_service_credits",
	} {
		series := mc.Find(name)
		if len(series) == 0 {
			t.Fatalf("%q: not found", name)
		}
		for _, s := range series {
			if n, ok := s.Labels["name"]; !ok || n == "" {
				t.Errorf("%q: name label missing or empty", name)
			}
			if id, ok := s.Labels["id"]; !ok || id == "" {
				t.Errorf("%q: id label missing or empty", name)
			}
		}
	}
}

// TestCreditUsageHasServiceLabels verifies credit usage metrics carry service_type+service.
func TestCreditUsageHasServiceLabels(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_used_compute_credits",
		"snowflake_used_cloud_services_credits",
	} {
		series := mc.Find(name)
		if len(series) == 0 {
			t.Fatalf("%q: not found", name)
		}
		for _, s := range series {
			if st, ok := s.Labels["service_type"]; !ok || st == "" {
				t.Errorf("%q: service_type label missing or empty", name)
			}
			if svc, ok := s.Labels["service"]; !ok || svc == "" {
				t.Errorf("%q: service label missing or empty", name)
			}
		}
	}
}

// TestAutoClusteringHas6LabelGroup verifies auto-clustering metrics carry the full 6-label group.
func TestAutoClusteringHas6LabelGroup(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_auto_clustering_credits",
		"snowflake_auto_clustering_bytes",
		"snowflake_auto_clustering_rows",
	} {
		series := mc.Find(name)
		if len(series) == 0 {
			t.Fatalf("%q: not found", name)
		}
		for _, s := range series {
			for _, lbl := range []string{"table_name", "table_id", "schema_name", "schema_id", "database_name", "database_id"} {
				if v, ok := s.Labels[lbl]; !ok || v == "" {
					t.Errorf("%q: label %q missing or empty", name, lbl)
				}
			}
		}
	}
}

// TestTableStorageHas6LabelGroup verifies the 4 per-table storage metrics carry the 6-label group.
func TestTableStorageHas6LabelGroup(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_table_active_bytes",
		"snowflake_table_time_travel_bytes",
		"snowflake_table_failsafe_bytes",
		"snowflake_table_clone_bytes",
	} {
		series := mc.Find(name)
		if len(series) == 0 {
			t.Fatalf("%q: not found", name)
		}
		for _, s := range series {
			for _, lbl := range []string{"table_name", "table_id", "schema_name", "schema_id", "database_name", "database_id"} {
				if v, ok := s.Labels[lbl]; !ok || v == "" {
					t.Errorf("%q: label %q missing or empty", name, lbl)
				}
			}
		}
	}
}

// TestDeletedTablesHasNoGroupLabels verifies snowflake_table_deleted_tables carries no group labels.
func TestDeletedTablesHasNoGroupLabels(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	series := mc.Find("snowflake_table_deleted_tables")
	if len(series) == 0 {
		t.Fatal("snowflake_table_deleted_tables: not found")
	}
	groupLabels := []string{"name", "id", "table_name", "table_id", "schema_name", "schema_id",
		"database_name", "database_id", "service_type", "service", "client_type", "client_version"}
	for _, s := range series {
		for _, lbl := range groupLabels {
			if _, ok := s.Labels[lbl]; ok {
				t.Errorf("snowflake_table_deleted_tables: unexpected group label %q (I13: absent labels must be omitted)", lbl)
			}
		}
	}
}

// TestDBReplicationHasDatabaseLabels verifies db replication metrics carry database_name+database_id.
func TestDBReplicationHasDatabaseLabels(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_db_replication_used_credits",
		"snowflake_db_replication_transferred_bytes",
	} {
		series := mc.Find(name)
		if len(series) == 0 {
			t.Fatalf("%q: not found", name)
		}
		for _, s := range series {
			if dn, ok := s.Labels["database_name"]; !ok || dn == "" {
				t.Errorf("%q: database_name label missing or empty", name)
			}
			if did, ok := s.Labels["database_id"]; !ok || did == "" {
				t.Errorf("%q: database_id label missing or empty", name)
			}
		}
	}
}

// TestCustomConfig verifies a non-default config is honoured.
func TestCustomConfig(t *testing.T) {
	cfg := &snowflake.Config{
		Account:    "myco-prod",
		Warehouses: []string{"wh_reporting"},
		Databases:  []string{"prod_db"},
	}
	c, err := snowflake.Build(cfg, &fixture.Set{Seed: "custom"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	// Instance label must reflect custom account.
	up := mc.Find("snowflake_up")
	if len(up) == 0 {
		t.Fatal("snowflake_up not found")
	}
	if up[0].Labels["instance"] != "myco-prod:9975" {
		t.Errorf("instance=%q want %q", up[0].Labels["instance"], "myco-prod:9975")
	}

	// Custom warehouse must appear.
	whSeries := mc.Find("snowflake_warehouse_executed_queries")
	found := false
	for _, s := range whSeries {
		if s.Labels["name"] == "wh_reporting" {
			found = true
		}
	}
	if !found {
		t.Error("custom warehouse wh_reporting not found in warehouse_executed_queries")
	}

	// Custom database must appear.
	dbSeries := mc.Find("snowflake_database_bytes")
	found = false
	for _, s := range dbSeries {
		if s.Labels["name"] == "prod_db" {
			found = true
		}
	}
	if !found {
		t.Error("custom database prod_db not found in database_bytes")
	}
}

// ── Per-series realism: peer series must not be lockstep ──────────────────────

// TestPeerWarehousesCreditsDiffer asserts that two different warehouses emit DISTINCT
// values for warehouse credit metrics — not the same byte-identical number.
func TestPeerWarehousesCreditsDiffer(t *testing.T) {
	cfg := &snowflake.Config{
		Warehouses: []string{"wh_compute", "wh_etl", "wh_reporting"},
	}
	c, err := snowflake.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_warehouse_used_compute_credits",
		"snowflake_warehouse_used_cloud_service_credits",
		"snowflake_warehouse_executed_queries",
		"snowflake_warehouse_overloaded_queue_size",
	} {
		series := mc.Find(name)
		if len(series) < 3 {
			t.Fatalf("%s: expected ≥3 series (one per warehouse), got %d", name, len(series))
		}
		seen := map[float64]string{}
		for _, s := range series {
			wh := s.Labels["name"]
			if prev, ok := seen[s.Value]; ok {
				t.Errorf("%s: warehouses %q and %q emit identical value %.6f (lockstep)", name, prev, wh, s.Value)
			}
			seen[s.Value] = wh
		}
	}
}

// TestPeerDatabaseStorageDiffers asserts that two different databases emit DISTINCT
// values for database storage metrics.
func TestPeerDatabaseStorageDiffers(t *testing.T) {
	cfg := &snowflake.Config{
		Databases: []string{"analytics", "raw", "staging"},
	}
	c, err := snowflake.Build(cfg, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, name := range []string{
		"snowflake_database_bytes",
		"snowflake_database_failsafe_bytes",
		"snowflake_db_replication_used_credits",
		"snowflake_db_replication_transferred_bytes",
	} {
		series := mc.Find(name)
		if len(series) < 3 {
			t.Fatalf("%s: expected ≥3 series (one per db), got %d", name, len(series))
		}
		seen := map[float64]string{}
		for _, s := range series {
			db := s.Labels["name"]
			if db == "" {
				db = s.Labels["database_name"]
			}
			if prev, ok := seen[s.Value]; ok {
				t.Errorf("%s: databases %q and %q emit identical value %.6f (lockstep)", name, prev, db, s.Value)
			}
			seen[s.Value] = db
		}
	}
}

// TestWarehouseMetricsDriftOverTime asserts a warehouse's executed_queries metric
// takes ≥5 distinct values across 30 ticks at 13-minute steps (Wander is active).
func TestWarehouseMetricsDriftOverTime(t *testing.T) {
	c := buildDefault(t)
	const name = "snowflake_warehouse_executed_queries"
	seen := map[float64]bool{}
	base := testNow
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		if err := c.Tick(context.Background(), base.Add(time.Duration(i)*13*time.Minute), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		// Pin a FIXED warehouse series by label (not s[0]) — state.Collect is map-ordered, so
		// indexing [0] could jump between warehouses and mask whether a single series drifts.
		var found bool
		for _, ser := range mc.Find(name) {
			if ser.Labels["name"] == "wh_compute" {
				seen[ser.Value] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s: no wh_compute series at tick %d", name, i)
		}
	}
	if len(seen) < 5 {
		t.Errorf("%s: only %d distinct values across 30 ticks — series is near-frozen", name, len(seen))
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// simple insertion sort — small N
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// silence unused import warning (strings is used in TestNoHistogramSuffixes)
var _ = strings.HasSuffix
