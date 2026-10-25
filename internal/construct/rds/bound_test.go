// SPDX-License-Identifier: AGPL-3.0-only

package rds_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/construct/rds"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// Real-world basis: an application tier holds a connection for about 100 ms per request (80 ms
// of it the database's own query time), 50 requests/s, from a 20-connection pool: 5 busy
// connections, 25% utilization. The database slowing 4x stretches the hold to 340 ms and the
// busy connections to 17.
func boundSet(mutate func(*causal.Resource, *causal.Resource)) causal.Set {
	pool := causal.Resource{
		Name: "orders-pool", Kind: causal.KindPool, Size: 20, HoldMS: 100, HeldAcross: "orders-db-engine",
		AcquireTimeoutMS: 2000, Demand: &causal.Demand{OffPeakRPS: 50, PeakRPS: 50, Load: causal.LoadConstant},
	}
	dep := causal.Resource{Name: "orders-db-engine", Kind: causal.KindDependency, LatencyMS: 80, SlowFactor: 4}
	if mutate != nil {
		mutate(&pool, &dep)
	}
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{pool, dep}}}}
}

func buildBound(t *testing.T, resource string, set causal.Set) (core.Construct, error) {
	t.Helper()
	cfg := &rds.Config{}
	cfg.BindResource(resource)
	cfg.UseResources(set)
	fx := &fixture.Set{DB: coretest.DB("postgres"), Cloud: coretest.Cloud(), Env: coretest.Env(), Seed: "test"}
	return rds.Registration().Build(cfg, fx)
}

