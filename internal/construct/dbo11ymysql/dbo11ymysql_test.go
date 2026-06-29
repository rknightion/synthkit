// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ymysql_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/dbo11ymysql"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func buildMySQL(t *testing.T) (core.Construct, *coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick failed: %v", err)
	}
	return c, mc, lc
}

func findStream(lc *coretest.LogCapture, op string) []string {
	var out []string
	for _, s := range lc.Streams {
		if s.Labels["op"] == op {
			for _, l := range s.Lines {
				out = append(out, l.Body)
			}
		}
	}
	return out
}

func parseLogfmtField(line, key string) string {
	// Simple logfmt parser for testing: find key= and return unquoted or quoted value.
	prefix := key + "="
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(prefix):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		// quoted
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return ""
		}
		return rest[1 : end+1]
	}
	// unquoted — ends at next space or end
	end := strings.IndexByte(rest, ' ')
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// ── Build validation ──────────────────────────────────────────────────────────

func TestBuildRejectsPostgres(t *testing.T) {
	pgDB := coretest.DB("postgres")
	_, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: pgDB})
	if err == nil {
		t.Fatal("expected error for postgres engine, got nil")
	}
	if !strings.Contains(err.Error(), "mysql") {
		t.Errorf("error should mention mysql, got: %v", err)
	}
}

func TestBuildRejectsNilDB(t *testing.T) {
	_, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{})
	if err == nil {
		t.Fatal("expected error for nil DB, got nil")
	}
}

// ── Metric inventory and connection_info ─────────────────────────────────────

