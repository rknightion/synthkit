// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/pushstatus"
	"github.com/rknightion/synthkit/internal/runner"
)

func TestBundledRuntimeNamesAreResolvedCanonicalIdentities(t *testing.T) {
	manager := bpsource.NewManager(bpsource.Options{
		BakedDir:       filepath.Join("..", "..", "..", "blueprints"),
		BlueprintNames: []string{"*"},
		DataDir:        t.TempDir(),
		Registry:       runner.Catalog(),
		RuntimeLimits:  blueprint.RuntimeLimits{},
	})
	loaded, _, diagnostics := manager.Resolve(context.Background())
	if len(diagnostics) != 0 {
		t.Fatalf("bundled runtime load diagnostics = %+v", diagnostics)
	}
	gotInput := make([]string, 0, len(loaded))
	for _, item := range loaded {
		gotInput = append(gotInput, item.Resolved.Name)
	}
	got, duplicates := runtimeNames(gotInput)
	if len(duplicates) != 0 {
		t.Fatalf("resolved canonical runtime names contain duplicates: %v", duplicates)
	}
	want := []string{
		"acme-ai-eval", "acme-ai-platform", "acme-ai-platform-eval", "aws-cloud-services",
		"aws-cloudwatch-infra", "csp-azure", "dbo11y-mysql", "fleet-management",
		"grafana-ai-o11y", "high-dpm-churn", "hostfleet", "hosts-bare", "hosts-linux-docker",
		"hosts-macos", "hosts-windows", "k8s-control-plane", "k8s-cost-power", "k8s-full-stack",
		"k8s-logs-events", "k8s-minimal", "k8s-otel-native", "k8s-windows-mixed", "netobs-enterprise",
		"netobs-global", "netobs-spoke", "otlp-native", "profiling-demo", "synthetic-checks",
	}
	if len(got) != len(want) {
		t.Fatalf("resolved runtime name count = %d, want %d; names=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolved runtime names[%d] = %q, want %q; all=%v", i, got[i], want[i], got)
		}
	}
}

func TestRunEnumeratesEveryCanonicalRuntimeNameAndRejectsDrift(t *testing.T) {
	names := makeNames(28)
	server := identityServer(t, names, true)
	defer server.Close()

	r, err := run(context.Background(), config{controlURL: server.URL, promURL: server.URL, expectedCount: 28})
	if err != nil {
		t.Fatal(err)
	}
	if r.RuntimeCount != 28 || len(r.Rows) != 28 {
		t.Fatalf("runtime rows = %d/%d, want 28/28", r.RuntimeCount, len(r.Rows))
	}
	for i, row := range r.Rows {
		if row.Blueprint != names[i] || row.Verdict != verdictHealthy || row.MetricChecks != 1 {
			t.Fatalf("row[%d] = %+v", i, row)
		}
	}

	auto, err := run(context.Background(), config{controlURL: server.URL, promURL: server.URL, expectedCount: -1})
	if err != nil {
		t.Fatal(err)
	}
	if auto.ExpectedCount != 28 || auto.Rows[0].Verdict != verdictHealthy {
		t.Fatalf("derived runtime count must remain healthy: %+v", auto)
	}

	drift, err := run(context.Background(), config{controlURL: server.URL, promURL: server.URL, expectedCount: 26})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range drift.Rows {
		if row.Verdict != verdictLaneFailure || row.PreventingLane != "runtime_identity" {
			t.Fatalf("count drift must be a runtime-identity failure, got %+v", row)
		}
	}
}

