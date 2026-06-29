// SPDX-License-Identifier: AGPL-3.0-only

package sm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// ————————————————————————————————————————————————————————————————————————————
// Helpers
// ————————————————————————————————————————————————————————————————————————————

// twoChecks returns a Config with two named checks.
func twoChecks() *Config {
	return &Config{
		Checks: []CheckConfig{
			{Name: "api-health", Target: "https://api.example.com/health", FrequencyMs: 60000},
			{Name: "gateway-health"},
		},
	}
}

// buildConstruct builds from cfg with seed "test".
func buildConstruct(t *testing.T, cfg *Config) *Construct {
	t.Helper()
	fx := &fixture.Set{Seed: "test"}
	c, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c.(*Construct)
}

// findSeries returns all series with the given name from batch.
func findSeries(batch []promrw.Series, name string) []promrw.Series {
	var out []promrw.Series
	for _, s := range batch {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// findSeriesFor returns all series with the given name and job label value.
func findSeriesFor(batch []promrw.Series, name, job string) []promrw.Series {
	var out []promrw.Series
	for _, s := range batch {
		if s.Name == name && s.Labels["job"] == job {
			out = append(out, s)
		}
	}
	return out
}

// worldWith returns a test World scoped to both Metrics and Logs.
func worldWith(m *coretest.MetricCapture, l *coretest.LogCapture) *core.World {
	return coretest.World(m, l, nil)
}

// noon is a fixed weekday midday timestamp (plateau hours — shape factor = 1.0).
var noon = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) // Monday

// ————————————————————————————————————————————————————————————————————————————
// Tests
// ————————————————————————————————————————————————————————————————————————————

// TestSeriesInventory verifies that each configured check emits the full set of
// required metric series (5 scalars + histogram family + sm_check_info).
func TestSeriesInventory(t *testing.T) {
	c := buildConstruct(t, twoChecks())
	eng := shape.New("", nil) // no incidents
	series, _ := c.build(noon, eng)

	requiredPerCheck := []string{
		"probe_success",
		"probe_duration_seconds",
		"probe_all_success_count",
		"probe_all_success_sum",
		"probe_all_duration_seconds_count",
		"probe_all_duration_seconds_sum",
		"probe_all_duration_seconds_bucket",
		"sm_check_info",
	}

	for _, job := range []string{"api-health", "gateway-health"} {
		for _, want := range requiredPerCheck {
			found := findSeriesFor(series, want, job)
			// sm_check_info has different labels set — check by name only, no job filter needed
			if want == "sm_check_info" {
				all := findSeries(series, want)
				matched := false
				for _, s := range all {
					if s.Labels["job"] == job {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("sm_check_info missing for job=%q", job)
				}
				continue
			}
			if len(found) == 0 {
				t.Errorf("missing series %q for job=%q", want, job)
			}
		}
	}
}

// TestBaseLabels verifies that every emitted series carries {job, instance, probe,
// config_version} — the SM app join key.
func TestBaseLabels(t *testing.T) {
	c := buildConstruct(t, twoChecks())
	series, _ := c.build(noon, shape.New("", nil))

	for _, s := range series {
		// sm_check_info uses info labels (superset of base); skip it for the
		// minimal base-label check.
		if s.Name == "sm_check_info" {
			continue
		}
		if s.Name == "probe_all_duration_seconds_bucket" {
			// bucket has an extra `le` label — base set still present
		}
		for _, k := range []string{"job", "instance", "probe", "config_version"} {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("series %q missing base label %q (labels: %v)", s.Name, k, s.Labels)
			}
		}
	}
}

// TestSMCheckInfoLabels verifies sm_check_info carries the full metadata label set.
func TestSMCheckInfoLabels(t *testing.T) {
	c := buildConstruct(t, &Config{
		Checks: []CheckConfig{{Name: "api-health", Target: "https://api.example.com/health"}},
	})
	series, _ := c.build(noon, shape.New("", nil))

	var info *promrw.Series
	for i := range series {
		if series[i].Name == "sm_check_info" {
			info = &series[i]
			break
		}
	}
	if info == nil {
		t.Fatal("sm_check_info not found")
	}
	if info.Value != 1.0 {
		t.Errorf("sm_check_info value = %v, want 1", info.Value)
	}
	for _, k := range []string{
		"job", "instance", "probe", "config_version",
		"check_name", "region", "frequency", "geohash", "alert_sensitivity",
	} {
		if _, ok := info.Labels[k]; !ok {
			t.Errorf("sm_check_info missing label %q", k)
		}
	}
	if info.Labels["check_name"] != "http" {
		t.Errorf("check_name = %q, want http", info.Labels["check_name"])
	}
	if info.Labels["region"] != defaultProbeRegion {
		t.Errorf("region = %q, want %q", info.Labels["region"], defaultProbeRegion)
	}
	if info.Labels["geohash"] != defaultProbeGeohash {
		t.Errorf("geohash = %q, want %q", info.Labels["geohash"], defaultProbeGeohash)
	}
	if info.Labels["frequency"] != "60000" {
		t.Errorf("frequency = %q, want 60000", info.Labels["frequency"])
	}
}

// TestConfigVersionStabilityAcrossTicks verifies that config_version is identical
// on sm_check_info and the probe_all_* series across multiple ticks (the join key
// must never drift).
func TestConfigVersionStabilityAcrossTicks(t *testing.T) {
	cfg := &Config{Checks: []CheckConfig{{Name: "api-health"}}}
	fx := &fixture.Set{Seed: "demo"}

	build1, _ := Build(cfg, fx)
	build2, _ := Build(cfg, fx)

	c1 := build1.(*Construct)
	c2 := build2.(*Construct)
	eng := shape.New("", nil)

	t1 := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 15, 9, 1, 0, 0, time.UTC)

	s1, _ := c1.build(t1, eng)
	s2, _ := c2.build(t2, eng)

	cv1 := versionFromBatch(t, s1, "api-health")
	cv2 := versionFromBatch(t, s2, "api-health")
	if cv1 != cv2 {
		t.Errorf("config_version unstable across ticks: tick1=%q tick2=%q", cv1, cv2)
	}

	// Also verify it is consistent across ALL series in one tick.
	for _, s := range s1 {
		if s.Name == "probe_all_duration_seconds_bucket" {
			continue // bucket also has `le`, still has config_version
		}
		cv, ok := s.Labels["config_version"]
		if !ok {
			continue // sm_check_info has it; bucket-less series don't have it in the loop — already checked above
		}
		if cv != cv1 {
			t.Errorf("series %q config_version=%q != %q", s.Name, cv, cv1)
		}
	}
}

