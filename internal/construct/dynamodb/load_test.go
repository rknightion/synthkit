// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
)

func loadConfig(load string) *Config {
	cfg := config()
	cfg.Tables[0].ProvisionedReads = 40
	cfg.Tables[0].ProvisionedWrites = 40
	cfg.Tables[0].Load = load
	cfg.Tables[0].Operations = []Operation{
		{Name: "GetItem", RequestsPerSecond: 10, ReadUnits: 1, LatencyMS: 4},
		{Name: "PutItem", RequestsPerSecond: 5, WriteUnits: 2, LatencyMS: 6},
	}
	return cfg
}

func shapedWorld(w *capture) *core.World {
	return &core.World{Metrics: w, Shape: shape.New("UTC", nil)}
}

func tickAt(t *testing.T, c core.Construct, at time.Time, w *capture) {
	t.Helper()
	if err := c.Tick(context.Background(), at, shapedWorld(w)); err != nil {
		t.Fatal(err)
	}
}

func value(t *testing.T, w *capture, name string, labels map[string]string) float64 {
	t.Helper()
	for _, s := range w.batch {
		if s.Name != name {
			continue
		}
		ok := true
		for k, v := range labels {
			if s.Labels[k] != v {
				ok = false
			}
		}
		if ok {
			return s.Value
		}
	}
	t.Fatalf("missing %s %v", name, labels)
	return 0
}

// First observation of a fresh instance at the given time (one 60 s window).
func firstWindow(t *testing.T, cfg *Config, at time.Time) *capture {
	t.Helper()
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	tickAt(t, c, at, w)
	return w
}

var (
	weekdayNoon  = time.Date(2027, 1, 13, 12, 0, 0, 0, time.UTC) // Wednesday plateau
	weekdayNight = time.Date(2027, 1, 13, 3, 0, 0, 0, time.UTC)  // overnight floor
	weekendNoon  = time.Date(2027, 1, 16, 12, 0, 0, 0, time.UTC) // Saturday
)

const consumedRead = "aws_dynamodb_consumed_read_capacity_units_sum"

func TestBusinessLoadFollowsTheCurve(t *testing.T) {
	lbl := map[string]string{"dimension_TableName": "orders"}
	noon := value(t, firstWindow(t, loadConfig("business"), weekdayNoon), consumedRead, lbl)
	night := value(t, firstWindow(t, loadConfig("business"), weekdayNight), consumedRead, lbl)
	weekend := value(t, firstWindow(t, loadConfig("business"), weekendNoon), consumedRead, lbl)
	// Declared rate is the peak: 10 rps x 60 s = 600 units at most, and well above 70%.
	if noon > 600 || noon < 420 {
		t.Fatalf("noon %v outside (420, 600]", noon)
	}
	if night > noon/5 || night <= 0 {
		t.Fatalf("overnight %v should be a small share of noon %v", night, noon)
	}
	if weekend > noon*0.4 || weekend < noon*0.2 {
		t.Fatalf("weekend %v should be about 0.3 of weekday %v", weekend, noon)
	}
	// Latency sample counts follow the same rate.
	op := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "GetItem"}
	if n := value(t, firstWindow(t, loadConfig("business"), weekdayNight), "aws_dynamodb_successful_request_latency_sample_count", op); n != night {
		t.Fatalf("latency sample_count %v != read units %v (1 unit per GetItem)", n, night)
	}
}

func TestConstantLoadIsByteIdenticalToOmitted(t *testing.T) {
	run := func(load string) []string {
		c, err := Build(loadConfig(load), fixtures())
		if err != nil {
			t.Fatal(err)
		}
		w := &capture{}
		var out []string
		for i := 0; i < 90; i++ {
			tickAt(t, c, weekdayNight.Add(time.Duration(i)*7*time.Minute), w)
			var tick []string
			for _, s := range w.batch {
				tick = append(tick, s.Name+"|"+s.Labels["dimension_Operation"]+"|"+strconv.FormatFloat(s.Value, 'g', -1, 64))
			}
			sort.Strings(tick)
			out = append(out, tick...)
		}
		return out
	}
	a, b := run(""), run("constant")
	if len(a) == 0 || strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Fatal("omitted and constant load differ")
	}
}

