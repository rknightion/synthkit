// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ymysql_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/construct/dbo11ymysql"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// A 20-connection application pool holds each of 50 requests/s for 100 ms,
// including 80 ms in the database. A 4x database slowdown raises hold to 340 ms.
func mysqlResources() causal.Set {
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "db-pool", Kind: causal.KindPool, Size: 20, HoldMS: 100, HeldAcross: "db-engine", AcquireTimeoutMS: 2000, Demand: &causal.Demand{OffPeakRPS: 50, PeakRPS: 50, Load: causal.LoadConstant}},
		{Name: "db-engine", Kind: causal.KindDependency, LatencyMS: 80, SlowFactor: 4},
	}}}}
}

func mysqlConfig(t *testing.T, resource string) *dbo11ymysql.Config {
	t.Helper()
	cfg := &dbo11ymysql.Config{}
	binder, ok := any(cfg).(interface {
		BindResource(string)
		UseResources(causal.Set)
	})
	if !ok {
		t.Fatal("MySQL config lacks causal binding/consumer seam")
	}
	binder.BindResource(resource)
	binder.UseResources(mysqlResources())
	return cfg
}

func mysqlBuild(t *testing.T, cfg *dbo11ymysql.Config) core.Construct {
	t.Helper()
	c, err := dbo11ymysql.Build(cfg, &fixture.Set{DB: coretest.DB("mysql"), Env: coretest.Env()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mysqlTick(t *testing.T, c core.Construct, now time.Time, eng *shape.Engine) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	mc, lc := &coretest.MetricCapture{}, &coretest.LogCapture{}
	if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc, Logs: lc}); err != nil {
		t.Fatal(err)
	}
	return mc, lc
}

func sortedMySQL(series []promrw.Series) []promrw.Series {
	sort.Slice(series, func(i, j int) bool {
		a, _ := json.Marshal(series[i])
		b, _ := json.Marshal(series[j])
		return string(a) < string(b)
	})
	return series
}

func TestMySQLUnboundExactOutput(t *testing.T) {
	c := mysqlBuild(t, &dbo11ymysql.Config{})
	mc, lc := &coretest.MetricCapture{}, &coretest.LogCapture{}
	eng := shape.New("UTC", []string{"lock_contention@2026-10-24T10:00/10m#1@" + coretest.DB("mysql").Name})
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc, Logs: lc}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal([]any{sortedMySQL(mc.All()), lc.Streams})
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	const want = "8b5b2fc10b17711a8eead1162269e9ce9fa11176da3073805692599cbcd84f07"
	if got != want {
		t.Fatalf("complete unbound output digest = %s, want %s", got, want)
	}
}