// TestConnectionInfoSixLabels asserts that database_observability_connection_info
// carries ALL SIX metric-specific labels (§3.1.1 / T1).
func TestConnectionInfoSixLabels(t *testing.T) {
	_, mc, _ := buildMySQL(t)

	series := mc.Find("database_observability_connection_info")
	if len(series) == 0 {
		t.Fatal("database_observability_connection_info not emitted")
	}

	required := []string{
		"provider_name", "provider_region", "provider_account",
		"db_instance_identifier", "engine", "engine_version",
	}
	for _, k := range required {
		found := false
		for _, s := range series {
			if _, ok := s.Labels[k]; ok {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("database_observability_connection_info missing label %q", k)
		}
	}

	// engine must be "mysql"
	for _, s := range series {
		if s.Labels["engine"] != "mysql" {
			t.Errorf("expected engine=mysql, got %q", s.Labels["engine"])
		}
	}
}

// TestConnectionInfoTargetLabels asserts job, instance, server_id present (§2.1).
func TestConnectionInfoTargetLabels(t *testing.T) {
	_, mc, _ := buildMySQL(t)
	series := mc.Find("database_observability_connection_info")
	if len(series) == 0 {
		t.Fatal("database_observability_connection_info not emitted")
	}
	for _, tgt := range []string{"job", "instance", "server_id"} {
		if _, ok := series[0].Labels[tgt]; !ok {
			t.Errorf("connection_info missing target label %q", tgt)
		}
	}
	if series[0].Labels["job"] != "integrations/db-o11y" {
		t.Errorf("expected job=integrations/db-o11y, got %q", series[0].Labels["job"])
	}
}

// TestMetricInventory checks that the expected metric families are present.
func TestMetricInventory(t *testing.T) {
	_, mc, _ := buildMySQL(t)
	names := mc.Names()
	want := []string{
		"database_observability_connection_info",
		"database_observability_setup_consumers_enabled",
		"database_observability_wait_event_seconds_total",
		"mysql_global_status_bytes_received",
		"mysql_global_status_bytes_sent",
		"mysql_global_status_innodb_buffer_pool_read_requests",
		"mysql_global_status_innodb_buffer_pool_reads",
		"mysql_global_status_questions",
		"mysql_global_status_slow_queries",
		"mysql_global_status_threads_connected",
		"mysql_global_status_threads_running",
		"mysql_global_status_uptime",
		"mysql_global_variables_max_connections",
		"mysql_global_variables_max_digest_length",
		"mysql_global_variables_performance_schema",
		"mysql_global_variables_performance_schema_max_digest_length",
		"mysql_global_variables_performance_schema_max_sql_text_length",
		"mysql_perf_schema_events_statements_errors_total",
		"mysql_perf_schema_events_statements_lock_time_seconds_total",
		"mysql_perf_schema_events_statements_rows_examined_total",
		"mysql_perf_schema_events_statements_rows_sent_total",
		"mysql_perf_schema_events_statements_seconds_total",
		"mysql_perf_schema_events_statements_total",
		// SK-13: source/replica naming (not old master/slave)
		"mysql_slave_status_seconds_behind_source",
		"mysql_slave_status_replica_io_running",
		"mysql_slave_status_replica_sql_running",
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	for _, w := range want {
		if !nameSet[w] {
			t.Errorf("expected metric %q not found; got: %v", w, names)
		}
	}
	// Old slave/master names must NOT appear (SK-13).
	for _, bad := range []string{
		"mysql_slave_status_seconds_behind_master",
		"mysql_slave_status_slave_io_running",
		"mysql_slave_status_slave_sql_running",
	} {
		if nameSet[bad] {
			t.Errorf("old metric name %q must NOT be emitted (SK-13)", bad)
		}
	}
}

// ── Perf statements ───────────────────────────────────────────────────────────

// TestPerfStatementsDigestMatchesFixture asserts the digest label equals the fixture
// Query.ID (cross-signal join rule T5).
func TestPerfStatementsDigestMatchesFixture(t *testing.T) {
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, &coretest.LogCapture{}, nil)
	now := time.Now()
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	stmtSeries := mc.Find("mysql_perf_schema_events_statements_total")
	if len(stmtSeries) == 0 {
		t.Fatal("mysql_perf_schema_events_statements_total not emitted")
	}

	// Build a set of expected digests from the fixture.
	expected := map[string]bool{}
	for _, q := range db.Queries {
		expected[q.ID] = true
	}
	// Every emitted series must have a digest in the fixture.
	for _, s := range stmtSeries {
		digest := s.Labels["digest"]
		if digest == "" {
			t.Error("perf_statements series missing digest label")
		} else if !expected[digest] {
			t.Errorf("digest %q not in fixture query IDs", digest)
		}
	}
	// And every fixture ID should appear.
	found := map[string]bool{}
	for _, s := range stmtSeries {
		found[s.Labels["digest"]] = true
	}
	for _, q := range db.Queries {
		if !found[q.ID] {
			t.Errorf("fixture query ID %q not emitted as digest", q.ID)
		}
	}
}

// TestPerfStatementsNoDigestText asserts digest_text is ABSENT from perf_statements
// metric labels (§3.1.5 T2 — it lives only in query_association log lines).
func TestPerfStatementsNoDigestText(t *testing.T) {
	_, mc, _ := buildMySQL(t)
	for _, name := range []string{
		"mysql_perf_schema_events_statements_total",
		"mysql_perf_schema_events_statements_seconds_total",
		"mysql_perf_schema_events_statements_rows_sent_total",
		"mysql_perf_schema_events_statements_rows_examined_total",
		"mysql_perf_schema_events_statements_errors_total",
		"mysql_perf_schema_events_statements_lock_time_seconds_total",
	} {
		for _, s := range mc.Find(name) {
			if _, ok := s.Labels["digest_text"]; ok {
				t.Errorf("%s must NOT carry digest_text label (T2)", name)
			}
		}
	}
}

// ── Wait event metric (MySQL-only) ────────────────────────────────────────────

// TestWaitEventSecondsTotalPresent asserts the MySQL-only metric is emitted (§3.1.6 T3).
func TestWaitEventSecondsTotalPresent(t *testing.T) {
	_, mc, _ := buildMySQL(t)
	series := mc.Find("database_observability_wait_event_seconds_total")
	if len(series) == 0 {
		t.Fatal("database_observability_wait_event_seconds_total not found (MySQL-only metric, T3)")
	}
}

// TestWaitEventSecondsTotalOnlySlowQueries asserts it is emitted only for slow queries.
func TestWaitEventSecondsTotalOnlySlowQueries(t *testing.T) {
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, &coretest.LogCapture{}, nil)
	if err := c.Tick(context.Background(), time.Now(), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	slowIDs := map[string]bool{}
	for _, q := range db.Queries {
		if q.Slow {
			slowIDs[q.ID] = true
		}
	}
	for _, s := range mc.Find("database_observability_wait_event_seconds_total") {
		if !slowIDs[s.Labels["digest"]] {
			t.Errorf("wait_event_seconds_total emitted for non-slow query digest %q", s.Labels["digest"])
		}
	}
}

// ── Log ops ───────────────────────────────────────────────────────────────────

// TestLogOpsPresent checks all expected op values appear in streams.
func TestLogOpsPresent(t *testing.T) {
	_, _, lc := buildMySQL(t)
	want := []string{
		"query_sample", "wait_event", "wait_event_v2",
		"query_association", "query_parsed_table_name",
		"table_detection", "create_statement",
		"explain_plan_output", "health_status",
	}
	ops := map[string]bool{}
	for _, s := range lc.Streams {
		ops[s.Labels["op"]] = true
	}
	for _, op := range want {
		if !ops[op] {
			t.Errorf("expected log op %q not found in streams", op)
		}
	}
}

// TestStreamLabels asserts every stream carries the four required labels (§2.2).
func TestStreamLabels(t *testing.T) {
	_, _, lc := buildMySQL(t)
	for _, s := range lc.Streams {
		for _, k := range []string{"job", "instance", "server_id", "op"} {
			if s.Labels[k] == "" {
				t.Errorf("stream op=%q missing label %q", s.Labels["op"], k)
			}
		}
		if s.Labels["job"] != "integrations/db-o11y" {
			t.Errorf("stream op=%q job=%q want integrations/db-o11y", s.Labels["op"], s.Labels["job"])
		}
	}
}

// TestQuerySampleTimestampInPast asserts line timestamps are in the past relative to
// tick time (T7 / M8 timestamp rule).
func TestQuerySampleTimestampInPast(t *testing.T) {
	_, _, lc := buildMySQL(t)
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	for _, s := range lc.Streams {
		op := s.Labels["op"]
		if op != "query_sample" && op != "wait_event" && op != "wait_event_v2" {
			continue
		}
		for _, l := range s.Lines {
			if !l.T.Before(now) {
				t.Errorf("op=%q line T=%v should be before tick time %v", op, l.T, now)
			}
		}
	}
}

// TestElapsedTimeMsIdentical asserts elapsed_time and elapsed_time_ms carry the same
// string value in query_sample lines (§3.2.1 T13).
func TestElapsedTimeMsIdentical(t *testing.T) {
	_, _, lc := buildMySQL(t)
	for _, line := range findStream(lc, "query_sample") {
		et := parseLogfmtField(line, "elapsed_time")
		etms := parseLogfmtField(line, "elapsed_time_ms")
		if et == "" {
			t.Error("query_sample line missing elapsed_time")
		}
		if etms == "" {
			t.Error("query_sample line missing elapsed_time_ms")
		}
		if et != etms {
			t.Errorf("elapsed_time=%q != elapsed_time_ms=%q (T13)", et, etms)
		}
	}
}

// TestQuerySampleDigestMatchesFixture asserts digest= in query_sample log lines equals
// a fixture query ID (cross-signal join T5).
func TestQuerySampleDigestMatchesFixture(t *testing.T) {
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lc := &coretest.LogCapture{}
	w := coretest.World(&coretest.MetricCapture{}, lc, nil)
	if err := c.Tick(context.Background(), time.Now(), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	ids := map[string]bool{}
	for _, q := range db.Queries {
		ids[q.ID] = true
	}
	for _, line := range findStream(lc, "query_sample") {
		d := parseLogfmtField(line, "digest")
		if d == "" {
			t.Error("query_sample line missing digest field")
		} else if !ids[d] {
			t.Errorf("query_sample digest %q not in fixture query IDs", d)
		}
	}
}

// TestWaitEventBothOps asserts both wait_event (v1) and wait_event_v2 are emitted (§3.2.2/3.2.3).
func TestWaitEventBothOps(t *testing.T) {
	_, _, lc := buildMySQL(t)
	ops := map[string]bool{}
	for _, s := range lc.Streams {
		ops[s.Labels["op"]] = true
	}
	if !ops["wait_event"] {
		t.Error("wait_event op not found")
	}
	if !ops["wait_event_v2"] {
		t.Error("wait_event_v2 op not found")
	}
}

// TestWaitEventV2HasWaitEventType asserts wait_event_v2 lines contain wait_event_type
// with a classifier value (T17).
func TestWaitEventV2HasWaitEventType(t *testing.T) {
	_, _, lc := buildMySQL(t)
	validTypes := map[string]bool{
		"IO Wait": true, "Lock Wait": true,
		"Engine Wait": true, "Network Wait": true,
	}
	lines := findStream(lc, "wait_event_v2")
	if len(lines) == 0 {
		t.Fatal("no wait_event_v2 lines found")
	}
	for _, line := range lines {
		wt := parseLogfmtField(line, "wait_event_type")
		if wt == "" {
			t.Error("wait_event_v2 line missing wait_event_type")
		} else if !validTypes[wt] {
			t.Errorf("wait_event_type %q not a valid classifier value", wt)
		}
	}
}

// TestHealthAlloyVersion asserts AlloyVersion check has value "v1.16.0" (§3.2.10 T16).
func TestHealthAlloyVersion(t *testing.T) {
	_, _, lc := buildMySQL(t)
	lines := findStream(lc, "health_status")
	if len(lines) == 0 {
		t.Fatal("no health_status lines")
	}
	found := false
	for _, line := range lines {
		if parseLogfmtField(line, "check") == "AlloyVersion" {
			found = true
			v := parseLogfmtField(line, "value")
			if v != "v1.16.0" {
				t.Errorf("AlloyVersion value=%q want v1.16.0 (T16)", v)
			}
		}
	}
	if !found {
		t.Error("AlloyVersion check not found in health_status")
	}
}

// TestHealthThreeChecks asserts all three health checks are emitted (§3.2.10).
func TestHealthThreeChecks(t *testing.T) {
	_, _, lc := buildMySQL(t)
	checks := map[string]bool{}
	for _, line := range findStream(lc, "health_status") {
		c := parseLogfmtField(line, "check")
		checks[c] = true
	}
	for _, want := range []string{"AlloyVersion", "RequiredGrantsPresent", "PerformanceSchemaHasRows"} {
		if !checks[want] {
			t.Errorf("health check %q not found", want)
		}
	}
}

// ── Counter monotonicity ──────────────────────────────────────────────────────

// TestCountersMonotone asserts cumulative counters increase across two ticks (I3).
func TestCountersMonotone(t *testing.T) {
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, &coretest.LogCapture{}, nil)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}

	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, &coretest.LogCapture{}, nil)
	if err := c.Tick(context.Background(), now.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	// Map tick1 values by (name, label-sig).
	type key struct{ name, sig string }
	v1 := map[key]float64{}
	for _, s := range mc1.All() {
		v1[key{s.Name, labelSig(s.Labels)}] = s.Value
	}
	counters := []string{
		"mysql_global_status_questions",
		"mysql_global_status_bytes_received",
		"mysql_global_status_bytes_sent",
		"mysql_global_status_uptime",
		"mysql_perf_schema_events_statements_total",
		"mysql_perf_schema_events_statements_rows_sent_total",
		"database_observability_wait_event_seconds_total",
	}
	counterSet := map[string]bool{}
	for _, n := range counters {
		counterSet[n] = true
	}
	for _, s := range mc2.All() {
		if !counterSet[s.Name] {
			continue
		}
		k := key{s.Name, labelSig(s.Labels)}
		if prior, ok := v1[k]; ok {
			if s.Value < prior {
				t.Errorf("counter %q decreased tick1=%v tick2=%v (I3)", s.Name, prior, s.Value)
			}
		}
	}
}

