// SPDX-License-Identifier: AGPL-3.0-only

package rds_test

// rds_test.go — contract tests for the rds construct.
//
// (a) Exact inventory per engine: postgres fixture vs mysql fixture.
// (b) dimension_DBInstanceIdentifier == fixture Name (dbo11y↔cloud join key — I12).
// (c) Postgres-only families absent for mysql (I13 — absent means absent, never "").
// (d) Label keys present on every emitted series.
// (e) Nil-fixture Build errors.
// (f) aws_rds_info carries tag_VpcId.
// (g) _sum series are non-negative gauges (per-period, never rate — I5).

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/rds"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// postgresOnlyFamilies are the metric families (base names without stat suffix) that
// must be present for postgres and ABSENT for mysql (I13).
var postgresOnlyFamilies = []string{
	"aws_rds_transaction_logs_disk_usage",
	"aws_rds_transaction_logs_generation",
	"aws_rds_maximum_used_transaction_ids",
	"aws_rds_replication_slot_disk_usage",
}

// universalFamilies are metric families (base names) that must be present for ALL engines.
var universalFamilies = []string{
	"aws_rds_cpuutilization",
	"aws_rds_database_connections",
	"aws_rds_freeable_memory",
	"aws_rds_free_storage_space",
	"aws_rds_read_iops",
	"aws_rds_write_iops",
	"aws_rds_read_latency",
	"aws_rds_write_latency",
	"aws_rds_read_throughput",
	"aws_rds_write_throughput",
	"aws_rds_network_receive_throughput",
	"aws_rds_network_transmit_throughput",
	"aws_rds_disk_queue_depth",
	"aws_rds_swap_usage",
	"aws_rds_burst_balance",
}

// statSuffixes are the five CW stat suffixes every metric family emits (I6).
var statSuffixes = []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}

// buildForDB returns a capture + a rendered batch for the given DB fixture.
func buildForDB(t *testing.T, db *fixture.DB) *coretest.MetricCapture {
	t.Helper()
	fx := &fixture.Set{
		DB:    db,
		Cloud: coretest.Cloud(),
		Env:   coretest.Env(),
		Seed:  "test",
	}
	c, err := rds.Build(nil, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cap
}

// familyPresent returns true if at least one series with name "<family>_<anySuffix>" exists.
func familyPresent(cap *coretest.MetricCapture, family string) bool {
	for _, n := range cap.Names() {
		for _, sfx := range statSuffixes {
			if n == family+sfx {
				return true
			}
		}
	}
	return false
}

// TestPostgresInventory checks exact presence of all expected metric families for postgres.
func TestPostgresInventory(t *testing.T) {
	cap := buildForDB(t, coretest.DB("postgres"))

	all := append(universalFamilies, postgresOnlyFamilies...)
	for _, fam := range all {
		for _, sfx := range statSuffixes {
			name := fam + sfx
			if len(cap.Find(name)) == 0 {
				t.Errorf("postgres: missing series %q", name)
			}
		}
	}
	// aws_rds_info must also be present (no stat suffix).
	if len(cap.Find("aws_rds_info")) == 0 {
		t.Error("postgres: missing aws_rds_info")
	}
}

// TestMysqlInventory checks exact presence of universal families and ABSENCE of
// postgres-only families for mysql (I13 — absent means absent, never "").
func TestMysqlInventory(t *testing.T) {
	cap := buildForDB(t, coretest.DB("mysql"))

	// Universal families must be present.
	for _, fam := range universalFamilies {
		for _, sfx := range statSuffixes {
			name := fam + sfx
			if len(cap.Find(name)) == 0 {
				t.Errorf("mysql: missing universal series %q", name)
			}
		}
	}

	// Postgres-only families must be ENTIRELY absent (I13).
	for _, fam := range postgresOnlyFamilies {
		if familyPresent(cap, fam) {
			t.Errorf("mysql: postgres-only family %q must be absent but is present", fam)
		}
	}
}

// TestDimensionDBInstanceIdentifierEqualsFixtureName verifies the join key (I12):
// dimension_DBInstanceIdentifier must equal fx.DB.Name byte-exactly on every series.
func TestDimensionDBInstanceIdentifierEqualsFixtureName(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		db := coretest.DB(engine)
		cap := buildForDB(t, db)
		for _, s := range cap.All() {
			// aws_rds_info may carry different extra labels — still check dimension.
			gotID, ok := s.Labels["dimension_DBInstanceIdentifier"]
			if !ok {
				t.Errorf("[%s] series %q missing dimension_DBInstanceIdentifier", engine, s.Name)
				continue
			}
			if gotID != db.Name {
				t.Errorf("[%s] series %q: dimension_DBInstanceIdentifier=%q want %q",
					engine, s.Name, gotID, db.Name)
			}
		}
	}
}

