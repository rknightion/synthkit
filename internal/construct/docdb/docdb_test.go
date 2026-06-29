// SPDX-License-Identifier: AGPL-3.0-only

package docdb

import (
	"context"
	"testing"
	"time"

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
