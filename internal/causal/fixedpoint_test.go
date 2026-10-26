// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// chainSet is a request tree where a capacity resource is read before it is solved: front calls
// mid (5xx resent twice), mid meets its pool and calls leaf (no retries), leaf meets its
// concurrency limit. The limit gates on the pool (it is solved after it), while the pool's
// arrivals count front's resends of mid's 503, which the limit's 429 causes. Test-only sizes.
func chainSet(withFlow bool) Set {
	f := &Flow{Nodes: []FlowNode{
		{Parent: -1},
		{Parent: 0, Policy: CallPolicy{Retries: 2, RetryOn: []string{Retry5xx}}, Uses: []string{"mid-pool"}},
		{Parent: 1, Uses: []string{"leaf-conc"}},
	}}
	s := set(
		Resource{Name: "mid-pool", Kind: KindPool, Size: 1000, HoldMS: 50, HeldAcross: "mid-db", AcquireTimeoutMS: 3000, Demand: constant(60)},
		Resource{Name: "mid-db", Kind: KindDependency, LatencyMS: 10},
		Resource{Name: "leaf-conc", Kind: KindConcurrency, Size: 40, HoldMS: 400, HeldAcross: "leaf-dep", Demand: constant(60)},
		Resource{Name: "leaf-dep", Kind: KindDependency, LatencyMS: 100},
	)
	load := func(res string, node int) Load {
		l := Load{Resource: res, Share: 1, Policy: f.Nodes[node].Policy}
		if withFlow {
			l.Flow, l.Node = f, node
		}
		return l
	}
	s.Loads = []Load{load("mid-pool", 1), load("leaf-conc", 2)}
	return s
}

// At a fault's onset the pool reads the limit's reject share of the same step, not the healthy
// previous one (SKT-0205): the step is solved to its joint fixed point, so the pool's demand is
// front's 60 rps times 1 + r + r^2 for the limit's r on that step, and every attempt of mid
// reaches leaf. Solved once in order, the pool read r = 0 and kept 60 rps while the app already
// resent the throttled calls.
func TestFlowDownstreamRejectReadOnTheOnsetStep(t *testing.T) {
	clk := newClock().on(ModeConcurrencyReduced, "leaf-conc", t0, t0.Add(time.Hour), 0.75)
	m := mustCompile(t, chainSet(true))
	m.Advance(t0, clk)
	pool, _ := m.State("mid-pool", t0)
	lim, _ := m.State("leaf-conc", t0)
	r := lim.RejectShare
	if r <= 0.3 {
		t.Fatalf("the reduced limit rejects %v, want a large share", r)
	}
	if want := 60 * (1 + r + r*r); math.Abs(pool.Demand-want) > 1e-9*want {
		t.Errorf("pool demand %v on the onset step, want 60 x (1 + r + r^2) = %v for the limit's r %v", pool.Demand, want, r)
	}
	if math.Abs(lim.Demand-pool.Demand) > 1e-9*pool.Demand {
		t.Errorf("limit demand %v, want every admitted attempt of mid, %v", lim.Demand, pool.Demand)
	}
	// The fixed point holds: the limit's reject share is what its own demand gives.
	if want := 1 - 10/(lim.Demand*0.4); math.Abs(r-want) > 1e-9 {
		t.Errorf("limit reject %v, want 1 - capacity/offered = %v", r, want)
	}
}

// Healthy, the extra passes never run: the late-read limit rejects nothing on every step, so the
// flow model stays bitwise the Policy-only model over a moving business curve.
func TestFlowFixedPointHealthyIdentity(t *testing.T) {
	mk := func() *fakeClock {
		c := newClock()
		c.factor = func(t time.Time) float64 { return 0.5 + 0.5*math.Sin(float64(t.Unix())/3600) }
		return c
	}
	curve := func(withFlow bool) Set {
		s := chainSet(withFlow)
		if withFlow {
			// Supplying fallback timing alone must not change a no-timeout flow's arithmetic
			// or add clock reads, even with a shared large legacy request duration.
			s.Loads[0].Flow.Legacy = &LegacyTiming{P95MS: 5000, EntryStretch: FaultScale{Mode: "latency_storm", Scope: "front", AtFull: 4}}
			for i := range s.Loads[0].Flow.Nodes {
				s.Loads[0].Flow.Nodes[i].Stretch = FaultScale{Mode: "latency_storm", Scope: "node", AtFull: 4}
			}
		}
		for i := range s.Envs[0].Resources {
			if s.Envs[0].Resources[i].Demand != nil {
				s.Envs[0].Resources[i].Demand = &Demand{OffPeakRPS: 5, PeakRPS: 60}
			}
		}
		return s
	}
	a, b := curve(true), curve(false)
	ma, mb := mustCompile(t, a), mustCompile(t, b)
	if len(ma.late) != 1 || ma.late[0].r.Name != "leaf-conc" {
		t.Fatalf("late reads %v, want the leaf limit", ma.late)
	}
	ca, cb := mk(), mk()
	for sec := 0; sec <= 2*3600; sec += 60 {
		now := t0.Add(time.Duration(sec) * time.Second)
		ma.Advance(now, ca)
		mb.Advance(now, cb)
		for _, r := range a.Envs[0].Resources {
			sa, _ := ma.State(r.Name, now)
			sb, _ := mb.State(r.Name, now)
			if !reflect.DeepEqual(sa, sb) {
				t.Fatalf("%s at +%ds: flow %+v, policy-only %+v", r.Name, sec, sa, sb)
			}
		}
	}
	if ca.evals != cb.evals {
		t.Errorf("clock reads differ: %d vs %d", ca.evals, cb.evals)
	}
}

