// SPDX-License-Identifier: AGPL-3.0-only

package langsmithplatform_test

// langsmithplatform_test.go — contract tests for the langsmith_platform construct.
//
// (a) Representative series present across service families.
// (b) No langsmith_*-prefixed invented series (standard exporters only).
// (c) Interface conformance: Kind / Signals / Interval.
// (d) job label form: "langsmith-<svc>" on every series.
// (e) instance label form: "<svc>:<port>" on every series.
// (f) Counter monotone across two ticks (state.Add semantics).
// (g) Gauge not accumulated (state.Set semantics).
// (h) http_request_duration_seconds expands to _bucket/_sum/_count (histogram).
// (i) Default zero Config emits all eight services.
// (j) Custom Services list gates output.
// (k) Nil Metrics writer is safe (no panic).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/langsmithplatform"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// buildWith builds a langsmith_platform construct with the given Config.
func buildWith(t *testing.T, cfg *langsmithplatform.Config) core.Construct {
	t.Helper()
	fx := &fixture.Set{Seed: "test"}
	c, err := langsmithplatform.Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

// buildDefault builds with a zero Config (all 8 default services).
func buildDefault(t *testing.T) core.Construct {
	t.Helper()
	return buildWith(t, &langsmithplatform.Config{})
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

// ── (c) Interface conformance ─────────────────────────────────────────────────

func TestInterfaceConformance(t *testing.T) {
	c := buildDefault(t)
	if c.Kind() != "langsmith_platform" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "langsmith_platform")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// ── (a) Representative series present across service families ─────────────────

func TestRepresentativeSeriesPresent(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	checks := []struct {
		desc string
		name string
		job  string
	}{
		// Python service — process family
		{"process_resident_memory_bytes/backend", "process_resident_memory_bytes", "langsmith-backend"},
		// Python service — python family
		{"python_gc_objects_collected_total/backend", "python_gc_objects_collected_total", "langsmith-backend"},
		// Python service — http counter
		{"http_requests_total/backend", "http_requests_total", "langsmith-backend"},
		// ClickHouse gauge
		{"ClickHouseMetrics_Query/clickhouse", "ClickHouseMetrics_Query", "langsmith-clickhouse"},
		// redis gauge
		{"redis_up/redis", "redis_up", "langsmith-redis"},
		// postgres gauge
		{"pg_up/postgres", "pg_up", "langsmith-postgres"},
		// nginx counter
		{"nginx_http_requests_total/nginx", "nginx_http_requests_total", "langsmith-nginx"},
	}

	for _, tc := range checks {
		found := false
		for _, s := range mc.Find(tc.name) {
			if s.Labels["job"] == tc.job {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("representative series %q with job=%q not found", tc.name, tc.job)
		}
	}
}

// ── (b) No langsmith_*-prefixed invented series ───────────────────────────────

func TestNoInventedLangsmithAppMetrics(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if strings.HasPrefix(s.Name, "langsmith_") {
			t.Errorf("invented langsmith_* series %q found — standard exporters must not use langsmith_ prefix", s.Name)
		}
	}
}

// ── (d) job label form ────────────────────────────────────────────────────────

func TestJobLabelForm(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		job := s.Labels["job"]
		if !strings.HasPrefix(job, "langsmith-") {
			t.Errorf("series %q: job=%q does not have prefix 'langsmith-'", s.Name, job)
		}
	}
}

// ── (e) instance label form ───────────────────────────────────────────────────

func TestInstanceLabelForm(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	// instance must be non-empty and contain ":"
	for _, s := range mc.All() {
		inst := s.Labels["instance"]
		if inst == "" {
			t.Errorf("series %q: instance label missing or empty", s.Name)
			continue
		}
		if !strings.Contains(inst, ":") {
			t.Errorf("series %q: instance=%q does not contain ':' (want <svc>:<port>)", s.Name, inst)
		}
	}
}

// Verify specific port assignments from signals/langsmith.md
func TestInstancePorts(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	wantPorts := map[string]string{
		"langsmith-backend":          "backend:1984",
		"langsmith-host-backend":     "host-backend:1985",
		"langsmith-platform-backend": "platform-backend:1986",
		"langsmith-playground":       "playground:1988",
		"langsmith-clickhouse":       "clickhouse:9363",
		"langsmith-redis":            "redis:9121",
		"langsmith-postgres":         "postgres:9187",
		"langsmith-nginx":            "nginx:9113",
	}

	seen := map[string]string{} // job -> instance
	for _, s := range mc.All() {
		job := s.Labels["job"]
		inst := s.Labels["instance"]
		seen[job] = inst
	}

	for job, wantInst := range wantPorts {
		if got, ok := seen[job]; !ok {
			t.Errorf("no series with job=%q found", job)
		} else if got != wantInst {
			t.Errorf("job=%q: instance=%q want %q", job, got, wantInst)
		}
	}
}

// ── (f) Counter monotone across two ticks ────────────────────────────────────

func TestCounterMonotone(t *testing.T) {
	c := buildDefault(t)

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

	const counterName = "redis_commands_processed_total"
	s1 := mc1.Find(counterName)
	s2 := mc2.Find(counterName)
	if len(s1) == 0 || len(s2) == 0 {
		t.Fatalf("counter %q not found (tick1=%d tick2=%d)", counterName, len(s1), len(s2))
	}
	if s2[0].Value <= s1[0].Value {
		t.Errorf("counter %q not monotone: tick1=%.4f tick2=%.4f", counterName, s1[0].Value, s2[0].Value)
	}
}

// ── (g) Gauge not accumulated ─────────────────────────────────────────────────

func TestGaugeNotAccumulated(t *testing.T) {
	c := buildDefault(t)

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

	const gaugeName = "redis_up"
	s2 := mc2.Find(gaugeName)
	if len(s2) == 0 {
		t.Fatalf("gauge %q not found after tick 2", gaugeName)
	}
	// redis_up should always be 1 (not accumulate to 2+)
	for _, s := range s2 {
		if s.Value > 1.5 {
			t.Errorf("gauge %q accumulated to %v (>1.5) — looks like Add instead of Set", gaugeName, s.Value)
		}
	}
}

// ── (h) http_request_duration_seconds is a histogram ─────────────────────────

func TestHTTPDurationHistogramExpands(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{
		Services: []string{"backend"},
	})
	mc := tickOnce(t, c)

	const base = "http_request_duration_seconds"
	if !histoPresent(mc, base) {
		t.Errorf("histogram %q: _bucket/_sum/_count not all present", base)
	}
	// Base name must NOT appear as a plain series
	if seriesPresent(mc, base) {
		t.Errorf("histogram %q: plain base name present — BASE name must not be emitted directly", base)
	}
	// No double-suffix
	if seriesPresent(mc, base+"_bucket_bucket") {
		t.Errorf("histogram %q: _bucket_bucket produced — BASE name not being passed to Observe", base)
	}
}