func TestOperationLoadOverridesTable(t *testing.T) {
	cfg := loadConfig("business")
	cfg.Tables[0].Operations[1].Load = "constant"
	w := firstWindow(t, cfg, weekdayNight)
	put := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "PutItem"}
	get := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "GetItem"}
	const sc = "aws_dynamodb_successful_request_latency_sample_count"
	if got := value(t, w, sc, put); got != 300 {
		t.Fatalf("constant PutItem override: %v want 300", got)
	}
	if got := value(t, w, sc, get); got >= 150 {
		t.Fatalf("business GetItem overnight should be far below 600: %v", got)
	}
	cfg = loadConfig("")
	cfg.Tables[0].Operations[0].Load = "business"
	w = firstWindow(t, cfg, weekdayNight)
	if got := value(t, w, sc, put); got != 300 {
		t.Fatalf("unset table: PutItem must stay constant: %v", got)
	}
	if got := value(t, w, sc, get); got >= 150 {
		t.Fatalf("operation-level business GetItem: %v", got)
	}
}

func TestBusinessLoadKeepsDemandUnderProvisioned(t *testing.T) {
	cfg := loadConfig("business")
	cfg.Tables[0].ProvisionedReads, cfg.Tables[0].ProvisionedWrites = 12, 12
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	var peak float64
	for i := 0; i < 24*7; i++ {
		tickAt(t, c, weekdayNight.Add(time.Duration(i)*time.Hour), w)
		for _, name := range []string{"aws_dynamodb_consumed_read_capacity_units_maximum", "aws_dynamodb_consumed_write_capacity_units_maximum"} {
			peak = math.Max(peak, value(t, w, name, map[string]string{"dimension_TableName": "orders"}))
		}
	}
	if peak > 12 {
		t.Fatalf("per-second peak %v exceeds provisioned 12", peak)
	}
}

func TestBusinessLoadScalesGlobalTableReplication(t *testing.T) {
	cfg := loadConfig("business")
	cfg.Tables[0].Operations = nil
	cfg.Tables[0].WriteSource = "GlobalTable"
	cfg.Tables[0].Replication = &Replication{WritesPerSecond: 10, UnitsPerWrite: 1}
	lbl := map[string]string{"dimension_TableName": "orders", "dimension_Source": "GlobalTable"}
	noon := value(t, firstWindow(t, cfg, weekdayNoon), "aws_dynamodb_consumed_write_capacity_units_sum", lbl)
	night := value(t, firstWindow(t, cfg, weekdayNight), "aws_dynamodb_consumed_write_capacity_units_sum", lbl)
	if noon < 420 || noon > 600 || night > noon/5 {
		t.Fatalf("replication noon=%v night=%v", noon, night)
	}
}

func TestLatencySpreadAcrossStats(t *testing.T) {
	cfg := loadConfig("")
	cfg.Tables[0].Operations[0].LatencyMinMS = 1.5
	cfg.Tables[0].Operations[0].LatencyMaxMS = 40
	op := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "GetItem"}
	w := firstWindow(t, cfg, weekdayNoon)
	n := value(t, w, "aws_dynamodb_successful_request_latency_sample_count", op)
	sum := value(t, w, "aws_dynamodb_successful_request_latency_sum", op)
	avg := value(t, w, "aws_dynamodb_successful_request_latency_average", op)
	lo := value(t, w, "aws_dynamodb_successful_request_latency_minimum", op)
	hi := value(t, w, "aws_dynamodb_successful_request_latency_maximum", op)
	if !(lo < avg && avg < hi) || lo < 1.5 || hi > 40 {
		t.Fatalf("expected 1.5 <= %v < %v < %v <= 40", lo, avg, hi)
	}
	if math.Abs(sum-n*avg) > 1e-6 || avg < 4*0.95 || avg > 4*1.05 {
		t.Fatalf("sum %v n %v avg %v", sum, n, avg)
	}
	// A constant PutItem sibling stays degenerate.
	put := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "PutItem"}
	if value(t, w, "aws_dynamodb_successful_request_latency_minimum", put) != 6 || value(t, w, "aws_dynamodb_successful_request_latency_maximum", put) != 6 {
		t.Fatal("undeclared bounds must keep constant latency")
	}
	// Deterministic from the inputs: a second instance reproduces the same window.
	w2 := firstWindow(t, cfg, weekdayNoon)
	if value(t, w2, "aws_dynamodb_successful_request_latency_maximum", op) != hi || value(t, w2, "aws_dynamodb_successful_request_latency_average", op) != avg {
		t.Fatal("latency spread not deterministic")
	}
	// The mean drifts between windows.
	c, _ := Build(cfg, fixtures())
	seen := map[float64]bool{}
	cw := &capture{}
	for i := 0; i < 10; i++ {
		tickAt(t, c, weekdayNoon.Add(time.Duration(i)*time.Minute), cw)
		seen[value(t, cw, "aws_dynamodb_successful_request_latency_average", op)] = true
	}
	if len(seen) < 5 {
		t.Fatalf("window mean latency should drift, saw %d distinct", len(seen))
	}
}

