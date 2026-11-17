// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/shape"
)

// Each independent lane earns one event per 128 observed active seconds. Binary
// exact rates distinguish lost carry from decimal floating-point boundaries.
func sparseFourLanes() *Config {
	cfg := config()
	cfg.Tables[0].Operations[0].RequestsPerSecond = 1.0 / 128
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
	return cfg
}

// Extract events from actual emitted series, not private carry/model state.
// Query and GetRecords share the latency family, so operation identity matters.
func fourLaneEvents(w *capture) [4]float64 {
	var result [4]float64
	for _, s := range w.batch {
		switch {
		case s.Name == "aws_dynamodb_successful_request_latency_sample_count" && s.Labels["dimension_Operation"] == "Query":
			result[0] += s.Value
		case s.Name == "aws_dynamodb_conditional_check_failed_requests_sum":
			result[1] += s.Value
		case s.Name == "aws_dynamodb_returned_bytes_sample_count":
			result[2] += s.Value
		case s.Name == "aws_dynamodb_consumed_write_capacity_units_sum" && s.Labels["dimension_Source"] == "GlobalTable":
			result[3] += s.Value
		}
	}
	return result
}

func TestSparseCarryAcrossCadenceJitter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spacing time.Duration
	}{
		{"exact_minute", time.Minute},
		{"minute_plus_nanosecond", time.Minute + time.Nanosecond},
		{"sixty_one_seconds", 61 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Build(sparseFourLanes(), fixtures())
			if err != nil {
				t.Fatal(err)
			}
			w := &capture{}
			world := &core.World{Metrics: w, Shape: shape.New("UTC", nil)}
			now := time.Unix(1800000000, 0)
			var total [4]float64
			for i := 0; i < 20; i++ {
				if err := c.Tick(context.Background(), now.Add(time.Duration(i)*tc.spacing), world); err != nil {
					t.Fatal(err)
				}
				events := fourLaneEvents(w)
				for j, v := range events {
					total[j] += v
				}
			}
			// Twenty capped 60s observations earn floor(1200/128)=9 events
			// in EACH lane, regardless of positive phase jitter. No interval replay.
			if total != [4]float64{9, 9, 9, 9} {
				t.Fatalf("operation/failure/poll/replica totals = %v, want [9 9 9 9]", total)
			}
			if w.calls != 20 {
				t.Fatalf("got %d batches for 20 active observations", w.calls)
			}
		})
	}
}

func TestLongGapRetainsEarnedCarryWithoutCatchup(t *testing.T) {
	c, err := Build(sparseFourLanes(), fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	world := &core.World{Metrics: w, Shape: shape.New("UTC", nil)}
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		offset time.Duration
		want   float64
	}{
		{0, 0},                          // 60/128 earned, not yet a complete event.
		{time.Minute, 0},                // 120/128 earned before the gap.
		{24 * time.Hour, 1},             // Prior 120/128 + ONLY the capped current 60/128.
		{24*time.Hour + time.Minute, 0}, // Remaining 52/128 + 60/128 < one event.
		{24*time.Hour + 2*time.Minute, 1},
	} {
		if err := c.Tick(context.Background(), now.Add(tc.offset), world); err != nil {
			t.Fatal(err)
		}
		want := [4]float64{tc.want, tc.want, tc.want, tc.want}
		if got := fourLaneEvents(w); got != want {
			t.Errorf("offset %v: operation/failure/poll/replica %v, want %v (retain earned carry, cap NEW accrual)", tc.offset, got, want)
		}
		if find(t, w, "aws_dynamodb_consumed_read_capacity_units_sample_count").Value != 60 {
			t.Fatal("capacity observations backfilled the gap")
		}
	}
}
