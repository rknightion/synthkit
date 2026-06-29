// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ypg_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/dbo11ypg"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ── Per-series realism: peer queryids must not emit byte-identical values ──

// TestPerQueryIDSpread asserts that peer pg_stat_statements_* series (different queryids)
// emit DISTINCT values — fixing the lockstep bug where every query got the same bf-scaled constant.
func TestPerQueryIDSpread(t *testing.T) {
	mc, _ := runTick(t)
	for _, name := range []string{
		"pg_stat_statements_calls_total",
		"pg_stat_statements_rows_total",
		"pg_stat_statements_seconds_total",
	} {
		byQID := map[string]float64{}
		for _, s := range mc.Find(name) {
			byQID[s.Labels["queryid"]] = s.Value
		}
		if len(byQID) < 2 {
			// coretest.DB has 5 queries; need at least 2 for a lockstep check.
			t.Fatalf("%s: expected ≥2 queryid series, got %d", name, len(byQID))
		}
		seen := map[float64]string{}
		for qid, v := range byQID {
			if prev, ok := seen[v]; ok {
				t.Errorf("%s: queryids %q and %q emit identical value %.6f (lockstep)", name, prev, qid, v)
			}
			seen[v] = qid
		}
	}
}

// TestPgStatStatementsDriftsOverTime asserts pg_stat_statements_calls_total drifts across ticks
// (Wander) rather than holding one constant value per series.
func TestPgStatStatementsDriftsOverTime(t *testing.T) {
	db := coretest.DB("postgres")
	fx := &fixture.Set{DB: db}
	c, err := dbo11ypg.Reg.Build(&dbo11ypg.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Use the first (non-slow) queryid.
	targetQID := ""
	for _, q := range db.Queries {
		if !q.Slow {
			targetQID = q.ID
			break
		}
	}
	if targetQID == "" {
		t.Fatal("no non-slow query in fixture")
	}
	const name = "pg_stat_statements_calls_total"
	// Track per-tick increments (delta between ticks) — these should vary due to Wander.
	seen := map[float64]bool{}
	base := testNow
	var prevVal float64
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, &coretest.LogCapture{}, nil)
		// 13-minute steps to sample across Wander's period.
		if err := c.Tick(context.Background(), base.Add(time.Duration(i)*13*time.Minute), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		var val float64
		for _, s := range mc.Find(name) {
			if s.Labels["queryid"] == targetQID {
				val = s.Value
				break
			}
		}
		if i > 0 {
			delta := val - prevVal
			seen[delta] = true
		}
		prevVal = val
	}
	if len(seen) < 5 {
		t.Errorf("%s: only %d distinct deltas across 30 ticks — series appears near-frozen", name, len(seen))
	}
}

// testNow is a stable mid-business-hours time used throughout.
var testNow = time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

// ── helpers ───────────────────────────────────────────────────────────────────

// runTick builds a postgres construct, ticks once, returns metric+log captures.
func runTick(t *testing.T) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	db := coretest.DB("postgres")
	fx := &fixture.Set{DB: db}
	c, err := dbo11ypg.Reg.Build(&dbo11ypg.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return mc, lc
}

// runTwoTicks runs two ticks of the same construct instance (for counter monotonicity tests).
func runTwoTicks(t *testing.T) (mc1, mc2 *coretest.MetricCapture) {
	t.Helper()
	db := coretest.DB("postgres")
	fx := &fixture.Set{DB: db}
	c, err := dbo11ypg.Reg.Build(&dbo11ypg.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc1 = &coretest.MetricCapture{}
	mc2 = &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}

	if err := c.Tick(context.Background(), testNow, coretest.World(mc1, lc, nil)); err != nil {
		t.Fatalf("Tick1: %v", err)
	}
	if err := c.Tick(context.Background(), testNow.Add(60*time.Second), coretest.World(mc2, lc, nil)); err != nil {
		t.Fatalf("Tick2: %v", err)
	}
	return mc1, mc2
}

