// SPDX-License-Identifier: AGPL-3.0-only
package redshift

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func ptr[T any](v T) *T { return &v }
func config() *Config {
	return &Config{Clusters: []Cluster{{Name: "warehouse", CollectorName: "collector", Snapshot: &Snapshot{CPUPercent: ptr(12.5), DatabaseConnections: ptr(4.0), Healthy: ptr(true), Maintenance: ptr(false), DiskUsedPercent: ptr(25.0), TotalTables: ptr(7.0)}, ReadIO: &IO{OperationsPerSecond: 0.5, LatencySeconds: ptr(0.02)}, WriteIO: &IO{OperationsPerSecond: 0.25, LatencySeconds: ptr(0.04)}, WLMQueues: []WLMQueue{{Name: "interactive", Waiting: ptr(0.0), Running: ptr(2.0)}}}}}
}
func fixtures() *fixture.Set {
	return &fixture.Set{Cloud: &fixture.Cloud{AccountID: "123456789012", Region: "eu-west-1"}}
}
func TestBuildTickInventory(t *testing.T) {
	c, err := Build(config(), fixtures())
	if err != nil {
		t.Fatal(err)
	}
	capture := &coretest.MetricCapture{}
	if err = c.Tick(context.Background(), time.Unix(1000, 0), &core.World{Metrics: capture}); err != nil {
		t.Fatal(err)
	}
	// The hand-selected ten-base allowlist, not a name generator or implementation inventory.
	expected := map[string][5]float64{
		"aws_redshift_cpuutilization":             {12.5, 12.5, 12.5, 12.5, 1},
		"aws_redshift_database_connections":       {4, 4, 4, 4, 1},
		"aws_redshift_health_status":              {1, 1, 1, 1, 1},
		"aws_redshift_maintenance_mode":           {0, 0, 0, 0, 1},
		"aws_redshift_percentage_disk_space_used": {25, 25, 25, 25, 1},
		"aws_redshift_total_table_count":          {7, 7, 7, 7, 1},
		"aws_redshift_read_latency":               {0.6, 0.02, 0.02, 0.02, 30},
		"aws_redshift_write_latency":              {0.6, 0.04, 0.04, 0.04, 15},
		"aws_redshift_wlmqueue_length":            {0, 0, 0, 0, 1},
		"aws_redshift_wlmrunning_queries":         {2, 2, 2, 2, 1},
	}
	if len(capture.All()) != 50 {
		t.Fatalf("got %d series, want exact 50", len(capture.All()))
	}
	for base, values := range expected {
		for i, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
			samples := capture.Find(base + suffix)
			if len(samples) != 1 {
				t.Fatalf("missing %s%s", base, suffix)
			}
			s := samples[0]
			if s.Kind != promrw.KindGauge {
				t.Fatalf("%s is not a period gauge", s.Name)
			}
			if s.Value != values[i] {
				t.Errorf("%s%s=%g want %g", base, suffix, s.Value, values[i])
			}
			want := 6
			if base == "aws_redshift_wlmqueue_length" || base == "aws_redshift_wlmrunning_queries" {
				want = 7
				if s.Labels["dimension_QueueName"] != "interactive" {
					t.Fatal(s.Labels)
				}
			}
			if len(s.Labels) != want || s.Labels["account_id"] != "123456789012" || s.Labels["region"] != "eu-west-1" || s.Labels["namespace"] != "AWS/Redshift" || s.Labels["job"] != "cloud/aws/redshift" || s.Labels["name"] != "collector" || s.Labels["dimension_ClusterIdentifier"] != "warehouse" {
				t.Fatalf("unexpected labels: %v", s.Labels)
			}
		}
	}
}