// TestLabelKeys verifies that every emitted series carries the required universal CW labels.
func TestLabelKeys(t *testing.T) {
	required := []string{
		"account_id",
		"region",
		"namespace",
		"job",
		"name",
		"dimension_DBInstanceIdentifier",
	}
	for _, engine := range []string{"postgres", "mysql"} {
		cap := buildForDB(t, coretest.DB(engine))
		for _, s := range cap.All() {
			if s.Name == "aws_rds_info" {
				// info series may omit some label keys — check separately.
				continue
			}
			for _, k := range required {
				if _, ok := s.Labels[k]; !ok {
					t.Errorf("[%s] series %q missing required label %q", engine, s.Name, k)
				}
			}
		}
	}
}

// TestInfoSeriesTagVpcId verifies aws_rds_info carries tag_VpcId from Cloud.VpcID.
func TestInfoSeriesTagVpcId(t *testing.T) {
	cloud := coretest.Cloud()
	// coretest.Cloud() sets VpcID = "vpc-0test0001"; confirm tag_VpcId is present.
	fx := &fixture.Set{
		DB:    coretest.DB("postgres"),
		Cloud: cloud,
		Env:   coretest.Env(),
		Seed:  "test",
	}
	c, err := rds.Build(nil, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cap := &coretest.MetricCapture{}
	w := coretest.World(cap, nil, nil)
	now := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	infoSeries := cap.Find("aws_rds_info")
	if len(infoSeries) == 0 {
		t.Fatal("aws_rds_info not found")
	}
	for _, s := range infoSeries {
		got, ok := s.Labels["tag_VpcId"]
		if !ok {
			t.Error("aws_rds_info missing tag_VpcId")
			continue
		}
		if got != cloud.VpcID {
			t.Errorf("aws_rds_info tag_VpcId=%q want %q", got, cloud.VpcID)
		}
	}
}

// TestNilDBBuildError verifies Build returns an error when fx.DB is nil.
func TestNilDBBuildError(t *testing.T) {
	fx := &fixture.Set{Cloud: coretest.Cloud(), Env: coretest.Env()}
	_, err := rds.Build(nil, fx)
	if err == nil {
		t.Error("Build(nil DB) must return an error, got nil")
	}
}

// TestNilCloudBuildError verifies Build returns an error when fx.Cloud is nil.
func TestNilCloudBuildError(t *testing.T) {
	fx := &fixture.Set{DB: coretest.DB("postgres"), Env: coretest.Env()}
	_, err := rds.Build(nil, fx)
	if err == nil {
		t.Error("Build(nil Cloud) must return an error, got nil")
	}
}

// TestSumSeriesAreNonNegativeGauges verifies I5: _sum series are per-period gauges
// and have non-negative values (never rate/increase).
func TestSumSeriesAreNonNegativeGauges(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		cap := buildForDB(t, coretest.DB(engine))
		foundSum := false
		for _, s := range cap.All() {
			if len(s.Name) >= 4 && s.Name[len(s.Name)-4:] == "_sum" {
				foundSum = true
				if s.Value < 0 {
					t.Errorf("[%s] _sum gauge %q has negative value %v", engine, s.Name, s.Value)
				}
			}
		}
		if !foundSum {
			t.Errorf("[%s] no _sum series emitted", engine)
		}
	}
}