func TestRunReportsSetupAndNamedBlockingLane(t *testing.T) {
	setup := identityServer(t, nil, false)
	defer setup.Close()
	r, err := run(context.Background(), config{controlURL: setup.URL, expectedNames: []string{"one"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].Verdict != verdictLaneFailure || r.Rows[0].PreventingLane != "runtime_identity" {
		t.Fatalf("missing expected runtime identity must not silently pass: %+v", r.Rows)
	}

	blocked := identityServer(t, []string{"one"}, false)
	defer blocked.Close()
	r, err = run(context.Background(), config{controlURL: blocked.URL, expectedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Rows[0]; got.Verdict != verdictLaneFailure || got.PreventingLane != "promrw" {
		t.Fatalf("blocked lane row = %+v", got)
	}

	setupRow := evaluate(context.Background(), &http.Client{}, config{}, "one", true, false,
		control.Schema{Blueprints: []string{"one"}}, control.InventoryReport{},
		control.StatusReport{Readiness: &control.ReadinessReport{SetupRequired: true}}, 1, true)
	if setupRow.Verdict != verdictSetup {
		t.Fatalf("setup row = %+v", setupRow)
	}
}

func TestRunRejectsDuplicateRuntimeIdentity(t *testing.T) {
	server := identityServer(t, []string{"one", "one"}, true)
	defer server.Close()
	r, err := run(context.Background(), config{controlURL: server.URL, promURL: server.URL, expectedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].PreventingLane != "runtime_identity" {
		t.Fatalf("duplicate runtime identity must fail: %+v", r.Rows)
	}
}

func TestEvaluateQueriesEveryInventoryMetricWithBoundedConcurrency(t *testing.T) {
	var active, maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{map[string]any{"value": []any{0, "1"}}}}})
	}))
	defer server.Close()

	metrics := make([]string, 32)
	for i := range metrics {
		metrics[i] = "metric_" + string(rune('a'+i))
	}
	inventory := control.InventoryReport{Blueprints: []control.BlueprintInventory{{
		Blueprint:  "one",
		Constructs: []control.ConstructInventory{{Kind: "test", Name: "test", MetricNames: metrics}},
	}}}
	got := evaluate(context.Background(), server.Client(), config{promURL: server.URL}, "one", true, false,
		control.Schema{Blueprints: []string{"one"}}, inventory,
		control.StatusReport{Readiness: &control.ReadinessReport{LiveReady: true}}, 1, true)
	if got.Verdict != verdictHealthy || got.MetricChecks != len(metrics) {
		t.Fatalf("row=%+v, want every inventory metric checked", got)
	}
	if maximum.Load() < 2 || maximum.Load() > metricQueryConcurrency {
		t.Fatalf("maximum concurrent queries=%d, want 2..%d", maximum.Load(), metricQueryConcurrency)
	}
}

func TestEvaluateUsesExplicitPerFamilyQueryIdentity(t *testing.T) {
	queries := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{map[string]any{"value": []any{0, "1"}}}}})
	}))
	defer server.Close()

	clusterIdentity := &control.QueryIdentity{Scope: "substrate", Labels: map[string]string{
		"cluster": "cluster-a", "k8s_cluster_name": "cluster-a",
	}}
	inventory := control.InventoryReport{Blueprints: []control.BlueprintInventory{{
		Blueprint: "one",
		Constructs: []control.ConstructInventory{{
			Kind:        "k8s_cluster",
			Name:        "cluster-a",
			Identity:    clusterIdentity,
			MetricNames: []string{"kube_node_info", "traces_host_info"},
			MetricIdentities: map[string]*control.QueryIdentity{
				"traces_host_info": {Scope: "substrate", Labels: map[string]string{}},
			},
		}},
	}}}
	got := evaluate(context.Background(), server.Client(), config{promURL: server.URL}, "one", true, false,
		control.Schema{Blueprints: []string{"one"}}, inventory,
		control.StatusReport{Readiness: &control.ReadinessReport{LiveReady: true}}, 1, true)
	if got.Verdict != verdictHealthy {
		t.Fatalf("row=%+v, want family-specific query identities to be queryable", got)
	}

	seen := map[string]bool{}
	for range 2 {
		seen[<-queries] = true
	}
	if !seen[`{__name__="kube_node_info",cluster="cluster-a",k8s_cluster_name="cluster-a"}`] {
		t.Fatalf("queries=%v, want cluster-scoped family to retain declared cluster selectors", seen)
	}
	if !seen[`{__name__="traces_host_info"}`] {
		t.Fatalf("queries=%v, want traces_host_info to use its explicit empty selector", seen)
	}
}

func identityServer(t *testing.T, names []string, ready bool) *httptest.Server {
	t.Helper()
	sort.Strings(names)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control/schema":
			_ = json.NewEncoder(w).Encode(control.Schema{Blueprints: names})
		case "/control/status":
			status := control.StatusReport{Readiness: &control.ReadinessReport{LiveReady: ready}}
			if !ready {
				status.Readiness.Lanes = []pushstatus.LaneStatus{{Name: "promrw", Configured: true, State: pushstatus.LaneNotAttempted}}
			}
			_ = json.NewEncoder(w).Encode(status)
		case "/control/inventory":
			inv := control.InventoryReport{}
			for _, name := range names {
				inv.Blueprints = append(inv.Blueprints, control.BlueprintInventory{Blueprint: name, Constructs: []control.ConstructInventory{{Kind: "test", Name: "test", MetricNames: []string{"test_metric"}, Identity: &control.QueryIdentity{Scope: "blueprint", Labels: map[string]string{"blueprint": name}}}}})
			}
			_ = json.NewEncoder(w).Encode(inv)
		case "/api/v1/query":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{map[string]any{"value": []any{0, "1"}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func makeNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = "runtime-" + string(rune('a'+i))
	}
	return names
}
