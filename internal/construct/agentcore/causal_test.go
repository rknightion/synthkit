// SPDX-License-Identifier: AGPL-3.0-only

package agentcore

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// outageClock fully reduces every concurrency resource between from and to.
type outageClock struct{ from, to time.Time }

func (o outageClock) Eval(now time.Time, mode, _ string) (bool, float64) {
	if mode == causal.ModeConcurrencyReduced && now.After(o.from) && !now.After(o.to) {
		return true, 1
	}
	return false, 0
}
func (outageClock) Factor(time.Time, float64, bool) float64 { return 1 }
func (outageClock) VolumeMultiplier() float64               { return 1 }

func boundSet() causal.Set {
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "planner-conc", Kind: causal.KindConcurrency, Size: 20, HoldMS: 900,
			Demand: &causal.Demand{OffPeakRPS: 10, PeakRPS: 10, Load: causal.LoadConstant}},
		{Name: "pool", Kind: causal.KindPool, Size: 5, HoldMS: 10, AcquireTimeoutMS: 100,
			Demand: &causal.Demand{OffPeakRPS: 1, PeakRPS: 1, Load: causal.LoadConstant}},
	}}}}
}

func boundFixture() *fixture.Set {
	return &fixture.Set{Cloud: &fixture.Cloud{AccountID: "111122223333", Region: "us-east-1"}}
}

func plannerSeries(mc *coretest.MetricCapture, name string) (float64, bool) {
	for _, s := range mc.Find(name) {
		if strings.HasSuffix(s.Labels["name"], "runtime/planner") {
			return s.Value, true
		}
	}
	return 0, false
}

// A window in which every request is rejected has no admitted work, so it reports no latency
// observation: the previous window's value must not linger in the state store.
func TestBoundRuntimeDropsLatencyWhenNothingIsAdmitted(t *testing.T) {
	cfg := &Config{Runtimes: []Runtime{{Agent: "planner", Resource: "planner-conc"}}}
	cfg.UseResources(boundSet())
	c, err := Build(cfg, boundFixture())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	clk := outageClock{from: start.Add(3 * time.Minute), to: start.Add(5 * time.Minute)}
	type tick struct {
		inv, thr, total, lat float64
		latSeen              bool
	}
	var got []tick
	for i := 1; i <= 7; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		c.(causal.Advancer).AdvanceCausal(now, clk)
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
			t.Fatal(err)
		}
		var k tick
		k.inv, _ = plannerSeries(mc, "aws_bedrock_agentcore_invocations_sum")
		k.thr, _ = plannerSeries(mc, "aws_bedrock_agentcore_throttles_sum")
		k.total, _ = plannerSeries(mc, "aws_bedrock_agentcore_total_errors_sum")
		k.lat, k.latSeen = plannerSeries(mc, "aws_bedrock_agentcore_latency_sum")
		got = append(got, k)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	for i, k := range got {
		minute := i + 1
		switch {
		case minute == 5 || minute == 4: // 09:03 to 09:05 is the outage: 09:04 and 09:05 windows lie inside it
			if !near(k.inv, 0) || !near(k.thr, 600) || !near(k.total, 600) || k.latSeen {
				t.Fatalf("minute %d: all-rejected window %+v, want 0 invocations, 600 throttles, 600 total errors and no latency", minute, k)
			}
		case minute == 1, minute == 2, minute >= 6:
			if !near(k.inv, 600) || !near(k.thr, 0) || !near(k.lat, 900) || !k.latSeen {
				t.Fatalf("minute %d: healthy window %+v", minute, k)
			}
		}
	}
}

// An agent named "global" has the deployment-wide series' ARN, so it cannot be bound.
func TestBoundRuntimeRejectsAgentNamedGlobal(t *testing.T) {
	cfg := &Config{Agents: []string{"global"}, Runtimes: []Runtime{{Agent: "global", Resource: "planner-conc"}}}
	cfg.UseResources(boundSet())
	if _, err := Build(cfg, boundFixture()); err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("build error %v, want a collision rejection", err)
	}
}

// Registration carries the resource vocabulary beside the legacy mode.
func TestRegistrationCarriesResourceModes(t *testing.T) {
	have := map[string]bool{}
	for _, m := range Registration().FailureModes {
		have[m.Name] = true
	}
	for _, m := range causal.FailureModes {
		if !have[m.Name] {
			t.Fatalf("registration lacks resource mode %q", m.Name)
		}
	}
	if !have["agentcore_throttle"] {
		t.Fatal("registration dropped the legacy agentcore_throttle mode")
	}
}

// A construct with no runtimes holds no model, ignores the runner hook and never needs resources.
func TestUnboundHoldsNoModel(t *testing.T) {
	c, err := Build(&Config{}, boundFixture())
	if err != nil {
		t.Fatal(err)
	}
	c.(causal.Advancer).AdvanceCausal(time.Now(), outageClock{})
	if c.(*Construct).model != nil {
		t.Fatal("unbound construct compiled a model")
	}
}

