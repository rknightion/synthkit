// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
)

// A real master-volume pause must suppress the writer, including account/capacity
// gauges, and neither create arrivals nor erase fractional active-window carry.
func TestMasterVolumePause(t *testing.T) {
	cfg := config()
	// Binary-exact sparse rates isolate pause/carry behavior from floating-point
	// rounding at an event boundary: 128 observed active seconds earn one event.
	cfg.Tables[0].Operations[0].RequestsPerSecond = 1.0 / 128
	cfg.Tables[0].Operations[0].ReadUnits = 0.5
	cfg.Tables[0].ConditionalFailuresPerSecond = 1.0 / 128
	cfg.Tables[0].Stream = &Stream{Label: "stream", RequestsPerSecond: 1.0 / 128, RecordsPerRequest: 2, BytesPerRecord: 3, LatencyMS: 4}
	replica := cfg.Tables[0]
	replica.Name = "replica"
	replica.CollectorName = "replica-collector"
	replica.Operations = nil
	replica.Stream = nil
	replica.ConditionalFailuresPerSecond = 0
	replica.WriteSource = "GlobalTable"
	replica.Replication = &Replication{WritesPerSecond: 1.0 / 128, UnitsPerWrite: 1}
	cfg.Tables = append(cfg.Tables, replica)
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", nil)
	w := &capture{}
	world := &core.World{Metrics: w, Shape: eng}
	now := time.Unix(1800000000, 0)
	run := func(offset time.Duration) {
		t.Helper()
		if err := c.Tick(context.Background(), now.Add(offset), world); err != nil {
			t.Fatal(err)
		}
	}
	run(0) // 60/128 arrivals retained independently in each modeled lane.
	calls := w.calls
	eng.SetVolumeMultiplier(0)
	run(10 * time.Second)
	run(1000 * time.Second)
	if w.calls != calls {
		t.Fatalf("master volume zero emitted %d batches", w.calls-calls)
	}
	eng.SetVolumeMultiplier(2) // Nonzero volume keeps the fixed operator model.
	run(1010 * time.Second)    // Establish active boundary, not a paused lookback.
	if w.calls != calls {
		t.Fatal("resume fabricated a paused reporting window")
	}
	run(1044 * time.Second) // (60+34)/128: still no completed requests.
	absent(t, w, "aws_dynamodb_successful_request_latency_sum")
	absent(t, w, "aws_dynamodb_returned_bytes_sum")
	absent(t, w, "aws_dynamodb_conditional_check_failed_requests_sum")
	if find(t, w, "aws_dynamodb_consumed_write_capacity_units_sum").Value != 0 {
		t.Fatal("replica traffic accrued during pause")
	}
	run(1078 * time.Second) // (60+34+34)/128: exactly one arrival in each lane.
	if find(t, w, "aws_dynamodb_returned_bytes_sum").Value != 6 || find(t, w, "aws_dynamodb_returned_bytes_sample_count").Value != 1 {
		t.Fatal("poll carry lost or pause-time polls fabricated")
	}
	if find(t, w, "aws_dynamodb_conditional_check_failed_requests_sum").Value != 1 {
		t.Fatal("failure carry lost or pause-time failures fabricated")
	}
	var query, replicated bool
	for _, s := range w.batch {
		if s.Name == "aws_dynamodb_consumed_read_capacity_units_sum" && s.Labels["dimension_TableName"] == "orders" {
			query = true
			if s.Value != 0.5 {
				t.Fatal("operation carry or fractional read capacity changed", s)
			}
		}
		if s.Name == "aws_dynamodb_consumed_write_capacity_units_sum" && s.Labels["dimension_Source"] == "GlobalTable" {
			replicated = true
			if s.Value != 1 {
				t.Fatal("replica carry lost or pause-time replicas fabricated", s)
			}
		}
	}
	if !query || !replicated {
		t.Fatal("missing modeled lane")
	}
}

func TestInitiallyPausedHasNoLookback(t *testing.T) {
	cfg := config()
	cfg.Tables[0].Operations[0].RequestsPerSecond = 0.1
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", nil)
	eng.SetVolumeMultiplier(0)
	w := &capture{}
	world := &core.World{Metrics: w, Shape: eng}
	now := time.Unix(1800000000, 0)
	for _, offset := range []time.Duration{0, 100 * time.Second} {
		if err := c.Tick(context.Background(), now.Add(offset), world); err != nil {
			t.Fatal(err)
		}
	}
	eng.SetVolumeMultiplier(1)
	if err := c.Tick(context.Background(), now.Add(1000*time.Second), world); err != nil {
		t.Fatal(err)
	}
	if w.calls != 0 {
		t.Fatal("initial pause/resume emitted a lookback batch")
	}
	if err := c.Tick(context.Background(), now.Add(1010*time.Second), world); err != nil {
		t.Fatal(err)
	}
	if find(t, w, "aws_dynamodb_successful_request_latency_sample_count").Value != 1 {
		t.Fatal("resumed elapsed window ignored")
	}
}
