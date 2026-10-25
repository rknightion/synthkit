// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// storyResources are the causal stories' resources (e2e causal stories fixture): one 60 rps
// intake path, every demand constant so the expected volumes are exact.
func storyResources() []Resource {
	return []Resource{
		// Declared before the quota on purpose: the solve order must follow the request path.
		{Name: "intake-db-pool", Kind: KindPool, Size: 10, HoldMS: 20, HeldAcross: "records-db", AcquireTimeoutMS: 3000, Demand: constant(60),
			Error: &ErrorTemplate{Status: 503, Type: "SQLTransientConnectionException"}},
		{Name: "records-db", Kind: KindDependency, LatencyMS: 15, SlowFactor: 12},
		{Name: "intake-quota", Kind: KindQuota, LimitRPS: 80, Demand: constant(60)},
		{Name: "partner-api", Kind: KindDependency, LatencyMS: 120},
		{Name: "partner-api-cert", Kind: KindCredential, Of: "partner-api", CredentialType: CredentialServerCertificate},
		{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 80, HoldMS: 900, HeldAcross: "partner-api", Demand: constant(60),
			Error: &ErrorTemplate{Status: 429, Type: "TooManyRequestsException"}},
		{Name: "validation-queue", Kind: KindQueue, Size: 40, HoldMS: 400, HeldAcross: "external-validator", Demand: constant(60)},
		{Name: "external-validator", Kind: KindDependency, LatencyMS: 350, SlowFactor: 3},
		{Name: "completeness-rule", Kind: KindRule, RejectShare: 0.35, Error: &ErrorTemplate{Status: 422}},
	}
}

// storyFlow is the intake app's request tree as the app workload declares it: intake-api meets
// its quota, pool and rule, then calls records-db, enrich-fn (throttles resent twice) and the
// validation publisher in turn; enrich-fn calls the partner gateway (5xx, timeout and connect
// resent once).
func storyFlow() *Flow {
	return &Flow{Nodes: []FlowNode{
		{Parent: -1, Uses: []string{"intake-quota", "intake-db-pool", "completeness-rule"}},
		{Parent: 0, Uses: []string{"records-db"}},
		{Parent: 0, Policy: CallPolicy{TimeoutMS: 10000, Retries: 2, BackoffMS: 200, RetryOn: []string{RetryThrottle}},
			Uses: []string{"enrich-fn-concurrency"}},
		{Parent: 2, Policy: CallPolicy{TimeoutMS: 2000, Retries: 1, RetryOn: []string{Retry5xx, RetryTimeout, RetryConnect}},
			Uses: []string{"partner-api", "partner-api-cert"}},
		{Parent: 0, Uses: []string{"validation-queue"}},
	}}
}

// storySet is the stories' set with the app's four capacity loads; withFlow places them in the
// request tree, without it they are the Policy-only loads of the v1 closed loop.
func storySet(withFlow bool) Set {
	f := storyFlow()
	load := func(res string, node int) Load {
		l := Load{Resource: res, Share: 1, Policy: f.Nodes[node].Policy}
		if withFlow {
			l.Flow, l.Node = f, node
		}
		return l
	}
	s := set(storyResources()...)
	s.Loads = []Load{load("intake-quota", 0), load("intake-db-pool", 0), load("enrich-fn-concurrency", 2), load("validation-queue", 4)}
	return s
}

// storyStep advances a fresh model to t0 with one fault active on the final step only (so a
// resource solved before the one gating it would still read the healthy previous step) and
// returns the demand of each capacity resource at t0.
func storyStep(t *testing.T, withFlow bool, mode, target string, intensity float64) map[string]State {
	t.Helper()
	clk := newClock()
	if mode != "" {
		clk.on(mode, target, t0, t0.Add(Step), intensity)
	}
	m := mustCompile(t, storySet(withFlow))
	m.Advance(t0, clk)
	out := map[string]State{}
	for _, name := range []string{"intake-quota", "intake-db-pool", "enrich-fn-concurrency", "validation-queue"} {
		st, ok := m.State(name, t0)
		if !ok {
			t.Fatalf("no state for %s", name)
		}
		out[name] = st
	}
	return out
}