func TestSingleRequestLatencyHasNoSpread(t *testing.T) {
	st := latencyStats(Operation{LatencyMS: 4, LatencyMinMS: 1, LatencyMaxMS: 30}, 1, weekdayNoon, "k")
	if st.Minimum != st.Maximum || st.Minimum != st.Average || st.Sum != st.Average {
		t.Fatalf("%+v", st)
	}
	for _, n := range []float64{2, 3, 7, 100, 5000} {
		s := latencyStats(Operation{LatencyMS: 4, LatencyMinMS: 1, LatencyMaxMS: 30}, n, weekdayNoon, "k")
		if s.Minimum > s.Average || s.Average > s.Maximum || s.Minimum < 1 || s.Maximum > 30 {
			t.Fatalf("n=%v %+v", n, s)
		}
		if s.Maximum > s.Sum-(n-1)*s.Minimum+1e-9 {
			t.Fatalf("n=%v max unrealizable for sum: %+v", n, s)
		}
	}
}

func TestLoadAndLatencyValidation(t *testing.T) {
	for name, mut := range map[string]func(*Config){
		"table load":     func(c *Config) { c.Tables[0].Load = "diurnal" },
		"op load":        func(c *Config) { c.Tables[0].Operations[0].Load = "x" },
		"min only":       func(c *Config) { c.Tables[0].Operations[0].LatencyMinMS = 1 },
		"max only":       func(c *Config) { c.Tables[0].Operations[0].LatencyMaxMS = 9 },
		"min above mean": func(c *Config) { c.Tables[0].Operations[0].LatencyMinMS, c.Tables[0].Operations[0].LatencyMaxMS = 5, 9 },
		"max below mean": func(c *Config) { c.Tables[0].Operations[0].LatencyMinMS, c.Tables[0].Operations[0].LatencyMaxMS = 1, 3 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadConfig("")
			mut(cfg)
			if _, err := Build(cfg, fixtures()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

// Every emitted (Sum, SampleCount, Minimum, Maximum) must be realizable by real
// samples inside the declared bounds: one sample at each extreme and the other n-2
// summing to the remainder, i.e. Maximum <= Sum-(n-1)*Minimum and
// Minimum >= Sum-(n-1)*Maximum, with Minimum <= Average <= Maximum.
func TestLatencyStatsRealizableForSmallSampleCounts(t *testing.T) {
	const eps = 1e-9
	for _, o := range []Operation{
		{LatencyMS: 40, LatencyMinMS: 12, LatencyMaxMS: 900},
		{LatencyMS: 4, LatencyMinMS: 1.5, LatencyMaxMS: 40},
		{LatencyMS: 4, LatencyMinMS: 4, LatencyMaxMS: 4},
		{LatencyMS: 4, LatencyMinMS: 3.9, LatencyMaxMS: 4.1},
		{LatencyMS: 250, LatencyMinMS: 1, LatencyMaxMS: 251},
	} {
		for n := 1.0; n <= 300; n++ {
			for m := 0; m < 40; m++ {
				s := latencyStats(o, n, weekdayNoon.Add(time.Duration(m)*time.Minute), "k")
				if s.Minimum < o.LatencyMinMS-eps || s.Maximum > o.LatencyMaxMS+eps || s.Minimum > s.Average+eps || s.Average > s.Maximum+eps {
					t.Fatalf("%+v n=%v: bounds/order violated %+v", o, n, s)
				}
				if math.Abs(s.Sum-n*s.Average) > eps*n {
					t.Fatalf("%+v n=%v: sum %v != n*avg", o, n, s)
				}
				if s.Maximum > s.Sum-(n-1)*s.Minimum+eps || s.Minimum < s.Sum-(n-1)*s.Maximum-eps {
					t.Fatalf("%+v n=%v: %+v unrealizable for sum/count", o, n, s)
				}
				if n == 1 && (s.Minimum != s.Average || s.Maximum != s.Average) {
					t.Fatalf("n=1 must be degenerate: %+v", s)
				}
			}
		}
	}
	// The reported counter-example shape: n=2, mean 40, bounds 12/900 must be two
	// samples that exist (max 470 / min 26 would not sum to 80).
	s := latencyStats(Operation{LatencyMS: 40, LatencyMinMS: 12, LatencyMaxMS: 900}, 2, weekdayNoon, "k")
	if s.Maximum+s.Minimum > s.Sum+1e-9 || s.Maximum+s.Minimum < s.Sum-1e-9 {
		t.Fatalf("n=2: the two samples must be exactly min and max summing to Sum: %+v", s)
	}
}