// timeoutSet is front calling fn under a 1500 ms timeout resent twice; fn's own work is its
// concurrency limit's hold (400 ms, held across a 300 ms dependency slowed up to 5x) and it fails
// on its own at hopRate. Test-only values.
func timeoutSet(hopRate float64) Set {
	f := &Flow{Nodes: []FlowNode{
		{Parent: -1},
		{Parent: 0, Policy: CallPolicy{TimeoutMS: 1500, Retries: 2, RetryOn: []string{RetryTimeout, Retry5xx}},
			Uses: []string{"fn-conc"}, Duration: "fn-conc",
			Stretch:  FaultScale{Mode: "latency_storm", Scope: "fn", AtFull: 4},
			HopError: HopError{Rate: hopRate, Mode: "error_spike", Scope: "fn", PerIntensity: 0.6, Status: 500}},
	}}
	s := set(
		Resource{Name: "fn-conc", Kind: KindConcurrency, Size: 1000, HoldMS: 400, HeldAcross: "slow-dep", Demand: constant(60)},
		Resource{Name: "slow-dep", Kind: KindDependency, LatencyMS: 300, SlowFactor: 5},
	)
	s.Loads = []Load{{Resource: "fn-conc", Share: 1, Policy: f.Nodes[1].Policy, Flow: f, Node: 1}}
	return s
}

// tail is P(X > y) for X lognormal with mean m and DurationSigma, the evaluator's draw.
func tail(y, m float64) float64 {
	median := m / math.Exp(DurationSigma*DurationSigma/2)
	return 0.5 * math.Erfc(math.Log(y/median)/(DurationSigma*math.Sqrt2))
}

// The attempts the app resends because they ran past the caller's timeout or failed on their own
// count as demand at the callee (SKT-0205): with the dependency slowed 5x, fn's hold is 1600 ms
// and the share of attempts outlasting 1500 ms is the lognormal tail of that hold; a
// latency_storm stretches the draw; an error_spike fails 0.6 x intensity of the attempts that
// finish in time, before the declared rate.
func TestFlowTimeoutAndHopErrorResendsAreDemand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hopRate float64
		faults  [][3]any // mode, scope, intensity
		mean    float64  // the hold the draw is around, times the stretch
		hop     float64  // the hop failure share
	}{
		{"healthy, declared rate", 0.2, nil, 400, 0.2},
		{"dependency slowed", 0, [][3]any{{ModeDependencySlow, "slow-dep", 1.0}}, 1600, 0},
		{"latency storm", 0, [][3]any{{"latency_storm", "fn", 0.5}}, 400 * 2.5, 0},
		{"error spike", 0.2, [][3]any{{"error_spike", "fn", 0.5}}, 400, 0.3 + 0.7*0.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := newClock()
			for _, f := range tc.faults {
				clk.on(f[0].(string), f[1].(string), t0, t0.Add(time.Hour), f[2].(float64))
			}
			m := mustCompile(t, timeoutSet(tc.hopRate))
			m.Advance(t0, clk)
			st, _ := m.State("fn-conc", t0)
			tau := tail(1500, tc.mean)
			r := tau + (1-tau)*tc.hop // timed out, or finished in time and failed on its own
			if want := 60 * (1 + r + r*r); math.Abs(st.Demand-want) > 1e-9*want {
				t.Errorf("demand %v, want 60 x (1 + r + r^2) = %v for r %v (timed out %v)", st.Demand, want, r, tau)
			}
		})
	}
}

