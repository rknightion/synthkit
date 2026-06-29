// SPDX-License-Identifier: AGPL-3.0-only

package glue

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestGlue_EmitsJobMetrics(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		Seed:  "test-seed",
	}
	c, err := Build(&Config{Jobs: []string{"my-etl-job"}}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Aggregate delta metric present.
	if got := mc.Find("aws_glue_driver_aggregate_bytes_read_average"); len(got) == 0 {
		t.Fatal("missing aws_glue_driver_aggregate_bytes_read_average")
	}
	if got := mc.Find("aws_glue_driver_aggregate_bytes_read_sum"); len(got) == 0 {
		t.Fatal("missing aws_glue_driver_aggregate_bytes_read_sum")
	}
	// JVM gauge metric present.
	if got := mc.Find("aws_glue_driver_jvm_heap_usage_average"); len(got) == 0 {
		t.Fatal("missing aws_glue_driver_jvm_heap_usage_average")
	}
	// Namespace must be literal "Glue" (NOT "AWS/Glue").
	for _, s := range mc.Find("aws_glue_driver_aggregate_bytes_read_average") {
		if s.Labels["namespace"] != "Glue" {
			t.Errorf("namespace=%q want Glue", s.Labels["namespace"])
		}
		if s.Labels["dimension_JobName"] != "my-etl-job" {
			t.Errorf("dimension_JobName=%q want my-etl-job", s.Labels["dimension_JobName"])
		}
		if s.Labels["dimension_JobRunId"] != "ALL" {
			t.Errorf("dimension_JobRunId=%q want ALL", s.Labels["dimension_JobRunId"])
		}
		if s.Labels["job"] != "cloud/aws/glue" {
			t.Errorf("job label=%q want cloud/aws/glue", s.Labels["job"])
		}
	}
}
