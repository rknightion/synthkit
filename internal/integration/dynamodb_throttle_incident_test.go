// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
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

// A declared table incident, loaded from blueprint YAML through the real loader and catalog,
// drives the DynamoDB throttle families: absent at baseline, positive while it is active, and
// only on the table it names.
func TestDynamoDBTableThrottleIncident(t *testing.T) {
	data, err := os.ReadFile("../construct/dynamodb/testdata/table-throttle-incident.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	var tableTargets []string
	for _, tg := range res.Targets {
		if tg.Axis == "table" {
			tableTargets = append(tableTargets, tg.Name)
		}
	}
	if strings.Join(tableTargets, ",") != "fixture-orders,fixture-audit" {
		t.Fatalf("table targets %v", tableTargets)
	}
	if len(res.Incidents) != 1 || !strings.HasPrefix(res.Incidents[0], "table_throttle_storm@2026-10-24T09:10/10m#1@fixture-orders") {
		t.Fatalf("compiled incidents %v", res.Incidents)
	}

	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	for _, withIncident := range []bool{false, true} {
		var entries []string
		if withIncident {
			entries = res.Incidents
		}
		eng := shape.New("UTC", entries)
		built := 0
		for _, ci := range res.Constructs {
			if ci.Kind != "dynamodb" {
				continue
			}
			built++
			cr, _ := reg.Construct(ci.Kind)
			c, err := cr.Build(ci.Config, ci.Fixtures)
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 30; i++ {
				mc := &coretest.MetricCapture{}
				if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc}); err != nil {
					t.Fatal(err)
				}
				throttles := map[string]float64{}
				for _, s := range mc.All() {
					if strings.HasSuffix(s.Name, "_sum") && strings.Contains(s.Name, "throttle") {
						throttles[s.Name+"/"+s.Labels["dimension_TableName"]+"/"+s.Labels["dimension_Operation"]] += s.Value
					}
				}
				active := withIncident && i >= 10 && i <= 19
				if !active {
					if len(throttles) != 0 {
						t.Fatalf("incident=%v minute %d: throttle series without an active incident: %v", withIncident, i, throttles)
					}
					continue
				}
				// 60 GetItem and 30 PutItem requests per minute, intensity 1: all throttled again.
				for key, want := range map[string]float64{
					"aws_dynamodb_throttled_requests_sum/fixture-orders/GetItem": 60,
					"aws_dynamodb_throttled_requests_sum/fixture-orders/PutItem": 30,
					"aws_dynamodb_read_throttle_events_sum/fixture-orders/":      60,
					"aws_dynamodb_write_throttle_events_sum/fixture-orders/":     30,
				} {
					if throttles[key] != want {
						t.Fatalf("minute %d: %s = %v, want %v (all %v)", i, key, throttles[key], want, throttles)
					}
				}
				if len(throttles) != 4 {
					t.Fatalf("minute %d: the untargeted table or another family throttled: %v", i, throttles)
				}
			}
		}
		if built != 1 {
			t.Fatalf("want one dynamodb construct, built %d", built)
		}
	}
}
