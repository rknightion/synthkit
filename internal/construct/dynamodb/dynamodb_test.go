// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type capture struct {
	batch []promrw.Series
	calls int
}

func (w *capture) Write(_ context.Context, b []promrw.Series) error {
	w.batch = b
	w.calls++
	return nil
}
func config() *Config {
	return &Config{Account: Account{MaxReads: 1000, MaxWrites: 1000, MaxTableReads: 500, MaxTableWrites: 500}, Tables: []Table{{Name: "orders", CollectorName: "declared-collector-orders", ProvisionedReads: 10, ProvisionedWrites: 5, Operations: []Operation{{Name: "Query", RequestsPerSecond: 0.01, ReadUnits: 1, LatencyMS: 4, ReturnedItems: 2}}}}}
}
func fixtures() *fixture.Set {
	return &fixture.Set{Cloud: &fixture.Cloud{AccountID: "synthetic-account", Region: "eu-west-1"}}
}
func tick(t *testing.T, c core.Construct, at time.Time, w *capture) {
	t.Helper()
	if err := c.Tick(context.Background(), at, &core.World{Metrics: w}); err != nil {
		t.Fatal(err)
	}
}
func find(t *testing.T, w *capture, name string) promrw.Series {
	t.Helper()
	for _, s := range w.batch {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("missing %s", name)
	return promrw.Series{}
}
func absent(t *testing.T, w *capture, name string) {
	t.Helper()
	for _, s := range w.batch {
		if s.Name == name {
			t.Fatalf("unexpected %s", name)
		}
	}
}

// Exercises the real registration/Build/Tick writer boundary: sparse traffic must
// carry between periods, capacity still samples idle, and sample counts are not magnitudes.
func TestSparseWindowsAndProfiles(t *testing.T) {
	reg := Registration()
	if reg.Kind != "dynamodb" || reg.Scope != core.ScopeBlueprint {
		t.Fatal(reg)
	}
	cfg := config()
	c, err := reg.Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	now := time.Unix(1800000000, 0)
	tick(t, c, now, w)
	if got := find(t, w, "aws_dynamodb_consumed_read_capacity_units_sample_count").Value; got != 60 {
		t.Fatal(got)
	}
	if got := find(t, w, "aws_dynamodb_consumed_read_capacity_units_sum").Value; got != 0 {
		t.Fatal(got)
	}
	absent(t, w, "aws_dynamodb_successful_request_latency_sum")
	tick(t, c, now.Add(time.Minute), w)
	if got := find(t, w, "aws_dynamodb_successful_request_latency_sum").Value; got != 4 {
		t.Fatal(got)
	}
	if got := find(t, w, "aws_dynamodb_successful_request_latency_sample_count").Value; got != 1 {
		t.Fatal(got)
	}
	items := find(t, w, "aws_dynamodb_returned_item_count_sum")
	if items.Value != 2 || items.Labels["dimension_Operation"] != "Query" {
		t.Fatal(items)
	}
	for _, s := range w.batch {
		if s.Kind != promrw.KindGauge || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || s.Value < 0 {
			t.Fatal(s)
		}
		if strings.Contains(s.Name, "account_") || strings.Contains(s.Name, "max_provisioned_table_") {
			if s.Labels["name"] != "global" || s.Labels["dimension_TableName"] != "" {
				t.Fatal(s)
			}
		} else if s.Labels["name"] != "declared-collector-orders" {
			t.Fatal(s)
		}
		if _, ok := s.Labels["dimension_StreamLabel"]; ok {
			t.Fatal(s)
		}
	}
	calls := w.calls
	tick(t, c, now.Add(time.Minute), w)
	tick(t, c, now, w)
	if w.calls != calls {
		t.Fatal("repeated/backward window emitted")
	}
	tick(t, c, now.Add(24*time.Hour), w)
	absent(t, w, "aws_dynamodb_successful_request_latency_sum")
	tick(t, c, now.Add(24*time.Hour+time.Minute), w)
	if find(t, w, "aws_dynamodb_consumed_read_capacity_units_sum").Value != 1 {
		t.Fatal("gap backfilled or carry lost")
	}
}

func TestStreamAndSourceAreExplicit(t *testing.T) {
	cfg := config()
	cfg.Tables[0].Operations = []Operation{{Name: "PutItem", RequestsPerSecond: 1, WriteUnits: 1, LatencyMS: 3}}
	cfg.Tables[0].WriteSource = "Customer"
	cfg.Tables[0].Stream = &Stream{Label: "declared-stream", RequestsPerSecond: 0.5, RecordsPerRequest: 3, BytesPerRecord: 100, LatencyMS: 8}
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	tick(t, c, time.Unix(1800000000, 0), w)
	if find(t, w, "aws_dynamodb_consumed_write_capacity_units_sum").Labels["dimension_Source"] != "Customer" {
		t.Fatal("missing explicit source")
	}
	s := find(t, w, "aws_dynamodb_returned_bytes_sum")
	if s.Value != 9000 || s.Labels["dimension_Operation"] != "GetRecords" || s.Labels["dimension_StreamLabel"] != "declared-stream" {
		t.Fatal(s)
	}
	if find(t, w, "aws_dynamodb_returned_bytes_sample_count").Value != 30 {
		t.Fatal("bytes used as sample count")
	}
	absent(t, w, "aws_dynamodb_returned_item_count_sum")
	for _, s := range w.batch {
		if s.Labels["dimension_Operation"] == "PutItem" && s.Labels["dimension_StreamLabel"] != "" {
			t.Fatal("union-filled stream")
		}
	}
}

func TestValidationAndIdle(t *testing.T) {
	fx := fixtures()
	fx.Cloud.CloudWatchExport = "otlp"
	if _, err := Build(config(), fx); err == nil || !strings.Contains(err.Error(), "otlp") {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Tables[0].Operations[0].ReturnedItems = 1.5 },
		func(c *Config) {
			c.Tables[0].Stream = &Stream{Label: "stream", RequestsPerSecond: 0.01, RecordsPerRequest: 1.5, BytesPerRecord: 3, LatencyMS: 4}
		},
		func(c *Config) {
			c.Tables[0].Stream = &Stream{Label: "stream", RequestsPerSecond: 0.01, RecordsPerRequest: 2, BytesPerRecord: 3.5, LatencyMS: 4}
		},
		func(c *Config) { c.Tables[0].CollectorName = "" }, func(c *Config) { c.Tables[0].Operations[0].RequestsPerSecond = math.NaN() },
		func(c *Config) { c.Tables[0].ProvisionedReads = math.Inf(1) }, func(c *Config) { c.Tables[0].WriteSource = "inferred" },
		func(c *Config) { c.Tables[0].Operations[0].Name = "GetRecords" }, func(c *Config) { c.Tables[0].Operations[0].ReadUnits = -1 },
		func(c *Config) { c.Tables[0].Operations[0].Name = "PutItem" }, func(c *Config) { c.Tables = append(c.Tables, c.Tables[0]) },
	} {
		cfg := config()
		mutate(cfg)
		if _, err := Build(cfg, fixtures()); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	cfg := config()
	cfg.Tables[0].Operations = nil
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	now := time.Unix(1800000000, 0)
	tick(t, c, now, w)
	if find(t, w, "aws_dynamodb_consumed_write_capacity_units_sample_count").Value != 60 {
		t.Fatal("idle capacity missing")
	}
	absent(t, w, "aws_dynamodb_returned_bytes_sum")
	absent(t, w, "aws_dynamodb_info")
	first := find(t, w, "aws_dynamodb_account_provisioned_read_capacity_utilization_sum").Value
	tick(t, c, now.Add(time.Minute), w)
	if find(t, w, "aws_dynamodb_account_provisioned_read_capacity_utilization_sum").Value != first {
		t.Fatal("cumulative account stat")
	}
}
