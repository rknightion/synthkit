// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// chainBlueprint is a test-only declaration: one Lambda publisher with a healthy peer, and one
// Solace queue it publishes into with a healthy peer queue. The scenario couples a function
// failure with the queue no longer receiving publishes. Rates are modest steady streams, not
// tuned to force emission.
const chainBlueprint = `
name: chain-incident
label: chain-incident
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
      lambda:
        functions:
          - {name: response-publisher, invocations_per_min: 30, duration_ms: 120, error_fraction: 0.01}
          - {name: response-auditor, invocations_per_min: 30, duration_ms: 80}
integrations:
  solace:
    brokers:
      - name: broker-a
        vpns:
          - name: vpn-1
            queues:
              - name: responses
              - name: audit
scenarios:
  - name: responses_unpublished
    title: Responses decided but never published
    effects:
      - {mode: function_error_spike, target: response-publisher, intensity: 1}
      - {mode: function_duration_storm, target: response-publisher, intensity: 1}
      - {mode: queue_publish_drop, target: responses, intensity: 1}
      - {mode: queue_consumer_stall, target: audit, intensity: 0}
`

const chainSchedule = `
incidents:
  - scenario: responses_unpublished
    at: "2026-10-24T09:10"
    for: 10m
`

// The wire names of the two axes, pinned literally (they appear in targets and wildcards).
const axisFunction, axisQueue failuremode.Axis = "function", "queue"

var chainStart = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

func TestChainTargetsResolveToDeclaredFunctionsAndQueues(t *testing.T) {
	r, err := blueprint.Load([]byte(chainBlueprint+chainSchedule), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]failuremode.Axis{}
	for _, tg := range r.Targets {
		have[tg.Name] = tg.Axis
	}
	for name, axis := range map[string]failuremode.Axis{
		"response-publisher": axisFunction, "response-auditor": axisFunction,
		"responses": axisQueue, "audit": axisQueue,
	} {
		if have[name] != axis {
			t.Errorf("target %q: axis %q, want %q (targets %v)", name, have[name], axis, have)
		}
	}
	joined := strings.Join(r.Incidents, "\n")
	for _, want := range []string{
		"function_error_spike@2026-10-24T09:10/10m#1@response-publisher",
		"queue_publish_drop@2026-10-24T09:10/10m#1@responses",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("incident %q not compiled; have:\n%s", want, joined)
		}
	}
}

func TestChainUnknownTargetsAreLoud(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{"target: response-publisher, intensity: 1}\n      - {mode: function_duration_storm", "target: nope-fn, intensity: 1}\n      - {mode: function_duration_storm", `unknown target "nope-fn"`},
		{"target: responses,", "target: nope-queue,", `unknown target "nope-queue"`},
		{"mode: queue_publish_drop, target: responses", "mode: queue_publish_drop, target: response-auditor", "not declared on target"},
		{"mode: queue_publish_drop, target: responses", "mode: queue_publish_drop, target: \"function:*\"", "not declared on axis"},
	} {
		text := strings.Replace(chainBlueprint, c.from, c.to, 1)
		if text == chainBlueprint {
			t.Fatalf("mutation %q did not apply", c.from)
		}
		if _, err := blueprint.Load([]byte(text), runner.Catalog()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v", c.want, err)
		}
	}
}

// renderChain ticks the real Lambda and Solace constructs for 30 minutes from chainStart, with
// the compiled scheduled incidents or none, keyed by minute index.
func renderChain(t *testing.T, r *blueprint.Resolved, incidents bool) map[int]map[string]float64 {
	t.Helper()
	var entries []string
	if incidents {
		entries = r.Incidents
	}
	eng := shape.New("UTC", entries)
	out := map[int]map[string]float64{}
	reg := runner.Catalog()
	built := 0
	for _, ci := range r.Constructs {
		if ci.Kind != "lambda" && ci.Kind != "solace" {
			continue
		}
		built++
		cr, _ := reg.Construct(ci.Kind)
		c, err := cr.Build(ci.Config, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 30; i++ {
			mc := &coretest.MetricCapture{}
			if err := c.Tick(context.Background(), chainStart.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc}); err != nil {
				t.Fatal(err)
			}
			if out[i] == nil {
				out[i] = map[string]float64{}
			}
			for _, s := range mc.All() {
				out[i][seriesKey(s.Name, s.Labels)] = s.Value
			}
		}
	}
	if built != 2 {
		t.Fatalf("want lambda and solace instances, built %d", built)
	}
	return out
}

func seriesKey(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k, v := range labels {
		keys = append(keys, k+"="+v)
	}
	sort.Strings(keys)
	return name + "{" + strings.Join(keys, ",") + "}"
}

// sum adds every sample of name whose labels include sel.
func sum(m map[string]float64, name, sel string) float64 {
	v := 0.0
	for k, x := range m {
		if strings.HasPrefix(k, name+"{") && strings.Contains(k, sel) {
			v += x
		}
	}
	return v
}