// streamsByOp collects log lines grouped by their "op" stream label.
func streamsByOp(lc *coretest.LogCapture) map[string][]string {
	out := make(map[string][]string)
	for _, s := range lc.Streams {
		op := s.Labels["op"]
		for _, line := range s.Lines {
			out[op] = append(out[op], line.Body)
		}
	}
	return out
}

// parseLogfmt is a minimal logfmt body parser for test assertions.
// Returns the unquoted value for the given key, or "" if absent.
func parseLogfmt(body, key string) string {
	prefix := key + "="
	idx := strings.Index(body, prefix)
	if idx == -1 {
		return ""
	}
	rest := body[idx+len(prefix):]
	if len(rest) == 0 {
		return ""
	}
	if rest[0] == '"' {
		var val strings.Builder
		i := 1
		for i < len(rest) {
			if rest[i] == '\\' && i+1 < len(rest) {
				switch rest[i+1] {
				case '"':
					val.WriteByte('"')
				case '\\':
					val.WriteByte('\\')
				case 'n':
					val.WriteByte('\n')
				case 't':
					val.WriteByte('\t')
				default:
					val.WriteByte(rest[i+1])
				}
				i += 2
				continue
			}
			if rest[i] == '"' {
				break
			}
			val.WriteByte(rest[i])
			i++
		}
		return val.String()
	}
	end := strings.IndexByte(rest, ' ')
	if end == -1 {
		return rest
	}
	return rest[:end]
}

// hasField reports whether a logfmt body contains key= (any value).
func hasField(body, key string) bool {
	return strings.Contains(body, key+"=")
}

// ── (a) exact metric inventory ────────────────────────────────────────────────

func TestMetricInventory(t *testing.T) {
	mc, _ := runTick(t)
	names := mc.Names()

	want := []string{
		"database_observability_connection_info",
		"database_observability_pg_error_log_parse_failures_total",
		"database_observability_pg_errors_total",
		"pg_locks_count",
		"pg_postmaster_start_time_seconds",
		"pg_replication_is_replica",
		"pg_replication_lag",
		"pg_stat_bgwriter_buffers_alloc",
		"pg_stat_bgwriter_buffers_clean",
		"pg_stat_database_numbackends",
		"pg_stat_statements_calls_total",
		"pg_stat_statements_rows_total",
		"pg_stat_statements_seconds_total",
		"pg_up",
	}

	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, w := range want {
		if !nameSet[w] {
			t.Errorf("metric %q absent; got names: %v", w, names)
		}
	}
	// Assert no unexpected metric names.
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	for _, n := range names {
		if !wantSet[n] {
			t.Errorf("unexpected metric %q emitted", n)
		}
	}
}

// TestLogOpInventory asserts all expected log ops are emitted.
func TestLogOpInventory(t *testing.T) {
	_, lc := runTick(t)
	ops := make(map[string]bool)
	for _, s := range lc.Streams {
		ops[s.Labels["op"]] = true
	}
	want := []string{
		"query_sample",
		"wait_event",
		"wait_event_v2",
		"query_association",
		"query_parsed_table_name",
		"table_detection",
		"create_statement",
		"explain_plan_output",
		"health_status",
	}
	for _, op := range want {
		if !ops[op] {
			t.Errorf("log op %q absent; got: %v", op, ops)
		}
	}
}

// ── (b) queryid values == fixture Query IDs, decimal int64 strings ────────────