// A bound resource's demand follows the calls that reach it (SKT-0168.09.33): the L11 proof
// measured the declared 60 rps at every bound construct whatever stopped the calls upstream
// (SQS 3600 sent while the publisher got none, Lambda 3600 against 2356 and 2351 intake calls).
func TestFlowDemandFollowsUpstreamAdmission(t *testing.T) {
	poolReject := 1 - 1/(60*0.185/10) // records-db slowed 12x: rho 1.11
	for _, tc := range []struct {
		name, mode, target string
		intensity          float64
		want               map[string]float64
	}{
		{"healthy", "", "", 0, map[string]float64{"intake-quota": 60, "intake-db-pool": 60, "enrich-fn-concurrency": 60, "validation-queue": 60}},
		// Story 1: every handshake to the partner fails, enrich-fn answers 502 (not resent on
		// throttle-only retries), so the publisher is never called and the queue gets nothing.
		{"story 1 certificate", ModeCredentialExpired, "partner-api-cert", 1,
			map[string]float64{"intake-quota": 60, "intake-db-pool": 60, "enrich-fn-concurrency": 60, "validation-queue": 0}},
		// Story 3: the regressed rule rejects 35% at the entry, before any call.
		{"story 3 rule", ModeRuleRegression, "completeness-rule", 1,
			map[string]float64{"intake-quota": 60, "intake-db-pool": 60, "enrich-fn-concurrency": 39, "validation-queue": 39}},
		// Story 5: the slowed database exhausts the pool, whose rejections never reach the calls.
		{"story 5 pool", ModeDependencySlow, "records-db", 1,
			map[string]float64{"intake-quota": 60, "intake-db-pool": 60, "enrich-fn-concurrency": 60 * (1 - poolReject), "validation-queue": 60 * (1 - poolReject)}},
		// Story 5 variant: the halved quota admits 40 rps, and only those meet the pool.
		{"story 5 quota", ModeQuotaReduced, "intake-quota", 0.5,
			map[string]float64{"intake-quota": 60, "intake-db-pool": 40, "enrich-fn-concurrency": 40, "validation-queue": 40}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := storyStep(t, true, tc.mode, tc.target, tc.intensity)
			for name, w := range tc.want {
				if d := got[name].Demand; math.Abs(d-w) > 1e-9*math.Max(1, w) {
					t.Errorf("%s demand %v, want %v", name, d, w)
				}
			}
		})
	}
}

// Story 2: throttles resent twice amplify the function's own demand (the v1 closed loop, kept),
// and only the calls that finally succeed reach the publisher: 60 x (1 - p^3).
func TestFlowRetriesAmplifyAndGate(t *testing.T) {
	got := storyStep(t, true, ModeConcurrencyReduced, "enrich-fn-concurrency", 0.7)
	fn := got["enrich-fn-concurrency"]
	p := fn.RejectShare
	if p <= 0 {
		t.Fatalf("reduced concurrency rejects nothing: %+v", fn)
	}
	if want := 60 * (1 + p + p*p); math.Abs(fn.Demand-want) > 1e-6 {
		t.Errorf("function demand %v, want 60 x (1 + p + p^2) = %v", fn.Demand, want)
	}
	if v1 := storyStep(t, false, ModeConcurrencyReduced, "enrich-fn-concurrency", 0.7)["enrich-fn-concurrency"]; math.Abs(v1.Demand-fn.Demand) > 1e-6 {
		t.Errorf("own-reject amplification changed: flow %v, policy-only %v", fn.Demand, v1.Demand)
	}
	if want, q := 60*(1-p*p*p), got["validation-queue"].Demand; math.Abs(q-want) > 1e-6 {
		t.Errorf("queue demand %v, want the calls that succeed, 60 x (1 - p^3) = %v", q, want)
	}
}

// The tree gates and never resends: a failure deeper in the callee's subtree that a caller
// retrying 5xx resends (the certificate's handshake failure reaching intake-api as enrich-fn's
// 502) adds no arrivals at the function (design §3.3, v1), while the publisher behind the failed
// call still gets nothing.
func TestFlowSubtreeFailureNotResent(t *testing.T) {
	s := storySet(true)
	s.Loads[2].Flow.Nodes[2].Policy.RetryOn = []string{RetryThrottle, Retry5xx}
	s.Loads[2].Policy = s.Loads[2].Flow.Nodes[2].Policy
	clk := newClock().on(ModeCredentialExpired, "partner-api-cert", t0, t0.Add(Step), 1)
	m := mustCompile(t, s)
	m.Advance(t0, clk)
	fn, _ := m.State("enrich-fn-concurrency", t0)
	q, _ := m.State("validation-queue", t0)
	if fn.Demand != 60 || q.Demand != 0 {
		t.Fatalf("function demand %v (want 60), queue %v (want 0)", fn.Demand, q.Demand)
	}
}

