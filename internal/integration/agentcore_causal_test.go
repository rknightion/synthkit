// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"math"
	"sort"
	"strconv"
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

// agentcoreCausalYAML is a test-only declaration (never a deployable blueprint): the planner
// runtime bound to a concurrency resource, the retriever runtime unbound, and three scheduled
// incidents. The constant 10 rps demand against 900 ms executions offers 9 of 20 concurrent
// requests, so the healthy runtime never throttles; partner-api (120 ms, 4x slow) is the
// dependency held across the request. Legacy region-scoped throttle compatibility is tested
// separately through a raw shape schedule after valid public blueprint loading.
const agentcoreCausalYAML = `
name: agentcore-causal
label: agentcore-causal
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      agentcore:
        agents: [planner, retriever]
        runtimes:
          - { agent: planner, resource: planner-concurrency }
    resources:
      - { name: partner-api, kind: dependency, latency_ms: 120, slow_factor: 4 }
      - name: planner-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        held_across: partner-api
        demand: { off_peak_rps: 10, peak_rps: 10, load: constant }
scenarios:
  - name: reduced
    effects: [{ mode: concurrency_reduced, target: planner-concurrency, intensity: 0.7 }]
  - name: unavailable
    effects: [{ mode: dependency_unavailable, target: partner-api, intensity: 0.5 }]
  - name: slow
    effects: [{ mode: dependency_slow, target: partner-api, intensity: 1 }]
incidents:
  - { scenario: reduced, at: "2026-10-24T09:10", for: 10m }
  - { scenario: unavailable, at: "2026-10-24T09:25", for: 5m }
  - { scenario: slow, at: "2026-10-24T09:35", for: 5m }
`

const (
	agentcorePlannerARN = "arn:aws:bedrock-agentcore:eu-west-1:111122223333:runtime/planner"
	agentcoreGlobalARN  = "arn:aws:bedrock-agentcore:eu-west-1:111122223333:runtime/global"
)

// agentcoreMinute is one runtime's invocation-class sums for a tick, plus whether the latency
// family was observed at all.
type agentcoreMinute struct {
	invocations, throttles, systemErrors, totalErrors, latency float64
	latencySeen                                                bool
}

func agentcoreSeries(mc *coretest.MetricCapture, name string) agentcoreMinute {
	var m agentcoreMinute
	for _, s := range mc.All() {
		if s.Labels["name"] != name {
			continue
		}
		switch s.Name {
		case "aws_bedrock_agentcore_invocations_sum":
			m.invocations = s.Value
		case "aws_bedrock_agentcore_throttles_sum":
			m.throttles = s.Value
		case "aws_bedrock_agentcore_system_errors_sum":
			m.systemErrors = s.Value
		case "aws_bedrock_agentcore_total_errors_sum":
			m.totalErrors = s.Value
		case "aws_bedrock_agentcore_latency_sum":
			m.latency, m.latencySeen = s.Value, true
		}
	}
	return m
}

func buildAgentcore(t *testing.T, text string) (core.Construct, *blueprint.Resolved) {
	t.Helper()
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatal(err)
	}
	cr, _ := reg.Construct("agentcore")
	for i := range res.Constructs {
		if res.Constructs[i].Kind == "agentcore" {
			c, err := cr.Build(res.Constructs[i].Config, res.Constructs[i].Fixtures)
			if err != nil {
				t.Fatal(err)
			}
			return c, res
		}
	}
	t.Fatal("no agentcore construct")
	return nil, nil
}

// tickAgentcore runs the construct for 55 one-minute ticks from 09:01 against the given engine
// and returns every tick's capture. The runner advances every causal consumer on each master
// tick before the construct ticks.
func tickAgentcore(t *testing.T, c core.Construct, eng *shape.Engine) map[int]*coretest.MetricCapture {
	t.Helper()
	adv, ok := c.(causal.Advancer)
	if !ok {
		t.Fatal("agentcore construct is not a causal.Advancer")
	}
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	out := map[int]*coretest.MetricCapture{}
	for i := 1; i <= 55; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		adv.AdvanceCausal(now, eng)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		out[i] = mc
	}
	return out
}