func TestQueryIDDecimalString(t *testing.T) {
	mc, lc := runTick(t)
	db := coretest.DB("postgres")

	expectedIDs := make(map[string]bool)
	for _, q := range db.Queries {
		expectedIDs[q.ID] = true
	}

	// Check pg_stat_statements_calls_total metric labels.
	for _, s := range mc.Find("pg_stat_statements_calls_total") {
		qid, ok := s.Labels["queryid"]
		if !ok {
			t.Errorf("pg_stat_statements_calls_total missing queryid label: %v", s.Labels)
			continue
		}
		if !expectedIDs[qid] {
			t.Errorf("metric queryid %q not in fixture IDs", qid)
		}
		for _, ch := range qid {
			if ch < '0' || ch > '9' {
				t.Errorf("metric queryid %q contains non-decimal character %q (must be decimal int64 string; T4)", qid, ch)
			}
		}
	}

	// Check query_association log lines.
	byOp := streamsByOp(lc)
	for _, body := range byOp["query_association"] {
		qid := parseLogfmt(body, "queryid")
		if !expectedIDs[qid] {
			t.Errorf("query_association queryid %q not in fixture IDs", qid)
		}
		for _, ch := range qid {
			if ch < '0' || ch > '9' {
				t.Errorf("query_association queryid %q has non-decimal char %q (T4)", qid, ch)
			}
		}
	}
}

// ── (c) database_observability_wait_event_seconds_total ABSENT ───────────────

func TestWaitEventSecondsTotalAbsent(t *testing.T) {
	mc, _ := runTick(t)
	for _, s := range mc.All() {
		if s.Name == "database_observability_wait_event_seconds_total" {
			t.Errorf("database_observability_wait_event_seconds_total MUST NOT be emitted for Postgres (MySQL-only, T3): found labels %v", s.Labels)
		}
	}
}

// ── (d) explain_plan_output uses schema= and digest= keys (T6 / §B.8) ────────

func TestExplainUsesSchemaAndDigestKeys(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	lines, ok := byOp["explain_plan_output"]
	if !ok || len(lines) == 0 {
		t.Fatal("no explain_plan_output log lines emitted")
	}
	db := coretest.DB("postgres")
	expectedIDs := make(map[string]bool)
	for _, q := range db.Queries {
		expectedIDs[q.ID] = true
	}
	for _, body := range lines {
		if !hasField(body, "schema") {
			t.Errorf("explain_plan_output missing schema=: %s", body)
		}
		if hasField(body, "datname") {
			t.Errorf("explain_plan_output must NOT have datname= (T6/§B.8): %s", body)
		}
		if !hasField(body, "digest") {
			t.Errorf("explain_plan_output missing digest=: %s", body)
		}
		if hasField(body, "queryid") {
			t.Errorf("explain_plan_output must NOT have queryid= (T6/§B.8): %s", body)
		}
		dig := parseLogfmt(body, "digest")
		if !expectedIDs[dig] {
			t.Errorf("explain_plan_output digest=%q not in fixture query IDs", dig)
		}
	}
}

// ── (e) duration fields use Go time.Duration.String() format (T8) ────────────

func TestDurationGoFormat(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	durationFields := []string{"query_time", "wait_time", "xact_time", "cpu_time"}
	for _, op := range []string{"query_sample", "wait_event", "wait_event_v2"} {
		for _, body := range byOp[op] {
			for _, field := range durationFields {
				val := parseLogfmt(body, field)
				if val == "" {
					continue // field may be conditional
				}
				if _, err := time.ParseDuration(val); err != nil {
					t.Errorf("op=%q field %q=%q: not a valid Go duration (T8): %v", op, field, val, err)
				}
			}
		}
	}
}

// ── (f) timestamp rule: query_sample/wait lines have timestamps in the past (T7) ─

func TestTimestampInPast(t *testing.T) {
	_, lc := runTick(t)
	for _, s := range lc.Streams {
		op := s.Labels["op"]
		if op != "query_sample" && op != "wait_event" && op != "wait_event_v2" {
			continue
		}
		for _, line := range s.Lines {
			if !line.T.Before(testNow) {
				t.Errorf("op=%q line timestamp %v is not before tick time %v (T7: must be now-query_time)", op, line.T, testNow)
			}
		}
	}
}

// ── (g) counters monotonically non-decreasing across ticks (I3) ───────────────