func versionFromBatch(t *testing.T, batch []promrw.Series, job string) string {
	t.Helper()
	for _, s := range batch {
		if s.Labels["job"] == job {
			if cv, ok := s.Labels["config_version"]; ok {
				return cv
			}
		}
	}
	t.Fatalf("no series with config_version found for job=%q", job)
	return ""
}

// TestSeedDifferentProduceDifferentConfigVersion ensures the deterministic hash
// varies with the seed (two different blueprints must not collide).
func TestSeedDifferentProduceDifferentConfigVersion(t *testing.T) {
	cv1 := configVersion("blueprint-a", "api-health")
	cv2 := configVersion("blueprint-b", "api-health")
	if cv1 == cv2 {
		t.Errorf("different seeds produced identical config_version %q", cv1)
	}
}

// TestConfigVersionIsDecimalInteger verifies config_version looks like a uint64
// decimal (the SM app join key shape).
func TestConfigVersionIsDecimalInteger(t *testing.T) {
	cv := configVersion("test", "my-check")
	if cv == "" {
		t.Fatal("config_version is empty")
	}
	for _, ch := range cv {
		if ch < '0' || ch > '9' {
			t.Errorf("config_version %q contains non-digit %q", cv, string(ch))
		}
	}
}

// TestCountersMonotone verifies probe_all_success_count and
// probe_all_duration_seconds_count are monotonically non-decreasing across ticks.
func TestCountersMonotone(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	eng := shape.New("", nil)

	var prevSuccessCount, prevDurCount float64
	for tick := range 5 {
		now := noon.Add(time.Duration(tick) * time.Minute)
		series, _ := c.build(now, eng)

		sc := seriesValue(t, series, "probe_all_success_count", "api-health")
		dc := seriesValue(t, series, "probe_all_duration_seconds_count", "api-health")

		if sc < prevSuccessCount {
			t.Errorf("tick %d: probe_all_success_count not monotone: %v < %v", tick, sc, prevSuccessCount)
		}
		if dc < prevDurCount {
			t.Errorf("tick %d: probe_all_duration_seconds_count not monotone: %v < %v", tick, dc, prevDurCount)
		}
		prevSuccessCount = sc
		prevDurCount = dc
	}
}

