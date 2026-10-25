// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/agentcore"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

const agentcoreThrottleBlueprint = `name: throttle-admission
environments:
  - name: eu-west-1
    resources:
      - name: planner-conc
        kind: concurrency
        size: 20
        hold_ms: 900
        demand: {off_peak_rps: 10, peak_rps: 10, load: constant}
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-test-eu
      agentcore:
        agents: [planner, retriever]
        runtimes:
          - {agent: planner, resource: planner-conc}
  - name: us-east-2
    cloud:
      provider: aws
      account_id: "444455556666"
      region: us-east-2
      vpc_id: vpc-test-us
      agentcore:
        agents: [planner, retriever]
`

// Admission must reject an entire mixed deployment if the effect reaches even one
// bound runtime, not silently limit the story to its unbound peer or global series.
func TestAgentCoreThrottleAdmission(t *testing.T) {
	reg := runner.Catalog()
	for _, tc := range []struct {
		name, target    string
		boundEnv        string
		unbound, reject bool
	}{
		{name: "mixed deployment", target: "eu-west-1", reject: true},
		{name: "unrelated region", target: "us-east-2"},
		{name: "environment differs from region", boundEnv: "production", target: "production", reject: true},
		{name: "region scope reaches another environment", boundEnv: "production", target: "eu-west-1", reject: true},
		{name: "cloud wildcard", target: "cloud:*", reject: true},
		{name: "unscoped", reject: true},
		{name: "legacy region", target: "eu-west-1", unbound: true},
		{name: "legacy wildcard", target: "cloud:*", unbound: true},
		{name: "legacy unscoped", unbound: true},
	} {
		for _, scenario := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/scenario=%v", tc.name, scenario), func(t *testing.T) {
				text := agentcoreThrottleBlueprint
				if tc.boundEnv != "" {
					text = strings.Replace(text, "name: eu-west-1", "name: "+tc.boundEnv, 1)
					// Keep eu-west-1 an addressable cloud target in the second case.
					text = strings.Replace(text, "name: us-east-2", "name: eu-west-1", 1)
				}
				if tc.unbound {
					text = strings.Replace(text, "        runtimes:\n          - {agent: planner, resource: planner-conc}\n", "", 1)
				}
				if scenario {
					text += fmt.Sprintf("scenarios:\n  - name: throttle\n    effects:\n      - {mode: agentcore_throttle, target: %q}\nincidents:\n  - {scenario: throttle, at: '10:00', for: 10m}\n", tc.target)
				} else {
					text += fmt.Sprintf("incidents:\n  - {kind: agentcore_throttle, target: %q, at: '10:00', for: 10m}\n", tc.target)
				}
				r, err := blueprint.Load([]byte(text), reg)
				if tc.reject {
					if err == nil {
						t.Fatal("accepted legacy throttle reaching bound planner runtime")
					}
					for _, want := range []string{"agentcore_throttle", "planner", "planner-conc", "concurrency_reduced"} {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("error lacks actionable binding context %q: %v", want, err)
						}
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(r.Incidents) == 0 {
					t.Fatal("accepted legacy throttle lost its schedule")
				}
			})
		}
	}
}

