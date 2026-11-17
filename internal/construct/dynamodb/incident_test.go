// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
)

// The wire names of the axis and mode, pinned literally: they appear in blueprints and targets.
const (
	wireMode = "table_throttle_storm"
	wireAxis = failuremode.Axis("table")
)

func incidentConfig() *Config {
	cfg := config()
	cfg.Tables[0].ProvisionedReads = 10
	cfg.Tables[0].ProvisionedWrites = 5
	cfg.Tables[0].Operations = []Operation{
		{Name: "GetItem", RequestsPerSecond: 1, ReadUnits: 1, LatencyMS: 4},
		{Name: "PutItem", RequestsPerSecond: 0.5, WriteUnits: 2, LatencyMS: 6},
		{Name: "Query", RequestsPerSecond: 0.5, ReadUnits: 1, LatencyMS: 5, ReturnedItems: 2},
	}
	other := cfg.Tables[0]
	other.Name = "audit"
	other.CollectorName = "declared-collector-audit"
	cfg.Tables = append(cfg.Tables, other)
	return cfg
}

var incidentStart = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// minutes ticks a fresh instance once a minute for n minutes with the given incident entries.
func minutes(t *testing.T, cfg *Config, entries []string, n int) []*capture {
	t.Helper()
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", entries)
	var out []*capture
	for i := 1; i <= n; i++ {
		w := &capture{}
		if err := c.Tick(context.Background(), incidentStart.Add(time.Duration(i)*time.Minute), &core.World{Metrics: w, Shape: eng}); err != nil {
			t.Fatal(err)
		}
		out = append(out, w)
	}
	return out
}

func byKey(w *capture) map[string]float64 {
	out := map[string]float64{}
	for _, s := range w.batch {
		out[s.Name+"|"+s.Labels["dimension_TableName"]+"|"+s.Labels["dimension_Operation"]] = s.Value
	}
	return out
}

func throttleSeries(w *capture, table string) []string {
	var out []string
	for _, s := range w.batch {
		if strings.Contains(s.Name, "throttle") && s.Labels["dimension_TableName"] == table {
			out = append(out, s.Name)
		}
	}
	return out
}

func TestTableThrottleStormDrivesThrottleFamilies(t *testing.T) {
	// Active for the ticks at 09:10 to 09:19 (at <= now < at+for), table "orders" only.
	got := minutes(t, incidentConfig(), []string{wireMode + "@2026-10-24T09:10/10m#1@orders"}, 30)
	orders := map[string]string{"dimension_TableName": "orders"}
	get := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "GetItem"}
	put := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "PutItem"}
	for i, w := range got {
		minute := i + 1
		active := minute >= 10 && minute <= 19
		if !active {
			if names := throttleSeries(w, "orders"); len(names) != 0 {
				t.Fatalf("minute %d (no incident): throttle series %v", minute, names)
			}
		} else {
			// Intensity 1 throttles as many requests as were admitted: 60 GetItem, 30 PutItem.
			wantStats(t, "ThrottledRequests GetItem", series(t, w, "throttled_requests", get), 60)
			wantStats(t, "ThrottledRequests PutItem", series(t, w, "throttled_requests", put), 30)
			wantStats(t, "ReadThrottleEvents", series(t, w, "read_throttle_events", orders), 60)
			wantStats(t, "WriteThrottleEvents", series(t, w, "write_throttle_events", orders), 30)
			// Admitted requests, capacity and latency are unchanged.
			if v := value(t, w, "aws_dynamodb_successful_request_latency_sample_count", get); v != 60 {
				t.Fatalf("minute %d: admitted GetItem %v want 60", minute, v)
			}
			if v := value(t, w, "aws_dynamodb_consumed_read_capacity_units_sum", orders); v != 90 {
				t.Fatalf("minute %d: consumed read units %v want 90", minute, v)
			}
			for _, s := range w.batch {
				if s.Labels["dimension_Operation"] == "Query" && strings.Contains(s.Name, "throttle") {
					t.Fatalf("uncaptured Query throttled: %s", s.Name)
				}
			}
		}
		// A table the incident does not name is never throttled.
		if names := throttleSeries(w, "audit"); len(names) != 0 {
			t.Fatalf("minute %d: untargeted table throttled: %v", minute, names)
		}
	}
}

func TestTableThrottleStormScalesWithIntensityAndDefaultOutputIsUnchanged(t *testing.T) {
	half := minutes(t, incidentConfig(), []string{wireMode + "@2026-10-24T09:00/30m#0.5@orders"}, 3)
	for _, w := range half {
		wantStats(t, "ThrottledRequests GetItem", series(t, w, "throttled_requests", map[string]string{"dimension_TableName": "orders", "dimension_Operation": "GetItem"}), 30)
		wantStats(t, "WriteThrottleEvents", series(t, w, "write_throttle_events", map[string]string{"dimension_TableName": "orders"}), 15)
	}
	// No incident, or an engine that never schedules the mode, leaves every sample as before.
	plain := minutes(t, incidentConfig(), nil, 3)
	other := minutes(t, incidentConfig(), []string{"latency_spike@2026-10-24T09:00/30m#1@orders"}, 3)
	for i := range plain {
		a, b := byKey(plain[i]), byKey(other[i])
		if len(a) != len(b) {
			t.Fatalf("minute %d: %d vs %d series", i+1, len(a), len(b))
		}
		for k, v := range a {
			if got, ok := b[k]; !ok || got != v {
				t.Fatalf("minute %d: %s=%v became %v (present %v)", i+1, k, v, got, ok)
			}
		}
		if len(throttleSeries(plain[i], "orders")) != 0 {
			t.Fatalf("default output carries throttle series")
		}
	}
}

// An incident adds to a declared baseline without disturbing it: the baseline throttles keep
// their own carry, so outside the window the declared draw is exactly what it was.
func TestTableThrottleStormAddsToDeclaredBaseline(t *testing.T) {
	cfg := throttleConfig()
	base := minutes(t, cfg, nil, 3)
	inc := minutes(t, throttleConfig(), []string{wireMode + "@2026-10-24T09:02/1m#1@orders"}, 3)
	get := map[string]string{"dimension_TableName": "orders", "dimension_Operation": "GetItem"}
	for i := range base {
		b := value(t, base[i], "aws_dynamodb_throttled_requests_sum", get)
		g := value(t, inc[i], "aws_dynamodb_throttled_requests_sum", get)
		if i == 1 {
			if g != b+60 {
				t.Fatalf("storm minute: %v want baseline %v + 60", g, b)
			}
		} else if g != b {
			t.Fatalf("minute %d outside the window: %v want baseline %v", i+1, g, b)
		}
	}
}

func TestTableAxisDeclaration(t *testing.T) {
	targets := incidentConfig().FailureTargets()
	if len(targets) != 2 || targets[0] != (failuremode.Target{Name: "orders", Axis: wireAxis}) || targets[1] != (failuremode.Target{Name: "audit", Axis: wireAxis}) {
		t.Fatalf("targets %v", targets)
	}
	// The table-axis mode, then the resource vocabulary every causal consumer registers (SKT-0185).
	modes := Registration().FailureModes
	if len(modes) != 1+len(causal.FailureModes) || modes[0].Name != wireMode || modes[0].Axis != wireAxis || modes[0].Help == "" || !reflect.DeepEqual(modes[1:], causal.FailureModes) {
		t.Fatalf("modes %v", modes)
	}
}