func seriesValue(t *testing.T, batch []promrw.Series, name, job string) float64 {
	t.Helper()
	for _, s := range batch {
		if s.Name == name && s.Labels["job"] == job {
			return s.Value
		}
	}
	t.Fatalf("series %q for job=%q not found", name, job)
	return 0
}

// TestHistogramLEStyle verifies that bucket le labels use LEBare format (minimal
// decimals, no forced ".0" on integer bounds — matching the predecessor's SMProbeDurationBuckets).
func TestHistogramLEStyle(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	series, _ := c.build(noon, shape.New("", nil))

	// Collect all `le` values from probe_all_duration_seconds_bucket.
	var les []string
	for _, s := range series {
		if s.Name == "probe_all_duration_seconds_bucket" {
			if le, ok := s.Labels["le"]; ok {
				les = append(les, le)
			}
		}
	}
	if len(les) == 0 {
		t.Fatal("no probe_all_duration_seconds_bucket series found")
	}

	// Expected LEBare rendering of smProbeDurationBuckets + +Inf:
	// [0.1, 0.25, 0.5, 1, 2.5, 5, 10] → "0.1","0.25","0.5","1","2.5","5","10","+Inf"
	wantContains := []string{"0.1", "0.25", "0.5", "1", "2.5", "5", "10", "+Inf"}
	leSet := make(map[string]bool, len(les))
	for _, l := range les {
		leSet[l] = true
	}
	for _, want := range wantContains {
		if !leSet[want] {
			t.Errorf("bucket le=%q not found; got %v", want, les)
		}
	}

	// LEBare must NOT have ".0" suffixes on integer bounds.
	for _, le := range les {
		if le == "+Inf" {
			continue
		}
		if strings.HasSuffix(le, ".0") && !strings.Contains(le[:len(le)-2], ".") {
			t.Errorf("LEBare bucket le=%q has forced .0 suffix (want LEBare, not LEDotZero)", le)
		}
	}

	// Verify the count matches expected boundaries + +Inf.
	if len(les) != len(smProbeDurationBuckets)+1 {
		t.Errorf("bucket count = %d, want %d", len(les), len(smProbeDurationBuckets)+1)
	}
}

// TestLogStreamLabels verifies the exact Loki stream label set per the extract §1.7.
func TestLogStreamLabels(t *testing.T) {
	c := buildConstruct(t, &Config{
		Checks: []CheckConfig{{Name: "api-health", Target: "https://api.example.com/health"}},
	})
	eng := shape.New("", nil)
	// The probe outcome is a ~2% background failure draw (smFailureRate) off the global,
	// unseedable rand (shape.go:41), so a single tick is occasionally unhealthy. Retry until a
	// HEALTHY tick so the success-path log shape is asserted deterministically (the failure-path
	// shape has its own test). Healthy is ~98% of ticks → found in a couple of tries.
	_, streams := c.build(noon, eng)
	for range 500 {
		if len(streams) == 1 && streams[0].Labels["probe_success"] == "1" {
			break
		}
		_, streams = c.build(noon, eng)
	}
	if len(streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(streams))
	}
	st := streams[0]
	if st.Labels["probe_success"] != "1" {
		t.Fatal("no healthy probe tick found in 500 attempts")
	}

	requiredStreamLabels := []string{
		"source", "check_name", "instance", "job", "probe", "region", "probe_success",
	}
	for _, k := range requiredStreamLabels {
		if _, ok := st.Labels[k]; !ok {
			t.Errorf("log stream missing label %q", k)
		}
	}
	if st.Labels["source"] != "synthetic-monitoring-agent" {
		t.Errorf("source = %q, want synthetic-monitoring-agent", st.Labels["source"])
	}
	if st.Labels["check_name"] != "http" {
		t.Errorf("check_name = %q, want http", st.Labels["check_name"])
	}
	if st.Labels["probe_success"] != "1" {
		t.Errorf("probe_success = %q, want 1 (healthy tick)", st.Labels["probe_success"])
	}
	// Log body must contain duration_seconds and msg.
	if len(st.Lines) == 0 {
		t.Fatal("stream has no log lines")
	}
	body := st.Lines[0].Body
	if !strings.Contains(body, "duration_seconds=") {
		t.Errorf("log body missing duration_seconds: %q", body)
	}
	if !strings.Contains(body, `msg="Check succeeded"`) {
		t.Errorf("log body missing expected msg: %q", body)
	}
	if !strings.Contains(body, "source=synthetic-monitoring-agent") {
		t.Errorf("log body missing source field: %q", body)
	}
}