func TestChainScenarioMovesLambdaAndSolaceOnlyWhileActive(t *testing.T) {
	r, err := blueprint.Load([]byte(chainBlueprint+chainSchedule), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	base := renderChain(t, r, false)
	inc := renderChain(t, r, true)
	const pub, aud = "dimension_FunctionName=response-publisher", "dimension_FunctionName=response-auditor"
	const qResp, qAudit = "queue_name=responses", "queue_name=audit"
	for i := 1; i <= 30; i++ {
		b, a := base[i], inc[i]
		active := i >= 10 && i < 20
		// Untouched peers never move; intensity-zero effects leave the baseline unchanged.
		for k, v := range b {
			if strings.Contains(k, aud) || strings.Contains(k, qAudit) {
				if a[k] != v {
					t.Fatalf("minute %d: untouched peer series %s moved %v -> %v", i, k, v, a[k])
				}
			}
		}
		if i < 10 {
			if fmt.Sprint(a) != fmt.Sprint(b) {
				t.Fatalf("minute %d: output changed before the window opened", i)
			}
			continue
		}
		inv := sum(a, "aws_lambda_invocations_sum", pub)
		errs := sum(a, "aws_lambda_errors_sum", pub)
		dur := sum(a, "aws_lambda_duration_average", pub)
		spooled := sum(a, "solace_queue_msg_spooled", qResp)
		if active {
			if inv == 0 || errs != inv {
				t.Errorf("minute %d: error spike at intensity 1 should fail every execution: errors %v invocations %v", i, errs, inv)
			}
			if want := 4 * sum(b, "aws_lambda_duration_average", pub); dur != want {
				t.Errorf("minute %d: duration storm average %v, want %v", i, dur, want)
			}
			if prev := sum(inc[i-1], "solace_queue_msg_spooled", qResp); i > 10 && spooled != prev {
				t.Errorf("minute %d: publish drop still spooling into the queue: %v -> %v", i, prev, spooled)
			}
		} else if i >= 21 {
			if errs >= inv {
				t.Errorf("minute %d: errors %v did not recover below invocations %v after the window", i, errs, inv)
			}
			if dur != sum(b, "aws_lambda_duration_average", pub) {
				t.Errorf("minute %d: duration did not recover", i)
			}
			if prev := sum(inc[i-1], "solace_queue_msg_spooled", qResp); spooled <= prev {
				t.Errorf("minute %d: publishes did not resume after the window: %v -> %v", i, prev, spooled)
			}
		}
	}
	// The window must actually show as a change on the targeted series.
	if sum(inc[15], "solace_queue_msg_spooled", qResp) >= sum(base[15], "solace_queue_msg_spooled", qResp) {
		t.Fatal("publish drop never reduced the queue's spooled total")
	}
}

func TestChainEffectsUnitSemantics(t *testing.T) {
	stall := strings.Replace(chainBlueprint, "{mode: queue_consumer_stall, target: audit, intensity: 0}", "{mode: queue_consumer_stall, target: audit, intensity: 1}\n      - {mode: function_throttle_storm, target: response-auditor, intensity: 0.5}", 1)
	r, err := blueprint.Load([]byte(stall+chainSchedule), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	base := renderChain(t, r, false)
	inc := renderChain(t, r, true)
	const aud, qAudit = "dimension_FunctionName=response-auditor", "queue_name=audit"
	// Consumer stall: nothing is delivered, so the backlog and spool usage grow every window.
	for i := 11; i < 20; i++ {
		if d := sum(inc[i], "solace_queue_msg_total_deleted", qAudit) - sum(inc[i-1], "solace_queue_msg_total_deleted", qAudit); d != 0 {
			t.Errorf("minute %d: stalled consumers still deleted %v messages", i, d)
		}
		if sum(inc[i], "solace_queue_spool_usage_msgs", qAudit) <= sum(inc[i-1], "solace_queue_spool_usage_msgs", qAudit) {
			t.Errorf("minute %d: stalled queue depth did not grow", i)
		}
	}
	if sum(base[15], "solace_queue_spool_usage_msgs", qAudit) != 0 {
		t.Fatal("healthy baseline queue should hold no backlog")
	}
	// Throttle storm at 0.5: throttles add half the admitted executions; invocations unchanged.
	if got, want := sum(inc[15], "aws_lambda_throttles_sum", aud), 0.5*sum(inc[15], "aws_lambda_invocations_sum", aud); got != want {
		t.Errorf("throttle storm: throttles %v, want %v", got, want)
	}
	if sum(inc[15], "aws_lambda_invocations_sum", aud) != sum(base[15], "aws_lambda_invocations_sum", aud) {
		t.Error("throttle storm changed admitted invocations")
	}
	if sum(base[15], "aws_lambda_throttles_sum", aud) != 0 {
		t.Error("baseline auditor should not throttle")
	}
}

// TestChainScenarioLiveActivation drives the unchanged control-plane path: activating the
// scenario by id moves the targeted series, and deactivating it stops the effect.
func TestChainScenarioLiveActivation(t *testing.T) {
	reg := runner.Catalog()
	bp, err := blueprint.Load([]byte(chainBlueprint), reg)
	if err != nil {
		t.Fatal(err)
	}
	mc := &coretest.MetricCapture{}
	rn := runner.New(runner.Sinks{Metrics: mc, Logs: &coretest.LogCapture{}}, reg, runner.Options{})
	if err := rn.AddBlueprint(bp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rn.DrainQueues(ctx)
	})
	tick := func(i int, active bool) map[string]float64 {
		cs := control.DefaultState()
		if active {
			cs.ActiveScenarios = []string{"chain-incident/responses_unpublished"}
		}
		rn.ApplyControl(cs)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := rn.RunOnce(ctx, chainStart.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, s := range mc.All() {
			if s.T.Equal(chainStart.Add(time.Duration(i) * time.Minute)) {
				out[seriesKey(s.Name, s.Labels)] = s.Value
			}
		}
		return out
	}
	const pub = "dimension_FunctionName=response-publisher"
	tick(1, false)
	healthy := tick(2, false)
	if e, n := sum(healthy, "aws_lambda_errors_sum", pub), sum(healthy, "aws_lambda_invocations_sum", pub); n == 0 || e >= n {
		t.Fatalf("healthy publisher: errors %v invocations %v", e, n)
	}
	on := tick(3, true)
	if e, n := sum(on, "aws_lambda_errors_sum", pub), sum(on, "aws_lambda_invocations_sum", pub); n == 0 || e != n {
		t.Fatalf("live scenario: errors %v invocations %v, want every execution failing", e, n)
	}
	spooled := sum(on, "solace_queue_msg_spooled", "queue_name=responses")
	if again := tick(4, true); sum(again, "solace_queue_msg_spooled", "queue_name=responses") != spooled {
		t.Fatal("live publish drop still spooling into the queue")
	}
	off := tick(5, false)
	if e, n := sum(off, "aws_lambda_errors_sum", pub), sum(off, "aws_lambda_invocations_sum", pub); e >= n {
		t.Fatalf("deactivated scenario still failing: errors %v invocations %v", e, n)
	}
}

// A declared function or queue whose name is already a target on another axis stays loadable,
// is not added as an ambiguous target, and is reported as a warning.
func TestChainCrossAxisNameIsWarnedNotAmbiguous(t *testing.T) {
	text := strings.Replace(chainBlueprint, "name: response-auditor", "name: prod", 1)
	text = strings.Replace(text, "- name: audit\n", "- name: response-publisher\n", 1)
	text = strings.Replace(text, "target: audit,", "target: response-publisher,", 1)
	r, err := blueprint.Load([]byte(text), runner.Catalog())
	if err == nil {
		t.Fatal("queue_consumer_stall on a function-axis name must be rejected")
	}
	text = strings.Replace(text, "      - {mode: queue_consumer_stall, target: response-publisher, intensity: 0}\n", "", 1)
	if r, err = blueprint.Load([]byte(text), runner.Catalog()); err != nil {
		t.Fatal(err)
	}
	axes := map[string][]failuremode.Axis{}
	for _, tg := range r.Targets {
		axes[tg.Name] = append(axes[tg.Name], tg.Axis)
	}
	if fmt.Sprint(axes["prod"]) != "[cloud]" || fmt.Sprint(axes["response-publisher"]) != "[function]" {
		t.Fatalf("collisions changed the inventory: prod=%v response-publisher=%v", axes["prod"], axes["response-publisher"])
	}
	joined := strings.Join(r.Warnings, "\n")
	for _, want := range []string{`function "prod" is not an incident target`, `queue "response-publisher" is not an incident target`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning %q missing; have:\n%s", want, joined)
		}
	}
}

// A declared name the schedule grammar cannot carry is warned, and a wildcard on its axis says
// which declared names it does not reach.
func TestChainUnaddressableNamesAreWarned(t *testing.T) {
	text := strings.Replace(chainBlueprint, "- name: audit\n", "- name: \"audit/v1\"\n", 1)
	text = strings.Replace(text, "target: audit,", "target: \"queue:*\",", 1)
	r, err := blueprint.Load([]byte(text), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range r.Targets {
		if tg.Name == "audit/v1" {
			t.Fatal("unaddressable queue name became a target")
		}
	}
	joined := strings.Join(r.Warnings, "\n")
	for _, want := range []string{`queue "audit/v1" is not an incident target`, `"queue:*" does not reach the declared names that are not incident targets: audit/v1`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning %q missing; have:\n%s", want, joined)
		}
	}
	if _, err := blueprint.Load([]byte(strings.Replace(text, "target: \"queue:*\",", "target: \"audit/v1\",", 1)), runner.Catalog()); err == nil || !strings.Contains(err.Error(), `unknown target "audit/v1"`) {
		t.Fatalf("naming an unaddressable queue: got %v", err)
	}
}