// Expanded cloud names must survive schedule parsing unchanged, even when they
// name only an unbound deployment. Otherwise trimming can broaden a scope or a
// delimiter can make shape.New silently skip the authored incident.
func TestAgentCoreThrottleScopeAdmission(t *testing.T) {
	reg := runner.Catalog()
	for _, tc := range []struct {
		name, scope string
		wildcard    bool
		unbound     bool
	}{
		{name: "space only", scope: " "},
		{name: "padded region", scope: " eu-west-1 "},
		{name: "unicode only", scope: "\u00a0\u2003"},
		{name: "unicode leading edge", scope: "\u00a0eu-west-1"},
		{name: "unicode trailing edge", scope: "eu-west-1\u2003"},
		{name: "last scope delimiter", scope: "unbound@eu-west-1"},
		{name: "last duration delimiter", scope: "unbound/10m"},
		{name: "wildcard space only", scope: " ", wildcard: true},
		{name: "wildcard unicode edge", scope: "eu-west-1\u0085", wildcard: true},
		{name: "wildcard delimiter", scope: "unbound/10m", wildcard: true},
		{name: "fully unbound whitespace", scope: " ", unbound: true},
		{name: "fully unbound wildcard unicode", scope: "\u00a0unbound", wildcard: true, unbound: true},
	} {
		for _, scenario := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/scenario=%v", tc.name, scenario), func(t *testing.T) {
				text := strings.Replace(agentcoreThrottleBlueprint, "name: eu-west-1", "name: production", 1)
				text = strings.Replace(text, "name: us-east-2", fmt.Sprintf("name: %q", tc.scope), 1)
				if tc.unbound {
					text = strings.Replace(text, "        runtimes:\n          - {agent: planner, resource: planner-conc}\n", "", 1)
				}
				target := tc.scope
				if tc.wildcard {
					target = "cloud:*"
				}
				if scenario {
					text += fmt.Sprintf("scenarios:\n  - name: throttle\n    effects:\n      - {mode: agentcore_throttle, target: %q}\nincidents:\n  - {scenario: throttle, at: '10:00', for: 10m}\n", target)
				} else {
					text += fmt.Sprintf("incidents:\n  - {kind: agentcore_throttle, target: %q, at: '10:00', for: 10m}\n", target)
				}
				_, err := blueprint.Load([]byte(text), reg)
				if err == nil {
					t.Fatal("accepted a cloud scope that cannot survive schedule compilation unchanged")
				}
				// In wildcard cases the bound production target comes first. Scope
				// validation must precede reachability, not depend on target order.
				for _, want := range []string{"agentcore_throttle", "scheduler scope", fmt.Sprintf("%q", tc.scope)} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error lacks scope context %q: %v", want, err)
					}
				}
			})
		}
	}
}

// The loader fence must not remove deployment-global output, even with bound runtimes.
func TestAgentCoreBoundRuntimeRetainsGlobalSeries(t *testing.T) {
	reg := runner.Catalog()
	r, err := blueprint.Load([]byte(agentcoreThrottleBlueprint), reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, ci := range r.Constructs {
		if ci.Kind != "agentcore" {
			continue
		}
		c, err := agentcore.Build(ci.Config, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), time.Date(2026, 10, 24, 10, 1, 0, 0, time.UTC), coretest.World(mc, nil, nil)); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range mc.Find("aws_bedrock_agentcore_invocations_sum") {
			if strings.HasSuffix(s.Labels["name"], ":runtime/global") {
				found = true
				if s.Value <= 0 {
					t.Fatalf("global invocations not positive: %v", s.Value)
				}
			}
		}
		if !found {
			t.Fatalf("deployment %q lost global series", ci.Name)
		}
		globalThrottle := func(mc *coretest.MetricCapture) float64 {
			for _, s := range mc.Find("aws_bedrock_agentcore_throttles_sum") {
				if strings.HasSuffix(s.Labels["name"], ":runtime/global") {
					return s.Value
				}
			}
			t.Fatal("missing deployment-global throttles")
			return 0
		}
		baseline := globalThrottle(mc)
		amplified := &coretest.MetricCapture{}
		world := coretest.World(amplified, nil, nil)
		world.Shape = shape.New("UTC", []string{"agentcore_throttle@10:00/10m@" + ci.Fixtures.Cloud.Region})
		if err := c.Tick(context.Background(), time.Date(2026, 10, 24, 10, 1, 0, 0, time.UTC), world); err != nil {
			t.Fatal(err)
		}
		if got := globalThrottle(amplified); baseline <= 0 || got <= baseline {
			t.Fatalf("deployment-global throttle no longer responds: baseline=%v active=%v", baseline, got)
		}
	}
}