// A runtime bound to a concurrency resource, loaded from blueprint YAML through the real loader
// and catalog, follows the resource model's window: concurrency_reduced throttles, a held-across
// dependency_unavailable fails requests (system and total errors), dependency_slow stretches
// latency, and every family drains to the healthy baseline after each incident.
func TestBoundAgentCoreRuntimeFollowsResourceWindow(t *testing.T) {
	c, res := buildAgentcore(t, agentcoreCausalYAML)
	eng := shape.New("UTC", res.Incidents)
	ticks := tickAgentcore(t, c, eng)
	healthy := agentcoreMinute{invocations: 600, totalErrors: 0, latency: 900, latencySeen: true}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1 }
	for i := 1; i <= 55; i++ {
		g := agentcoreSeries(ticks[i], agentcorePlannerARN)
		if g.systemErrors > g.invocations {
			t.Fatalf("minute %d: system errors %v exceed invocations %v", i, g.systemErrors, g.invocations)
		}
		// Counts agree: the constant 10 rps demand is either admitted or throttled, never lost.
		if !near(g.invocations+g.throttles, 600) {
			t.Fatalf("minute %d: invocations %v + throttles %v != 600 arrivals", i, g.invocations, g.throttles)
		}
		if !near(g.totalErrors, g.systemErrors+g.throttles) {
			t.Fatalf("minute %d: total errors %v != system %v + throttles %v", i, g.totalErrors, g.systemErrors, g.throttles)
		}
		switch {
		case i == 10: // one of twelve steps of the window is inside the concurrency_reduced incident
			if g.throttles <= 0 || g.throttles >= 200 || g.invocations >= 600 {
				t.Fatalf("minute 10: partial throttling expected, got %+v", g)
			}
		case i >= 11 && i <= 19: // concurrency 20 -> 6 at intensity 0.7: 9 offered, 1 - 6/9 rejected
			if !near(g.invocations, 400) || !near(g.throttles, 200) || g.systemErrors != 0 || !near(g.latency, 900) {
				t.Fatalf("minute %d under concurrency_reduced: %+v", i, g)
			}
		case i >= 26 && i <= 29: // half the calls to the held-across dependency fail
			if !near(g.invocations, 600) || !near(g.systemErrors, 300) || g.throttles != 0 || !near(g.latency, 900) {
				t.Fatalf("minute %d under dependency_unavailable: %+v", i, g)
			}
		case i >= 36 && i <= 39: // h = 900 + 120 x (4 - 1) = 1260 ms; 12.6 offered of 20, no throttling
			if !near(g.invocations, 600) || g.systemErrors != 0 || g.throttles != 0 || !near(g.latency, 1260) {
				t.Fatalf("minute %d under dependency_slow: %+v", i, g)
			}
		case i == 20 || i == 25 || i == 30 || i == 35 || i == 40: // window straddles an incident edge
			if g == healthy {
				t.Fatalf("minute %d: an edge window shows no effect: %+v", i, g)
			}
		default: // before, between and after resource incidents: drained
			if !near(g.invocations, healthy.invocations) || g.throttles != 0 || g.systemErrors != 0 || g.totalErrors != 0 || !near(g.latency, healthy.latency) || !g.latencySeen {
				t.Fatalf("minute %d: %+v, want the healthy baseline %+v", i, g, healthy)
			}
		}
	}
}

// Unbound output is unchanged: removing the runtimes block leaves every series that is not the
// bound runtime's byte for byte the same (deployment-wide series, the other families and the
// resource-usage family), and the bound runtime's series differ only by the extra name label value
// set. The bound runtime carries only the families the model supplies.
func TestBoundAgentCoreLeavesUnboundSeriesUnchanged(t *testing.T) {
	bound, res := buildAgentcore(t, agentcoreCausalYAML)
	unboundText := strings.Replace(agentcoreCausalYAML, "        runtimes:\n          - { agent: planner, resource: planner-concurrency }\n", "", 1)
	unbound, _ := buildAgentcore(t, unboundText)
	b := tickAgentcore(t, bound, shape.New("UTC", res.Incidents))
	u := tickAgentcore(t, unbound, shape.New("UTC", res.Incidents))
	boundFamilies := map[string]bool{}
	for i := 1; i <= 55; i++ {
		var rest []string
		for _, s := range b[i].All() {
			if s.Labels["name"] == agentcorePlannerARN {
				boundFamilies[strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(s.Name, "_sum"), "_average"), "_maximum"), "_minimum"), "_sample_count")] = true
				continue
			}
			rest = append(rest, seriesKey(s.Name, s.Labels, s.Value))
		}
		var want []string
		for _, s := range u[i].All() {
			want = append(want, seriesKey(s.Name, s.Labels, s.Value))
		}
		sort.Strings(rest)
		sort.Strings(want)
		if strings.Join(rest, "\n") != strings.Join(want, "\n") {
			t.Fatalf("minute %d: the bound deployment changed an unbound series", i)
		}
		if got := agentcoreSeries(u[i], agentcorePlannerARN); got != (agentcoreMinute{}) {
			t.Fatalf("minute %d: the unbound deployment emitted a runtime series: %+v", i, got)
		}
	}
	for _, want := range []string{"invocations", "throttles", "system_errors", "total_errors", "latency"} {
		if !boundFamilies["aws_bedrock_agentcore_"+want] {
			t.Fatalf("bound runtime lacks %s; has %v", want, boundFamilies)
		}
	}
	if len(boundFamilies) != 5 {
		t.Fatalf("bound runtime emits families the model does not supply: %v", boundFamilies)
	}
}

