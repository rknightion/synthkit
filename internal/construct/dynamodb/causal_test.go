// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// quotaSet declares three quotas, each in the unit its operation binds it in:
//   - get-rcu: read capacity units per second. Demand 5 RCU/s, limit 6 RCU/s, so it is healthy.
//   - put-rps: requests per second. Demand 1.5 rps, limit 2 rps.
//   - put-wcu: write capacity units per second. Demand 4 WCU/s, limit 5 WCU/s.
//
// A plain dependency is declared beside them for the wrong-kind check.
func quotaSet() causal.Set {
	constant := func(rps float64) *causal.Demand {
		return &causal.Demand{OffPeakRPS: rps, PeakRPS: rps, Load: causal.LoadConstant}
	}
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "get-rcu", Kind: causal.KindQuota, LimitRPS: 6, Demand: constant(5)},
		{Name: "put-rps", Kind: causal.KindQuota, LimitRPS: 2, Demand: constant(1.5)},
		{Name: "put-wcu", Kind: causal.KindQuota, LimitRPS: 5, Demand: constant(4)},
		{Name: "dep", Kind: causal.KindDependency, LatencyMS: 10},
	}}}}
}

// boundTable binds GetItem to get-rcu in read units (0.5 RCU per request: an eventually
// consistent read of an item up to 4 KB) and PutItem to put-rps in requests (2 WCU per request: an
// item over 1 KB and up to 2 KB). Query stays unbound beside them.
func boundTable() *Config {
	cfg := config()
	cfg.Tables[0].ProvisionedReads = 10
	cfg.Tables[0].ProvisionedWrites = 5
	cfg.Tables[0].Operations = []Operation{
		{Name: "GetItem", ReadUnits: 0.5, LatencyMS: 4, Resource: "get-rcu", QuotaUnit: "read_units"},
		{Name: "PutItem", WriteUnits: 2, LatencyMS: 6, Resource: "put-rps", QuotaUnit: "requests"},
		{Name: "Query", RequestsPerSecond: 0.5, ReadUnits: 1, LatencyMS: 5, ReturnedItems: 2},
	}
	cfg.UseResources(quotaSet())
	return cfg
}

func causalFixtures() *fixture.Set {
	fx := fixtures()
	fx.Env = &fixture.Env{Name: "prod"}
	return fx
}

var quotaStart = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// boundMinutes ticks a fresh instance once a minute for n minutes, advancing its causal model on
// every tick first as the runner does.
func boundMinutes(t *testing.T, cfg *Config, entries []string, n int) []*capture {
	t.Helper()
	c, err := Build(cfg, causalFixtures())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", entries)
	var out []*capture
	for i := 1; i <= n; i++ {
		now := quotaStart.Add(time.Duration(i) * time.Minute)
		if adv, ok := c.(causal.Advancer); ok {
			adv.AdvanceCausal(now, eng)
		}
		w := &capture{}
		if err := c.Tick(context.Background(), now, &core.World{Metrics: w, Shape: eng}); err != nil {
			t.Fatal(err)
		}
		out = append(out, w)
	}
	return out
}