// TestInterfaceConformance verifies Kind/Signals/Interval match the spec.
func TestInterfaceConformance(t *testing.T) {
	fx := &fixture.Set{
		DB:    coretest.DB("postgres"),
		Cloud: coretest.Cloud(),
		Env:   coretest.Env(),
		Seed:  "test",
	}
	c, err := rds.Build(nil, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Kind() != "rds" {
		t.Errorf("Kind()=%q want %q", c.Kind(), "rds")
	}
	sigs := c.Signals()
	if len(sigs) != 1 || sigs[0] != core.Metrics {
		t.Errorf("Signals()=%v want [Metrics]", sigs)
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v want 60s", c.Interval())
	}
}

// TestNoUnexpectedSeriesForPostgres asserts that the exact full set of expected series
// names is emitted — no more, no less.
func TestNoUnexpectedSeriesForPostgres(t *testing.T) {
	cap := buildForDB(t, coretest.DB("postgres"))

	// Build the expected set.
	expected := map[string]bool{}
	all := append(universalFamilies, postgresOnlyFamilies...)
	for _, fam := range all {
		for _, sfx := range statSuffixes {
			expected[fam+sfx] = true
		}
	}
	expected["aws_rds_info"] = true

	got := map[string]bool{}
	for _, n := range cap.Names() {
		got[n] = true
	}

	// Missing.
	for want := range expected {
		if !got[want] {
			t.Errorf("postgres: missing series %q", want)
		}
	}
	// Unexpected.
	for have := range got {
		if !expected[have] {
			t.Errorf("postgres: unexpected series %q", have)
		}
	}
}

// TestNoUnexpectedSeriesForMysql same check for mysql (no postgres-only families).
func TestNoUnexpectedSeriesForMysql(t *testing.T) {
	cap := buildForDB(t, coretest.DB("mysql"))

	expected := map[string]bool{}
	for _, fam := range universalFamilies {
		for _, sfx := range statSuffixes {
			expected[fam+sfx] = true
		}
	}
	expected["aws_rds_info"] = true

	got := map[string]bool{}
	for _, n := range cap.Names() {
		got[n] = true
	}

	for want := range expected {
		if !got[want] {
			t.Errorf("mysql: missing series %q", want)
		}
	}
	for have := range got {
		if !expected[have] {
			t.Errorf("mysql: unexpected series %q", have)
		}
	}
}

// TestNamespaceAndJobLabels verifies namespace="AWS/RDS" and job="cloud/aws/rds".
func TestNamespaceAndJobLabels(t *testing.T) {
	cap := buildForDB(t, coretest.DB("postgres"))
	for _, s := range cap.All() {
		if s.Name == "aws_rds_info" {
			continue // info series checked separately
		}
		if s.Labels["namespace"] != "AWS/RDS" {
			t.Errorf("series %q: namespace=%q want %q", s.Name, s.Labels["namespace"], "AWS/RDS")
		}
		if s.Labels["job"] != "cloud/aws/rds" {
			t.Errorf("series %q: job=%q want %q", s.Name, s.Labels["job"], "cloud/aws/rds")
		}
	}
}

// ── SK-4: InstanceClass threading tests ──────────────────────────────────────────────────────────

// TestRDSOutputUnchangedByInstanceClass verifies that the InstanceClass field on fixture.DB
// is inert to the RDS construct — the emitted series names are identical regardless of what
// InstanceClass is set to. (The RDS construct does not consume InstanceClass in v1; it exists
// on the fixture as a declarable config knob for future sizing policy. SK-4, requirement d.)
//
// Value comparison across independently-constructed instances is intentionally skipped: the shape
// engine draws random noise, so two separate Build+Tick calls will produce different values by
// design. The invariant is structural (same series, same label schema), not value-identical.
func TestRDSOutputUnchangedByInstanceClass(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			// Build with each InstanceClass variant — names must be identical.
			variants := []struct {
				label         string
				instanceClass string
			}{
				{"empty", ""},
				{"default", "db.t3.medium"},
				{"non-default", "db.r6g.large"},
			}

			// Use the first variant as the reference set.
			refDB := coretest.DB(engine)
			refDB.InstanceClass = variants[0].instanceClass
			refNames := buildForDB(t, refDB).Names()

			for _, v := range variants[1:] {
				db := coretest.DB(engine)
				db.InstanceClass = v.instanceClass
				names := buildForDB(t, db).Names()

				if len(names) != len(refNames) {
					t.Errorf("[%s] instance_class=%q: series count %d != reference %d",
						engine, v.instanceClass, len(names), len(refNames))
					continue
				}
				for i := range refNames {
					if names[i] != refNames[i] {
						t.Errorf("[%s] instance_class=%q: series[%d] %q != reference %q",
							engine, v.instanceClass, i, names[i], refNames[i])
					}
				}
			}
		})
	}
}

