// SPDX-License-Identifier: AGPL-3.0-only

package runner

// incident_integration_test.go — end-to-end proof that ONE fired AxisDatabase incident reaches
// BOTH lanes a database fans into (the RDS CloudWatch construct, blueprint-scoped, and the dbo11y
// construct, substrate-scoped) via the SHARED per-blueprint shape engine. This is the runtime
// guarantee the per-construct unit tests cannot show: they inject a shape.Engine by hand, whereas
// here the fire travels control-plane → runner Live closure → both constructs' Worlds.
//
// connection_saturation is chosen because BOTH the rds and dbo11y_mysql constructs react to it
// (RDS database_connections, dbo11y mysql threads_connected). The baseline and fire runs share the
// same seed + tick time, so the shape noise is identical between them — the incident is the ONLY
// difference, making "fire > baseline" an exact, deterministic correlation assertion.
// (replication_lag is NOT used: a standalone RDS primary emits no ReplicaLag — live-reference-confirmed — so
// that mode reaches only the dbo11y lane, not both.)

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
)

// runDBIncident builds a one-blueprint runner whose single database fans into both the rds and
// dbo11y_mysql constructs (sharing one fixture.Set, exactly as the resolver wires a both-lanes DB),
// optionally fires connection_saturation scoped to the DB, runs one cycle, and returns the two lanes'
// connection readings: (aws_rds_database_connections, mysql_global_status_threads_connected).
func runDBIncident(t *testing.T, fire bool) (rdsConns, dboConns float64) {
	t.Helper()
	db := coretest.DB("mysql")
	shared := &fixture.Set{DB: db, Cloud: coretest.Cloud(), Env: coretest.Env(), Seed: "intg:" + db.Name}

	res := &blueprint.Resolved{
		Name:  "intg",
		Label: "intg",
		Constructs: []blueprint.ConstructInstance{
			{Kind: "rds", Name: db.Name, Config: &struct{}{}, Fixtures: shared},
			{Kind: "dbo11y_mysql", Name: db.Name, Config: &struct{}{}, Fixtures: shared},
		},
		// The database is an addressable AxisDatabase target (mirrors blueprint/scenario.go).
		Targets: []blueprint.Target{{Name: db.Name, Axis: failuremode.AxisDatabase}},
	}

	mc, lc := &coretest.MetricCapture{}, &coretest.LogCapture{}
	r := New(Sinks{Metrics: mc, Logs: lc}, Catalog(), Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("AddBlueprint: %v", err)
	}

	st := control.State{VolumeMultiplier: 1.0}
	if fire {
		st.Failures = map[string]control.FailureSetting{
			"connection_saturation": {Enabled: true, Intensity: 1.0, Scope: db.Name},
		}
	}
	r.ApplyControl(st)

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := r.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if s := mc.Find("aws_rds_database_connections_average"); len(s) > 0 {
		rdsConns = s[0].Value
	}
	if s := mc.Find("mysql_global_status_threads_connected"); len(s) > 0 {
		dboConns = s[0].Value
	}
	return rdsConns, dboConns
}

// TestDBIncidentReachesBothLanes proves cross-lane correlation: one fired connection_saturation
// incident (scoped to the shared db.Name) drives BOTH the RDS CloudWatch lane
// (aws_rds_database_connections) and the dbo11y lane (mysql_global_status_threads_connected) above
// their baseline in the same cycle.
func TestDBIncidentReachesBothLanes(t *testing.T) {
	// Baseline (no incident) vs fire, same seed+tick → identical shape noise, so the incident is the
	// only difference. One fired connection_saturation must lift BOTH lanes above their baseline.
	rdsBase, dboBase := runDBIncident(t, false)
	rdsHot, dboHot := runDBIncident(t, true)

	if rdsBase <= 0 || dboBase <= 0 {
		t.Fatalf("baseline connections not emitted (rds=%v dbo=%v)", rdsBase, dboBase)
	}
	if rdsHot <= rdsBase {
		t.Errorf("under connection_saturation, aws_rds_database_connections=%v not above baseline %v (CloudWatch lane did not react)", rdsHot, rdsBase)
	}
	if dboHot <= dboBase {
		t.Errorf("under connection_saturation, mysql_global_status_threads_connected=%v not above baseline %v (dbo11y lane did not react)", dboHot, dboBase)
	}
}