func seriesKey(name string, labels map[string]string, v float64) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteString("|" + k + "=" + labels[k])
	}
	return b.String() + "|" + strconv.FormatFloat(v, 'g', -1, 64)
}

// Public loading rejects legacy throttle effects reaching a bound runtime. This
// ad-hoc/runtime escape-hatch compatibility proof injects a raw region-scoped shape
// schedule only AFTER valid public loading: deployment-global series still amplify,
// while the bound runtime stays at its resource's healthy baseline. It does not
// assert admission of a scheduled blueprint scenario (the loader tests reject that).
func TestBoundAgentCoreRawLegacyScheduleCompatibility(t *testing.T) {
	c, res := buildAgentcore(t, agentcoreCausalYAML)
	legacySchedule := append([]string(nil), res.Incidents...)
	legacySchedule = append(legacySchedule, "agentcore_throttle@2026-10-24T09:45/5m#1@eu-west-1")
	with := tickAgentcore(t, c, shape.New("UTC", legacySchedule))
	noIncidents := strings.Index(agentcoreCausalYAML, "incidents:")
	quiet, _ := buildAgentcore(t, agentcoreCausalYAML[:noIncidents])
	without := tickAgentcore(t, quiet, shape.New("UTC", nil))
	for i := 46; i <= 49; i++ { // wholly inside the 09:45 to 09:50 region-throttle incident
		g, base := agentcoreSeries(with[i], agentcoreGlobalARN), agentcoreSeries(without[i], agentcoreGlobalARN)
		if g.throttles <= base.throttles*2 {
			t.Fatalf("minute %d: deployment-wide throttles %v did not amplify over %v", i, g.throttles, base.throttles)
		}
		if bound, quietBound := agentcoreSeries(with[i], agentcorePlannerARN), agentcoreSeries(without[i], agentcorePlannerARN); bound != quietBound {
			t.Fatalf("minute %d: the bound runtime reacted to agentcore_throttle: %+v vs %+v", i, bound, quietBound)
		}
	}
}

func loadAgentcoreCausal(t *testing.T, text string) error {
	t.Helper()
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatalf("load failed before the build check: %v", err)
	}
	cr, _ := reg.Construct("agentcore")
	for _, ci := range res.Constructs {
		if ci.Kind == "agentcore" {
			_, err := cr.Build(ci.Config, ci.Fixtures)
			return err
		}
	}
	return nil
}

// A runtime binding is validated when the construct is built: the agent must be declared, the
// resource must exist and be a concurrency resource, neither may repeat, and the runtime family
// must be on.
func TestBoundAgentCoreRejectsBadBindings(t *testing.T) {
	const bind = "          - { agent: planner, resource: planner-concurrency }\n"
	for name, tc := range map[string]struct{ text, want string }{
		"unknown agent":  {strings.Replace(agentcoreCausalYAML, "agent: planner,", "agent: nobody,", 1), "not a declared agent"},
		"wrong kind":     {strings.Replace(agentcoreCausalYAML, "resource: planner-concurrency }", "resource: partner-api }", 1), "want kind concurrency"},
		"unknown":        {strings.Replace(agentcoreCausalYAML, "resource: planner-concurrency }", "resource: nope }", 1), "not a declared resource"},
		"agent twice":    {strings.Replace(agentcoreCausalYAML, bind, bind+bind, 1), "bound twice"},
		"shared":         {strings.Replace(agentcoreCausalYAML, bind, bind+"          - { agent: retriever, resource: planner-concurrency }\n", 1), "bind the same resource"},
		"no resource":    {strings.Replace(agentcoreCausalYAML, "resource: planner-concurrency }", "resource: \"\" }", 1), "needs a resource"},
		"no resources":   {agentcoreCausalYAML[:strings.Index(agentcoreCausalYAML, "    resources:")], "declares no environments[].resources"},
		"no runtime sub": {strings.Replace(agentcoreCausalYAML, "agents: [planner, retriever]\n", "agents: [planner, retriever]\n        sub_signals: [resource_usage]\n", 1), "runtime sub-signal"},
	} {
		if err := loadAgentcoreCausal(t, tc.text); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: build error %v, want %q", name, err, tc.want)
		}
	}
}