// TestReplicationLabels asserts every replication series carries master_host + master_uuid,
// carries NO channel_name, and that the UUID/host match the expected shapes (SK-13).
func TestReplicationLabels(t *testing.T) {
	_, mc, _ := buildMySQL(t)

	replNames := []string{
		"mysql_slave_status_seconds_behind_source",
		"mysql_slave_status_replica_io_running",
		"mysql_slave_status_replica_sql_running",
	}

	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hostRe := regexp.MustCompile(`^10\.\d+\.\d+\.\d+$`)

	for _, name := range replNames {
		series := mc.Find(name)
		if len(series) == 0 {
			t.Errorf("replication metric %q not found", name)
			continue
		}
		for _, s := range series {
			if _, ok := s.Labels["channel_name"]; ok {
				t.Errorf("%s must NOT carry channel_name label (SK-13)", name)
			}
			uuid := s.Labels["master_uuid"]
			if uuid == "" {
				t.Errorf("%s missing master_uuid label", name)
			} else if !uuidRe.MatchString(uuid) {
				t.Errorf("%s master_uuid=%q does not match 8-4-4-4-12 format", name, uuid)
			}
			host := s.Labels["master_host"]
			if host == "" {
				t.Errorf("%s missing master_host label", name)
			} else if !hostRe.MatchString(host) {
				t.Errorf("%s master_host=%q does not match 10.x.x.x format", name, host)
			}
		}
	}

	// Determinism: two ticks should produce same master_uuid and master_host.
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, &coretest.LogCapture{}, nil)
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w1); err != nil {
		t.Fatalf("Tick1: %v", err)
	}
	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, &coretest.LogCapture{}, nil)
	if err := c.Tick(context.Background(), now.Add(60*time.Second), w2); err != nil {
		t.Fatalf("Tick2: %v", err)
	}
	s1 := mc1.Find("mysql_slave_status_seconds_behind_source")
	s2 := mc2.Find("mysql_slave_status_seconds_behind_source")
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatal("seconds_behind_source not found in determinism check")
	}
	if s1[0].Labels["master_uuid"] != s2[0].Labels["master_uuid"] {
		t.Errorf("master_uuid not deterministic: %q vs %q", s1[0].Labels["master_uuid"], s2[0].Labels["master_uuid"])
	}
	if s1[0].Labels["master_host"] != s2[0].Labels["master_host"] {
		t.Errorf("master_host not deterministic: %q vs %q", s1[0].Labels["master_host"], s2[0].Labels["master_host"])
	}
}