// TestFailurePath verifies that when an sm_probe_failure incident is scheduled,
// probe_success → 0, duration → smTimeoutSeconds, log body → "Check failed",
// and the Loki stream label probe_success → "0".
func TestFailurePath(t *testing.T) {
	c := buildConstruct(t, &Config{
		Checks: []CheckConfig{{Name: "api-health", Target: "https://api.example.com/health"}},
	})

	// Schedule a guaranteed failure window covering `noon`.
	eng := shape.New("UTC", []string{
		"sm_probe_failure@2026-06-15T12:00:00Z/1h",
	})

	series, streams := c.build(noon, eng)

	// probe_success must be 0.
	ps := seriesValue(t, series, "probe_success", "api-health")
	if ps != 0.0 {
		t.Errorf("probe_success under failure = %v, want 0", ps)
	}

	// probe_duration_seconds must equal smTimeoutSeconds.
	pd := seriesValue(t, series, "probe_duration_seconds", "api-health")
	if pd != smTimeoutSeconds {
		t.Errorf("probe_duration_seconds under failure = %v, want %v", pd, smTimeoutSeconds)
	}

	// Loki stream must have probe_success="0" and "Check failed" in body.
	if len(streams) == 0 {
		t.Fatal("no Loki streams emitted")
	}
	st := streams[0]
	if st.Labels["probe_success"] != "0" {
		t.Errorf("stream label probe_success = %q, want 0", st.Labels["probe_success"])
	}
	if len(st.Lines) == 0 {
		t.Fatal("stream has no log lines")
	}
	if !strings.Contains(st.Lines[0].Body, `msg="Check failed"`) {
		t.Errorf("failure log body = %q, want msg=Check failed", st.Lines[0].Body)
	}
}