func TestCountersMonotone(t *testing.T) {
	mc1, mc2 := runTwoTicks(t)

	counters := []string{
		"pg_stat_statements_calls_total",
		"pg_stat_statements_seconds_total",
		"pg_stat_statements_rows_total",
		"pg_stat_bgwriter_buffers_clean",
		"pg_stat_bgwriter_buffers_alloc",
		"database_observability_pg_errors_total",
		"database_observability_pg_error_log_parse_failures_total",
	}

	for _, name := range counters {
		var sum1, sum2 float64
		for _, s := range mc1.Find(name) {
			sum1 += s.Value
		}
		for _, s := range mc2.Find(name) {
			sum2 += s.Value
		}
		if sum2 < sum1 {
			t.Errorf("counter %q decreased: tick1 sum=%v, tick2 sum=%v (I3: must be cumulative)", name, sum1, sum2)
		}
	}
}

// ── (h) Build rejects mysql fixture ──────────────────────────────────────────

func TestBuildRejectsMysql(t *testing.T) {
	db := coretest.DB("mysql")
	fx := &fixture.Set{DB: db}
	_, err := dbo11ypg.Reg.Build(&dbo11ypg.Config{}, fx)
	if err == nil {
		t.Fatal("Build with engine=mysql must return an error")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("error should mention postgres, got: %v", err)
	}
}

// ── additional coverage ───────────────────────────────────────────────────────

// TestConnectionInfoSixLabels asserts ALL SIX required metric labels on connection_info (T1 / §4.1.1).
func TestConnectionInfoSixLabels(t *testing.T) {
	mc, _ := runTick(t)
	series := mc.Find("database_observability_connection_info")
	if len(series) == 0 {
		t.Fatal("database_observability_connection_info not emitted")
	}
	required := []string{
		"provider_name", "provider_region", "provider_account",
		"db_instance_identifier", "engine", "engine_version",
	}
	for _, s := range series {
		for _, k := range required {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("database_observability_connection_info missing label %q; labels=%v", k, s.Labels)
			}
		}
		if got := s.Labels["engine"]; got != "postgres" {
			t.Errorf("connection_info engine=%q, want %q", got, "postgres")
		}
	}
}

// TestHealthStatusSixChecks verifies exactly six health checks (§4.2.9).
func TestHealthStatusSixChecks(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	if n := len(byOp["health_status"]); n != 6 {
		t.Errorf("health_status: want 6 lines, got %d", n)
	}
}

// TestHealthAlloyVersion asserts AlloyVersion="v1.16.3" (T16 — running Alloy version).
func TestHealthAlloyVersion(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	for _, body := range byOp["health_status"] {
		if parseLogfmt(body, "check") == "AlloyVersion" {
			v := parseLogfmt(body, "value")
			if v != "v1.16.3" {
				t.Errorf("AlloyVersion health check value=%q, want %q (T16)", v, "v1.16.3")
			}
			return
		}
	}
	t.Error("AlloyVersion check not found in health_status lines")
}

// TestQueryAssociationNoDigest verifies Postgres uses queryid= (not digest=) in query_association (§4.2.4).
func TestQueryAssociationNoDigest(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	lines := byOp["query_association"]
	if len(lines) == 0 {
		t.Fatal("no query_association lines emitted")
	}
	for _, body := range lines {
		if !hasField(body, "queryid") {
			t.Errorf("query_association missing queryid=: %s", body)
		}
		if hasField(body, "digest") {
			t.Errorf("query_association must NOT have digest= (Postgres uses queryid=): %s", body)
		}
		if !hasField(body, "datname") {
			t.Errorf("query_association missing datname=: %s", body)
		}
		if hasField(body, "schema") {
			t.Errorf("query_association must NOT have schema= (Postgres uses datname=): %s", body)
		}
	}
}

// TestWaitEventNameColonFormat verifies wait_event_name is "<type>:<event>" format (T9).
func TestWaitEventNameColonFormat(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	for _, op := range []string{"wait_event", "wait_event_v2"} {
		for _, body := range byOp[op] {
			wen := parseLogfmt(body, "wait_event_name")
			if wen == "" {
				t.Errorf("op=%q missing wait_event_name=: %s", op, body)
				continue
			}
			if !strings.Contains(wen, ":") {
				t.Errorf("op=%q wait_event_name=%q must be <type>:<event> colon-joined (T9)", op, wen)
			}
		}
	}
}

