// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
)

func TestLoadedContainerMemoryBudgets(t *testing.T) {
	stock, err := os.ReadFile(filepath.Join("..", "..", "blueprints", "otlp-native.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const mib = 1024 * 1024
	for _, test := range []struct {
		name    string
		yaml    []byte
		budgets map[string][2]float64
	}{
		{"stock", stock, map[string][2]float64{"otlp-api-enriched": {64 * mib, 128 * mib}}},
		{"explicit_and_below_floor", []byte(memoryBudgetBlueprint), map[string][2]float64{
			"memory-small": {8 * mib, 48 * mib},
			"memory-low":   {1 * mib, 8 * mib},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reg := runner.Catalog()
			resolved, err := blueprint.Load(test.yaml, reg)
			if err != nil {
				t.Fatal(err)
			}
			if err := blueprint.ValidateSet([]*blueprint.Resolved{resolved}); err != nil {
				t.Fatal(err)
			}
			capture := &coretest.MetricCapture{}
			r := runner.New(runner.Sinks{Metrics: capture, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}}, reg, runner.Options{})
			if err := r.AddBlueprint(resolved); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				r.DrainQueues(ctx)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			start := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
			overRequest := map[string]bool{}
			for tick, load := range []float64{1, 1, 3, 20, .1, 1} {
				state := control.DefaultState()
				state.VolumeMultiplier = load
				if tick >= 2 && test.name == "stock" {
					state.Scaling[resolved.Name+"/otlp-api-enriched"] = 5
				}
				r.ApplyControl(state)
				before := len(capture.All())
				if err := r.RunOnce(ctx, start.Add(time.Duration(tick)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				limits, requests := map[string]float64{}, map[string]float64{}
				memory := map[string]map[string]float64{}
				for _, s := range capture.All()[before:] {
					budget, ok := test.budgets[s.Labels["container"]]
					if !ok {
						continue
					}
					key := s.Labels["namespace"] + "/" + s.Labels["pod"] + "/" + s.Labels["container"]
					switch s.Name {
					case "kube_pod_container_resource_limits", "kube_pod_container_resource_requests":
						if s.Labels["resource"] != "memory" {
							continue
						}
						if s.Name == "kube_pod_container_resource_limits" {
							limits[key] = s.Value
							if s.Value != budget[1] {
								t.Fatalf("declared limit changed for %s: %g", key, s.Value)
							}
						} else {
							requests[key] = s.Value
							if s.Value != budget[0] {
								t.Fatalf("declared request changed for %s: %g", key, s.Value)
							}
						}
					case "container_memory_working_set_bytes", "container_memory_rss", "container_memory_cache", "container_memory_usage_bytes":
						if memory[key] == nil {
							memory[key] = map[string]float64{}
						}
						memory[key][s.Name] = s.Value
					}
				}
				if len(limits) == 0 || len(memory) != len(limits) || len(requests) != len(limits) {
					t.Fatalf("tick%d: missing joined container budgets: limits=%d requests=%d memory=%d", tick, len(limits), len(requests), len(memory))
				}
				wantPods := 2 * len(test.budgets)
				if tick >= 2 && test.name == "stock" {
					wantPods = 5
				}
				if len(limits) != wantPods {
					t.Fatalf("tick%d: expected %d joined pods, got %d", tick, wantPods, len(limits))
				}
				for key, limit := range limits {
					m := memory[key]
					if len(m) != 4 {
						t.Fatalf("tick%d: missing memory components for %s: %v", tick, key, m)
					}
					for name, value := range m {
						if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > limit {
							t.Errorf("tick%d load%g: %s %s=%g outside declared memory budget [0,%g]", tick, load, key, name, value, limit)
						}
					}
					if m["container_memory_rss"] > m["container_memory_working_set_bytes"] || m["container_memory_working_set_bytes"] > m["container_memory_usage_bytes"] || m["container_memory_cache"] > m["container_memory_usage_bytes"] {
						t.Fatalf("tick%d: inconsistent memory components for %s: %v", tick, key, m)
					}
					if m["container_memory_working_set_bytes"] > requests[key] {
						overRequest[key[strings.LastIndexByte(key, '/')+1:]] = true
					}
				}
			}
			for container := range test.budgets {
				if !overRequest[container] {
					t.Errorf("container %s never exceeded its request; a request must not become a usage ceiling", container)
				}
			}
		})
	}
}

const memoryBudgetBlueprint = `
name: memory-budget
shape: business_hours_plateau
timezone: UTC
environments:
  - name: prod
    cloud: {provider: aws, account_id: "111122223333", region: eu-west-1, vpc_id: vpc-memory-budget}
    cluster:
      type: eks
      name: memory-budget-prod
      node_groups: [{name: general, instance_type: m6i.large, desired: 2}]
      observability: {cloudwatch: false}
workloads:
  - type: app
    name: memory-app
    runs_on: memory-budget-prod
    traffic: {off_peak_rps: 0.1, peak_rps: 1}
    services:
      - name: memory-small
        entry: true
        calls: [memory-low]
        resources: {mem_request: 8388608, mem_limit: 50331648}
      - name: memory-low
        resources: {mem_request: 1048576, mem_limit: 8388608}
`
