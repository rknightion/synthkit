// SPDX-License-Identifier: AGPL-3.0-only

package mwaa

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

func TestMWAA_EmitsBothNamespaces(t *testing.T) {
	fx := &fixture.Set{
		Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"},
		Seed:  "test-seed",
	}
	c, err := Build(&Config{Environments: []string{"my-env"}}, fx)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// aws_mwaa_* (AWS/MWAA namespace) emitted.
	if got := mc.Find("aws_mwaa_queued_tasks_average"); len(got) == 0 {
		t.Fatal("missing aws_mwaa_queued_tasks_average")
	}
	if got := mc.Find("aws_mwaa_queued_tasks_sum"); len(got) == 0 {
		t.Fatal("missing aws_mwaa_queued_tasks_sum")
	}
	// aws_amazonmwaa_* (AmazonMWAA namespace) emitted.
	if got := mc.Find("aws_amazonmwaa_scheduler_heartbeat_average"); len(got) == 0 {
		t.Fatal("missing aws_amazonmwaa_scheduler_heartbeat_average")
	}
	// Namespace labels correct.
	for _, s := range mc.Find("aws_mwaa_queued_tasks_average") {
		if s.Labels["namespace"] != "AWS/MWAA" {
			t.Errorf("namespace=%q want AWS/MWAA", s.Labels["namespace"])
		}
		if s.Labels["dimension_Environment"] != "my-env" {
			t.Errorf("dimension_Environment=%q want my-env", s.Labels["dimension_Environment"])
		}
	}
	for _, s := range mc.Find("aws_amazonmwaa_scheduler_heartbeat_average") {
		if s.Labels["namespace"] != "AmazonMWAA" {
			t.Errorf("namespace=%q want AmazonMWAA", s.Labels["namespace"])
		}
	}
}