func TestFlowArrivalsArithmetic(t *testing.T) {
	q := &compiled{r: Resource{Name: "q", Kind: KindQuota}}
	c := &compiled{r: Resource{Name: "c", Kind: KindPool}}
	r := &compiled{r: Resource{Name: "r", Kind: KindRule}}
	d := &compiled{r: Resource{Name: "d", Kind: KindQueue}}
	for _, x := range []*compiled{q, c, r, d} {
		x.key = x.failKey()
	}
	byName := map[string]*compiled{"q": q, "c": c, "r": r, "d": d}
	f := &Flow{Nodes: []FlowNode{
		{Parent: -1, Uses: []string{"q"}},
		{Parent: 0, Policy: CallPolicy{Retries: 2, RetryOn: []string{Retry5xx}}, Uses: []string{"c", "r"}},
		{Parent: 0, Uses: []string{"d"}},
	}}
	cf := compileFlow(f, byName)
	rej := map[*compiled]float64{q: 0.2, c: 0.5, r: 0.1}
	fn := func(x *compiled) float64 { return rej[x] }
	// c: the quota admits 0.8 (c's own 503 resends follow the load's Policy, not the tree).
	if got, want := cf.gateFor(f, 1, "c").reach(fn), 0.8; math.Abs(got-want) > 1e-12 {
		t.Errorf("reach at c = %v, want %v", got, want)
	}
	// r: met after the pool admits.
	if got, want := cf.gateFor(f, 1, "r").reach(fn), 0.8*0.5; math.Abs(got-want) > 1e-12 {
		t.Errorf("reach at r = %v, want %v", got, want)
	}
	// d: reached when the call into node 1 finally succeeds: each attempt passes both uses with
	// 0.5 x 0.9, the pool's 503 is resent twice and the rule's 422 never is.
	if got, want := cf.gateFor(f, 2, "d").reach(fn), 0.8*0.45*(1+0.5+0.25); math.Abs(got-want) > 1e-12 {
		t.Errorf("reach at d = %v, want %v", got, want)
	}
	clear(rej)
	for _, n := range []struct {
		node int
		res  string
	}{{0, "q"}, {1, "c"}, {1, "r"}, {2, "d"}} {
		if got := cf.gateFor(f, n.node, n.res).reach(fn); got != 1 {
			t.Errorf("healthy reach at %s = %v, want exactly 1", n.res, got)
		}
	}
}

// Without a fault the flow changes nothing: every state and window is bitwise the Policy-only
// model's over a day of a moving business curve, as are the arrivals the constructs count.
func TestFlowHealthyIdentity(t *testing.T) {
	mk := func() *fakeClock {
		c := newClock()
		c.factor = func(t time.Time) float64 { return 0.5 + 0.5*math.Sin(float64(t.Unix())/3600) }
		return c
	}
	withFlow := func() Set {
		s := storySet(true)
		for i := range s.Envs[0].Resources {
			if dm := s.Envs[0].Resources[i].Demand; dm != nil {
				s.Envs[0].Resources[i].Demand = &Demand{OffPeakRPS: 5, PeakRPS: 60}
			}
		}
		return s
	}
	a := withFlow()
	b := withFlow()
	for i := range b.Loads {
		b.Loads[i].Flow, b.Loads[i].Node = nil, 0
	}
	ma, mb := mustCompile(t, a), mustCompile(t, b)
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
			wa, _ := ma.Window(r.Name, now.Add(-time.Minute), now)
			wb, _ := mb.Window(r.Name, now.Add(-time.Minute), now)
			if !reflect.DeepEqual(wa, wb) {
				t.Fatalf("%s window at +%ds differs", r.Name, sec)
			}
		}
	}
	if ca.evals != cb.evals {
		t.Errorf("clock reads differ: %d vs %d", ca.evals, cb.evals)
	}
}

// Two models of the same faulted set agree at every grid point (consumers agree by determinism).
func TestFlowDeterminism(t *testing.T) {
	clk := func() *fakeClock {
		return newClock().
			on(ModeRuleRegression, "completeness-rule", t0.Add(-10*time.Minute), t0, 1).
			on(ModeQuotaReduced, "intake-quota", t0.Add(-5*time.Minute), t0.Add(5*time.Minute), 0.5).
			on(ModeConcurrencyReduced, "enrich-fn-concurrency", t0.Add(-7*time.Minute), t0.Add(3*time.Minute), 0.7)
	}
	a, b := mustCompile(t, storySet(true)), mustCompile(t, storySet(true))
	ca, cb := clk(), clk()
	for sec := 0; sec <= 900; sec += 5 {
		now := t0.Add(time.Duration(sec-600) * time.Second)
		a.Advance(now, ca)
		if sec%60 == 0 {
			b.Advance(now, cb)
			for _, r := range storyResources() {
				sa, _ := a.State(r.Name, now)
				sb, _ := b.State(r.Name, now)
				if !reflect.DeepEqual(sa, sb) {
					t.Fatalf("%s at %ds: %+v vs %+v", r.Name, sec, sa, sb)
				}
			}
		}
	}
}

func TestValidateFlow(t *testing.T) {
	for name, mut := range map[string]func(*Load){
		"has no nodes":         func(l *Load) { l.Flow.Nodes = nil },
		"entry and has no":     func(l *Load) { l.Flow.Nodes[0].Parent = 2 },
		"must precede it":      func(l *Load) { l.Flow.Nodes[1].Parent = 3 },
		"unknown resource":     func(l *Load) { l.Flow.Nodes[1].Uses = []string{"nope"} },
		"twice":                func(l *Load) { l.Flow.Nodes[1].Uses = []string{"records-db", "records-db"} },
		"retries 9":            func(l *Load) { l.Flow.Nodes[3].Policy.Retries = 9 },
		"node 4 does not use":  func(l *Load) { l.Resource = "intake-quota" },
		"node 99 does not use": func(l *Load) { l.Node = 99 },
	} {
		t.Run(name, func(t *testing.T) {
			s := storySet(true)
			s.Loads = s.Loads[3:]
			mut(&s.Loads[0])
			err := Validate(s)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Validate = %v, want an error containing %q", err, name)
			}
		})
	}
	if err := Validate(storySet(true)); err != nil {
		t.Fatal(err)
	}
}
