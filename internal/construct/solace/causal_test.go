// SPDX-License-Identifier: AGPL-3.0-only

package solace_test

// causal_test.go - the opt-in causal binding: a queue that names a `queue` resource takes its
// flows from the causal model. The loader-level proof (real blueprint, real incidents) is in
// internal/integration; these tests drive the construct directly with an injected resource set.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/construct/solace"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
)

const boundYAML = `
targets: [QueueDetails, QueueStats]
brokers:
  - name: broker-a
    vpns:
      - name: vpn-1
        queues:
          - { name: orders-q, resource: orders-queue, spool_quota_mb: 4 }
          - { name: audit, publish_msgs_per_sec: 2 }
`

// ordersSet is a queue drained by four consumers at 400 ms a message, 10 msg/s at baseline,
// whose hold stretches with the external validator it waits on (350 ms, x3 at intensity 1).
func ordersSet() causal.Set {
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "orders-queue", Kind: causal.KindQueue, Size: 4, HoldMS: 400, HeldAcross: "validator",
			Demand: &causal.Demand{PeakRPS: 8, Load: causal.LoadConstant}},
		{Name: "validator", Kind: causal.KindDependency, LatencyMS: 350, SlowFactor: 3},
	}}}}
}

// slowWindow is a causal.Clock whose only input is dependency_slow on the validator at full
// intensity during [from, to).
type slowWindow struct{ from, to time.Time }

func (s slowWindow) Eval(now time.Time, mode, scope string) (bool, float64) {
	if mode == causal.ModeDependencySlow && scope == "validator" && !now.Before(s.from) && now.Before(s.to) {
		return true, 1
	}
	return false, 0
}
func (slowWindow) Factor(time.Time, float64, bool) float64 { return 1 }
func (slowWindow) VolumeMultiplier() float64               { return 1 }

func buildBound(t *testing.T, set causal.Set) *solace.Construct {
	t.Helper()
	cfg := decodeStrict(t, boundYAML)
	cfg.UseResources(set)
	c, err := solace.Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.(*solace.Construct)
}

func queueGauge(mc *coretest.MetricCapture, name, queue string) float64 {
	for _, s := range mc.Find(name) {
		if s.Labels["queue_name"] == queue {
			return s.Value
		}
	}
	return -1
}

// A held-across dependency slowdown, with no consumer fault at all, grows the bound queue's
// backlog (mu 10 -> 3.6 msg/s against 8 msg/s arriving) and the queue drains after it ends.
func TestBoundQueueBacklogFollowsHeldAcrossDependency(t *testing.T) {
	c := buildBound(t, ordersSet())
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	clk := slowWindow{from: start.Add(10 * time.Minute), to: start.Add(25 * time.Minute)}
	eng := shape.New("UTC", nil)
	var peak float64
	for i := 1; i <= 50; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		c.AdvanceCausal(now, clk) // the runner's per-master-tick hook
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		depth := queueGauge(mc, "solace_queue_spool_usage_msgs", "orders-q")
		switch {
		case i < 10:
			if depth != 0 {
				t.Fatalf("minute %d: healthy depth %v", i, depth)
			}
		case i >= 10 && i < 25:
			peak = max(peak, depth)
		case i >= 45:
			if depth != 0 {
				t.Fatalf("minute %d: did not drain: depth %v", i, depth)
			}
		}
		if got, want := queueGauge(mc, "solace_queue_binds", "orders-q"), 4.0; got != want {
			t.Fatalf("binds %v, want the resource size %v", got, want)
		}
	}
	if peak < 1000 {
		t.Fatalf("slow validator grew the backlog to only %v", peak)
	}
}

func TestBoundQueueRequiresDeclaredResource(t *testing.T) {
	cfg := decodeStrict(t, boundYAML)
	if _, err := solace.Build(cfg, nil); err == nil || !strings.Contains(err.Error(), `"orders-queue"`) {
		t.Fatalf("bound queue with no resources declared: %v", err)
	}
}

// Declared resources change nothing for a config that binds none of them.
func TestUnboundOutputIgnoresInjectedResources(t *testing.T) {
	run := func(inject bool) string {
		cfg := decodeStrict(t, curveYAML(nil))
		if inject {
			cfg.UseResources(ordersSet())
		}
		c, err := solace.Build(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		mc := &coretest.MetricCapture{}
		w := coretest.World(mc, nil, nil)
		start := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
		for i := range 40 {
			if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), w); err != nil {
				t.Fatal(err)
			}
		}
		var lines []string
		for _, s := range mc.All() {
			lines = append(lines, fmt.Sprintf("%s|%v|%v|%v", s.Name, s.Labels, s.Value, s.Kind))
		}
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	if a, b := run(false), run(true); a != b {
		t.Fatal("injected resources changed unbound output")
	}
}

func TestBoundQueueIsNotALegacyFailureTarget(t *testing.T) {
	cfg := decodeStrict(t, boundYAML)
	var got []string
	for _, tg := range cfg.FailureTargets() {
		got = append(got, tg.Name)
	}
	if strings.Join(got, ",") != "audit" {
		t.Fatalf("queue-axis targets %v, want only the unbound queue", got)
	}
}

func TestRegistrationRegistersResourceVocabulary(t *testing.T) {
	have := map[string]bool{}
	for _, m := range solace.Registration().FailureModes {
		have[m.Name] = true
	}
	for _, m := range append([]string{solace.ModePublishDrop, solace.ModeConsumerStall}, modeNames(causal.FailureModes)...) {
		if !have[m] {
			t.Errorf("mode %q not registered", m)
		}
	}
}

func modeNames(ms []failuremode.Mode) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// Without a shape engine the model cannot advance: a bound queue stays empty and nothing panics.
func TestBoundQueueWithoutShapeEngineIsEmpty(t *testing.T) {
	c := buildBound(t, ordersSet())
	mc := &coretest.MetricCapture{}
	if err := c.Tick(context.Background(), time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC), &core.World{Metrics: mc}); err != nil {
		t.Fatal(err)
	}
	if d := queueGauge(mc, "solace_queue_spool_usage_msgs", "orders-q"); d != 0 {
		t.Fatalf("depth %v without a model clock", d)
	}
}
