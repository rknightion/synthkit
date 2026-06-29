// SPDX-License-Identifier: AGPL-3.0-only

package neptune

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestNeptune_EmitsClusterStatsWithRoleSplit(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		DB:    &fixture.DB{Name: "app-neptune", Engine: "neptune"},
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
	if got := mc.Find("aws_neptune_cpuutilization_average"); len(got) == 0 {
		t.Fatal("missing aws_neptune_cpuutilization_average")
	}
	if got := mc.Find("aws_neptune_gremlin_requests_per_sec_sum"); len(got) == 0 {
		t.Fatal("missing aws_neptune_gremlin_requests_per_sec_sum")
	}
	roles := map[string]bool{}
	for _, s := range mc.Find("aws_neptune_cpuutilization_average") {
		roles[s.Labels["dimension_Role"]] = true
		if s.Labels["dimension_DBClusterIdentifier"] != "app-neptune" {
			t.Errorf("cluster id=%q want app-neptune", s.Labels["dimension_DBClusterIdentifier"])
		}
		if s.Labels["namespace"] != "AWS/Neptune" {
			t.Errorf("namespace=%q want AWS/Neptune", s.Labels["namespace"])
		}
	}
	if !roles["WRITER"] || !roles["READER"] {
		t.Fatalf("want both WRITER and READER role series, got %v", roles)
	}
}