// TestSlaveStatusSeriesComplete asserts all 13 confirmed replication series are emitted (SK-13).
func TestSlaveStatusSeriesComplete(t *testing.T) {
	_, mc, _ := buildMySQL(t)
	want := []string{
		"mysql_slave_status_seconds_behind_source",
		"mysql_slave_status_replica_io_running",
		"mysql_slave_status_replica_sql_running",
		"mysql_slave_status_source_port",
		"mysql_slave_status_source_retry_count",
		"mysql_slave_status_source_ssl_allowed",
		"mysql_slave_status_relay_log_pos",
		"mysql_slave_status_relay_log_space",
		"mysql_slave_status_exec_source_log_pos",
		"mysql_slave_status_skip_counter",
		"mysql_slave_status_last_errno",
		"mysql_slave_status_last_sql_errno",
		"mysql_slave_status_get_source_public_key",
	}
	nameSet := map[string]bool{}
	for _, n := range mc.Names() {
		nameSet[n] = true
	}
	for _, name := range want {
		if !nameSet[name] {
			t.Errorf("replication metric %q not found (SK-13)", name)
		}
	}
}

// TestQueryDataLocksGated asserts query_data_locks op is absent without lock_contention,
// and present (with all 8 fields) when lock_contention is active (SK-14).
func TestQueryDataLocksGated(t *testing.T) {
	// Without any failure mode active — op must be absent.
	_, _, lc := buildMySQL(t)
	ops := map[string]bool{}
	for _, s := range lc.Streams {
		ops[s.Labels["op"]] = true
	}
	if ops["query_data_locks"] {
		t.Error("query_data_locks must NOT appear without lock_contention active")
	}

	// With lock_contention active via live hook — op must be present with 8 fields.
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lc2 := &coretest.LogCapture{}
	sh := shape.New("", nil)
	sh.Live = func(mode string) []shape.LiveFailure {
		if mode == "lock_contention" {
			return []shape.LiveFailure{{Enabled: true, Intensity: 1.0, Scope: db.Name}}
		}
		return nil
	}
	w2 := &core.World{Shape: sh, Metrics: &coretest.MetricCapture{}, Logs: lc2}
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w2); err != nil {
		t.Fatalf("Tick with lock_contention: %v", err)
	}

	lockLines := findStream(lc2, "query_data_locks")
	if len(lockLines) == 0 {
		t.Fatal("query_data_locks must be emitted when lock_contention is active")
	}
	requiredFields := []string{
		"waiting_digest", "waiting_digest_text",
		"blocking_digest", "blocking_digest_text",
		"waiting_timer_wait", "waiting_lock_time",
		"blocking_timer_wait", "blocking_lock_time",
	}
	for _, line := range lockLines {
		for _, field := range requiredFields {
			if parseLogfmtField(line, field) == "" {
				t.Errorf("query_data_locks line missing field %q: %s", field, line)
			}
		}
	}
}

