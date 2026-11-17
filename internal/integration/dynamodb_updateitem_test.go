// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
)

// A declared UpdateItem operation, loaded from blueprint YAML through the real loader and catalog,
// emits SuccessfulRequestLatency on TableName+Operation=UpdateItem and nothing else of its own, and
// splits the table's write demand with PutItem: ConsumedWriteCapacityUnits (TableName only) equals
// that of a table serving the same demand as PutItem alone.
func TestDynamoDBUpdateItemSplitsWriteDemand(t *testing.T) {
	data, err := os.ReadFile("../construct/dynamodb/testdata/update-item.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
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
		start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
		for i := 1; i <= 5; i++ {
			mc := &coretest.MetricCapture{}
			if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Metrics: mc}); err != nil {
				t.Fatal(err)
			}
			// table -> name -> operation -> value, for the sparse and capacity families only.
			vals := map[string]float64{}
			var keys []string
			for _, s := range mc.All() {
				tbl := s.Labels["dimension_TableName"]
				if tbl == "" {
					continue
				}
				k := s.Name + "/" + tbl + "/" + s.Labels["dimension_Operation"]
				vals[k] = s.Value
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, f := range []string{"_sum", "_average", "_sample_count"} {
				w, s := "aws_dynamodb_consumed_write_capacity_units"+f+"/fixture-whole/", "aws_dynamodb_consumed_write_capacity_units"+f+"/fixture-split/"
				if _, ok := vals[w]; !ok || vals[w] != vals[s] {
					t.Fatalf("minute %d: consumed write %s whole=%v split=%v (%v)", i, f, vals[w], vals[s], keys)
				}
			}
			if vals["aws_dynamodb_consumed_write_capacity_units_sum/fixture-split/"] != 60 {
				t.Fatalf("minute %d: split write units %v, want 60", i, vals["aws_dynamodb_consumed_write_capacity_units_sum/fixture-split/"])
			}
			// 0.5 requests per second for 60 s: 30 requests each, latency sum 30 x declared mean.
			for k, want := range map[string]float64{
				"aws_dynamodb_successful_request_latency_sum/fixture-split/PutItem":             30 * 6,
				"aws_dynamodb_successful_request_latency_sum/fixture-split/UpdateItem":          30 * 8,
				"aws_dynamodb_successful_request_latency_sample_count/fixture-split/UpdateItem": 30,
				"aws_dynamodb_successful_request_latency_sum/fixture-whole/PutItem":             60 * 6,
			} {
				if vals[k] != want {
					t.Fatalf("minute %d: %s = %v, want %v (%v)", i, k, vals[k], want, keys)
				}
			}
			// UpdateItem owns only its latency series: no returned-item, throttle or read series,
			// and no UpdateItem on the other table.
			for _, k := range keys {
				if !strings.HasSuffix(k, "/UpdateItem") {
					continue
				}
				if !strings.HasPrefix(k, "aws_dynamodb_successful_request_latency_") || !strings.Contains(k, "/fixture-split/") {
					t.Fatalf("minute %d: unexpected UpdateItem series %s", i, k)
				}
			}
		}
	}
	if built != 1 {
		t.Fatalf("want one dynamodb construct, built %d", built)
	}
}