// ── (i) Default zero Config emits all eight services ─────────────────────────

func TestDefaultConfigEmitsAllServices(t *testing.T) {
	c := buildDefault(t)
	mc := tickOnce(t, c)

	// One anchor per service
	anchors := map[string]string{
		"langsmith-backend":          "process_resident_memory_bytes",
		"langsmith-host-backend":     "process_resident_memory_bytes",
		"langsmith-platform-backend": "process_resident_memory_bytes",
		"langsmith-playground":       "process_resident_memory_bytes",
		"langsmith-clickhouse":       "ClickHouseMetrics_Query",
		"langsmith-redis":            "redis_up",
		"langsmith-postgres":         "pg_up",
		"langsmith-nginx":            "nginx_http_requests_total",
	}

	for job, metric := range anchors {
		found := false
		for _, s := range mc.Find(metric) {
			if s.Labels["job"] == job {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default config: service job=%q anchor %q absent", job, metric)
		}
	}
}

// ── (j) Custom Services list gates output ─────────────────────────────────────

func TestCustomServicesGating(t *testing.T) {
	// Only emit redis; other services should produce no series.
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"redis"}})
	mc := tickOnce(t, c)

	// redis series must be present
	if !seriesPresent(mc, "redis_up") {
		t.Error("custom Services=[redis]: redis_up absent")
	}
	// nginx / clickhouse / postgres / python series must be absent
	absent := []string{
		"nginx_http_requests_total",
		"ClickHouseMetrics_Query",
		"pg_up",
		"process_resident_memory_bytes",
	}
	for _, name := range absent {
		if seriesPresent(mc, name) {
			t.Errorf("custom Services=[redis]: %q present but should be absent", name)
		}
	}
}

// ── (k) Nil Metrics writer is safe ───────────────────────────────────────────

func TestNilMetricsWriterSafe(t *testing.T) {
	c := buildDefault(t)
	w := coretest.World(nil, nil, nil)
	if err := c.Tick(context.Background(), testNow, w); err != nil {
		t.Fatalf("Tick with nil Metrics writer: %v", err)
	}
}