// TestQueryDataLocksTwoQueries guards against the 2-query lock-pair index panic: a DB with
// exactly two queries (reachable via observability.digests: 2) must emit query_data_locks
// under lock_contention WITHOUT panicking on an out-of-range blocking index.
func TestQueryDataLocksTwoQueries(t *testing.T) {
	db := coretest.DB("mysql")
	if len(db.Queries) < 2 {
		t.Fatalf("fixture has %d queries, need ≥2 to truncate", len(db.Queries))
	}
	db.Queries = db.Queries[:2] // exactly two queries

	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sh := shape.New("", nil)
	sh.Live = func(mode string) []shape.LiveFailure {
		if mode == "lock_contention" {
			return []shape.LiveFailure{{Enabled: true, Intensity: 1.0, Scope: db.Name}}
		}
		return nil
	}
	lc := &coretest.LogCapture{}
	w := &core.World{Shape: sh, Metrics: &coretest.MetricCapture{}, Logs: lc}
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil { // must not panic
		t.Fatalf("Tick with 2 queries + lock_contention: %v", err)
	}
	if len(findStream(lc, "query_data_locks")) == 0 {
		t.Error("query_data_locks must emit (1 pair) with exactly two queries under contention")
	}
}

// TestExplainPlanLeafCondition asserts that the "Table Scan" leaf node inside
// explain_plan_output contains a non-empty "condition" key in its details map.
// This covers SK-14 (cantfind) — live-verified Percona 8.4 shape.
func TestExplainPlanLeafCondition(t *testing.T) {
	_, _, lc := buildMySQL(t)
	lines := findStream(lc, "explain_plan_output")
	if len(lines) == 0 {
		t.Fatal("no explain_plan_output lines found")
	}

	for _, line := range lines {
		encoded := parseLogfmtField(line, "explain_plan_output")
		if encoded == "" {
			t.Fatal("explain_plan_output field missing or empty")
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("base64 decode failed: %v", err)
		}
		var plan map[string]any
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatalf("JSON unmarshal failed: %v", err)
		}

		// Walk: plan → children[0] → children[0] → details["condition"]
		root, ok := plan["plan"].(map[string]any)
		if !ok {
			t.Fatal("plan.plan is not a map")
		}
		children0, ok := root["children"].([]any)
		if !ok || len(children0) == 0 {
			t.Fatal("plan.plan.children missing or empty")
		}
		nested, ok := children0[0].(map[string]any)
		if !ok {
			t.Fatal("plan.plan.children[0] is not a map")
		}
		leafChildren, ok := nested["children"].([]any)
		if !ok || len(leafChildren) == 0 {
			t.Fatal("plan.plan.children[0].children missing or empty")
		}
		leaf, ok := leafChildren[0].(map[string]any)
		if !ok {
			t.Fatal("Table Scan leaf is not a map")
		}
		details, ok := leaf["details"].(map[string]any)
		if !ok {
			t.Fatal("Table Scan leaf.details is not a map")
		}
		cond, ok := details["condition"]
		if !ok {
			t.Fatal("Table Scan leaf.details missing \"condition\" key (SK-14)")
		}
		condStr, ok := cond.(string)
		if !ok || condStr == "" {
			t.Errorf("condition must be a non-empty string, got %v", cond)
		}
	}
}