// unavailableClock makes the dependency's calls fail to connect with the given share between from
// and to.
type unavailableClock struct {
	from, to time.Time
	share    float64
}

func (u unavailableClock) Eval(now time.Time, mode, _ string) (bool, float64) {
	if mode == causal.ModeDependencyUnavailable && now.After(u.from) && !now.After(u.to) {
		return true, u.share
	}
	return false, 0
}
func (unavailableClock) Factor(time.Time, float64, bool) float64 { return 1 }
func (unavailableClock) VolumeMultiplier() float64               { return 1 }

func heldSet() causal.Set {
	return causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: []causal.Resource{
		{Name: "partner-api", Kind: causal.KindDependency, LatencyMS: 120, SlowFactor: 4},
		{Name: "planner-conc", Kind: causal.KindConcurrency, Size: 20, HoldMS: 900, HeldAcross: "partner-api",
			Demand: &causal.Demand{OffPeakRPS: 10, PeakRPS: 10, Load: causal.LoadConstant}},
	}}}}
}

func latencyStats(mc *coretest.MetricCapture) (sum, avg, lo, hi, count float64, seen bool) {
	for _, s := range mc.All() {
		if !strings.HasSuffix(s.Labels["name"], "runtime/planner") {
			continue
		}
		switch s.Name {
		case "aws_bedrock_agentcore_latency_sum":
			sum, seen = s.Value, true
		case "aws_bedrock_agentcore_latency_average":
			avg = s.Value
		case "aws_bedrock_agentcore_latency_minimum":
			lo = s.Value
		case "aws_bedrock_agentcore_latency_maximum":
			hi = s.Value
		case "aws_bedrock_agentcore_latency_sample_count":
			count = s.Value
		}
	}
	return
}

// A bound runtime's latency is the mix of the invocations the model completes and those it fails
// (SKT-0207): an invocation that fails at the held-across dependency ends at its failed call, not
// at the completing hold, so the mean agrees with the concurrency the model holds (Little's law:
// in-use = served rate x mean latency). Minimum and Maximum are the two populations. Healthy
// windows keep the completing hold on every stat.
func TestBoundRuntimeLatencyMixesFailedInvocations(t *testing.T) {
	cfg := &Config{Runtimes: []Runtime{{Agent: "planner", Resource: "planner-conc"}}}
	cfg.UseResources(heldSet())
	start := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		share float64
	}{{"half of the calls fail", 0.5}, {"every call fails", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Build(cfg, boundFixture())
			if err != nil {
				t.Fatal(err)
			}
			clk := unavailableClock{from: start.Add(2 * time.Minute), to: start.Add(6 * time.Minute), share: tc.share}
			for i := 1; i <= 8; i++ {
				now := start.Add(time.Duration(i) * time.Minute)
				c.(causal.Advancer).AdvanceCausal(now, clk)
				mc := &coretest.MetricCapture{}
				if err := c.Tick(context.Background(), now, coretest.World(mc, nil, nil)); err != nil {
					t.Fatal(err)
				}
				sum, avg, lo, hi, count, seen := latencyStats(mc)
				if !seen || count != 60 || sum != avg {
					t.Fatalf("minute %d: latency %v %v %v %v %v seen=%v", i, sum, avg, lo, hi, count, seen)
				}
				switch {
				case i <= 2 || i >= 8: // healthy: the completing hold on every stat
					if avg != 900 || lo != 900 || hi != 900 {
						t.Fatalf("minute %d healthy: latency avg %v min %v max %v, want 900", i, avg, lo, hi)
					}
				case i >= 4 && i <= 6: // wholly inside the incident
					st, _ := c.(*Construct).model.State("planner-conc", now)
					// Little's law on the model's own in-use: served rate x mean latency.
					if want := st.InUse / st.ServedRPS * 1000; math.Abs(avg-want) > 1e-6*want {
						t.Fatalf("minute %d: mean latency %v, but in-use %v at %v rps implies %v", i, avg, st.InUse, st.ServedRPS, want)
					}
					if lo >= 900 || hi > 900 || avg >= 900 || lo > avg || avg > hi {
						t.Fatalf("minute %d: latency avg %v min %v max %v, want failed invocations shorter than the 900 ms hold", i, avg, lo, hi)
					}
					if tc.share == 1 && !(lo == avg && hi == avg) {
						t.Fatalf("minute %d: every invocation fails, so every stat is one value: %v %v %v", i, avg, lo, hi)
					}
					if tc.share < 1 && !(hi == 900 && lo < avg) {
						t.Fatalf("minute %d: a mix has the completing hold as its maximum: %v %v %v", i, avg, lo, hi)
					}
				}
			}
		})
	}
}
