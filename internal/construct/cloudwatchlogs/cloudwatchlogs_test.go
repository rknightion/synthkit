// SPDX-License-Identifier: AGPL-3.0-only

package cloudwatchlogs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

type capture struct {
	batch []promrw.Series
	calls int
	err   error
}

func (w *capture) Write(_ context.Context, b []promrw.Series) error {
	w.batch = b
	w.calls++
	return w.err
}
func ptr(v float64) *float64 { return &v }
func config(rate float64) *Config {
	return &Config{LogGroups: []LogGroup{{Name: "/synthetic/intake", EventsPerSecond: ptr(rate), UncompressedBytesPerEvent: ptr(256)}}}
}
func cloud() *fixture.Set {
	return &fixture.Set{Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "eu-west-1"}}
}
func build(t *testing.T, cfg *Config) core.Construct {
	t.Helper()
	c, e := Registration().Build(cfg, cloud())
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func tick(t *testing.T, c core.Construct, at time.Time, eng *shape.Engine) *capture {
	t.Helper()
	w := &capture{}
	if e := c.Tick(context.Background(), at, &core.World{Metrics: w, Shape: eng}); e != nil {
		t.Fatal(e)
	}
	return w
}
func count(t *testing.T, w *capture) float64 {
	t.Helper()
	for _, s := range w.batch {
		if s.Name == "aws_logs_incoming_log_events_sum" {
			return s.Value
		}
	}
	if w.calls != 0 {
		t.Fatal("idle observation wrote a batch")
	}
	return 0
}

// Expected names, dimensions and units derive from signals/cw.md P36 and
// https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/CloudWatch-Logs-Monitoring-CloudWatch-Metrics.html
// IncomingBytes observes uncompressed bytes; IncomingLogEvents has AWS unit None,
// not Count. Neither unit is serialized as a new label or metric-name suffix.
func TestRegistrationBuildTickInventory(t *testing.T) {
	r := Registration()
	if r.Kind != "cloudwatch_logs" || r.Scope != core.ScopeBlueprint {
		t.Fatal(r)
	}
	cfg, ok := r.NewConfig().(*Config)
	if !ok {
		t.Fatal("typed config required")
	}
	dec := yaml.NewDecoder(strings.NewReader("log_groups:\n  - name: /synthetic/intake\n    events_per_second: 2\n    uncompressed_bytes_per_event: 256\n"))
	dec.KnownFields(true)
	if e := dec.Decode(cfg); e != nil {
		t.Fatal(e)
	}
	c := build(t, cfg)
	if c.Kind() != "cloudwatch_logs" || c.Interval() != time.Minute || !reflect.DeepEqual(c.Signals(), []core.SignalClass{core.Metrics}) {
		t.Fatal("registration contract")
	}
	w := tick(t, c, time.Unix(1800000000, 0), nil)
	want := map[string]float64{
		"aws_logs_incoming_log_events_sum": 120, "aws_logs_incoming_log_events_average": 1, "aws_logs_incoming_log_events_maximum": 1, "aws_logs_incoming_log_events_minimum": 1, "aws_logs_incoming_log_events_sample_count": 120,
		"aws_logs_incoming_bytes_sum": 30720, "aws_logs_incoming_bytes_average": 256, "aws_logs_incoming_bytes_maximum": 256, "aws_logs_incoming_bytes_minimum": 256, "aws_logs_incoming_bytes_sample_count": 120,
	}
	labels := map[string]string{"account_id": "111122223333", "region": "eu-west-1", "namespace": "AWS/Logs", "job": "cloud/aws/logs", "name": "global", "dimension_LogGroupName": "/synthetic/intake"}
	if w.calls != 1 || len(w.batch) != 10 {
		t.Fatalf("inventory: %+v", w)
	}
	for _, s := range w.batch {
		v, ok := want[s.Name]
		if !ok || v != s.Value || s.Kind != promrw.KindGauge || !reflect.DeepEqual(s.Labels, labels) {
			t.Fatalf("unexpected series: %+v", s)
		}
		delete(want, s.Name)
	}
	if len(want) != 0 {
		t.Fatal(want)
	}
}

func TestSparseCarryJitterAndGap(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, gap := range []time.Duration{60*time.Second + time.Nanosecond, 61 * time.Second, 24 * time.Hour} {
		t.Run(gap.String(), func(t *testing.T) {
			c := build(t, config(1.0/128)) // exact binary rate: 60/128 carry must survive the capped gap
			if n := count(t, tick(t, c, now, nil)); n != 0 {
				t.Fatal(n)
			}
			at := now.Add(gap)
			if n := count(t, tick(t, c, at, nil)); n != 0 {
				t.Fatal(n)
			}
			if n := count(t, tick(t, c, at.Add(8*time.Second), nil)); n != 1 {
				t.Fatalf("lost pre-gap fraction: %v", n)
			}
			if n := count(t, tick(t, c, at.Add(9*time.Second), nil)); n != 0 {
				t.Fatal("stale sparse observation", n)
			}
		})
	}
	c := build(t, config(0.125))
	total := count(t, tick(t, c, now, nil))
	for i := 1; i <= 80; i++ {
		total += count(t, tick(t, c, now.Add(time.Duration(i)*100*time.Millisecond), nil))
	}
	if total != 8 {
		t.Fatalf("jitter lost residual: %v", total)
	}
	if n := count(t, tick(t, c, now.Add(8*time.Second), nil)); n != 0 {
		t.Fatal("repeat", n)
	}
	if n := count(t, tick(t, c, now, nil)); n != 0 {
		t.Fatal("backward", n)
	}
	if n := count(t, tick(t, c, now.Add(12*time.Second), nil)); n != 1 {
		t.Fatal("backward moved fence", n)
	}
	// No epsilon may turn genuinely subthreshold traffic into an event.
	c = build(t, config(math.Nextafter(1.0/64, 0)))
	if n := count(t, tick(t, c, now, nil)); n != 0 {
		t.Fatal(n)
	}
	if n := count(t, tick(t, c, now.Add(4*time.Second), nil)); n != 0 {
		t.Fatal("rounded up subthreshold traffic", n)
	}
	if n := count(t, tick(t, c, now.Add(4*time.Second+time.Nanosecond), nil)); n != 1 {
		t.Fatal("lost near-boundary carry", n)
	}
}

func TestRealMasterPauseAndResume(t *testing.T) {
	now := time.Unix(1800000000, 0)
	eng := &shape.Engine{}
	c := build(t, config(1.0/128))
	if count(t, tick(t, c, now, eng)) != 0 {
		t.Fatal("premature event")
	}
	eng.SetVolumeMultiplier(0)
	if w := tick(t, c, now.Add(time.Minute), eng); w.calls != 0 {
		t.Fatal("paused batch")
	}
	if w := tick(t, c, now.Add(24*time.Hour), eng); w.calls != 0 {
		t.Fatal("paused gap batch")
	}
	eng.SetVolumeMultiplier(2) // positive volume does not multiply the declared rate
	at := now.Add(25 * time.Hour)
	if w := tick(t, c, at, eng); w.calls != 0 {
		t.Fatal("resume backfill")
	}
	if count(t, tick(t, c, at.Add(time.Minute), eng)) != 0 {
		t.Fatal("pause accrued traffic")
	}
	if count(t, tick(t, c, at.Add(68*time.Second), eng)) != 1 {
		t.Fatal("pause discarded active fraction")
	}
	eng.SetVolumeMultiplier(0)
	c = build(t, config(1))
	if tick(t, c, now, eng).calls != 0 {
		t.Fatal("initial pause")
	}
	eng.SetVolumeMultiplier(1)
	if tick(t, c, at, eng).calls != 0 {
		t.Fatal("initially paused bootstrap")
	}
	if count(t, tick(t, c, at.Add(time.Second), eng)) != 1 {
		t.Fatal("resume boundary")
	}
	// A high-rate active construct must suppress all ten series on pause.
	eng.SetVolumeMultiplier(0)
	if tick(t, c, at.Add(2*time.Second), eng).calls != 0 {
		t.Fatal("active pause batch")
	}
}

func TestBuildRefusalsAndStrictFields(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.LogGroups = nil }, func(c *Config) { c.LogGroups = make([]LogGroup, 101) },
		func(c *Config) { c.LogGroups = append(c.LogGroups, c.LogGroups[0]) },
		func(c *Config) { c.LogGroups[0].Name = "" }, func(c *Config) { c.LogGroups[0].Name = " padded " },
		func(c *Config) { c.LogGroups[0].EventsPerSecond = nil }, func(c *Config) { c.LogGroups[0].UncompressedBytesPerEvent = nil },
	} {
		cfg := config(1)
		mutate(cfg)
		if _, e := Build(cfg, cloud()); e == nil {
			t.Fatal("accepted invalid config", cfg)
		}
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 1000001, math.MaxFloat64} {
		cfg := config(v)
		if _, e := Build(cfg, cloud()); e == nil {
			t.Fatal("accepted rate", v)
		}
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 0, 0.5, 256.5, 65537, math.MaxFloat64} {
		cfg := config(0)
		cfg.LogGroups[0].UncompressedBytesPerEvent = ptr(v)
		if _, e := Build(cfg, cloud()); e == nil {
			t.Fatal("accepted bytes", v)
		}
	}
	for _, raw := range []any{nil, Config{}, (*Config)(nil), "wrong"} {
		if _, e := Build(raw, cloud()); e == nil {
			t.Fatal("accepted wrong config")
		}
	}
	for _, fx := range []*fixture.Set{nil, {}, {Cloud: &fixture.Cloud{}}, {Cloud: &fixture.Cloud{AccountID: " a ", Region: "r"}}, {Cloud: &fixture.Cloud{AccountID: "a", Region: " r "}}} {
		if _, e := Build(config(1), fx); e == nil {
			t.Fatal("accepted bad cloud")
		}
	}
	for _, mode := range []string{"otlp", "other"} {
		fx := cloud()
		fx.Cloud.CloudWatchExport = mode
		if _, e := Build(config(1), fx); e == nil {
			t.Fatal("accepted mode", mode)
		}
	}
	for _, field := range []string{"destination_type: Lambda", "filter_name: f", "collector_name: x", "enrichment: {}", "pipeline: p", "account_id: a", "region: r", "delivery: {}"} {
		raw := "log_groups:\n  - name: g\n    events_per_second: 0\n    uncompressed_bytes_per_event: 1\n    " + field + "\n"
		d := yaml.NewDecoder(strings.NewReader(raw))
		d.KnownFields(true)
		if e := d.Decode(Registration().NewConfig()); e == nil {
			t.Fatal("accepted unsupported YAML", field)
		}
	}
	for _, raw := range []string{"log_groups: [{name: g, events_per_second: 0, uncompressed_bytes_per_event: 1.5}]", "log_groups: [{name: g, uncompressed_bytes_per_event: 1}]"} {
		cfg := &Config{}
		d := yaml.NewDecoder(strings.NewReader(raw))
		d.KnownFields(true)
		if e := d.Decode(cfg); e == nil {
			if _, e = Build(cfg, cloud()); e == nil {
				t.Fatal("accepted invalid YAML", raw)
			}
		}
	}
}