// ── Exporter-family isolation: Python vs non-Python ──────────────────────────

// TestClickHouseOnlyForClickHouseService ensures ClickHouse* metrics only appear
// for the clickhouse service, not for Python services.
func TestClickHouseOnlyForClickHouseService(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"backend"}})
	mc := tickOnce(t, c)

	if seriesPresent(mc, "ClickHouseMetrics_Query") {
		t.Error("ClickHouseMetrics_Query present for backend service — should be clickhouse-only")
	}
}

// TestPythonFamilyOnlyForPythonServices ensures process_* only appears for Python services.
func TestPythonFamilyOnlyForPythonServices(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"clickhouse"}})
	mc := tickOnce(t, c)

	if seriesPresent(mc, "process_resident_memory_bytes") {
		t.Error("process_resident_memory_bytes present for clickhouse service — should be Python-service-only")
	}
}

// ── Env-scoping (Spec 3) ──────────────────────────────────────────────────────

// TestEnvScopedStampsEnvLabel: Build with an Env fixture → all platform-health series
// carry env=tst1.
func TestEnvScopedStampsEnvLabel(t *testing.T) {
	fx := &fixture.Set{Seed: "s", Env: &fixture.Env{Name: "tst1", Weight: 0.3, NonProd: true}}
	c, err := langsmithplatform.Build(&langsmithplatform.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if s.Labels["env"] != "tst1" {
			t.Errorf("series %q: env=%q want %q", s.Name, s.Labels["env"], "tst1")
		}
	}
}

// TestAggregateEnvUnchanged: Build with no Env (aggregate) → env label absent on all series.
func TestAggregateEnvUnchanged(t *testing.T) {
	fx := &fixture.Set{Seed: "s"}
	c, err := langsmithplatform.Build(&langsmithplatform.Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := tickOnce(t, c)

	for _, s := range mc.All() {
		if _, ok := s.Labels["env"]; ok {
			t.Errorf("series %q: env label present in aggregate mode (want absent per I13)", s.Name)
		}
	}
}

// ── (l) New metric families: pg_stat_activity_count, pg_replication_lag,
//         ClickHouseProfileEvents_SelectedRows, ClickHouseProfileEvents_InsertedRows ───────

// TestPgStatActivityCount verifies the new gauge emitted per connection-state label.
func TestPgStatActivityCount(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"postgres"}})
	mc := tickOnce(t, c)

	const name = "pg_stat_activity_count"
	series := mc.Find(name)
	if len(series) == 0 {
		t.Fatalf("%q: no series emitted", name)
	}

	// Must carry a low-card state label.
	for _, s := range series {
		if s.Labels["state"] == "" {
			t.Errorf("%q: series missing state label (labels=%v)", name, s.Labels)
		}
	}

	// Expected states present.
	wantStates := []string{"active", "idle", "idle in transaction"}
	for _, want := range wantStates {
		found := false
		for _, s := range series {
			if s.Labels["state"] == want {
				found = true
				if s.Value <= 0 {
					t.Errorf("%q state=%q: value=%v want >0", name, want, s.Value)
				}
				break
			}
		}
		if !found {
			t.Errorf("%q: state=%q series absent", name, want)
		}
	}
}

// TestPgStatActivityCountIsGauge verifies pg_stat_activity_count does not accumulate (gauge, not counter).
func TestPgStatActivityCountIsGauge(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"postgres"}})

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

	// Gauge values must not double — all active-state values should stay <= 4 (not grow unbounded).
	for _, s := range mc2.Find("pg_stat_activity_count") {
		if s.Labels["state"] == "active" && s.Value > 10 {
			t.Errorf("pg_stat_activity_count state=active: value=%v after 2 ticks looks like Add not Set", s.Value)
		}
	}
}

// TestPgReplicationLag verifies pg_replication_lag is emitted as a small positive gauge.
func TestPgReplicationLag(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"postgres"}})

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

	const name = "pg_replication_lag"
	s1 := mc1.Find(name)
	s2 := mc2.Find(name)
	if len(s1) == 0 {
		t.Fatalf("%q: not emitted on tick 1", name)
	}
	if len(s2) == 0 {
		t.Fatalf("%q: not emitted on tick 2", name)
	}

	// Value should be small and non-negative.
	for _, s := range s1 {
		if s.Value < 0 {
			t.Errorf("%q: negative value %v", name, s.Value)
		}
		if s.Value > 5 {
			t.Errorf("%q: value=%v unusually large (want near-0 seconds)", name, s.Value)
		}
	}
	// Gauge: must not accumulate across ticks.
	if s2[0].Value > 5 {
		t.Errorf("%q: value=%v after tick 2 (looks accumulated — expected gauge not counter)", name, s2[0].Value)
	}
}