// All temporal assertions observe actual Tick output, never private model state.
func TestSparsePauseAndGaps(t *testing.T) {
	for _, gap := range []time.Duration{60*time.Second + time.Nanosecond, 61 * time.Second, 24 * time.Hour} {
		t.Run(gap.String(), func(t *testing.T) {
			cfg := config()
			cfg.Clusters[0].ReadIO.OperationsPerSecond = 1.0 / 128
			cfg.Clusters[0].WriteIO = nil
			c, err := Build(cfg, fixtures())
			if err != nil {
				t.Fatal(err)
			}
			cap := &coretest.MetricCapture{}
			eng := &shape.Engine{}
			w := &core.World{Metrics: cap, Shape: eng}
			now := time.Unix(1000, 0)
			tick := func(at time.Time) {
				t.Helper()
				if err := c.Tick(context.Background(), at, w); err != nil {
					t.Fatal(err)
				}
			}
			tick(now) // .46875 earned, no latency
			now = now.Add(gap)
			tick(now) // .9375; must not cap away previous carry
			if len(cap.Find("aws_redshift_read_latency_sum")) != 0 {
				t.Fatal("fabricated subthreshold I/O")
			}
			before := len(cap.Batches)
			tick(now)
			tick(now.Add(-time.Second))
			if len(cap.Batches) != before {
				t.Fatal("duplicate/backward batch")
			}
			eng.SetVolumeMultiplier(0)
			now = now.Add(time.Second)
			tick(now)
			now = now.Add(time.Hour)
			tick(now)
			if len(cap.Batches) != before {
				t.Fatal("master pause emitted stock or I/O")
			}
			eng.SetVolumeMultiplier(3)
			now = now.Add(time.Hour)
			tick(now)
			if len(cap.Batches) != before {
				t.Fatal("first positive boundary emitted")
			}
			now = now.Add(8 * time.Second)
			tick(now) // exact 1 observation, preserved pre-pause .9375
			got := cap.Find("aws_redshift_read_latency_sample_count")
			if len(got) != 1 || got[0].Value != 1 {
				t.Fatalf("carry lost or pause/gap backfill: %v", got)
			}
			now = now.Add(time.Nanosecond)
			tick(now)
			if len(cap.Find("aws_redshift_read_latency_sum")) != 1 {
				t.Fatal("stale sparse latency re-emitted")
			}
		})
	}
}

func TestInitialPauseAndJitter(t *testing.T) {
	cfg := config()
	cfg.Clusters[0].ReadIO.OperationsPerSecond = 0.125
	cfg.Clusters[0].WriteIO = nil
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	cap := &coretest.MetricCapture{}
	eng := &shape.Engine{}
	eng.SetVolumeMultiplier(0)
	w := &core.World{Metrics: cap, Shape: eng}
	now := time.Unix(1000, 0)
	tick := func(d time.Duration) {
		t.Helper()
		now = now.Add(d)
		if err := c.Tick(context.Background(), now, w); err != nil {
			t.Fatal(err)
		}
	}
	tick(0)
	eng.SetVolumeMultiplier(1)
	tick(time.Hour)
	if len(cap.Batches) != 0 {
		t.Fatal("initial pause/resume bootstrapped activity")
	}
	// Exact 8-second window split into nonuniform nanosecond intervals earns one.
	for _, d := range []time.Duration{time.Nanosecond, 3 * time.Second, 5*time.Second - 2*time.Nanosecond} {
		tick(d)
	}
	if len(cap.Find("aws_redshift_read_latency_sum")) != 0 {
		t.Fatal("epsilon fabricated event just below boundary")
	}
	tick(time.Nanosecond)
	got := cap.Find("aws_redshift_read_latency_sample_count")
	if len(got) != 1 || got[0].Value != 1 {
		t.Fatal(got)
	}
}