// ── Failure-mode emission (SK-13/14 parity with Postgres dbo11y) ──────────────

// sumMetric sums every series value for a metric name in one capture.
func sumMetric(mc *coretest.MetricCapture, name string) float64 {
	var sum float64
	for _, s := range mc.Find(name) {
		sum += s.Value
	}
	return sum
}

// failModeTicks is the per-tick noise sample count. Shape.Noise draws from a process-global
// RNG (math/rand/v2), so a single active-vs-baseline tick is noise-polluted. Summing a metric
// over many ticks averages the ±10% jitter out (its mean →1, stderr →0.1/√N), so the failure
// FACTOR (5×/8×) dominates with an astronomical margin — the comparison is robust, not flaky.
const failModeTicks = 64

// sumModeOverTicks builds one mysql construct with the named failure mode active (mode=""
// means baseline), ticks it failModeTicks times at a fixed business-hours minute, and returns
// the running total of sumMetric(name) across every tick.
func sumModeOverTicks(t *testing.T, mode, name string) float64 {
	t.Helper()
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sh := shape.New("", nil)
	if mode != "" {
		sh.Live = func(m string) []shape.LiveFailure {
			if m == mode {
				return []shape.LiveFailure{{Enabled: true, Intensity: 1.0, Scope: db.Name}}
			}
			return nil
		}
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) // Monday noon (peak diurnal plateau)
	var total float64
	for i := range failModeTicks {
		mc := &coretest.MetricCapture{}
		w := &core.World{Shape: sh, Metrics: mc, Logs: &coretest.LogCapture{}}
		if err := c.Tick(context.Background(), now.Add(time.Duration(i)*time.Minute), w); err != nil {
			t.Fatalf("Tick %d (mode=%q): %v", i, mode, err)
		}
		total += sumMetric(mc, name)
	}
	return total
}