// TestClickHouseProfileEventsSelectedInsertedRows verifies the two new cumulative counters
// are emitted and monotone across ticks.
func TestClickHouseProfileEventsSelectedInsertedRows(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"clickhouse"}})

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

	for _, name := range []string{
		"ClickHouseProfileEvents_SelectedRows",
		"ClickHouseProfileEvents_InsertedRows",
	} {
		s1 := mc1.Find(name)
		s2 := mc2.Find(name)
		if len(s1) == 0 {
			t.Fatalf("%q: not emitted on tick 1", name)
		}
		if len(s2) == 0 {
			t.Fatalf("%q: not emitted on tick 2", name)
		}
		// Counters must be monotone (state.Add).
		if s2[0].Value <= s1[0].Value {
			t.Errorf("%q: not monotone — tick1=%.0f tick2=%.0f (want tick2 > tick1)", name, s1[0].Value, s2[0].Value)
		}
		// Values should be substantial (>0 at bf≈1).
		if s1[0].Value <= 0 {
			t.Errorf("%q: tick1 value=%v want >0", name, s1[0].Value)
		}
		// job label must identify clickhouse service.
		if s1[0].Labels["job"] != "langsmith-clickhouse" {
			t.Errorf("%q: job=%q want %q", name, s1[0].Labels["job"], "langsmith-clickhouse")
		}
	}
}

// ── Per-series value variation: Python services must not be lockstep ─────────

// TestPythonServicesEmitDistinctValues asserts that the four Python services
// (backend, host-backend, platform-backend, playground) emit DISTINCT values for
// process_resident_memory_bytes. Before the fix all four emit the same constant
// (150_000_000 * bf) — byte-identical lockstep.
func TestPythonServicesEmitDistinctValues(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{
		Services: []string{"backend", "host-backend", "platform-backend", "playground"},
	})
	mc := tickOnce(t, c)

	for _, name := range []string{
		"process_resident_memory_bytes",
		"process_virtual_memory_bytes",
		"process_open_fds",
		"http_requests_total",
	} {
		byJob := map[string]float64{}
		for _, s := range mc.Find(name) {
			byJob[s.Labels["job"]] = s.Value
		}
		if len(byJob) < 4 {
			t.Fatalf("%s: expected 4 services, got %d", name, len(byJob))
		}
		seen := map[float64]string{}
		for job, v := range byJob {
			if prev, ok := seen[v]; ok {
				t.Errorf("%s: services %q and %q emit identical value %.6f (lockstep)", name, prev, job, v)
			}
			seen[v] = job
		}
	}
}

// TestPythonServiceDriftsOverTime asserts that process_resident_memory_bytes for one
// Python service takes ≥5 distinct values across 30 ticks at 13-minute steps (Wander).
func TestPythonServiceDriftsOverTime(t *testing.T) {
	c := buildWith(t, &langsmithplatform.Config{Services: []string{"backend"}})
	const name = "process_resident_memory_bytes"
	seen := map[float64]bool{}
	base := testNow
	for i := 0; i < 30; i++ {
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		if err := c.Tick(context.Background(), base.Add(time.Duration(i)*13*time.Minute), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
		for _, s := range mc.Find(name) {
			if s.Labels["job"] == "langsmith-backend" {
				seen[s.Value] = true
			}
		}
	}
	if len(seen) < 5 {
		t.Errorf("%s/backend: only %d distinct values across 30 ticks — series is near-frozen", name, len(seen))
	}
}

// ── Registration ──────────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	reg := langsmithplatform.Registration()
	if reg.Kind != "langsmith_platform" {
		t.Errorf("Registration Kind=%q want %q", reg.Kind, "langsmith_platform")
	}
	if reg.Scope != core.ScopeSubstrate {
		t.Errorf("Registration Scope=%v want ScopeSubstrate", reg.Scope)
	}
	if reg.Group != core.GroupIntegration {
		t.Errorf("Registration Group=%v want GroupIntegration", reg.Group)
	}
	if reg.NewConfig == nil {
		t.Error("Registration NewConfig is nil")
	}
	if reg.Build == nil {
		t.Error("Registration Build is nil")
	}
}