// TestDefaults verifies that omitted config fields are substituted with the expected
// defaults: target → "https://<name>.example.com/health", frequency → 60000,
// probe → "synthkit-private", region → "EMEA".
func TestDefaults(t *testing.T) {
	fx := &fixture.Set{Seed: "test"}
	c, err := Build(&Config{Checks: []CheckConfig{{Name: "my-service"}}}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rc := c.(*Construct).checks[0]
	if rc.target != "https://my-service.example.com/health" {
		t.Errorf("target = %q", rc.target)
	}
	if rc.frequencyMs != 60000 {
		t.Errorf("frequencyMs = %d", rc.frequencyMs)
	}
	if rc.probe != "synthkit-private" {
		t.Errorf("probe = %q", rc.probe)
	}
	if rc.region != defaultProbeRegion {
		t.Errorf("region = %q", rc.region)
	}
}

// TestTickIntegration is a full round-trip test via core.Construct.Tick using
// coretest.World capture sinks.
func TestTickIntegration(t *testing.T) {
	cfg := &Config{Checks: []CheckConfig{{Name: "api-health"}, {Name: "db-health"}}}
	fx := &fixture.Set{Seed: "demo"}
	cons, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := worldWith(mc, lc)

	if err := cons.Tick(context.Background(), noon, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Two checks → two log streams.
	if len(lc.Streams) != 2 {
		t.Errorf("expected 2 log streams, got %d", len(lc.Streams))
	}

	// Must have probe_success for each check.
	for _, job := range []string{"api-health", "db-health"} {
		found := mc.Find("probe_success")
		matched := false
		for _, s := range found {
			if s.Labels["job"] == job {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("probe_success missing for job=%q", job)
		}
	}
}

// TestKindAndSignals verifies the static Construct metadata.
func TestKindAndSignals(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	if c.Kind() != Kind {
		t.Errorf("Kind() = %q, want %q", c.Kind(), Kind)
	}
	sigs := c.Signals()
	if len(sigs) != 2 {
		t.Fatalf("Signals() = %v, want [Metrics, Logs]", sigs)
	}
	if sigs[0] != core.Metrics || sigs[1] != core.Logs {
		t.Errorf("Signals() = %v, want [Metrics, Logs]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval() = %v, want 60s", c.Interval())
	}
}

// TestBuildErrors verifies Build rejects bad inputs.
func TestBuildErrors(t *testing.T) {
	if _, err := Build("not a config", nil); err == nil {
		t.Error("expected error for wrong type")
	}
	if _, err := Build(&Config{Checks: nil}, nil); err == nil {
		t.Error("expected error for empty checks")
	}
}

// TestNoSummaryBuckets verifies probe_all_success has no _bucket series (it's a
// summary / counter pair, not a histogram — trap T3 in the extract).
func TestNoSummaryBuckets(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	series, _ := c.build(noon, shape.New("", nil))
	for _, s := range series {
		if s.Name == "probe_all_success_bucket" {
			t.Errorf("probe_all_success_bucket should NOT exist (it's a summary, not histogram): %v", s)
		}
	}
}

// TestProbeAllSuccessSumMonotone verifies probe_all_success_sum is non-decreasing.
// Under a healthy run it increments by 1 each tick; its value must be ≥ previous.
func TestProbeAllSuccessSumMonotone(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	eng := shape.New("", nil)
	var prev float64
	for tick := range 5 {
		now := noon.Add(time.Duration(tick) * time.Minute)
		series, _ := c.build(now, eng)
		v := seriesValue(t, series, "probe_all_success_sum", "api-health")
		if v < prev {
			t.Errorf("tick %d: probe_all_success_sum not monotone: %v < %v", tick, v, prev)
		}
		prev = v
	}
}

// TestHistogramNoBucketForProbeAllSuccess verifies the histogram family is only on
// probe_all_duration_seconds (not probe_all_success — trap T3).
func TestHistogramNoBucketForProbeAllSuccess(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	series, _ := c.build(noon, shape.New("", nil))

	for _, s := range series {
		if strings.HasPrefix(s.Name, "probe_all_success") && strings.HasSuffix(s.Name, "_bucket") {
			t.Errorf("unexpected bucket series %q (probe_all_success is a summary, not histogram)", s.Name)
		}
	}
}

// ————————————————————————————————————————————————————————————————————————————
// User-label tests (SK-26)
// ————————————————————————————————————————————————————————————————————————————

// checkWithLabels returns a Config with one check carrying user labels {team:"payments", tier:"gold"}.
func checkWithLabels() *Config {
	return &Config{
		Checks: []CheckConfig{
			{
				Name:   "api-health",
				Target: "https://api.example.com/health",
				Labels: map[string]string{"team": "payments", "tier": "gold"},
			},
		},
	}
}

// TestUserLabelsOnMetrics verifies that user labels appear as label_<k>=<v> on
// every probe_* metric series.
func TestUserLabelsOnMetrics(t *testing.T) {
	c := buildConstruct(t, checkWithLabels())
	series, _ := c.build(noon, shape.New("", nil))

	want := map[string]string{"label_team": "payments", "label_tier": "gold"}
	for _, s := range series {
		if s.Name == "sm_check_info" {
			continue // tested separately
		}
		for k, v := range want {
			if got, ok := s.Labels[k]; !ok {
				t.Errorf("series %q missing user label %q", s.Name, k)
			} else if got != v {
				t.Errorf("series %q label %q = %q, want %q", s.Name, k, got, v)
			}
		}
	}
}

// TestUserLabelsOnCheckInfo verifies that user labels appear on sm_check_info.
func TestUserLabelsOnCheckInfo(t *testing.T) {
	c := buildConstruct(t, checkWithLabels())
	series, _ := c.build(noon, shape.New("", nil))

	var info *promrw.Series
	for i := range series {
		if series[i].Name == "sm_check_info" {
			info = &series[i]
			break
		}
	}
	if info == nil {
		t.Fatal("sm_check_info not found")
	}
	want := map[string]string{"label_team": "payments", "label_tier": "gold"}
	for k, v := range want {
		if got, ok := info.Labels[k]; !ok {
			t.Errorf("sm_check_info missing user label %q", k)
		} else if got != v {
			t.Errorf("sm_check_info label %q = %q, want %q", k, got, v)
		}
	}
}

// TestUserLabelsOnLokiStream verifies that user labels appear on the Loki stream labels.
func TestUserLabelsOnLokiStream(t *testing.T) {
	c := buildConstruct(t, checkWithLabels())
	_, streams := c.build(noon, shape.New("", nil))

	if len(streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(streams))
	}
	want := map[string]string{"label_team": "payments", "label_tier": "gold"}
	for k, v := range want {
		if got, ok := streams[0].Labels[k]; !ok {
			t.Errorf("Loki stream missing user label %q", k)
		} else if got != v {
			t.Errorf("Loki stream label %q = %q, want %q", k, got, v)
		}
	}
}

// TestNoUserLabelsUnchanged verifies that a check with no labels: field produces no
// label_* keys anywhere — output is byte-identical to the baseline.
func TestNoUserLabelsUnchanged(t *testing.T) {
	c := buildConstruct(t, twoChecks())
	series, streams := c.build(noon, shape.New("", nil))

	for _, s := range series {
		for k := range s.Labels {
			if strings.HasPrefix(k, "label_") {
				t.Errorf("series %q has unexpected label_ key %q (no user labels declared)", s.Name, k)
			}
		}
	}
	for _, st := range streams {
		for k := range st.Labels {
			if strings.HasPrefix(k, "label_") {
				t.Errorf("Loki stream has unexpected label_ key %q (no user labels declared)", k)
			}
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Per-series value variation tests
// ────────────────────────────────────────────────────────────────────────────

// multiCheckCfg returns a Config with five named checks so we have enough peer
// series to verify that probe_duration_seconds values are distinct.
func multiCheckCfg() *Config {
	return &Config{
		Checks: []CheckConfig{
			{Name: "api-health"},
			{Name: "gateway-health"},
			{Name: "db-health"},
			{Name: "auth-health"},
			{Name: "payments-health"},
		},
	}
}

// TestPeerSeriesEmitDistinctDurations verifies that probe_duration_seconds emits
// DISTINCT values across peer checks (not all lockstep-identical).
func TestPeerSeriesEmitDistinctDurations(t *testing.T) {
	c := buildConstruct(t, multiCheckCfg())
	eng := shape.New("", nil)

	// Find a tick where all 5 checks are healthy so durations are in the expected range.
	var series []promrw.Series
	for attempt := 0; attempt < 200; attempt++ {
		series, _ = c.build(noon, eng)
		allHealthy := true
		for _, s := range series {
			if s.Name == "probe_success" && s.Value != 1.0 {
				allHealthy = false
				break
			}
		}
		if allHealthy {
			break
		}
	}

	// Collect probe_duration_seconds values per check.
	durs := map[string]float64{}
	for _, s := range series {
		if s.Name == "probe_duration_seconds" {
			durs[s.Labels["job"]] = s.Value
		}
	}
	if len(durs) < 5 {
		t.Fatalf("expected 5 probe_duration_seconds series, got %d", len(durs))
	}

	// All 5 peer series must have distinct values (no two identical).
	seen := map[float64]string{}
	for job, v := range durs {
		if prev, ok := seen[v]; ok {
			t.Errorf("probe_duration_seconds: jobs %q and %q emit identical value %.9f (lockstep)", prev, job, v)
		}
		seen[v] = job
	}
}

// TestDurationFamilyDriftsOverTime verifies that probe_duration_seconds takes ≥5
// distinct values across 30 ticks at 13-minute steps (Wander is active, not frozen).
func TestDurationFamilyDriftsOverTime(t *testing.T) {
	c := buildConstruct(t, &Config{Checks: []CheckConfig{{Name: "api-health"}}})
	eng := shape.New("", nil)

	seen := map[float64]bool{}
	for i := 0; i < 30; i++ {
		tick := noon.Add(time.Duration(i) * 13 * time.Minute)
		series, _ := c.build(tick, eng)
		for _, s := range series {
			if s.Name == "probe_duration_seconds" && s.Labels["job"] == "api-health" && s.Value > 0 {
				seen[s.Value] = true
			}
		}
	}
	if len(seen) < 5 {
		t.Errorf("probe_duration_seconds: only %d distinct values across 30 ticks — series is near-frozen (want ≥5)", len(seen))
	}
}

// TestStateLEBareStyle is a low-level invariant check: the histogram in state must
// use LEBare, so when state emits "1" for an integer bound it must NOT be "1.0".
func TestStateLEBareStyle(t *testing.T) {
	st := state.NewState()
	base := map[string]string{"job": "test", "instance": "x", "probe": "p", "config_version": "1"}
	st.Observe("probe_all_duration_seconds", base, smProbeDurationBuckets, state.LEBare, 0.8)
	for _, s := range st.Collect(time.Now()) {
		if s.Name != "probe_all_duration_seconds_bucket" {
			continue
		}
		le := s.Labels["le"]
		if le == "+Inf" {
			continue
		}
		// No LEBare bound from smProbeDurationBuckets should carry ".0" suffix on integer
		if strings.HasSuffix(le, ".0") {
			t.Errorf("LEBare bucket le=%q has .0 suffix — should not", le)
		}
		// "1" not "1.0", "5" not "5.0", "10" not "10.0"
		if le == "1.0" || le == "5.0" || le == "10.0" {
			t.Errorf("LEBare emitted LEDotZero format: le=%q", le)
		}
	}
}
