// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"strings"
	"testing"
	"time"
)

func throttleConfig() *Config {
	cfg := config()
	cfg.Tables[0].ProvisionedReads = 10
	cfg.Tables[0].ProvisionedWrites = 5
	cfg.Tables[0].Operations = []Operation{
		{Name: "GetItem", RequestsPerSecond: 1, ReadUnits: 1, LatencyMS: 4, ThrottledRequestsPerSecond: 0.5},
		{Name: "PutItem", RequestsPerSecond: 0.5, WriteUnits: 2, LatencyMS: 6, ThrottledRequestsPerSecond: 1},
	}
	return cfg
}

func series(t *testing.T, w *capture, base string, labels map[string]string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, stat := range []string{"sum", "average", "maximum", "minimum", "sample_count"} {
		out[stat] = value(t, w, "aws_dynamodb_"+base+"_"+stat, labels)
	}
	return out
}

func wantStats(t *testing.T, what string, got map[string]float64, n float64) {
	t.Helper()
	for stat, want := range map[string]float64{"sum": n, "sample_count": n, "average": 1, "maximum": 1, "minimum": 1} {
		if got[stat] != want {
			t.Fatalf("%s %s=%v want %v (all %v)", what, stat, got[stat], want, got)
		}
	}
}

// The 2026-10 lab capture on a provisioned table reported ThrottledRequests per
// TableName+Operation (GetItem, PutItem) and ReadThrottleEvents/WriteThrottleEvents per
// TableName, each with one sample of 1 per throttled event: Sum == SampleCount, Max == Min == 1,
// and no zero samples for unthrottled requests.
func TestThrottleEventsPerOperationAndTable(t *testing.T) {
	w := firstWindow(t, throttleConfig(), time.Unix(1700000000, 0))
	table := map[string]string{"dimension_TableName": "orders"}
	op := func(name string) map[string]string {
		return map[string]string{"dimension_TableName": "orders", "dimension_Operation": name}
	}
	wantStats(t, "GetItem ThrottledRequests", series(t, w, "throttled_requests", op("GetItem")), 30)
	wantStats(t, "PutItem ThrottledRequests", series(t, w, "throttled_requests", op("PutItem")), 60)
	wantStats(t, "ReadThrottleEvents", series(t, w, "read_throttle_events", table), 30)
	wantStats(t, "WriteThrottleEvents", series(t, w, "write_throttle_events", table), 60)
	// Throttled requests are not successful requests: latency and capacity are unchanged.
	if got := value(t, w, "aws_dynamodb_successful_request_latency_sample_count", op("GetItem")); got != 60 {
		t.Fatalf("successful GetItem requests %v want 60", got)
	}
	if got := value(t, w, "aws_dynamodb_consumed_read_capacity_units_sum", table); got != 60 {
		t.Fatalf("consumed read units %v want 60", got)
	}
	for _, s := range w.batch {
		if strings.Contains(s.Name, "throttle") && s.Labels["name"] != "declared-collector-orders" {
			t.Fatalf("%s carries name %q", s.Name, s.Labels["name"])
		}
	}
}

func TestThrottleMetricsAreOptIn(t *testing.T) {
	cfg := throttleConfig()
	for i := range cfg.Tables[0].Operations {
		cfg.Tables[0].Operations[i].ThrottledRequestsPerSecond = 0
	}
	w := firstWindow(t, cfg, time.Unix(1700000000, 0))
	for _, s := range w.batch {
		if strings.Contains(s.Name, "throttle") {
			t.Fatalf("default emitted %s", s.Name)
		}
	}
}

func TestOnlyThrottledOperationsAndDirectionsEmit(t *testing.T) {
	cfg := throttleConfig()
	cfg.Tables[0].Operations[1].ThrottledRequestsPerSecond = 0
	w := firstWindow(t, cfg, time.Unix(1700000000, 0))
	for _, s := range w.batch {
		if s.Name == "aws_dynamodb_write_throttle_events_sum" || (s.Name == "aws_dynamodb_throttled_requests_sum" && s.Labels["dimension_Operation"] == "PutItem") {
			t.Fatalf("unthrottled write path emitted %s %v", s.Name, s.Labels)
		}
	}
	value(t, w, "aws_dynamodb_read_throttle_events_sum", map[string]string{"dimension_TableName": "orders"})
}

