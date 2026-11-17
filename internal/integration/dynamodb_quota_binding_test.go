// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

const ddbQuotaFixture = "../construct/dynamodb/testdata/quota-binding.yaml"

// DynamoDB operations bound to quota resources, loaded from blueprint YAML through the real loader
// and catalog: quota_reduced on the read-unit quota throttles GetItem (ReadThrottleEvents and
// ThrottledRequests), quota_reduced on the request quota throttles PutItem (WriteThrottleEvents),
// both drain afterwards, the unbound table never throttles, and the control schema lists each
// bound operation as its quota's consumer.
func TestDynamoDBQuotaBinding(t *testing.T) {
	data, err := os.ReadFile(ddbQuotaFixture)
	if err != nil {
		t.Fatal(err)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range res.Targets {
		if tg.Axis == "table" && tg.Name != "fixture-audit" {
			t.Fatalf("table targets include %q, whose captured operations are all bound", tg.Name)
		}
	}
	consumers := map[string]string{}
	for _, r := range res.Resources {
		for _, c := range r.Consumers {
			consumers[r.Name] = c.Kind + "/" + c.Identity["dimension_TableName"] + "/" + c.Identity["dimension_Operation"]
		}
	}
	if consumers["orders-read-capacity"] != "dynamodb/fixture-orders/GetItem" || consumers["orders-put-rate"] != "dynamodb/fixture-orders/PutItem" {
		t.Fatalf("resource consumers %v", consumers)
	}
	var ci *blueprint.ConstructInstance
	for i := range res.Constructs {
		if res.Constructs[i].Kind == "dynamodb" {
			ci = &res.Constructs[i]
		}
	}
	if ci == nil {
		t.Fatal("no dynamodb construct")
	}
	cr, _ := reg.Construct("dynamodb")
	c, err := cr.Build(ci.Config, ci.Fixtures)
	if err != nil {
		t.Fatal(err)
	}
	adv, ok := c.(causal.Advancer)
	if !ok {
		t.Fatal("dynamodb construct is not a causal.Advancer")
	}
	eng := shape.New("UTC", res.Incidents)
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= 45; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		adv.AdvanceCausal(now, eng)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		got := map[string]float64{}
		for _, s := range mc.All() {
			if strings.HasSuffix(s.Name, "_sum") && (strings.Contains(s.Name, "throttle") || strings.Contains(s.Name, "latency")) {
				got[strings.TrimPrefix(s.Name, "aws_dynamodb_")+"/"+s.Labels["dimension_TableName"]+"/"+s.Labels["dimension_Operation"]] = s.Value
			}
		}
		// Latency sums are successes x latency_ms: GetItem 4 ms, PutItem 6 ms.
		gets, puts := got["successful_request_latency_sum/fixture-orders/GetItem"]/4, got["successful_request_latency_sum/fixture-orders/PutItem"]/6
		want := map[string]float64{}
		switch {
		case i >= 11 && i <= 19: // 6 x 0.5 = 3 RCU/s admitted of 5: 360 reads, 120 RCU = 240 throttled reads
			want["throttled_requests_sum/fixture-orders/GetItem"] = 240
			want["read_throttle_events_sum/fixture-orders/"] = 240
			if gets != 360 || puts != 90 {
				t.Fatalf("minute %d: successes GetItem %v PutItem %v, want 360, 90", i, gets, puts)
			}
		case i >= 31 && i <= 39: // 2 x 0.5 = 1 write/s admitted of 1.5: 60 writes, 30 throttled
			want["throttled_requests_sum/fixture-orders/PutItem"] = 30
			want["write_throttle_events_sum/fixture-orders/"] = 30
			if gets != 600 || puts != 60 {
				t.Fatalf("minute %d: successes GetItem %v PutItem %v, want 600, 60", i, gets, puts)
			}
		case i == 10 || i == 20 || i == 30 || i == 40:
			continue // the window straddles an incident edge
		default:
			if gets != 600 || puts != 90 {
				t.Fatalf("minute %d: healthy successes GetItem %v PutItem %v, want 600, 90", i, gets, puts)
			}
		}
		if got["successful_request_latency_sum/fixture-audit/GetItem"] != 240 {
			t.Fatalf("minute %d: unbound table moved: %v", i, got)
		}
		for key, v := range got {
			if strings.Contains(key, "throttle") && want[key] != v {
				t.Fatalf("minute %d: %s = %v, want %v (all %v)", i, key, v, want[key], got)
			}
		}
		for key, v := range want {
			if got[key] != v {
				t.Fatalf("minute %d: %s = %v, want %v (all %v)", i, key, got[key], v, got)
			}
		}
	}
}

// A table_throttle_storm cannot target a table whose throttle-captured operations are all bound:
// the loader rejects it as an unknown target instead of letting it silently do nothing.
func TestDynamoDBBoundTableRejectsThrottleStorm(t *testing.T) {
	data, err := os.ReadFile(ddbQuotaFixture)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "effects: [{mode: quota_reduced, target: orders-put-rate, intensity: 0.5}]",
		"effects: [{mode: table_throttle_storm, target: %s, intensity: 1}]", 1)
	if _, err := blueprint.Load([]byte(strings.Replace(text, "%s", "fixture-orders", 1)), runner.Catalog()); err == nil || !strings.Contains(err.Error(), `unknown target "fixture-orders"`) {
		t.Fatalf("storm on the bound table: load error %v, want an unknown-target rejection", err)
	}
	if _, err := blueprint.Load([]byte(strings.Replace(text, "%s", "fixture-audit", 1)), runner.Catalog()); err != nil {
		t.Fatalf("storm on the unbound table must still load: %v", err)
	}
}