func TestOwnedCopyAndRegistration(t *testing.T) {
	cfg := config()
	fx := fixtures()
	r := Registration()
	if r.Kind != "redshift" || r.Scope != core.ScopeBlueprint || reflect.TypeOf(r.NewConfig()) != reflect.TypeOf(cfg) {
		t.Fatal("registration ABI")
	}
	c, err := r.Build(cfg, fx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Clusters[0].Name = "mutated"
	cfg.Clusters[0].CollectorName = "global"
	*cfg.Clusters[0].Snapshot.CPUPercent = 999
	*cfg.Clusters[0].Snapshot.Healthy = false
	*cfg.Clusters[0].ReadIO.LatencySeconds = 999
	cfg.Clusters[0].ReadIO.OperationsPerSecond = 999
	cfg.Clusters[0].WLMQueues[0].Name = "mutated"
	*cfg.Clusters[0].WLMQueues[0].Running = 999
	fx.Cloud.AccountID = "mutated"
	fx.Cloud.Region = "mutated"
	cap := &coretest.MetricCapture{}
	if err = c.Tick(context.Background(), time.Unix(1000, 0), &core.World{Metrics: cap}); err != nil {
		t.Fatal(err)
	}
	for _, s := range cap.All() {
		if s.Labels["dimension_ClusterIdentifier"] != "warehouse" || s.Labels["name"] != "collector" || s.Labels["account_id"] != "123456789012" || s.Labels["region"] != "eu-west-1" {
			t.Fatal(s)
		}
	}
	for name, value := range map[string]float64{"aws_redshift_cpuutilization_sum": 12.5, "aws_redshift_health_status_sum": 1, "aws_redshift_read_latency_sum": 0.6, "aws_redshift_wlmrunning_queries_sum": 2} {
		if got := cap.Find(name); len(got) != 1 || got[0].Value != value {
			t.Fatal(name, got)
		}
	}
	// Suffix labels must not alias each other or the next observation's identity.
	cap.Batches[0][0].Labels["name"] = "poison"
	if err = c.Tick(context.Background(), time.Unix(1060, 0), &core.World{Metrics: cap}); err != nil {
		t.Fatal(err)
	}
	for _, s := range cap.Batches[1] {
		if s.Labels["name"] != "collector" {
			t.Fatal("writer mutation corrupted model")
		}
	}
}

func TestValidation(t *testing.T) {
	mutations := map[string]func(*Config){
		"empty":               func(c *Config) { c.Clusters = nil },
		"too many":            func(c *Config) { c.Clusters = make([]Cluster, 101) },
		"missing snapshot":    func(c *Config) { c.Clusters[0].Snapshot = nil },
		"missing cpu":         func(c *Config) { c.Clusters[0].Snapshot.CPUPercent = nil },
		"missing connections": func(c *Config) { c.Clusters[0].Snapshot.DatabaseConnections = nil },
		"missing health":      func(c *Config) { c.Clusters[0].Snapshot.Healthy = nil },
		"missing maintenance": func(c *Config) { c.Clusters[0].Snapshot.Maintenance = nil },
		"missing disk":        func(c *Config) { c.Clusters[0].Snapshot.DiskUsedPercent = nil },
		"missing tables":      func(c *Config) { c.Clusters[0].Snapshot.TotalTables = nil },
		"fractional count":    func(c *Config) { c.Clusters[0].Snapshot.TotalTables = ptr(0.5) },
		"negative count":      func(c *Config) { c.Clusters[0].Snapshot.DatabaseConnections = ptr(-1.0) },
		"count bound":         func(c *Config) { c.Clusters[0].Snapshot.TotalTables = ptr(1e9 + 1) },
		"percent bound":       func(c *Config) { c.Clusters[0].Snapshot.DiskUsedPercent = ptr(100.1) },
		"nan":                 func(c *Config) { c.Clusters[0].Snapshot.CPUPercent = ptr(math.NaN()) },
		"inf rate":            func(c *Config) { c.Clusters[0].ReadIO.OperationsPerSecond = math.Inf(1) },
		"negative rate":       func(c *Config) { c.Clusters[0].ReadIO.OperationsPerSecond = -1 },
		"rate bound":          func(c *Config) { c.Clusters[0].ReadIO.OperationsPerSecond = 1e6 + 1 },
		"latency missing":     func(c *Config) { c.Clusters[0].ReadIO.LatencySeconds = nil },
		"latency zero":        func(c *Config) { c.Clusters[0].WriteIO.LatencySeconds = ptr(0.0) },
		"latency bound":       func(c *Config) { c.Clusters[0].WriteIO.LatencySeconds = ptr(3600.1) },
		"latency infinity":    func(c *Config) { c.Clusters[0].ReadIO.LatencySeconds = ptr(math.Inf(1)) },
		"padded cluster":      func(c *Config) { c.Clusters[0].Name = " warehouse" },
		"blank collector":     func(c *Config) { c.Clusters[0].CollectorName = " " },
		"padded collector":    func(c *Config) { c.Clusters[0].CollectorName = "collector " },
		"global collector":    func(c *Config) { c.Clusters[0].CollectorName = "global" },
		"duplicate cluster":   func(c *Config) { v := c.Clusters[0]; v.CollectorName = "other"; c.Clusters = append(c.Clusters, v) },
		"duplicate collector": func(c *Config) { v := c.Clusters[0]; v.Name = "other"; c.Clusters = append(c.Clusters, v) },
		"duplicate queue":     func(c *Config) { c.Clusters[0].WLMQueues = append(c.Clusters[0].WLMQueues, c.Clusters[0].WLMQueues[0]) },
		"padded queue":        func(c *Config) { c.Clusters[0].WLMQueues[0].Name = " interactive" },
		"missing waiting":     func(c *Config) { c.Clusters[0].WLMQueues[0].Waiting = nil },
		"missing running":     func(c *Config) { c.Clusters[0].WLMQueues[0].Running = nil },
		"fractional queue":    func(c *Config) { c.Clusters[0].WLMQueues[0].Running = ptr(0.5) },
		"queue bound":         func(c *Config) { c.Clusters[0].WLMQueues[0].Waiting = ptr(1e9 + 1) },
		"queue length":        func(c *Config) { c.Clusters[0].WLMQueues = make([]WLMQueue, 101) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			c := config()
			mutate(c)
			if _, err := Build(c, fixtures()); err == nil {
				t.Fatal("accepted invalid assumption")
			}
		})
	}
	for _, raw := range []any{nil, Config{}, (*Config)(nil)} {
		if _, err := Build(raw, fixtures()); err == nil {
			t.Fatal("accepted wrong config")
		}
	}
	for _, fx := range []*fixture.Set{nil, {}, {Cloud: &fixture.Cloud{}}, {Cloud: &fixture.Cloud{AccountID: "account", Region: "region", CloudWatchExport: "otlp"}}, {Cloud: &fixture.Cloud{AccountID: "account", Region: "region", CloudWatchExport: "other"}}} {
		if _, err := Build(config(), fx); err == nil {
			t.Fatal("accepted invalid cloud")
		}
	}
	// Zero/false explicit fields and optional absence are valid; no implicit I/O.
	c := config()
	s := c.Clusters[0].Snapshot
	s.CPUPercent = ptr(0.0)
	s.DatabaseConnections = ptr(0.0)
	s.Healthy = ptr(false)
	s.DiskUsedPercent = ptr(0.0)
	s.TotalTables = ptr(0.0)
	c.Clusters[0].ReadIO = nil
	c.Clusters[0].WriteIO = nil
	c.Clusters[0].WLMQueues = nil
	built, err := Build(c, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	cap := &coretest.MetricCapture{}
	if err = built.Tick(context.Background(), time.Unix(1000, 0), &core.World{Metrics: cap}); err != nil {
		t.Fatal(err)
	}
	if len(cap.All()) != 30 {
		t.Fatal("optional families invented")
	}
}

type failingWriter struct{}

func (failingWriter) Write(context.Context, []promrw.Series) error {
	return errors.New("writer failure")
}
func TestWriterAndMaximumProducts(t *testing.T) {
	cfg := config()
	cfg.Clusters[0].ReadIO.OperationsPerSecond = 1e6
	cfg.Clusters[0].ReadIO.LatencySeconds = ptr(3600.0)
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	if err = c.Tick(context.Background(), now, nil); err == nil {
		t.Fatal("missing world accepted")
	}
	if err = c.Tick(context.Background(), now, &core.World{}); err == nil {
		t.Fatal("missing writer accepted")
	}
	cap := &coretest.MetricCapture{}
	if err = c.Tick(context.Background(), now, &core.World{Metrics: cap}); err != nil {
		t.Fatal(err)
	}
	for _, s := range cap.All() {
		if math.IsInf(s.Value, 0) || math.IsNaN(s.Value) {
			t.Fatal(s)
		}
	}
	if got := cap.Find("aws_redshift_read_latency_sum"); len(got) != 1 || got[0].Value != 216000000000 {
		t.Fatal(got)
	}
	if got := cap.Find("aws_redshift_read_latency_sample_count"); len(got) != 1 || got[0].Value != 60000000 {
		t.Fatal(got)
	}
	if err = c.Tick(context.Background(), now.Add(time.Minute), &core.World{Metrics: failingWriter{}}); err == nil {
		t.Fatal("writer failure swallowed")
	}
}