// A table that is fully throttled has no successful requests but still reports throttles.
func TestThrottlesWithoutSuccessfulRequests(t *testing.T) {
	cfg := throttleConfig()
	cfg.Tables[0].Operations[0].RequestsPerSecond = 0
	w := firstWindow(t, cfg, time.Unix(1700000000, 0))
	wantStats(t, "ReadThrottleEvents", series(t, w, "read_throttle_events", map[string]string{"dimension_TableName": "orders"}), 30)
	for _, s := range w.batch {
		if s.Name == "aws_dynamodb_successful_request_latency_sum" && s.Labels["dimension_Operation"] == "GetItem" {
			t.Fatal("throttled-only operation reported successful latency")
		}
	}
}

// Sparse throttles retain earned fractions between windows like other event rates.
func TestSparseThrottleCarry(t *testing.T) {
	cfg := throttleConfig()
	cfg.Tables[0].Operations = []Operation{{Name: "GetItem", RequestsPerSecond: 1, ReadUnits: 1, LatencyMS: 4, ThrottledRequestsPerSecond: 1.0 / 120}}
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	table := map[string]string{"dimension_TableName": "orders"}
	at := time.Unix(1700000000, 0)
	w := &capture{}
	tick(t, c, at, w)
	for _, s := range w.batch {
		if strings.Contains(s.Name, "throttle") {
			t.Fatalf("half an event fabricated %s", s.Name)
		}
	}
	tick(t, c, at.Add(time.Minute), w)
	wantStats(t, "ReadThrottleEvents", series(t, w, "read_throttle_events", table), 1)
}

func TestThrottleFollowsBusinessLoad(t *testing.T) {
	cfg := loadConfig("business")
	cfg.Tables[0].Operations[0].ThrottledRequestsPerSecond = 2
	night := firstWindow(t, cfg, weekdayNight)
	noon := firstWindow(t, cfg, weekdayNoon)
	table := map[string]string{"dimension_TableName": "orders"}
	n, d := value(t, night, "aws_dynamodb_read_throttle_events_sum", table), value(t, noon, "aws_dynamodb_read_throttle_events_sum", table)
	if n <= 0 || n >= d {
		t.Fatalf("throttles did not follow the business curve: night %v noon %v", n, d)
	}
	if d > 120 {
		t.Fatalf("throttle rate %v exceeds the declared peak (120/window)", d)
	}
}

func TestThrottleValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"negative":   func(c *Config) { c.Tables[0].Operations[0].ThrottledRequestsPerSecond = -1 },
		"non-finite": func(c *Config) { c.Tables[0].Operations[0].ThrottledRequestsPerSecond = 1e12 },
		"uncaptured query": func(c *Config) {
			c.Tables[0].Operations = []Operation{{Name: "Query", RequestsPerSecond: 1, ReadUnits: 1, LatencyMS: 4, ThrottledRequestsPerSecond: 1}}
		},
		"uncaptured scan": func(c *Config) {
			c.Tables[0].Operations = []Operation{{Name: "Scan", RequestsPerSecond: 1, ReadUnits: 1, LatencyMS: 4, ThrottledRequestsPerSecond: 1}}
		},
		"global table put": func(c *Config) {
			c.Tables[0].WriteSource = "GlobalTable"
			c.Tables[0].Replication = &Replication{WritesPerSecond: 1, UnitsPerWrite: 1}
			c.Tables[0].Operations = []Operation{{Name: "PutItem", WriteUnits: 1, LatencyMS: 6, ThrottledRequestsPerSecond: 1}}
		},
	} {
		cfg := throttleConfig()
		mutate(cfg)
		if _, err := Build(cfg, fixtures()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