// TestWaitEventV2ClassifiedBuckets verifies wait_event_v2 uses pre-classified bucket names (T17).
func TestWaitEventV2ClassifiedBuckets(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	validBuckets := map[string]bool{
		"Lock Wait":    true,
		"IO Wait":      true,
		"Engine Wait":  true,
		"Network Wait": true,
		"Timeout Wait": true,
	}
	for _, body := range byOp["wait_event_v2"] {
		wetType := parseLogfmt(body, "wait_event_type")
		if wetType == "" {
			t.Errorf("wait_event_v2 missing wait_event_type=: %s", body)
			continue
		}
		if !validBuckets[wetType] {
			t.Errorf("wait_event_v2 wait_event_type=%q is not a valid bucket name (T17): want one of %v", wetType, validBuckets)
		}
	}
}

// TestPgStatStatementsNoDigestText asserts digest_text is absent from pg_stat_statements_* (§4.1.10).
func TestPgStatStatementsNoDigestText(t *testing.T) {
	mc, _ := runTick(t)
	for _, name := range []string{
		"pg_stat_statements_calls_total",
		"pg_stat_statements_seconds_total",
		"pg_stat_statements_rows_total",
	} {
		for _, s := range mc.Find(name) {
			if _, ok := s.Labels["digest_text"]; ok {
				t.Errorf("%s MUST NOT have digest_text label (§4.1.10): %v", name, s.Labels)
			}
		}
	}
}

// TestBuildNilDB verifies Build rejects a nil fixture.DB.
func TestBuildNilDB(t *testing.T) {
	fx := &fixture.Set{}
	_, err := dbo11ypg.Reg.Build(&dbo11ypg.Config{}, fx)
	if err == nil {
		t.Fatal("Build with nil DB must return an error")
	}
}

