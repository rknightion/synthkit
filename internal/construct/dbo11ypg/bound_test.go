// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ypg_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/construct/dbo11ypg"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

func pgSet() causal.Set {
	// Application requests hold a connection 100ms (80ms in the database), at 50/s.
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "pool-prod", Kind: causal.KindPool, Size: 20, HoldMS: 100, HeldAcross: "db", AcquireTimeoutMS: 2000, Demand: &causal.Demand{OffPeakRPS: 50, PeakRPS: 50, Load: causal.LoadConstant}},
		{Name: "db", Kind: causal.KindDependency, LatencyMS: 80, SlowFactor: 4},
	}}}}
}
func pgBuild(t *testing.T, resource string, set causal.Set) core.Construct {
	t.Helper()
	cfg := &dbo11ypg.Config{}
	if resource != "" {
		b, ok := any(cfg).(interface{ BindResource(string) })
		if !ok {
			t.Fatal("Config lacks BindResource")
		}
		b.BindResource(resource)
		u, ok := any(cfg).(causal.Consumer)
		if !ok {
			t.Fatal("Config lacks causal.Consumer")
		}
		u.UseResources(set)
		d, ok := any(cfg).(causal.BindingDeclarer)
		if !ok {
			t.Fatal("Config lacks BindingDeclarer")
		}
		bs := d.ResourceBindings(causal.BindContext{Instance: "test-db"})
		if len(bs) != 1 || bs[0].Identity["db_instance_identifier"] != "test-db" {
			t.Fatalf("binding identity: %+v", bs)
		}
	}
	c, err := dbo11ypg.Reg.Build(cfg, &fixture.Set{DB: coretest.DB("postgres"), Env: &fixture.Env{Name: "prod", Weight: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func pgTick(t *testing.T, c core.Construct, now time.Time, eng *shape.Engine) (*coretest.MetricCapture, *coretest.LogCapture) {
	t.Helper()
	m, l := &coretest.MetricCapture{}, &coretest.LogCapture{}
	if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: m, Logs: l}); err != nil {
		t.Fatal(err)
	}
	return m, l
}
func TestBoundMinuteDrivesMetricsAndLogs(t *testing.T) {
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for _, resource := range []string{"pool", "db"} {
		t.Run(resource, func(t *testing.T) {
			c := pgBuild(t, resource, pgSet())
			base := pgBuild(t, resource, pgSet())
			a, ok := c.(causal.Advancer)
			if !ok {
				t.Fatal("missing Advancer")
			}
			eng := shape.New("UTC", []string{"dependency_slow@2026-10-24T10:01/1m#1@db", "pool_exhausted@2026-10-24T10:02/1m#0.9@pool-prod"})
			healthy := shape.New("UTC", nil)
			model, err := causal.Compile(pgSet())
			if err != nil {
				t.Fatal(err)
			}
			var prev, prevBase = map[string]float64{}, map[string]float64{}
			for tick := 1; tick <= 3; tick++ {
				now := start.Add(time.Duration(tick) * time.Minute)
				// Exercise master advancement with irregular sub-minute sampling and no emission.
				for at := now.Add(-time.Minute); !at.After(now); at = at.Add(7 * time.Second) {
					a.AdvanceCausal(at, eng)
					model.Advance(at, eng)
				}
				model.Advance(now, eng)
				m, l := pgTick(t, c, now, eng)
				bm, bl := pgTick(t, base, now, healthy)
				name := resource
				if resource == "pool" {
					name = "pool-prod"
				}
				var factor float64
				for at := now.Add(-55 * time.Second); !at.After(now); at = at.Add(causal.Step) {
					s, ok := model.State(name, at)
					if !ok || !s.At.Equal(at) {
						t.Fatal("missing model step")
					}
					factor += s.LatencyFactor
				}
				factor /= 12
				if tick == 2 && factor <= 1 {
					t.Fatal("fault did not affect reference model")
				}
				for _, s := range m.Find("pg_stat_statements_seconds_total") {
					id := s.Labels["queryid"]
					var bv float64
					for _, b := range bm.Find(s.Name) {
						if b.Labels["queryid"] == id {
							bv = b.Value
						}
					}
					got := (s.Value - prev[id]) / (bv - prevBase[id])
					if math.Abs(got-factor) > 1e-10 {
						t.Errorf("duration delta ratio %v want mean %v", got, factor)
					}
					prev[id] = s.Value
					prevBase[id] = bv
				}
				if resource == "pool" {
					win, _ := model.Window(name, now.Add(-time.Minute), now)
					if got := m.Find("pg_stat_database_numbackends")[0].Value; got != win.MeanInUse {
						t.Errorf("connections %v want mean %v", got, win.MeanInUse)
					}
				} else if m.Find("pg_stat_database_numbackends")[0].Value != bm.Find("pg_stat_database_numbackends")[0].Value {
					t.Error("dependency changed connections")
				}
				if len(m.Find("database_observability_wait_event_seconds_total")) != 0 {
					t.Error("Postgres emitted MySQL metric")
				}
				for si, s := range l.Streams {
					op := s.Labels["op"]
					if op != "query_sample" && op != "wait_event" && op != "wait_event_v2" {
						continue
					}
					for i, line := range s.Lines {
						b := bl.Streams[si].Lines[i]
						field := "query_time"
						if op != "query_sample" {
							field = "wait_time"
						}
						dur, e := time.ParseDuration(parseLogfmt(line.Body, field))
						if e != nil {
							t.Fatal(e)
						}
						bd, _ := time.ParseDuration(parseLogfmt(b.Body, field))
						want := time.Duration(float64(bd) * factor)
						if math.Abs(float64(dur-want)) > 2 {
							t.Errorf("%s duration %v want %v", op, dur, want)
						}
						if op != "query_sample" {
							// Wait timestamps must join the query sample by pid, not by wait duration.
							found := false
							for _, qs := range l.Streams {
								if qs.Labels["op"] == "query_sample" {
									for _, ql := range qs.Lines {
										if parseLogfmt(ql.Body, "pid") == parseLogfmt(line.Body, "pid") {
											found = true
											if !ql.T.Equal(line.T) {
												t.Error("wait/sample timestamps disagree")
											}
										}
									}
								}
							}
							if !found {
								t.Error("wait missing query sample")
							}
						} else if !line.T.Equal(now.Add(-dur).Add(-time.Duration(i%4) * 15 * time.Second)) {
							t.Error("query timestamp disagrees with rendered duration")
						}
						if parseLogfmt(line.Body, "wait_event_name") != parseLogfmt(b.Body, "wait_event_name") {
							t.Error("causal fault changed wait category")
						}
					}
				}
				for _, s := range m.Find("database_observability_pg_errors_total") {
					for _, b := range bm.Find(s.Name) {
						if s.Labels["sqlstate"] == b.Labels["sqlstate"] && s.Value != b.Value {
							t.Error("causal fault invented SQL errors")
						}
					}
				}
			}
		})
	}
}

func TestBindingValidationAndLegacyIsolation(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  causal.Set
		want string
	}{
		{"pool", causal.Set{}, "no environments"},
		{"missing", pgSet(), "not a declared resource"},
		{"queue", causal.Set{Envs: []causal.Declaration{{Resources: []causal.Resource{{Name: "queue", Kind: causal.KindQueue}}}}}, "dependency or pool"},
	} {
		cfg := &dbo11ypg.Config{}
		b, ok := any(cfg).(interface{ BindResource(string) })
		if !ok {
			t.Fatal("missing binder")
		}
		b.BindResource(tc.name)
		u, ok := any(cfg).(causal.Consumer)
		if !ok {
			t.Fatal("missing consumer")
		}
		u.UseResources(tc.set)
		_, err := dbo11ypg.Reg.Build(cfg, &fixture.Set{DB: coretest.DB("postgres")})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("binding %s: %v", tc.name, err)
		}
	}
	now := time.Date(2026, 10, 24, 10, 1, 0, 0, time.UTC)
	legacy := []string{"connection_saturation@2026-10-24T10:00/10m#1@test-db", "lock_contention@2026-10-24T10:00/10m#1@test-db", "slow_query_storm@2026-10-24T10:00/10m#1@test-db", "replication_lag@2026-10-24T10:00/10m#1@test-db"}
	m, l := pgTick(t, pgBuild(t, "pool", pgSet()), now, shape.New("UTC", legacy))
	bm, bl := pgTick(t, pgBuild(t, "pool", pgSet()), now, shape.New("UTC", nil))
	metricRows := func(cap *coretest.MetricCapture) []string {
		rows := []string{}
		for _, s := range cap.All() {
			b, _ := json.Marshal(s)
			rows = append(rows, string(b))
		}
		sort.Strings(rows)
		return rows
	}
	if !reflect.DeepEqual(metricRows(m), metricRows(bm)) || !reflect.DeepEqual(l.Streams, bl.Streams) {
		t.Fatal("bound database responded to legacy modes")
	}
	modes := map[string]bool{}
	for _, m := range dbo11ypg.Registration().FailureModes {
		modes[m.Name] = true
	}
	for _, m := range causal.FailureModes {
		if !modes[m.Name] {
			t.Errorf("missing resource mode %s", m.Name)
		}
	}
}

func TestUnboundFullOutputCompatibility(t *testing.T) {
	c := pgBuild(t, "", causal.Set{})
	eng := shape.New("UTC", []string{"connection_saturation@2026-10-24T10:00/10m#1@test-db", "lock_contention@2026-10-24T10:00/10m#1@test-db", "slow_query_storm@2026-10-24T10:00/10m#1@test-db", "replication_lag@2026-10-24T10:00/10m#1@test-db"})
	var rows []string
	for i := 0; i < 3; i++ {
		m, l := pgTick(t, c, time.Date(2026, 10, 24, 10, i, 0, 0, time.UTC), eng)
		for _, s := range m.All() {
			b, _ := json.Marshal(s)
			rows = append(rows, string(b))
		}
		for _, s := range l.Streams {
			b, _ := json.Marshal(s)
			rows = append(rows, string(b))
		}
	}
	sort.Strings(rows)
	b, _ := json.Marshal(rows)
	got := fmt.Sprintf("%x", sha256.Sum256(b))
	const want = "b88be60121e15b52ab2521d4d8f621feda2d12ac8d257d85fc900aaad3da50aa"
	if got != want {
		t.Fatalf("full unbound digest %s want %s", got, want)
	}
}