func TestIndependentGroupsAndAggregateBound(t *testing.T) {
	cfg := &Config{}
	for i := 0; i < 100; i++ {
		cfg.LogGroups = append(cfg.LogGroups, LogGroup{Name: fmt.Sprintf("group-%d", i), EventsPerSecond: ptr(1000000), UncompressedBytesPerEvent: ptr(65536)})
	}
	now := time.Unix(1800000000, 0)
	w := tick(t, build(t, cfg), now, nil)
	if len(w.batch) != 1000 {
		t.Fatalf("maximum declaration inventory: %d", len(w.batch))
	}
	total := 0.0
	for _, s := range w.batch {
		if s.Name == "aws_logs_incoming_bytes_sum" {
			total += s.Value
		}
	}
	if total != 100*60000000*65536 {
		t.Fatal("aggregate bound", total)
	}
	cfg.LogGroups = []LogGroup{
		{Name: "slow", EventsPerSecond: ptr(1.0 / 128), UncompressedBytesPerEvent: ptr(1)},
		{Name: "fast", EventsPerSecond: ptr(1), UncompressedBytesPerEvent: ptr(2)},
		{Name: "idle", EventsPerSecond: ptr(0), UncompressedBytesPerEvent: ptr(3)},
		{Name: "tiny", EventsPerSecond: ptr(math.SmallestNonzeroFloat64), UncompressedBytesPerEvent: ptr(4)},
	}
	c := build(t, cfg)
	for _, at := range []time.Time{now, now.Add(time.Minute), now.Add(68 * time.Second)} {
		w = tick(t, c, at, nil)
		counts := map[string]float64{}
		for _, s := range w.batch {
			if s.Name == "aws_logs_incoming_log_events_sum" {
				counts[s.Labels["dimension_LogGroupName"]] = s.Value
			}
		}
		want := map[string]float64{"fast": 60}
		if at.Equal(now.Add(68 * time.Second)) {
			want = map[string]float64{"slow": 1, "fast": 8}
		}
		if !reflect.DeepEqual(counts, want) {
			t.Fatalf("group carry leaked or stale output: %v, want %v", counts, want)
		}
	}
}