// A timeout counts only attempts that do not fail at the transport: a refused connection never
// outlasts the timeout, a pool rejection after its acquire timeout always does.
func TestFlowTimedOutRejections(t *testing.T) {
	pool := &compiled{r: Resource{Name: "p", Kind: KindPool, AcquireTimeoutMS: 3000}}
	rule := &compiled{r: Resource{Name: "r", Kind: KindRule}}
	fn := &flowNode{mean: 1000, storm: 1}
	if got := fn.timedOut(pool, 2000, 0); got != 1 {
		t.Errorf("pool rejection after a 3000 ms acquire timeout times out %v of a 2000 ms timeout, want all", got)
	}
	if got, want := fn.timedOut(rule, 2000, 0), tail(2000, 1000); math.Abs(got-want) > 1e-15 {
		t.Errorf("rule rejection after the work times out %v, want the work's tail %v", got, want)
	}
	if got := (&flowNode{}).over(2000, 0, 1); got != 0 {
		t.Errorf("a node without a duration times out %v, want 0", got)
	}
}

// Integrate the independent leaf's probability coordinate directly as a reference, without
// reusing elapsedServe or its compression. This exposes the finite quadrature's actual error,
// rather than masking it with the app sample's statistical tolerance.
func TestFlowChildElapsedQuadratureRefinement(t *testing.T) {
	cf := &cflow{nodes: []flowNode{
		{policy: CallPolicy{TimeoutMS: 700}, kids: []int{1}, mean: 50, storm: 1, covered: -1},
		{mean: 900, storm: 1, covered: -1},
	}}
	reject := func(*compiled) float64 { return 0 }
	reference := 0.0
	const points = 1 << 18
	for j := range points {
		u := (float64(j) + 0.5) / points
		y := 900 * math.Exp(DurationSigma*math.Sqrt2*math.Erfinv(2*u-1)-DurationSigma*DurationSigma/2)
		// Parent completes at max(X, 0.1 X + Y), X mean 50, Y mean 900.
		limit := math.Min(700, (700-y)/PreWorkFraction)
		reference += 1 - unitCDF(limit/50)
	}
	reference /= points
	for _, bins := range []int{elapsedBins, 512} {
		got := 0.0
		for _, e := range cf.elapsedServe(0, reject, bins, false) {
			if e.key.class == classTimeout {
				got += e.p
			}
		}
		errorLimit := 0.001
		if bins == 512 {
			errorLimit = 0.0001
		}
		t.Logf("bins %d timeout share %.8f reference %.8f absolute error %.8f", bins, got, reference, math.Abs(got-reference))
		if math.Abs(got-reference) > errorLimit {
			t.Errorf("%d cells timeout share %v reference %v, error exceeds %v", bins, got, reference, errorLimit)
		}
	}
}

// Reference the legacy leaf analytically conditional on the request budget. First attempts
// use L; the two resends use that SAME L times independent U[.8,1.2]. Integrating the marginal
// timeout share before its geometric series would materially underestimate this expectation.
func TestFlowLegacyTimingConvergence(t *testing.T) {
	q := &compiled{r: Resource{Name: "q", Kind: KindQuota}}
	f := &Flow{Legacy: &LegacyTiming{P95MS: 3000}, Nodes: []FlowNode{
		{Parent: -1},
		{Parent: 0, Uses: []string{"q"}, Policy: CallPolicy{TimeoutMS: 500, Retries: 2, RetryOn: []string{RetryTimeout}}},
	}}
	cf := compileFlow(f, map[string]*compiled{"q": q})
	cf.prepare(newClock(), t0)
	g := cf.gateFor(f, 1, "q")
	rej := func(*compiled) float64 { return 0 }
	reference := 0.0
	const samples = 1 << 18
	for j := range samples {
		u := (float64(j) + 0.5) / samples
		r := 3000 / math.Exp(1.645*0.5) * math.Exp(0.5*math.Sqrt2*math.Erfinv(float64(2*u)-1))
		lo, hi := 0.3*0.7*r, 0.7*r
		first, again := 0.0, 0.0
		if hi > 500 {
			x := math.Max(lo, 500)
			first = (hi - x) / (hi - lo)
			y := math.Min(hi, 625)
			if y > x {
				again = (1.2*(y-x) - 500*math.Log(y/x)) / 0.4
			}
			again += math.Max(0, hi-math.Max(lo, 625))
			again /= hi - lo
		}
		reference += 1 + first + again
	}
	reference /= samples
	for _, points := range []int{512, legacyPoints, 8192} {
		got := g.legacyArrivals(rej, points, legacyBins)
		t.Logf("points %d legacy attempts %.8f independent integral %.8f absolute error %.8f", points, got, reference, math.Abs(got-reference))
		limit := 0.003
		if points == 8192 {
			limit = 0.001
		}
		if math.Abs(got-reference) > limit {
			t.Errorf("%d points attempts %v reference %v, error exceeds %v", points, got, reference, limit)
		}
	}
}

