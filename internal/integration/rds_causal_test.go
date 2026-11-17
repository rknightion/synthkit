// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// loadRDSCausal loads the fixture through the real loader and builds every rds construct, the
// way the composition root does: bindings are validated by the construct's own Build.
func loadRDSCausal(t *testing.T, mutate func(string) string) (*blueprint.Resolved, error) {
	t.Helper()
	data, err := os.ReadFile("../construct/rds/testdata/database-dependency-slow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if mutate != nil {
		text = mutate(text)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		return nil, err
	}
	for _, ci := range res.Constructs {
		if ci.Kind != "rds" {
			continue
		}
		cr, _ := reg.Construct(ci.Kind)
		if _, err := cr.Build(ci.Config, ci.Fixtures); err != nil {
			return nil, err
		}
	}
	return res, nil
}

type rdsMinute struct{ read, write, conns float64 }

// runRDSCausal ticks every rds construct of the resolved blueprint for two hours and returns
// each database's average-stat readings by minute (1 = 10:01).
func runRDSCausal(t *testing.T, res *blueprint.Resolved, incidents []string) map[string]map[int]rdsMinute {
	t.Helper()
	reg := runner.Catalog()
	eng := shape.New("UTC", incidents)
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	out := map[string]map[int]rdsMinute{}
	for _, ci := range res.Constructs {
		if ci.Kind != "rds" {
			continue
		}
		cr, _ := reg.Construct(ci.Kind)
		c, err := cr.Build(ci.Config, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		mins := map[int]rdsMinute{}
		for i := 1; i <= 120; i++ {
			mc := &coretest.MetricCapture{}
			if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc}); err != nil {
				t.Fatal(err)
			}
			var m rdsMinute
			for _, s := range mc.All() {
				switch s.Name {
				case "aws_rds_read_latency_average":
					m.read = s.Value
				case "aws_rds_write_latency_average":
					m.write = s.Value
				case "aws_rds_database_connections_average":
					m.conns = s.Value
				}
			}
			mins[i] = m
		}
		out[ci.Name] = mins
	}
	if len(out) != 2 {
		t.Fatalf("want two rds constructs, built %d", len(out))
	}
	return out
}

func rdsMean(m map[int]rdsMinute, lo, hi int, f func(rdsMinute) float64) float64 {
	var sum float64
	for i := lo; i <= hi; i++ {
		sum += f(m[i])
	}
	return sum / float64(hi-lo+1)
}

// A database bound to a `pool` resource, loaded through the real loader and catalog, follows the
// causal model: while the dependency the pool is held across is slow, its read and write latency
// rise by the dependency's latency factor and DatabaseConnections climbs with the pool's busy
// connections; both recover afterwards. The unbound database beside it is untouched.
func TestRDSBoundDatabaseFollowsDependencySlowAndRecovers(t *testing.T) {
	res, err := loadRDSCausal(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Incidents) != 1 || !strings.HasPrefix(res.Incidents[0], "dependency_slow@2026-10-24T10:30/10m#1@orders-db-engine") {
		t.Fatalf("compiled incidents %v", res.Incidents)
	}
	healthy := runRDSCausal(t, res, nil)
	faulted := runRDSCausal(t, res, res.Incidents)

	orders, audit := faulted["orders-db"], faulted["audit-db"]
	ordersOK, auditOK := healthy["orders-db"], healthy["audit-db"]
	read := func(m rdsMinute) float64 { return m.read }
	write := func(m rdsMinute) float64 { return m.write }

	// Minute i covers (10:00+i-1m, 10:00+i m]; the fault is active over [10:30, 10:40), so minutes
	// 32 to 39 lie wholly inside it and minutes 42 onward wholly after it.
	baseRead, baseWrite := rdsMean(ordersOK, 1, 120, read), rdsMean(ordersOK, 1, 120, write)
	if r := rdsMean(orders, 32, 39, read) / baseRead; r < 3.4 || r > 4.6 {
		t.Fatalf("bound read latency rose %.2fx during dependency_slow, want about the 4x slow_factor", r)
	}
	if r := rdsMean(orders, 32, 39, write) / baseWrite; r < 3.4 || r > 4.6 {
		t.Fatalf("bound write latency rose %.2fx during dependency_slow, want about 4x", r)
	}
	for i := 1; i <= 120; i++ {
		switch {
		case i <= 29 || i >= 42:
			if math.Abs(orders[i].conns-5) > 1e-9 {
				t.Fatalf("minute %d outside the fault: connections %v, want the pool's 5 in use", i, orders[i].conns)
			}
			if orders[i] != ordersOK[i] {
				t.Fatalf("minute %d outside the fault differs from the healthy run (no recovery): %+v vs %+v", i, orders[i], ordersOK[i])
			}
		case i >= 32 && i <= 39:
			if math.Abs(orders[i].conns-17) > 1e-6 {
				t.Fatalf("minute %d during the fault: connections %v, want 17 in use (100 + 80 x 3 ms hold at 50 rps)", i, orders[i].conns)
			}
		}
	}
	// The unbound database keeps today's output exactly, fault or not.
	for i := 1; i <= 120; i++ {
		if audit[i] != auditOK[i] {
			t.Fatalf("unbound database moved at minute %d: %+v vs %+v", i, audit[i], auditOK[i])
		}
	}
	if audit[35].conns == 5 {
		t.Fatal("unbound database reads the pool")
	}
}

// The loader rejects a legacy database mode aimed at a bound database, and a binding it cannot
// honour.
func TestRDSBoundDatabaseDeclarationRejections(t *testing.T) {
	legacyEffect := func(target string) func(string) string {
		return func(s string) string {
			return strings.Replace(s, "      - {mode: dependency_slow, target: orders-db-engine, intensity: 1}\n",
				"      - {mode: slow_query_storm, target: "+target+", intensity: 1}\n", 1)
		}
	}
	for name, tc := range map[string]struct {
		mutate func(string) string
		want   string
	}{
		"legacy slow_query_storm on the bound database": {legacyEffect("orders-db"), "orders-db"},
		"resource that is not declared": {
			func(s string) string {
				return strings.Replace(s, "resource: orders-pool", "resource: no-such-resource", 1)
			},
			"no-such-resource",
		},
		"resource of the wrong kind": {
			func(s string) string {
				s = strings.Replace(s, "resource: orders-pool", "resource: orders-conc", 1)
				return strings.Replace(s, "      - name: orders-db-engine\n",
					"      - name: orders-conc\n        kind: concurrency\n        size: 4\n        hold_ms: 100\n        demand: {off_peak_rps: 1, peak_rps: 1, load: constant}\n      - name: orders-db-engine\n", 1)
			},
			"dependency or pool",
		},
		"binding without the cloudwatch lane": {
			func(s string) string {
				return strings.Replace(s, "name: orders-db, resource: orders-pool}", "name: orders-db, resource: orders-pool, observability: {cloudwatch: false}}", 1)
			},
			"cloudwatch",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadRDSCausal(t, tc.mutate); err == nil {
				t.Fatal("loader accepted the declaration")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
	// A legacy mode aimed at the unbound database still schedules.
	if _, err := loadRDSCausal(t, legacyEffect("audit-db")); err != nil {
		t.Fatalf("legacy mode on the unbound database must load: %v", err)
	}
	if _, err := loadRDSCausal(t, nil); err != nil {
		t.Fatalf("the unmutated declaration must load: %v", err)
	}
}
