// SPDX-License-Identifier: AGPL-3.0-only

package docdb

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestDocDB_EmitsClusterStatsWithRoleSplit(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		DB:    &fixture.DB{Name: "app-docdb", Engine: "docdb"},
	}
	c, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// A representative base emits all 5 stat suffixes.
	if got := mc.Find("aws_docdb_cpuutilization_average"); len(got) == 0 {
		t.Fatal("missing aws_docdb_cpuutilization_average")
	}
	if got := mc.Find("aws_docdb_database_connections_sum"); len(got) == 0 {
		t.Fatal("missing aws_docdb_database_connections_sum")
	}
	// WRITER/READER role split present on cluster series.
	roles := map[string]bool{}
	for _, s := range mc.Find("aws_docdb_database_connections_average") {
		roles[s.Labels["dimension_Role"]] = true
		if s.Labels["dimension_DBClusterIdentifier"] != "app-docdb" {
			t.Errorf("cluster id=%q want app-docdb", s.Labels["dimension_DBClusterIdentifier"])
		}
		if s.Labels["namespace"] != "AWS/DocDB" {
			t.Errorf("namespace=%q want AWS/DocDB", s.Labels["namespace"])
		}
	}
	if !roles["WRITER"] || !roles["READER"] {
		t.Fatalf("want both WRITER and READER role series, got %v", roles)
	}
	// (Per-period-gauge / state.Set-not-Add semantics for the CW path are guaranteed by
	// cw.EmitStats and covered by cwinfra's TestPerPeriodGauge_NoMonotonicAccumulation.)
}

// A pool-bound cluster reports the pool's in-use connections once: on the WRITER row, which
// takes the connections the service's pool holds. The READER row keeps its declared baseline,
// so summing the roles counts the pool's connections once rather than twice.
func TestDocDB_PoolBoundConnectionsAreNotDoubleCounted(t *testing.T) {
	res := decodeResources(t, `
- {name: records-db, kind: dependency, latency_ms: 5, slow_factor: 6}
- {name: records-pool, kind: pool, size: 20, hold_ms: 10, held_across: records-db, acquire_timeout_ms: 2000,
   demand: {off_peak_rps: 100, peak_rps: 100, load: constant}}
`)
	set := causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: res}}}
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		Env:   &fixture.Env{Name: "prod", Weight: 1},
		DB:    &fixture.DB{Name: "app-docdb", Engine: "docdb"},
	}
	cfg := &Config{}
	cfg.BindResource("records-pool")
	cfg.UseResources(set)
	bound, err := Build(cfg, fx)
	if err != nil {
		t.Fatalf("Build bound: %v", err)
	}
	unbound, err := Build(&Config{}, fx)
	if err != nil {
		t.Fatalf("Build unbound: %v", err)
	}
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	conns := func(c core.Construct) map[string]float64 {
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		out := map[string]float64{}
		for _, s := range mc.Find("aws_docdb_database_connections_average") {
			out[s.Labels["dimension_Role"]] = s.Value
		}
		return out
	}
	b, u := conns(bound), conns(unbound)
	// 100 rps holding a connection 10 ms is 1.0 busy connection.
	if math.Abs(b["WRITER"]-1.0) > 1e-9 {
		t.Errorf("WRITER connections %v, want the pool's 1.0 in use", b["WRITER"])
	}
	if b["READER"] != u["READER"] {
		t.Errorf("READER connections %v, want the declared baseline %v (the pool's in-use count is not a reader's)", b["READER"], u["READER"])
	}
}

func decodeResources(t *testing.T, doc string) []causal.Resource {
	t.Helper()
	var rs []causal.Resource
	dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
	dec.KnownFields(true)
	if err := dec.Decode(&rs); err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	return rs
}