func TestOwnedCopiesBoundsAndWriters(t *testing.T) {
	cfg := config(1)
	fx := cloud()
	c, e := Registration().Build(cfg, fx)
	if e != nil {
		t.Fatal(e)
	}
	*cfg.LogGroups[0].EventsPerSecond = 100
	*cfg.LogGroups[0].UncompressedBytesPerEvent = 1
	cfg.LogGroups[0].Name = "mutated"
	cfg.LogGroups = nil
	fx.Cloud.AccountID = "mutated"
	fx.Cloud.Region = "mutated"
	fx.Cloud.CloudWatchExport = "otlp"
	now := time.Unix(1800000000, 0)
	w := tick(t, c, now, nil)
	for _, s := range w.batch {
		if s.Labels["account_id"] != "111122223333" || s.Labels["region"] != "eu-west-1" || s.Labels["dimension_LogGroupName"] != "/synthetic/intake" {
			t.Fatal("aliased input", s)
		}
		if s.Name == "aws_logs_incoming_bytes_sum" && s.Value != 15360 {
			t.Fatal("aliased numeric input", s)
		}
		s.Labels["name"] = "writer-mutated"
	}
	for _, s := range tick(t, c, now.Add(time.Minute), nil).batch {
		if s.Labels["name"] != "global" {
			t.Fatal("writer mutated model")
		}
	}
	c = build(t, config(0))
	if tick(t, c, now, nil).calls != 0 || tick(t, c, now.Add(24*time.Hour), nil).calls != 0 {
		t.Fatal("zero rate emitted")
	}
	cfg = config(1000000)
	cfg.LogGroups[0].UncompressedBytesPerEvent = ptr(65536)
	c = build(t, cfg)
	w = tick(t, c, now, nil)
	for _, s := range w.batch {
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			t.Fatal("overflow", s)
		}
		if s.Name == "aws_logs_incoming_bytes_sum" && s.Value != 60000000*65536 {
			t.Fatal("bound sum", s)
		}
	}
	c = build(t, config(1))
	for _, world := range []*core.World{nil, {}} {
		if e := c.Tick(context.Background(), now, world); e == nil {
			t.Fatal("missing writer accepted")
		}
	}
	sentinel := errors.New("writer error")
	if e := c.Tick(context.Background(), now, &core.World{Metrics: &capture{err: sentinel}}); !errors.Is(e, sentinel) {
		t.Fatal("lost writer error", e)
	}
}