// TestRegMeta verifies the kind, scope, signals, and interval from Reg.
func TestRegMeta(t *testing.T) {
	if got := dbo11ypg.Reg.Kind; got != "dbo11y_postgres" {
		t.Errorf("Reg.Kind=%q, want %q", got, "dbo11y_postgres")
	}
	if got := dbo11ypg.Reg.Scope; got != core.ScopeSubstrate {
		t.Errorf("Reg.Scope=%v, want ScopeSubstrate", got)
	}

	db := coretest.DB("postgres")
	fx := &fixture.Set{DB: db}
	c, err := dbo11ypg.Reg.Build(&dbo11ypg.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Kind() != "dbo11y_postgres" {
		t.Errorf("Kind()=%q, want %q", c.Kind(), "dbo11y_postgres")
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Errorf("Signals() len=%d, want 2", len(sigs))
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v, want 60s", c.Interval())
	}
}

// TestCreateStatementNoCreateStatementField verifies Postgres create_statement has no DDL field (T14).
func TestCreateStatementNoCreateStatementField(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	lines := byOp["create_statement"]
	if len(lines) == 0 {
		t.Fatal("no create_statement lines emitted")
	}
	for _, body := range lines {
		if hasField(body, "create_statement") {
			t.Errorf("Postgres create_statement line must NOT have create_statement= DDL field (T14): %s", body)
		}
		if !hasField(body, "table_spec") {
			t.Errorf("Postgres create_statement line missing table_spec=: %s", body)
		}
		if !hasField(body, "datname") {
			t.Errorf("Postgres create_statement line missing datname=: %s", body)
		}
		if !hasField(body, "schema") {
			t.Errorf("Postgres create_statement line missing schema=: %s", body)
		}
	}
}

// TestLeaderPidEmptyNotOmitted verifies leader_pid="" is present (§B.5) in query_sample.
func TestLeaderPidEmptyNotOmitted(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	lines := byOp["query_sample"]
	if len(lines) == 0 {
		t.Fatal("no query_sample lines emitted")
	}
	for _, body := range lines {
		if !hasField(body, "leader_pid") {
			t.Errorf("query_sample missing leader_pid= (§B.5: empty string, not omitted): %s", body)
		}
	}
}

// ── SK-15 assertions ──────────────────────────────────────────────────────────

// TestQuerySampleHasQueryField (SK-15a): query_sample lines must contain a query= field
// with a non-empty, $N-parameterized SQL string.
func TestQuerySampleHasQueryField(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	lines := byOp["query_sample"]
	if len(lines) == 0 {
		t.Fatal("no query_sample lines emitted")
	}
	for _, body := range lines {
		if !hasField(body, "query") {
			t.Errorf("query_sample missing query= field (SK-15): %s", body)
			continue
		}
		q := parseLogfmt(body, "query")
		if q == "" {
			t.Errorf("query_sample query= is empty (SK-15): %s", body)
			continue
		}
		// Must contain at least one $N placeholder (parameterized SQL).
		if !strings.Contains(q, "$") {
			t.Errorf("query_sample query=%q is not $N-parameterized (SK-15): %s", q, body)
		}
	}
}

// TestWaitEventHasAppAndClient (SK-15b): wait_event and wait_event_v2 lines must carry
// app= and client= identity fields (same as query_sample).
func TestWaitEventHasAppAndClient(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	for _, op := range []string{"wait_event", "wait_event_v2"} {
		lines := byOp[op]
		if len(lines) == 0 {
			t.Errorf("no %q lines emitted", op)
			continue
		}
		for _, body := range lines {
			if !hasField(body, "app") {
				t.Errorf("op=%q missing app= (SK-15b): %s", op, body)
			}
			if !hasField(body, "client") {
				t.Errorf("op=%q missing client= (SK-15b): %s", op, body)
			}
		}
	}
}

// TestWaitEventBlockedByPidsConditional (SK-15c): blocked_by_pids must be "[<pid+1> <pid+2>]"
// for blocked=true entries (Lock:tuple), and "[]" for blocked=false entries.
func TestWaitEventBlockedByPidsConditional(t *testing.T) {
	_, lc := runTick(t)
	byOp := streamsByOp(lc)
	// Check both wait op variants.
	for _, op := range []string{"wait_event", "wait_event_v2"} {
		lines := byOp[op]
		if len(lines) == 0 {
			t.Errorf("no %q lines emitted", op)
			continue
		}
		foundEmpty := false
		foundNonEmpty := false
		for _, body := range lines {
			bpids := parseLogfmt(body, "blocked_by_pids")
			if bpids == "" {
				t.Errorf("op=%q missing blocked_by_pids= (SK-15c): %s", op, body)
				continue
			}
			if bpids == "[]" {
				foundEmpty = true
			} else {
				// Must be "[<n1> <n2>]" — space-separated pids (not comma).
				if !strings.HasPrefix(bpids, "[") || !strings.HasSuffix(bpids, "]") {
					t.Errorf("op=%q blocked_by_pids=%q not bracket-wrapped (SK-15c): %s", op, bpids, body)
					continue
				}
				inner := bpids[1 : len(bpids)-1]
				if strings.Contains(inner, ",") {
					t.Errorf("op=%q blocked_by_pids=%q must be space-separated, not comma (SK-15c): %s", op, bpids, body)
				}
				if inner == "" {
					t.Errorf("op=%q blocked_by_pids=%q inner is empty for non-[] entry (SK-15c): %s", op, bpids, body)
				}
				foundNonEmpty = true
			}
		}
		if !foundEmpty {
			t.Errorf("op=%q: no line with blocked_by_pids=\"[]\" found (SK-15c: non-blocked entries must be empty)", op)
		}
		if !foundNonEmpty {
			t.Errorf("op=%q: no line with non-empty blocked_by_pids found (SK-15c: Lock:tuple entry must have pids)", op)
		}
	}
}
