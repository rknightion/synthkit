// SPDX-License-Identifier: AGPL-3.0-only
package dynamodb

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// The permitted inventory is derived from the frozen 15-base source subset,
// not from the renderer's own list. It exercises all admitted dimensions.
func TestInventoryAndDeterminism(t *testing.T) {
	cfg := config()
	cfg.Tables[0].Operations = []Operation{{Name: "Query", RequestsPerSecond: 1, ReadUnits: 1, ReturnedItems: 2, LatencyMS: 4}, {Name: "PutItem", RequestsPerSecond: 1, WriteUnits: 1, LatencyMS: 3}}
	cfg.Tables[0].ConditionalFailuresPerSecond = 0.1
	cfg.Tables[0].Stream = &Stream{Label: "stream-orders", RequestsPerSecond: 1, RecordsPerRequest: 2, BytesPerRecord: 100, LatencyMS: 8}
	replica := cfg.Tables[0]
	replica.Name = "replica"
	replica.CollectorName = "declared-replica"
	replica.Operations = nil
	replica.Stream = nil
	replica.WriteSource = "GlobalTable"
	replica.Replication = &Replication{WritesPerSecond: 0.5, UnitsPerWrite: 1}
	cfg.Tables = append(cfg.Tables, replica)
	a, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	wa, wb := &capture{}, &capture{}
	now := time.Unix(1800000000, 0)
	tick(t, a, now, wa)
	tick(t, b, now, wb)
	normalize := func(batch []promrw.Series) []string {
		out := []string{}
		for _, s := range batch {
			labels, _ := json.Marshal(s.Labels)
			out = append(out, fmt.Sprintf("%s%s=%g", s.Name, labels, s.Value))
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(normalize(wa.batch), normalize(wb.batch)) {
		t.Fatal("non-deterministic independent instances")
	}
	expected := map[string]bool{}
	for _, s := range []string{"account_max_reads", "account_max_writes", "account_max_table_level_reads", "account_max_table_level_writes", "account_provisioned_read_capacity_utilization", "account_provisioned_write_capacity_utilization", "max_provisioned_table_read_capacity_utilization", "max_provisioned_table_write_capacity_utilization", "conditional_check_failed_requests", "consumed_read_capacity_units", "consumed_write_capacity_units", "returned_bytes", "returned_records_count", "returned_item_count", "successful_request_latency"} {
		expected["aws_dynamodb_"+s] = false
	}
	accounts := map[string]int{}
	for _, s := range wa.batch {
		base := ""
		for _, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
			if strings.HasSuffix(s.Name, suffix) {
				base = strings.TrimSuffix(s.Name, suffix)
				break
			}
		}
		if _, ok := expected[base]; !ok {
			t.Fatalf("unadmitted base %q", base)
		}
		expected[base] = true
		if strings.Contains(base, "account_") || strings.Contains(base, "max_provisioned_table_") {
			accounts[s.Name]++
			if len(s.Labels) != 5 {
				t.Fatal("table/enrichment leaked to account", s)
			}
		}
		if s.Labels["dimension_Source"] == "GlobalTable" && s.Value < 0 {
			t.Fatal(s)
		}
		if strings.HasSuffix(s.Name, "_sum") {
			labels, _ := json.Marshal(s.Labels)
			t.Logf("inventory %s %s", base, labels)
		}
	}
	for name, seen := range expected {
		if !seen {
			t.Fatal("missing admitted family", name)
		}
	}
	for name, n := range accounts {
		if n != 1 {
			t.Fatal("duplicated account field", name, n)
		}
	}
	// Build owns its inputs, including nested replication and operation models.
	cfg.Tables[0].Operations[0].LatencyMS = 999
	cfg.Tables[1].Replication.UnitsPerWrite = 999
	tick(t, a, now.Add(time.Minute), wa)
	querySeen, replicaSeen := false, false
	for _, s := range wa.batch {
		if s.Name == "aws_dynamodb_successful_request_latency_minimum" && s.Labels["dimension_Operation"] == "Query" {
			querySeen = true
			if s.Value != 4 {
				t.Fatal("aliased operation config", s)
			}
		}
		if s.Name == "aws_dynamodb_consumed_write_capacity_units_sum" && s.Labels["dimension_TableName"] == "replica" {
			replicaSeen = true
			if s.Value != 30 {
				t.Fatal("aliased replication config", s)
			}
		}
	}
	if !querySeen || !replicaSeen {
		t.Fatal("missing defensive-copy observations")
	}
}

func TestElapsedSparseAndWriterFailure(t *testing.T) {
	cfg := config()
	cfg.Tables[0].Operations[0].RequestsPerSecond = 0.005
	c, err := Build(cfg, fixtures())
	if err != nil {
		t.Fatal(err)
	}
	w := &capture{}
	now := time.Unix(1800000000, 0)
	tick(t, c, now, w)
	for i := 1; i <= 14; i++ {
		tick(t, c, now.Add(time.Duration(i)*10*time.Second), w)
	}
	if find(t, w, "aws_dynamodb_successful_request_latency_sample_count").Value != 1 {
		t.Fatal("fractional arrivals rounded away")
	}
	if find(t, w, "aws_dynamodb_consumed_read_capacity_units_sample_count").Value != 10 {
		t.Fatal("short reporting window ignored")
	}
	// A missing writer is a useful error, not a panic or consumed observation.
	if err := c.Tick(context.Background(), now.Add(210*time.Second), &core.World{}); err == nil {
		t.Fatal("missing writer accepted")
	}
}