func TestBoundInstanceRejectsInvalidBinding(t *testing.T) {
	for name, tc := range map[string]struct {
		resource string
		set      causal.Set
		want     string
	}{
		"no resources declared": {"orders-pool", causal.Set{}, "environments[].resources"},
		"unknown resource":      {"nope", boundSet(nil), `"nope"`},
		"resource of wrong kind": {"orders-queue", boundSet(func(p, _ *causal.Resource) {}),
			`"orders-queue"`},
	} {
		t.Run(name, func(t *testing.T) {
			set := tc.set
			if tc.resource == "orders-queue" {
				set.Envs[0].Resources = append(set.Envs[0].Resources, causal.Resource{
					Name: "orders-queue", Kind: causal.KindQueue, Size: 2, HoldMS: 100,
					Demand: &causal.Demand{OffPeakRPS: 1, PeakRPS: 1, Load: causal.LoadConstant},
				})
			}
			_, err := buildBound(t, tc.resource, set)
			if err == nil {
				t.Fatal("Build accepted the binding")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
	// A concurrency resource carries no database semantics either.
	set := boundSet(nil)
	set.Envs[0].Resources = append(set.Envs[0].Resources, causal.Resource{
		Name: "orders-conc", Kind: causal.KindConcurrency, Size: 2, HoldMS: 100,
		Demand: &causal.Demand{OffPeakRPS: 1, PeakRPS: 1, Load: causal.LoadConstant},
	})
	if _, err := buildBound(t, "orders-conc", set); err == nil || !strings.Contains(err.Error(), "dependency or pool") {
		t.Fatalf("concurrency binding: %v", err)
	}
	// An unbound instance with resources declared builds as before.
	cfg := &rds.Config{}
	cfg.UseResources(boundSet(nil))
	if _, err := rds.Registration().Build(cfg, &fixture.Set{DB: coretest.DB("postgres"), Cloud: coretest.Cloud(), Env: coretest.Env()}); err != nil {
		t.Fatalf("unbound instance beside declared resources: %v", err)
	}
}

func TestBoundInstanceImplementsAdvancerAndRegistersResourceModes(t *testing.T) {
	c, err := buildBound(t, "orders-pool", boundSet(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(causal.Advancer); !ok {
		t.Fatal("bound construct does not implement causal.Advancer")
	}
	have := map[string]bool{}
	for _, m := range rds.Registration().FailureModes {
		have[m.Name] = true
	}
	for _, m := range causal.FailureModes {
		if !have[m.Name] {
			t.Errorf("registration lacks resource mode %q", m.Name)
		}
	}
	for _, m := range rds.FailureModes {
		if !have[m.Name] {
			t.Errorf("registration lost legacy mode %q", m.Name)
		}
	}
}

// minuteReading is one minute's emitted values of the families a binding may move.
type minuteReading struct{ read, write, conns, cpu float64 }

// runBound ticks one instance for two hours, one tick a minute, and returns the average-stat
// values keyed by the minute index (1 = 10:01).
func runBound(t *testing.T, resource string, set causal.Set, incidents []string) map[int]minuteReading {
	t.Helper()
	var c core.Construct
	var err error
	if resource == "" {
		cfg := &rds.Config{}
		cfg.UseResources(set)
		c, err = rds.Registration().Build(cfg, &fixture.Set{DB: coretest.DB("postgres"), Cloud: coretest.Cloud(), Env: coretest.Env(), Seed: "test"})
	} else {
		c, err = buildBound(t, resource, set)
	}
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", incidents)
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	out := map[int]minuteReading{}
	for i := 1; i <= 120; i++ {
		cap := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: cap}); err != nil {
			t.Fatal(err)
		}
		var m minuteReading
		for _, s := range cap.All() {
			switch s.Name {
			case "aws_rds_read_latency_average":
				m.read = s.Value
			case "aws_rds_write_latency_average":
				m.write = s.Value
			case "aws_rds_database_connections_average":
				m.conns = s.Value
			case "aws_rds_cpuutilization_average":
				m.cpu = s.Value
			}
		}
		out[i] = m
	}
	return out
}

func mean(m map[int]minuteReading, lo, hi int, f func(minuteReading) float64) float64 {
	var sum float64
	for i := lo; i <= hi; i++ {
		sum += f(m[i])
	}
	return sum / float64(hi-lo+1)
}

// Minute i covers (10:00+i-1m, 10:00+i m]. The fault below is active over [10:30, 10:40), so
// minutes 32 to 39 lie wholly inside it and minutes 42 onward wholly after it.
const slowIncident = "dependency_slow@2026-10-24T10:30/10m#1@orders-db-engine"

func TestDependencyBoundLatencyRisesUnderDependencySlowAndRecovers(t *testing.T) {
	set := boundSet(func(_, d *causal.Resource) { d.SlowFactor = 6 })
	faulted := runBound(t, "orders-db-engine", set, []string{slowIncident})
	healthy := runBound(t, "orders-db-engine", set, nil)

	read := func(m minuteReading) float64 { return m.read }
	write := func(m minuteReading) float64 { return m.write }
	baseRead, baseWrite := mean(healthy, 1, 120, read), mean(healthy, 1, 120, write)
	if baseRead > 0.0012 || baseRead < 0.0006 {
		t.Fatalf("healthy read latency %v s, want about the 0.8 ms baseline", baseRead)
	}
	for i := 1; i <= 29; i++ {
		if faulted[i] != healthy[i] {
			t.Fatalf("minute %d before the fault differs from the healthy run: %+v vs %+v", i, faulted[i], healthy[i])
		}
	}
	// slow_factor 6 at intensity 1: both latency families rise about 6x while the fault lasts.
	if r := mean(faulted, 32, 39, read) / baseRead; r < 5.1 || r > 6.9 {
		t.Fatalf("read latency rose %.2fx during dependency_slow, want about 6x", r)
	}
	if r := mean(faulted, 32, 39, write) / baseWrite; r < 5.1 || r > 6.9 {
		t.Fatalf("write latency rose %.2fx during dependency_slow, want about 6x", r)
	}
	for i := 42; i <= 120; i++ {
		if faulted[i] != healthy[i] {
			t.Fatalf("minute %d after the fault differs from the healthy run (no recovery): %+v vs %+v", i, faulted[i], healthy[i])
		}
	}
	// A dependency has no connection count: DatabaseConnections keeps its baseline.
	for i := 1; i <= 120; i++ {
		if faulted[i].conns != healthy[i].conns || faulted[i].cpu != healthy[i].cpu {
			t.Fatalf("minute %d: connections/cpu moved under a dependency binding: %+v vs %+v", i, faulted[i], healthy[i])
		}
	}
}

func TestPoolBoundConnectionsFollowPoolInUseAndLatencyFollowsHeldAcrossDependency(t *testing.T) {
	set := boundSet(nil)
	faulted := runBound(t, "orders-pool", set, []string{slowIncident})
	healthy := runBound(t, "orders-pool", set, nil)

	// 50 rps x 100 ms = 5 busy connections, with no noise (the pool, not a draw, is the count).
	for i := 1; i <= 120; i++ {
		if math.Abs(healthy[i].conns-5) > 1e-9 {
			t.Fatalf("healthy minute %d: connections %v, want the pool's 5 in use", i, healthy[i].conns)
		}
	}
	// dependency_slow at intensity 1 (80 ms -> 320 ms): hold 100 + 80 x 3 = 340 ms, so 17 busy.
	for i := 32; i <= 39; i++ {
		if math.Abs(faulted[i].conns-17) > 1e-6 {
			t.Fatalf("minute %d during the fault: connections %v, want 17", i, faulted[i].conns)
		}
	}
	for i := 42; i <= 120; i++ {
		if math.Abs(faulted[i].conns-5) > 1e-9 {
			t.Fatalf("minute %d after the fault: connections %v, want recovered to 5", i, faulted[i].conns)
		}
	}
	read := func(m minuteReading) float64 { return m.read }
	baseRead := mean(healthy, 1, 120, read)
	if r := mean(faulted, 32, 39, read) / baseRead; r < 3.4 || r > 4.6 {
		t.Fatalf("read latency rose %.2fx under the pool's dependency_slow, want about 4x", r)
	}
	if r := mean(faulted, 42, 120, read) / baseRead; r < 0.9 || r > 1.1 {
		t.Fatalf("read latency after the fault is %.2fx baseline, want recovered", r)
	}
}

func TestPoolBoundConnectionsFollowPoolExhaustion(t *testing.T) {
	// pool_exhausted at 0.9 leaves 2 of 20 connections: 5 busy wanted, only 2 can be.
	faulted := runBound(t, "orders-pool", boundSet(nil), []string{"pool_exhausted@2026-10-24T10:30/10m#0.9@orders-pool"})
	for i := 32; i <= 39; i++ {
		if math.Abs(faulted[i].conns-2) > 1e-6 {
			t.Fatalf("minute %d under pool_exhausted: connections %v, want 2", i, faulted[i].conns)
		}
	}
	if math.Abs(faulted[50].conns-5) > 1e-9 {
		t.Fatalf("connections %v after the fault, want 5", faulted[50].conns)
	}
}

// A bound instance is moved by its resource alone: the legacy AxisDatabase modes (which the
// loader rejects for a bound database) are ignored if one reaches it anyway, for example a live
// activation.
func TestBoundInstanceIgnoresLegacyDatabaseModes(t *testing.T) {
	legacy := []string{
		"connection_saturation@2026-10-24T10:10/10m#1@test-db",
		"lock_contention@2026-10-24T10:25/10m#1@test-db",
		"slow_query_storm@2026-10-24T10:40/10m#1@test-db",
	}
	for _, resource := range []string{"orders-pool", "orders-db-engine"} {
		with := runBound(t, resource, boundSet(nil), legacy)
		without := runBound(t, resource, boundSet(nil), nil)
		for i := 1; i <= 120; i++ {
			if with[i] != without[i] {
				t.Fatalf("%s: minute %d moved by a legacy database mode: %+v vs %+v", resource, i, with[i], without[i])
			}
		}
	}
}

// Declaring resources without binding the instance leaves its output exactly the unbound
// output, legacy modes included.
func TestUnboundInstanceBesideDeclaredResourcesIsUnchanged(t *testing.T) {
	cfg := &rds.Config{}
	cfg.UseResources(boundSet(nil))
	for engine, want := range map[string]string{"postgres": unboundPostgresDigest, "mysql": unboundMySQLDigest} {
		if got := renderUnbound(t, engine, cfg); got != want {
			t.Errorf("%s: digest %s, want %s", engine, got, want)
		}
	}
}