// AC1: a bound operation's successful requests are its quota's admitted volume and its throttles
// the quota's rejected volume, each converted from the quota's declared unit to requests by the
// operation's own per-request capacity. quota_reduced on get-rcu (read units) and put-rps
// (requests) moves ThrottledRequests, ReadThrottleEvents and WriteThrottleEvents; healthy minutes
// emit no throttle series.
func TestBoundOperationThrottlesFollowQuotaUnit(t *testing.T) {
	got := boundMinutes(t, boundTable(), []string{
		"quota_reduced@2026-10-24T09:10/10m#0.5@get-rcu",
		"quota_reduced@2026-10-24T09:30/10m#0.5@put-rps",
	}, 45)
	table := map[string]string{"dimension_TableName": "orders"}
	op := func(name string) map[string]string {
		return map[string]string{"dimension_TableName": "orders", "dimension_Operation": name}
	}
	for i, w := range got {
		minute := i + 1
		k := byKey(w)
		getOK := k["aws_dynamodb_successful_request_latency_sample_count|orders|GetItem"]
		putOK := k["aws_dynamodb_successful_request_latency_sample_count|orders|PutItem"]
		switch {
		case minute >= 11 && minute <= 19:
			// get-rcu: 5 RCU/s offered, 3 admitted: 180 RCU = 360 reads, 120 RCU = 240 throttled reads.
			if getOK != 360 {
				t.Fatalf("minute %d: GetItem successes %v want 360", minute, getOK)
			}
			wantStats(t, "GetItem ThrottledRequests", series(t, w, "throttled_requests", op("GetItem")), 240)
			wantStats(t, "ReadThrottleEvents", series(t, w, "read_throttle_events", table), 240)
			if v := value(t, w, "aws_dynamodb_consumed_read_capacity_units_sum", table); v != 180+30 {
				t.Fatalf("minute %d: consumed read units %v want 210 (180 GetItem + 30 Query)", minute, v)
			}
			if putOK != 90 || len(throttleSeries(w, "orders")) != 10 {
				t.Fatalf("minute %d: PutItem moved or another family throttled: %v %v", minute, putOK, throttleSeries(w, "orders"))
			}
		case minute >= 31 && minute <= 39:
			// put-rps: 1.5 rps offered, 1 admitted: 60 writes (120 WCU), 30 throttled.
			if putOK != 60 {
				t.Fatalf("minute %d: PutItem successes %v want 60", minute, putOK)
			}
			wantStats(t, "PutItem ThrottledRequests", series(t, w, "throttled_requests", op("PutItem")), 30)
			wantStats(t, "WriteThrottleEvents", series(t, w, "write_throttle_events", table), 30)
			if v := value(t, w, "aws_dynamodb_consumed_write_capacity_units_sum", table); v != 120 {
				t.Fatalf("minute %d: consumed write units %v want 120", minute, v)
			}
			if getOK != 600 {
				t.Fatalf("minute %d: GetItem moved: %v", minute, getOK)
			}
		case minute == 10 || minute == 20 || minute == 30 || minute == 40:
			// The window straddles an incident edge.
		default:
			// Healthy: 5 RCU/s / 0.5 = 10 reads/s; 1.5 writes/s.
			if getOK != 600 || putOK != 90 {
				t.Fatalf("minute %d: healthy successes GetItem %v PutItem %v want 600, 90", minute, getOK, putOK)
			}
			if s := throttleSeries(w, "orders"); len(s) != 0 {
				t.Fatalf("minute %d: throttle series while healthy: %v", minute, s)
			}
			if v := value(t, w, "aws_dynamodb_consumed_read_capacity_units_sum", table); v != 300+30 {
				t.Fatalf("minute %d: consumed read units %v want 330", minute, v)
			}
			if v := value(t, w, "aws_dynamodb_consumed_write_capacity_units_sum", table); v != 180 {
				t.Fatalf("minute %d: consumed write units %v want 180", minute, v)
			}
		}
		if q := k["aws_dynamodb_successful_request_latency_sample_count|orders|Query"]; q != 30 {
			t.Fatalf("minute %d: unbound Query %v want 30", minute, q)
		}
	}
}

// A PutItem bound in write units converts rejected WCU to throttled writes by its write_units.
func TestBoundOperationWriteUnits(t *testing.T) {
	cfg := boundTable()
	cfg.Tables[0].Operations[1] = Operation{Name: "PutItem", WriteUnits: 2, LatencyMS: 6, Resource: "put-wcu", QuotaUnit: "write_units"}
	got := boundMinutes(t, cfg, []string{"quota_reduced@2026-10-24T09:10/10m#0.6@put-wcu"}, 15)
	table := map[string]string{"dimension_TableName": "orders"}
	// Healthy: 4 WCU/s / 2 = 2 writes/s.
	if v := byKey(got[4])["aws_dynamodb_successful_request_latency_sample_count|orders|PutItem"]; v != 120 {
		t.Fatalf("healthy PutItem %v want 120", v)
	}
	// Limit 5 x 0.4 = 2 WCU/s admitted (60 writes), 2 WCU/s rejected: 120 WCU = 60 throttled writes.
	w := got[12]
	if v := byKey(w)["aws_dynamodb_successful_request_latency_sample_count|orders|PutItem"]; v != 60 {
		t.Fatalf("reduced PutItem %v want 60", v)
	}
	wantStats(t, "WriteThrottleEvents", series(t, w, "write_throttle_events", table), 60)
}

// AC2: injecting a resource set into a configuration that binds no operation changes nothing:
// no model is compiled and every window's batch is identical to the set-less run.
func TestUnboundOutputUnchangedByResources(t *testing.T) {
	plain := incidentConfig()
	withSet := incidentConfig()
	withSet.UseResources(quotaSet())
	entries := []string{wireMode + "@2026-10-24T09:10/10m#1@orders", "quota_reduced@2026-10-24T09:10/10m#1@get-rcu"}
	a := boundMinutes(t, plain, entries, 25)
	b := boundMinutes(t, withSet, entries, 25)
	for i := range a {
		if x, y := canonical(a[i]), canonical(b[i]); !reflect.DeepEqual(x, y) || len(x) == 0 {
			t.Fatalf("minute %d: an unbound table moved when resources were declared:\n%v\n%v", i+1, x, y)
		}
	}
	c, err := Build(withSet, causalFixtures())
	if err != nil {
		t.Fatal(err)
	}
	if c.(*construct).model != nil {
		t.Fatal("a configuration with no bound operation compiled a causal model")
	}
}

// canonical renders a window's batch as sorted series lines: state.Collect emits in map order.
func canonical(w *capture) []string {
	var out []string
	for _, s := range w.batch {
		keys := make([]string, 0, len(s.Labels))
		for k, v := range s.Labels {
			keys = append(keys, k+"="+v)
		}
		slices.Sort(keys)
		out = append(out, fmt.Sprintf("%s{%s} %v @%d", s.Name, strings.Join(keys, ","), s.Value, s.T.UnixNano()))
	}
	slices.Sort(out)
	return out
}

func TestBoundOperationBuildRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"requests_per_second": {func(c *Config) { c.Tables[0].Operations[0].RequestsPerSecond = 1 }, "omit requests_per_second"},
		"throttled rate":      {func(c *Config) { c.Tables[0].Operations[0].ThrottledRequestsPerSecond = 1 }, "omit throttled_requests_per_second"},
		"load":                {func(c *Config) { c.Tables[0].Operations[0].Load = "business" }, "omit load"},
		"missing unit":        {func(c *Config) { c.Tables[0].Operations[0].QuotaUnit = "" }, "quota_unit"},
		"unknown unit":        {func(c *Config) { c.Tables[0].Operations[0].QuotaUnit = "bytes" }, "quota_unit"},
		"unit without resource": {func(c *Config) {
			c.Tables[0].Operations[2].QuotaUnit = "requests"
		}, "quota_unit requires resource"},
		"read units on PutItem": {func(c *Config) { c.Tables[0].Operations[1].QuotaUnit = "read_units" }, "read_units"},
		"write units on GetItem": {func(c *Config) {
			c.Tables[0].Operations[0].QuotaUnit = "write_units"
		}, "write_units"},
		"uncaptured Query": {func(c *Config) {
			c.Tables[0].Operations[2] = Operation{Name: "Query", ReadUnits: 1, LatencyMS: 5, Resource: "get-rcu", QuotaUnit: "read_units"}
			c.Tables[0].Operations = c.Tables[0].Operations[1:]
		}, "GetItem and PutItem"},
		"wrong kind":   {func(c *Config) { c.Tables[0].Operations[0].Resource = "dep" }, "want kind quota"},
		"unknown":      {func(c *Config) { c.Tables[0].Operations[0].Resource = "nope" }, "not a declared resource"},
		"no resources": {func(c *Config) { c.UseResources(causal.Set{}) }, "declares no environments[].resources"},
		"above provisioned": {func(c *Config) {
			c.Tables[0].ProvisionedReads = 5
		}, "exceeds provisioned capacity"},
		"shared resource": {func(c *Config) {
			other := c.Tables[0]
			other.Name, other.CollectorName = "audit", "declared-collector-audit"
			other.Operations = []Operation{{Name: "GetItem", ReadUnits: 1, LatencyMS: 4, Resource: "get-rcu", QuotaUnit: "read_units"}}
			c.Tables = append(c.Tables, other)
		}, "bind the same resource"},
	} {
		cfg := boundTable()
		tc.mutate(cfg)
		if _, err := Build(cfg, causalFixtures()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", name, err, tc.want)
		}
	}
	if _, err := Build(boundTable(), causalFixtures()); err != nil {
		t.Fatalf("valid bound table rejected: %v", err)
	}
}

// A table whose throttle-captured operations are all bound takes its throttles from its quotas, so
// it is not a table-axis target (a table_throttle_storm naming it is an unknown target at load,
// not a silent no-op); a table with an unbound GetItem or PutItem stays one. The registration
// carries the resource vocabulary beside the table-axis mode.
func TestBoundTableTargetsAndModes(t *testing.T) {
	cfg := boundTable()
	other := config().Tables[0]
	other.Name, other.CollectorName = "audit", "declared-collector-audit"
	other.Operations = []Operation{{Name: "GetItem", RequestsPerSecond: 1, ReadUnits: 1, LatencyMS: 4}}
	cfg.Tables = append(cfg.Tables, other)
	if got := cfg.FailureTargets(); !reflect.DeepEqual(got, []failuremode.Target{{Name: "audit", Axis: AxisTable}}) {
		t.Fatalf("targets %v", got)
	}
	var names []string
	for _, m := range Registration().FailureModes {
		names = append(names, m.Name)
	}
	if !slices.Contains(names, ModeThrottleStorm) || !slices.Contains(names, causal.ModeQuotaReduced) {
		t.Fatalf("modes %v", names)
	}
}

func TestResourceBindingsIdentity(t *testing.T) {
	got := boundTable().ResourceBindings(causal.BindContext{Env: "prod", Region: "eu-west-1", AccountID: "synthetic-account"})
	want := []causal.Binding{
		{Resource: "get-rcu", Identity: map[string]string{"account_id": "synthetic-account", "region": "eu-west-1", "name": "declared-collector-orders", "dimension_TableName": "orders", "dimension_Operation": "GetItem"}},
		{Resource: "put-rps", Identity: map[string]string{"account_id": "synthetic-account", "region": "eu-west-1", "name": "declared-collector-orders", "dimension_TableName": "orders", "dimension_Operation": "PutItem"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings %v", got)
	}
	if got := incidentConfig().ResourceBindings(causal.BindContext{}); len(got) != 0 {
		t.Fatalf("unbound bindings %v", got)
	}
}
