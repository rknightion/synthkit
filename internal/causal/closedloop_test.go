// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Live faults change between master ticks. Consumers advanced on every master tick (the
// composition root's AdvanceCausal hook) agree exactly at every grid point whatever cadence they
// read at; a model advanced only at its own 60 s cadence samples live inputs late and diverges,
// which is why the hook exists.
func TestLiveFaultsAgreeUnderMasterTickHook(t *testing.T) {
	s := cycleSet()
	clk := cycleClock()
	clk.live = map[string]float64{}
	a, b, lagging := mustCompile(t, s), mustCompile(t, s), mustCompile(t, s)
	start := t0.Add(700 * time.Millisecond)
	diverged := false
	for sec := 0; sec <= 3600; sec += 5 {
		now := start.Add(time.Duration(sec) * time.Second)
		// Operator toggles between ticks, off the 5 s grid.
		switch sec {
		case 300:
			clk.live[ModeConsumerSlowdown+"\x00validation-queue"] = 1
		case 420:
			clk.live[ModeDependencyUnavailable+"\x00external-validator"] = 0.4
		case 905:
			delete(clk.live, ModeConsumerSlowdown+"\x00validation-queue")
		case 1500:
			clk.live[ModePoolExhausted+"\x00orders-db-pool"] = 0.9
		case 1810:
			clk.live = map[string]float64{}
		}
		a.Advance(now, clk) // hook: every consumer, every master tick
		b.Advance(now, clk)
		if sec%60 == 0 {
			lagging.Advance(now, clk)
			for _, d := range s.Envs {
				for _, r := range d.Resources {
					sa, _ := a.State(r.Name, now)
					sb, _ := b.State(r.Name, now)
					if !reflect.DeepEqual(sa, sb) {
						t.Fatalf("%s at +%ds: consumers disagree under the hook: %+v vs %+v", r.Name, sec, sa, sb)
					}
					wa, _ := a.Window(r.Name, now.Add(-time.Minute), now)
					wb, _ := b.Window(r.Name, now.Add(-time.Minute), now)
					if !reflect.DeepEqual(wa, wb) {
						t.Fatalf("%s window at +%ds disagrees", r.Name, sec)
					}
					if sl, _ := lagging.State(r.Name, now); !reflect.DeepEqual(sa, sl) {
						diverged = true
					}
				}
			}
		}
	}
	if !diverged {
		t.Fatal("a model advanced only at its own cadence never diverged under live toggles: the test exercises nothing")
	}
}

// demand.from: a worker pool fed by a queue takes the queue's processed rate.
func TestDemandFromUpstream(t *testing.T) {
	far := t0.Add(time.Hour)
	q := Resource{Name: "jobs", Kind: KindQueue, Size: 4, HoldMS: 400, Demand: constant(8)}
	pool := Resource{Name: "worker-db-pool", Kind: KindPool, Size: 10, HoldMS: 20, AcquireTimeoutMS: 1000, Demand: &Demand{From: "jobs"}}
	// Declared downstream first: Compile still evaluates the upstream first.
	m := mustCompile(t, set(pool, q))
	clk := newClock().on(ModeConsumerSlowdown, "jobs", t0, far, 0.5)
	m.Advance(at(-1), clk)
	if st, _ := m.State("worker-db-pool", at(-1)); !near(st.Demand, 8) {
		t.Fatalf("healthy downstream demand %v", st.Demand)
	}
	m.Advance(t0, clk)
	if st, _ := m.State("worker-db-pool", t0); !near(st.Demand, 5) {
		t.Fatalf("slowed queue serves 5/s, downstream demand %v", st.Demand)
	}
}

// Expected retry attempts close the loop: the effective demand d solves
// d = d0 x (1 + p + p^2) with p the reject share at d, for an edge retrying throttles twice.
func TestRetryLoadFixedPoint(t *testing.T) {
	far := t0.Add(time.Hour)
	fn := Resource{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 20, HoldMS: 900, Demand: constant(15)}
	clk := newClock().on(ModeConcurrencyReduced, fn.Name, t0, far, 0.7)
	retry := CallPolicy{Retries: 2, RetryOn: []string{RetryThrottle}}
	s := set(fn)
	s.Loads = []Load{{Resource: fn.Name, Share: 1, Policy: retry}}
	m := mustCompile(t, s)
	m.Advance(t0, clk)
	st, _ := m.State(fn.Name, t0)
	p := st.RejectShare
	if want := 15 * (1 + p + p*p); !near(st.Demand, want) || st.Demand <= 15 {
		t.Fatalf("demand %v, want fixed point %v (p %v)", st.Demand, want, p)
	}
	if offered := st.Demand * 900 / 1000; !near(p, 1-6/offered) {
		t.Fatalf("reject %v not the physics at the fixed point", p)
	}
	w, _ := m.Window(fn.Name, at(-1), t0)
	if !near(w.Arrivals, st.Demand*5) || !near(w.Admitted, 6/0.9*5) {
		t.Fatalf("window counts %+v", w)
	}
	// Healthy: no rejection, no amplification.
	healthy := mustCompile(t, s)
	healthy.Advance(t0, newClock())
	if st, _ := healthy.State(fn.Name, t0); st.Demand != 15 {
		t.Fatalf("healthy demand amplified: %v", st.Demand)
	}
	// A policy that does not retry this resource's class adds nothing; a half share amplifies half.
	s.Loads = []Load{{Resource: fn.Name, Share: 1, Policy: CallPolicy{Retries: 3, RetryOn: []string{Retry5xx}}}}
	if st := stateAfterOneStep(t, s, clk, fn.Name); st.Demand != 15 {
		t.Fatalf("non-retried class amplified: %v", st.Demand)
	}
	s.Loads = []Load{{Resource: fn.Name, Share: 0.5, Policy: retry}}
	st = stateAfterOneStep(t, s, clk, fn.Name)
	if p := st.RejectShare; !near(st.Demand, 15*(0.5+0.5*(1+p+p*p))) {
		t.Fatalf("half-share demand %v", st.Demand)
	}
	// A queue producer retrying 503s on overflow.
	q := Resource{Name: "q", Kind: KindQueue, Size: 1, HoldMS: 1000, MaxDepth: 1, Demand: constant(10)}
	qs := set(q)
	qs.Loads = []Load{{Resource: "q", Share: 1, Policy: CallPolicy{Retries: 1, RetryOn: []string{Retry5xx}}}}
	m = mustCompile(t, qs)
	m.Advance(t0, newClock())
	st, _ = m.State("q", t0)
	if p := st.RejectShare; p <= 0 || !near(st.Demand, 10*(1+p)) {
		t.Fatalf("queue retry demand %+v", st)
	}
}