func assertAmplifies(t *testing.T, mode, name string) {
	t.Helper()
	base := sumModeOverTicks(t, "", name)
	hot := sumModeOverTicks(t, mode, name)
	if hot <= base {
		t.Errorf("%s: %s did not amplify (baseline-sum=%v active-sum=%v over %d ticks)",
			name, mode, base, hot, failModeTicks)
	}
}

// TestConnectionSaturationAmplifies asserts connection_saturation drives threads_connected and
// threads_running above baseline while never exceeding max_connections (SK parity with Postgres).
func TestConnectionSaturationAmplifies(t *testing.T) {
	assertAmplifies(t, "connection_saturation", "mysql_global_status_threads_connected")
	assertAmplifies(t, "connection_saturation", "mysql_global_status_threads_running")

	// Headroom: even at full saturation a single-tick threads_connected stays ≤ max_connections.
	db := coretest.DB("mysql")
	c, _ := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	sh := shape.New("", nil)
	sh.Live = func(m string) []shape.LiveFailure {
		if m == "connection_saturation" {
			return []shape.LiveFailure{{Enabled: true, Intensity: 1.0, Scope: db.Name}}
		}
		return nil
	}
	mc := &coretest.MetricCapture{}
	w := &core.World{Shape: sh, Metrics: mc, Logs: &coretest.LogCapture{}}
	if err := c.Tick(context.Background(), time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tc, maxConn := sumMetric(mc, "mysql_global_status_threads_connected"),
		sumMetric(mc, "mysql_global_variables_max_connections"); tc > maxConn {
		t.Errorf("threads_connected=%v exceeds max_connections=%v under saturation", tc, maxConn)
	}
}

// TestSlowQueryStormAmplifies asserts slow_query_storm drives the slow-query latency counter,
// the global slow_queries counter, and the wait-event seconds counter above baseline.
func TestSlowQueryStormAmplifies(t *testing.T) {
	assertAmplifies(t, "slow_query_storm", "mysql_perf_schema_events_statements_seconds_total")
	assertAmplifies(t, "slow_query_storm", "mysql_global_status_slow_queries")
	assertAmplifies(t, "slow_query_storm", "database_observability_wait_event_seconds_total")
}

// TestLockContentionAmplifiesLockTime asserts lock_contention amplifies the perf-schema
// lock_time counter — the metric signal that complements the query_data_locks log op.
func TestLockContentionAmplifiesLockTime(t *testing.T) {
	assertAmplifies(t, "lock_contention", "mysql_perf_schema_events_statements_lock_time_seconds_total")
}

// ── Per-series value variation ────────────────────────────────────────────────

// TestPeerSeriesDistinct asserts that peer series (different digests within the same schema)
// do NOT emit byte-identical values for volume/time/rows counters. Each digest must have a
// distinct accumulated delta after 30 ticks. Also checks that a single series shows drift
// (more than 1 distinct delta increment across ticks).
func TestPeerSeriesDistinct(t *testing.T) {
	db := coretest.DB("mysql")
	c, err := dbo11ymysql.Build(&dbo11ymysql.Config{}, &fixture.Set{DB: db})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// Collect cumulative values per (metric, digest) across 30 ticks at 13-minute steps.
	type seriesKey struct{ metric, digest string }
	tickVals := map[seriesKey][]float64{}

	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, &coretest.LogCapture{}, nil)
		now := base.Add(time.Duration(i) * 13 * time.Minute)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		for _, metric := range []string{
			"mysql_perf_schema_events_statements_total",
			"mysql_perf_schema_events_statements_rows_sent_total",
			"mysql_perf_schema_events_statements_rows_examined_total",
		} {
			for _, s := range mc.Find(metric) {
				sk := seriesKey{metric, s.Labels["digest"]}
				tickVals[sk] = append(tickVals[sk], s.Value)
			}
		}
	}

	// 1. Peer distinctness: for each metric, the per-digest values at tick 1
	//    must not all be identical across digests.
	for _, metric := range []string{
		"mysql_perf_schema_events_statements_total",
		"mysql_perf_schema_events_statements_rows_sent_total",
		"mysql_perf_schema_events_statements_rows_examined_total",
	} {
		firstByDigest := map[string]float64{}
		for sk, vals := range tickVals {
			if sk.metric == metric && len(vals) > 0 {
				firstByDigest[sk.digest] = vals[0]
			}
		}
		if len(firstByDigest) < 2 {
			t.Logf("%s: only %d digest series, skipping peer check", metric, len(firstByDigest))
			continue
		}
		seen := map[float64]string{}
		for digest, v := range firstByDigest {
			if prev, ok := seen[v]; ok {
				t.Errorf("%s: digests %q and %q emit identical value %.6f (lockstep)", metric, prev, digest, v)
			}
			seen[v] = digest
		}
	}

	// 2. Per-series drift: for one non-slow digest, the per-tick increments must show
	//    more than 1 distinct value across 30 ticks (Wander is applied).
	var fastDigest string
	for _, q := range db.Queries {
		if !q.Slow {
			fastDigest = q.ID
			break
		}
	}
	if fastDigest == "" {
		t.Skip("no non-slow digest in fixture")
	}
	sk := seriesKey{"mysql_perf_schema_events_statements_rows_sent_total", fastDigest}
	vals := tickVals[sk]
	if len(vals) < 2 {
		t.Fatalf("not enough ticks for drift check: %d", len(vals))
	}
	drifts := map[float64]bool{}
	for i := 1; i < len(vals); i++ {
		drifts[vals[i]-vals[i-1]] = true
	}
	if len(drifts) < 3 {
		t.Errorf("mysql_perf_schema_events_statements_rows_sent_total digest=%q: only %d distinct increments across 30 ticks — series may be frozen (Wander not applied)", fastDigest, len(drifts))
	}
}

func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple stable sort via concatenation (no need to import slices/sort for tests).
	for i := range len(keys) - 1 {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(m[k])
		sb.WriteByte(';')
	}
	return sb.String()
}