func TestMySQLBoundTick(t *testing.T) {
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for _, resource := range []string{"db-pool", "db-engine"} {
		t.Run(resource, func(t *testing.T) {
			baseline := mysqlBuild(t, mysqlConfig(t, resource))
			faulted := mysqlBuild(t, mysqlConfig(t, resource))
			adv, ok := faulted.(causal.Advancer)
			if !ok {
				t.Fatal("MySQL construct lacks causal.Advancer")
			}
			normal := shape.New("UTC", nil)
			fault := shape.New("UTC", []string{"dependency_slow@2026-10-24T10:01/1m#1@db-engine"})
			reference, err := causal.Compile(mysqlResources())
			if err != nil {
				t.Fatal(err)
			}
			previousBase, previousFault := map[string]float64{}, map[string]float64{}
			// Drive real master-clock advancement, with irregular observation times. Both renderers
			// must use the same half-open trailing minute, not just the final state.
			for at := start; !at.After(start.Add(3 * time.Minute)); at = at.Add(5 * time.Second) {
				adv.AdvanceCausal(at, fault)
				reference.Advance(at, fault)
				if at.Sub(start) != 90*time.Second && at.Sub(start) != 3*time.Minute {
					continue
				}
				bm, bl := mysqlTick(t, baseline, at, normal)
				fm, fl := mysqlTick(t, faulted, at, fault)
				from := at.Add(-time.Minute)
				win, ok := reference.Window(resource, from, at)
				if !ok {
					t.Fatal("missing reference window")
				}
				var sum float64
				var n int
				for ts := from.Add(causal.Step); !ts.After(at); ts = ts.Add(causal.Step) {
					s, ok := reference.State(resource, ts)
					if !ok {
						t.Fatal("missing reference state")
					}
					sum += s.LatencyFactor
					n++
				}
				factor := sum / float64(n)
				if at.Sub(start) == 90*time.Second && (factor <= 1 || factor >= 4) {
					t.Fatalf("expected mixed trailing-window latency, got %v", factor)
				}
				if at.Sub(start) == 3*time.Minute && factor != 1 {
					t.Fatalf("latency did not recover: %v", factor)
				}
				value := func(mc *coretest.MetricCapture, name string) float64 { return mc.Find(name)[0].Value }
				if resource == "db-pool" {
					if got := value(fm, "mysql_global_status_threads_connected"); math.Abs(got-win.MeanInUse) > 1e-12 {
						t.Fatalf("connected %v != window mean %v", got, win.MeanInUse)
					}
				} else if value(fm, "mysql_global_status_threads_connected") != value(bm, "mysql_global_status_threads_connected") {
					t.Fatal("dependency changed connection count")
				}
				for _, name := range []string{"mysql_global_status_threads_running", "mysql_global_variables_max_connections", "mysql_perf_schema_events_statements_errors_total"} {
					if !reflect.DeepEqual(sortedMySQL(fm.Find(name)), sortedMySQL(bm.Find(name))) {
						t.Fatalf("binding changed %s", name)
					}
				}
				// Compare cumulative counter DELTAS, including the recovery tick.
				for _, name := range []string{"mysql_perf_schema_events_statements_seconds_total", "mysql_perf_schema_events_statements_lock_time_seconds_total", "database_observability_wait_event_seconds_total"} {
					bs, fs := sortedMySQL(bm.Find(name)), sortedMySQL(fm.Find(name))
					for i := range bs {
						key := name + "|" + bs[i].Labels["digest"]
						bd, fd := bs[i].Value-previousBase[key], fs[i].Value-previousFault[key]
						if math.Abs(fd-bd*factor) > 1e-10 {
							t.Fatalf("%s duration delta not scaled by minute mean %v", name, factor)
						}
						previousBase[key], previousFault[key] = bs[i].Value, fs[i].Value
					}
				}
				for si, stream := range fl.Streams {
					op := stream.Labels["op"]
					if op != "query_sample" && op != "wait_event" && op != "wait_event_v2" {
						continue
					}
					for li, line := range stream.Lines {
						baseLine := bl.Streams[si].Lines[li]
						key := "elapsed_time"
						if op != "query_sample" {
							key = "wait_time"
						}
						bd, err := time.ParseDuration(parseLogfmtField(baseLine.Body, key))
						if err != nil {
							t.Fatal(err)
						}
						fd, err := time.ParseDuration(parseLogfmtField(line.Body, key))
						if err != nil {
							t.Fatal(err)
						}
						if math.Abs(float64(fd)-float64(bd)*factor) > 1 {
							t.Fatalf("%s not coherently scaled: %s", op, line.Body)
						}
						if op == "query_sample" {
							if parseLogfmtField(line.Body, "elapsed_time") != parseLogfmtField(line.Body, "elapsed_time_ms") {
								t.Fatal("elapsed fields differ")
							}
							want := at.Add(-fd).Add(-time.Duration(li%4) * 15 * time.Second)
							if line.T.Sub(want) > time.Nanosecond || want.Sub(line.T) > time.Nanosecond {
								t.Fatalf("query start %v want %v", line.T, want)
							}
							digest := parseLogfmtField(line.Body, "digest")
							found := false
							for _, s := range fm.Find("mysql_perf_schema_events_statements_seconds_total") {
								if s.Labels["digest"] == digest {
									found = true
								}
							}
							if !found {
								t.Fatal("metric/log digest join missing")
							}
						} else {
							if parseLogfmtField(line.Body, "wait_event_name") != parseLogfmtField(baseLine.Body, "wait_event_name") {
								t.Fatal("binding reclassified acquisition wait as server wait")
							}
						}
					}
				}
			}
		})
	}
}