// This reference integrates each fresh sibling's uniform jitter via the closed-form CDF of
// two uniform variates' sum; it does not reuse elapsedServe, emitElapsed or compression.
func TestFlowLegacySharedBudgetConvergence(t *testing.T) {
	p := &compiled{r: Resource{Name: "p", Kind: KindPool, HoldMS: 50}}
	f := &Flow{Legacy: &LegacyTiming{P95MS: 8000}, Nodes: []FlowNode{
		{Parent: -1},
		{Parent: 0, Uses: []string{"p"}, Duration: "p", Policy: CallPolicy{TimeoutMS: 1000, Retries: 2, RetryOn: []string{RetryTimeout}}},
		{Parent: 1}, {Parent: 1},
	}}
	cf := compileFlow(f, map[string]*compiled{"p": p})
	cf.prepare(newClock(), t0)
	g := cf.gateFor(f, 1, "p")
	rej := func(*compiled) float64 { return 0 }
	positiveSquare := func(x float64) float64 {
		if x <= 0 {
			return 0
		}
		return x * x
	}
	own := make([]float64, 64)
	for j := range own {
		u := (float64(j) + 0.5) / float64(len(own))
		own[j] = 50 * math.Exp(0.5*math.Sqrt2*math.Erfinv(float64(2*u)-1)-0.125)
	}
	reference := 0.0
	const samples = 1 << 16
	for j := 1; j <= samples; j++ {
		// Permuted prime bases avoid sharing the production quadrature's error pattern.
		r := 8000 / math.Exp(1.645*0.5) * math.Exp(0.5*math.Sqrt2*math.Erfinv(2*radicalInverse(j, 5)-1))
		b := 0.7 * r / 3
		x := b * (0.3 + 0.7*radicalInverse(j, 2))
		y := b * (0.3 + 0.7*radicalInverse(j, 3))
		first := 1 - unitCDF(math.Min(1000, (1000-x-y)/PreWorkFraction)/50)
		again := 0.0
		for _, work := range own {
			threshold := 1000 - PreWorkFraction*work
			z := threshold - 0.8*x - 0.8*y
			cdf := (positiveSquare(z) - positiveSquare(z-0.4*x) - positiveSquare(z-0.4*y) + positiveSquare(z-0.4*x-0.4*y)) / (2 * 0.4 * x * 0.4 * y)
			if work > 1000 {
				cdf = 0
			}
			again += 1 - clamp01(cdf)
		}
		again /= float64(len(own))
		reference += 1 + first*(1+again)
	}
	reference /= samples
	for _, resolution := range [][2]int{{legacyPoints, legacyBins}, {8192, 64}} {
		got := g.legacyArrivals(rej, resolution[0], resolution[1])
		t.Logf("points %d bins %d shared-budget attempts %.8f independent integral %.8f absolute error %.8f", resolution[0], resolution[1], got, reference, math.Abs(got-reference))
		limit := 0.004
		if resolution[0] == 8192 {
			limit = 0.001
		}
		if math.Abs(got-reference) > limit {
			t.Errorf("attempts %v reference %v, error exceeds %v", got, reference, limit)
		}
	}
}

func TestValidateFlowWorkFields(t *testing.T) {
	for name, mut := range map[string]func(*FlowNode){
		"is not one of its uses":   func(n *FlowNode) { n.Duration = "records-db" },
		"pool, concurrency or dep": func(n *FlowNode) { n.Duration = "partner-api-cert" },
		"factor 0.5":               func(n *FlowNode) { n.Stretch = FaultScale{Mode: "latency_storm", AtFull: 0.5} },
		"must be a 5xx":            func(n *FlowNode) { n.HopError = HopError{Rate: 0.1, Status: 422} },
		"must be in [0,1]":         func(n *FlowNode) { n.HopError = HopError{Rate: 2, Status: 500} },
		"but no mode":              func(n *FlowNode) { n.HopError = HopError{PerIntensity: 0.6, Status: 500} },
	} {
		t.Run(name, func(t *testing.T) {
			s := storySet(true)
			mut(&s.Loads[3].Flow.Nodes[3])
			if err := Validate(s); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Validate = %v, want an error containing %q", err, name)
			}
		})
	}
	s := storySet(true)
	s.Loads[0].Flow.Nodes[0].HopError = HopError{Rate: 0.1, Status: 500}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "has no hop error") {
		t.Fatalf("Validate = %v, want the entry's hop error rejected", err)
	}
}