// Work admitted to a capacity resource fails after admission when the dependency it is held
// across is unavailable or that dependency's credentials fail.
func TestFailedShareAndCredentials(t *testing.T) {
	far := t0.Add(time.Hour)
	dep := Resource{Name: "partner-api", Kind: KindDependency, LatencyMS: 120}
	cert := Resource{Name: "partner-api-cert", Kind: KindCredential, Of: "partner-api", CredentialType: CredentialServerCertificate}
	fn := Resource{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 20, HoldMS: 900, HeldAcross: "partner-api", Demand: constant(10)}
	s := set(dep, cert, fn)
	clk := newClock().on(ModeDependencyUnavailable, dep.Name, t0, far, 0.3).on(ModeCredentialExpired, cert.Name, t0, far, 0.5)
	m := mustCompile(t, s)
	m.Advance(t0, clk)
	st, _ := m.State(fn.Name, t0)
	if !near(st.FailedShare, 1-0.7*0.5) || st.RejectShare != 0 {
		t.Fatalf("failed share %+v", st)
	}
	w, _ := m.Window(fn.Name, at(-1), t0)
	if !near(w.Failed, 50*0.65) || !near(w.Served, 50) {
		t.Fatalf("window failed %+v", w)
	}
	if cs := s.CredentialsOf("partner-api"); len(cs) != 1 || cs[0].Name != cert.Name {
		t.Fatalf("CredentialsOf %+v", cs)
	}
	if st, _ := m.State(cert.Name, t0); st.FailedShare != 0.5 {
		t.Fatalf("credential failed share %v", st.FailedShare)
	}
}

func TestApplyConsumer(t *testing.T) {
	far := t0.Add(time.Hour)
	q := Resource{Name: "validation-queue", Kind: KindQueue, Size: 4, HoldMS: 400, Demand: constant(2)}
	m := mustCompile(t, set(q, Resource{Name: "d", Kind: KindDependency}))
	clk := newClock().on(ModeConsumerSlowdown, q.Name, t0, far, 1)
	m.Advance(at(11), clk) // 12 stalled steps: oldest message 60 s old
	e := m.ApplyConsumer(q.Name, at(11))
	if e.Fail || e.Wait != time.Minute || e.Mode != ModeConsumerSlowdown || !strings.Contains(e.Message, "oldest message 60 s") {
		t.Fatalf("consumer effect %+v", e)
	}
	if e := m.ApplyConsumer(q.Name, at(-5)); e.Wait != 0 || e.Message != "" {
		t.Fatalf("healthy consumer effect %+v", e)
	}
	if e := m.ApplyConsumer("d", at(11)); e.Wait != 0 || e.Kind != "" {
		t.Fatalf("non-queue consumer effect %+v", e)
	}
}

func TestValidateDemandFromAndLoads(t *testing.T) {
	q := Resource{Name: "q", Kind: KindQueue, Size: 1, HoldMS: 1, Demand: constant(1)}
	dep := Resource{Name: "d", Kind: KindDependency}
	pool := func(from string, extra func(*Demand)) Resource {
		dm := &Demand{From: from}
		if extra != nil {
			extra(dm)
		}
		return Resource{Name: "p", Kind: KindPool, Size: 1, HoldMS: 1, AcquireTimeoutMS: 1, Demand: dm}
	}
	cases := map[string]Set{
		"names no declared resource": set(q, pool("nope", nil)),
		"not a pool, concurrency":    set(q, dep, pool("d", nil)),
		"names the resource itself":  set(pool("p", nil)),
		"excludes off_peak_rps":      set(q, pool("q", func(d *Demand) { d.PeakRPS = 3 })),
		"demand.from cycle": set(
			Resource{Name: "a", Kind: KindQuota, LimitRPS: 1, Demand: &Demand{From: "b"}},
			Resource{Name: "b", Kind: KindQuota, LimitRPS: 1, Demand: &Demand{From: "a"}}),
		"unknown resource":       {Envs: set(q).Envs, Loads: []Load{{Resource: "x", Share: 1}}},
		"carries no demand":      {Envs: set(q, dep).Envs, Loads: []Load{{Resource: "d", Share: 1}}},
		"share 0 must be":        {Envs: set(q).Envs, Loads: []Load{{Resource: "q", Share: 0}}},
		"retries 9 must be":      {Envs: set(q).Envs, Loads: []Load{{Resource: "q", Share: 1, Policy: CallPolicy{Retries: 9}}}},
		"more than its declared": {Envs: set(q).Envs, Loads: []Load{{Resource: "q", Share: 0.6}, {Resource: "q", Share: 0.6}}},
	}
	for want, s := range cases {
		if err := Validate(s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	if err := Validate(set(q, pool("q", nil))); err != nil {
		t.Fatal(err)
	}
}