func TestMySQLInvalidBindings(t *testing.T) {
	for _, name := range []string{"missing", "db-pool"} {
		cfg := mysqlConfig(t, name)
		if name == "db-pool" {
			any(cfg).(causal.Consumer).UseResources(causal.Set{})
		}
		if _, err := dbo11ymysql.Build(cfg, &fixture.Set{DB: coretest.DB("mysql")}); err == nil {
			t.Fatalf("accepted invalid binding %q", name)
		}
	}
}

// A lost client-pool slot is not evidence of a server lock, SQL error, or a
// change to the server's configured limit. Legacy database-axis incidents must
// also be ignored by a bound adapter even when injected without the loader.
func TestMySQLPoolExhaustionAndLegacyIsolation(t *testing.T) {
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	cfg := mysqlConfig(t, "db-pool")
	bindings := any(cfg).(causal.BindingDeclarer).ResourceBindings(causal.BindContext{Instance: coretest.DB("mysql").Name})
	if len(bindings) != 1 || bindings[0].Identity["db_instance_identifier"] != coretest.DB("mysql").Name {
		t.Fatalf("wrong dbo identity: %+v", bindings)
	}
	have := map[string]bool{}
	for _, m := range dbo11ymysql.Registration().FailureModes {
		have[m.Name] = true
	}
	for _, m := range causal.FailureModes {
		if !have[m.Name] {
			t.Fatalf("missing resource mode %s", m.Name)
		}
	}
	for _, m := range dbo11ymysql.FailureModes {
		if !have[m.Name] {
			t.Fatalf("lost legacy mode %s", m.Name)
		}
	}
	for _, mode := range []string{"pool_exhausted", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			c := mysqlBuild(t, cfg)
			baseline := mysqlBuild(t, cfg)
			incident := "pool_exhausted@2026-10-24T10:00/5m#0.9@db-pool"
			if mode == "legacy" {
				incident = ""
			}
			incidents := []string{incident}
			if mode == "legacy" {
				incidents = nil
				for _, m := range dbo11ymysql.FailureModes {
					incidents = append(incidents, m.Name+"@2026-10-24T10:00/5m#1@"+coretest.DB("mysql").Name)
				}
			}
			eng := shape.New("UTC", incidents)
			for at := start; !at.After(start.Add(time.Minute)); at = at.Add(causal.Step) {
				c.(causal.Advancer).AdvanceCausal(at, eng)
			}
			fm, fl := mysqlTick(t, c, start.Add(time.Minute), eng)
			bm, bl := mysqlTick(t, baseline, start.Add(time.Minute), shape.New("UTC", nil))
			if mode == "legacy" {
				if !reflect.DeepEqual(sortedMySQL(fm.All()), sortedMySQL(bm.All())) || !reflect.DeepEqual(fl.Streams, bl.Streams) {
					t.Fatal("legacy database incident moved bound output")
				}
			} else {
				conns := fm.Find("mysql_global_status_threads_connected")[0].Value
				if math.Abs(conns-2) > 1e-12 {
					t.Fatalf("exhausted pool connected=%v want 2", conns)
				}
				if !reflect.DeepEqual(fl.Streams, bl.Streams) {
					t.Fatal("pool acquisition pressure invented server waits")
				}
				for _, name := range []string{"mysql_global_status_threads_running", "mysql_global_variables_max_connections", "mysql_perf_schema_events_statements_errors_total"} {
					if !reflect.DeepEqual(sortedMySQL(fm.Find(name)), sortedMySQL(bm.Find(name))) {
						t.Fatalf("pool exhaustion moved %s", name)
					}
				}
			}
		})
	}
	// A declared queue cannot be used as a database.
	set := mysqlResources()
	set.Envs[0].Resources[0].Kind = causal.KindQueue
	set.Envs[0].Resources[0].HeldAcross = ""
	any(cfg).(causal.Consumer).UseResources(set)
	if _, err := dbo11ymysql.Build(cfg, &fixture.Set{DB: coretest.DB("mysql"), Env: coretest.Env()}); err == nil {
		t.Fatal("accepted queue binding")
	}
}