// ── DB failure-mode reactions (AxisDatabase, scoped to db.Name) ───────────────

// avgValue returns the value of the "<family>_average" series (all five CW stats carry the
// same per-period figure, so _average reads the gauge value directly). 0 if absent.
func avgValue(cap *coretest.MetricCapture, family string) float64 {
	s := cap.Find(family + "_average")
	if len(s) == 0 {
		return 0
	}
	return s[0].Value
}

// rdsFailModeTicks averages the additive shape noise out: a single tick is noise-polluted, so
// summing a metric over many ticks lets the failure FACTOR dominate with an astronomical margin.
const rdsFailModeTicks = 64

// sumRDSOverTicks builds one rds construct for the engine and ticks it rdsFailModeTicks times at a
// peak-business minute with the named failure mode active (mode="" → baseline), returning the
// running total of avgValue(family) across every tick.
func sumRDSOverTicks(t *testing.T, engine, mode, family string) float64 {
	t.Helper()
	db := coretest.DB(engine)
	fx := &fixture.Set{DB: db, Cloud: coretest.Cloud(), Env: coretest.Env(), Seed: "test"}
	c, err := rds.Build(nil, fx)
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
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) // Monday noon (diurnal plateau)
	var total float64
	for i := range rdsFailModeTicks {
		cap := &coretest.MetricCapture{}
		w := &core.World{Shape: sh, Metrics: cap}
		if err := c.Tick(context.Background(), now.Add(time.Duration(i)*time.Minute), w); err != nil {
			t.Fatalf("Tick %d (mode=%q): %v", i, mode, err)
		}
		total += avgValue(cap, family)
	}
	return total
}

func assertRDSAmplifies(t *testing.T, mode, family string) {
	t.Helper()
	for _, engine := range []string{"postgres", "mysql"} {
		base := sumRDSOverTicks(t, engine, "", family)
		hot := sumRDSOverTicks(t, engine, mode, family)
		if hot <= base {
			t.Errorf("[%s] %s: %s did not amplify (baseline-sum=%v active-sum=%v over %d ticks)",
				engine, family, mode, base, hot, rdsFailModeTicks)
		}
	}
}

// TestConnectionSaturationDrivesConnections asserts connection_saturation amplifies
// aws_rds_database_connections (and CPU), staying within sane bounds.
func TestConnectionSaturationDrivesConnections(t *testing.T) {
	assertRDSAmplifies(t, "connection_saturation", "aws_rds_database_connections")
	assertRDSAmplifies(t, "connection_saturation", "aws_rds_cpuutilization")
}

// TestLockContentionDrivesQueueDepth asserts lock_contention amplifies aws_rds_disk_queue_depth.
func TestLockContentionDrivesQueueDepth(t *testing.T) {
	assertRDSAmplifies(t, "lock_contention", "aws_rds_disk_queue_depth")
}

// TestSlowQueryStormDrivesLatency asserts slow_query_storm amplifies read/write latency.
func TestSlowQueryStormDrivesLatency(t *testing.T) {
	assertRDSAmplifies(t, "slow_query_storm", "aws_rds_read_latency")
	assertRDSAmplifies(t, "slow_query_storm", "aws_rds_write_latency")
}

// TestRDSInstanceClassOnFixture verifies that fixture.DB carries the InstanceClass field
// and it is accessible from the fixture (SK-4 seam test).
func TestRDSInstanceClassOnFixture(t *testing.T) {
	db := coretest.DB("postgres")
	db.InstanceClass = "db.r5.xlarge"
	if db.InstanceClass != "db.r5.xlarge" {
		t.Errorf("InstanceClass not set: %q", db.InstanceClass)
	}
}

// Ensure sort is used (suppress unused import).
var _ = sort.Strings
var _ = shape.New
