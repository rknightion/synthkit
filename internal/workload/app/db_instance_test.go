// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
)

// dbInstanceCfg is a frontend→api→pg(db) graph whose db leaf declares db_instance: envpg — the
// BASE RDS name. The per-env instance ("envpg-<lower(env)>") is resolved from the binding's
// Databases, so the db-client span carries the real RDS identity (server.address / db.namespace).
func dbInstanceCfg() *Config {
	return &Config{
		Traffic: Traffic{OffPeakRPS: 20, PeakRPS: 50},
		Services: []ServiceNode{
			{Name: "web-fe", Type: "frontend", Entry: true, Calls: []string{"api"}},
			{Name: "api", Type: "web", Runtime: "go", Calls: []string{"pg"}},
			{Name: "pg", Type: "db", DBInstance: "envpg"},
		},
	}
}

// envDB builds a per-env RDS fixture named "<base>-<lower(env)>" with a realistic endpoint FQDN
// (first DNS label == the db_instance_identifier), mirroring buildDBFixture.
func envDB(base, env string) *fixture.DB {
	name := base + "-" + strings.ToLower(env)
	host := name + ".abc123def456.us-east-1.rds.amazonaws.com"
	return &fixture.DB{
		Engine:        "postgres",
		EngineVersion: "16.4",
		Name:          name,
		InstanceKey:   "postgresql://" + host + ":5432/app",
		Databases:     []string{"app"},
	}
}

// buildAppEnv builds an app workload bound to the named env with the given databases available.
func buildAppEnv(t *testing.T, cfg *Config, env string, dbs ...*fixture.DB) *Workload {
	t.Helper()
	w, err := build(cfg, core.Binding{
		Name:      "demo-" + env,
		Env:       &fixture.Env{Name: env, Weight: 1.0},
		Cluster:   coretest.Cluster(),
		Databases: dbs,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return w.(*Workload)
}

// TestApp_DBLeafSpanCarriesEnvRDSIdentity asserts the db-leaf CLIENT span ("call pg") in env BVE
// carries the env RDS identity: db.system.name=postgresql, db.namespace=app, and server.address
// whose first DNS label is the per-env RDS db_instance_identifier "envpg-bve".
func TestApp_DBLeafSpanCarriesEnvRDSIdentity(t *testing.T) {
	w := buildAppEnv(t, dbInstanceCfg(), "BVE", envDB("envpg", "BVE"))
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})

	r := w.m.mintOne(now, world.Shape)
	tc := world.Traces.(*coretest.TraceCapture)
	if err := w.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}

	var found bool
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			if sp.Name != "call pg" {
				continue
			}
			found = true
			if got, _ := sp.Attrs["db.system.name"].(string); got != "postgresql" {
				t.Errorf("db.system.name = %q, want postgresql", got)
			}
			if got, _ := sp.Attrs["db.namespace"].(string); got != "app" {
				t.Errorf("db.namespace = %q, want app", got)
			}
			if _, dep := sp.Attrs["db.name"]; dep {
				t.Errorf("deprecated db.name must not be emitted alongside db.namespace")
			}
			addr, _ := sp.Attrs["server.address"].(string)
			if first, _, _ := strings.Cut(addr, "."); first != "envpg-bve" {
				t.Errorf("server.address = %q, want first DNS label envpg-bve", addr)
			}
		}
	}
	if !found {
		t.Fatal("no 'call pg' db-leaf client span emitted")
	}
}

// TestApp_DBLeafEdgeConnectionTypeDatabase asserts the service-graph edge api→pg (a db leaf)
// carries connection_type=database (mirroring web_service tickServiceGraph), while a non-db edge
// keeps connection_type="".
func TestApp_DBLeafEdgeConnectionTypeDatabase(t *testing.T) {
	w := buildAppEnv(t, dbInstanceCfg(), "BVE", envDB("envpg", "BVE"))
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil) // EmitSpanMetrics defaults true
	if err := w.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	connByServer := map[string]string{}
	for _, s := range mc.Find("traces_service_graph_request_total") {
		connByServer[s.Labels["server"]] = s.Labels["connection_type"]
	}
	if connByServer["pg"] != "database" {
		t.Errorf("edge to db leaf pg: connection_type=%q, want database (have %v)", connByServer["pg"], connByServer)
	}
	if connByServer["api"] != "" {
		t.Errorf("edge to instrumented service api: connection_type=%q, want \"\"", connByServer["api"])
	}
}
