// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"reflect"
	"testing"
	"time"
)

const eps = 1e-9

func near(a, b float64) bool { return math.Abs(a-b) <= eps*math.Max(1, math.Abs(b)) }

// stateAfterOneStep advances a fresh model over one grid point (t0) and returns the state.
func stateAfterOneStep(t *testing.T, s Set, clk *fakeClock, name string) State {
	t.Helper()
	m := mustCompile(t, s)
	m.Advance(t0, clk)
	st, ok := m.State(name, t0)
	if !ok {
		t.Fatalf("no state for %s", name)
	}
	return st
}

type want struct {
	demand, capacity, util, reject, waitMS, lf, inUse, waiting float64
	mode                                                       string
}

func checkState(t *testing.T, st State, w want) {
	t.Helper()
	got := want{st.Demand, st.Capacity, st.Utilization, st.RejectShare, st.WaitMS, st.LatencyFactor, st.InUse, st.Waiting, st.Mode}
	for _, f := range []struct {
		name string
		g, w float64
	}{
		{"demand", got.demand, w.demand}, {"capacity", got.capacity, w.capacity}, {"utilization", got.util, w.util},
		{"reject", got.reject, w.reject}, {"waitMS", got.waitMS, w.waitMS}, {"latency factor", got.lf, w.lf},
		{"in use", got.inUse, w.inUse}, {"waiting", got.waiting, w.waiting},
	} {
		if !near(f.g, f.w) {
			t.Errorf("%s = %v, want %v", f.name, f.g, f.w)
		}
	}
	if got.mode != w.mode {
		t.Errorf("mode = %q, want %q", got.mode, w.mode)
	}
}

var db = Resource{Name: "records-db", Kind: KindDependency, LatencyMS: 15, SlowFactor: 12}

// pool: a 10-connection pool holding each connection for a 20 ms transaction (one 15 ms query
// against records-db plus overhead), at 60 rps: healthy offered concurrency 1.2 (rho 0.12).
// dependency_slow at intensity 1 (lock contention, 15 ms -> 180 ms) stretches the hold to
// h = 20 + 15 x (12 - 1) = 185 ms: offered 11.1, rho 1.11, so the pool saturates and rejects.
func poolRes(d float64) Resource {
	return Resource{Name: "orders-db-pool", Kind: KindPool, Size: 10, HoldMS: 20, HeldAcross: "records-db",
		AcquireTimeoutMS: 3000, Demand: constant(d)}
}

func TestPoolFormulas(t *testing.T) {
	far := t0.Add(time.Hour)
	w75 := 143.75 * (0.8625 - 0.8) / (1 - 0.8625) // h = 20 + 15 x 8.25, rho 0.8625
	cases := []struct {
		name string
		d    float64
		clk  *fakeClock
		hold float64
		want want
	}{
		{"below knee", 60, newClock(), 20, want{demand: 60, capacity: 10, util: 0.12, lf: 1, inUse: 1.2}},
		// rho 0.9: wait = h (rho - knee) / (1 - rho) = 20 x 0.1 / 0.1 = 20 ms.
		{"between knee and 1", 450, newClock(), 20, want{demand: 450, capacity: 10, util: 0.9, waitMS: 20, lf: 1, inUse: 9, waiting: 450 * 20.0 / 1000}},
		{"dependency_slow crosses rho >= 1", 60, newClock().on(ModeDependencySlow, "records-db", t0, far, 1), 185,
			want{demand: 60, capacity: 10, util: 1.11, reject: 1 - 1/1.11, waitMS: 3000, lf: 12, inUse: 10, waiting: 60 * (1 / 1.11) * 3}},
		{"dependency_slow 0.75 queues below saturation", 60, newClock().on(ModeDependencySlow, "records-db", t0, far, 0.75), 143.75,
			want{demand: 60, capacity: 10, util: 0.8625, waitMS: w75, lf: 9.25, inUse: 8.625, waiting: 60 * w75 / 1000}},
		{"pool_exhausted 0.8", 150, newClock().on(ModePoolExhausted, "orders-db-pool", t0, far, 0.8), 20,
			want{demand: 150, capacity: 2, util: 1.5, reject: 1 - 1/1.5, waitMS: 3000, lf: 1, inUse: 2, waiting: 150 * (1 / 1.5) * 3, mode: ModePoolExhausted}},
		{"pool_exhausted 1: no connections", 60, newClock().on(ModePoolExhausted, "orders-db-pool", t0, far, 1), 20,
			want{demand: 60, capacity: 0, util: 1, reject: 1, waitMS: 3000, lf: 1, inUse: 0, waiting: 0, mode: ModePoolExhausted}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := stateAfterOneStep(t, set(db, poolRes(tc.d)), tc.clk, "orders-db-pool")
			checkState(t, st, tc.want)
			if !near(st.HoldMS, tc.hold) || !near(st.ServedRPS, tc.d*(1-tc.want.reject)) {
				t.Errorf("hold %v served %v", st.HoldMS, st.ServedRPS)
			}
		})
	}
}

func TestConcurrencyQuotaFormulas(t *testing.T) {
	far := t0.Add(time.Hour)
	fn := Resource{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 20, HoldMS: 900, Demand: constant(15)}
	quota := Resource{Name: "intake-quota", Kind: KindQuota, LimitRPS: 80, Demand: constant(60)}
	checkState(t, stateAfterOneStep(t, set(fn), newClock(), fn.Name),
		want{demand: 15, capacity: 20, util: 13.5 / 20, lf: 1, inUse: 13.5})
	// concurrency_reduced 0.7: limit 6 against offered 13.5.
	checkState(t, stateAfterOneStep(t, set(fn), newClock().on(ModeConcurrencyReduced, fn.Name, t0, far, 0.7), fn.Name),
		want{demand: 15, capacity: 6, util: 13.5 / 6, reject: 1 - 6/13.5, lf: 1, inUse: 6, mode: ModeConcurrencyReduced})
	checkState(t, stateAfterOneStep(t, set(quota), newClock(), quota.Name),
		want{demand: 60, capacity: 80, util: 0.75, lf: 1, inUse: 60})
	checkState(t, stateAfterOneStep(t, set(quota), newClock().on(ModeQuotaReduced, quota.Name, t0, far, 0.5), quota.Name),
		want{demand: 60, capacity: 40, util: 1.5, reject: 1 - 40.0/60, lf: 1, inUse: 40, mode: ModeQuotaReduced})
}

func TestDependencyCredentialRuleFormulas(t *testing.T) {
	far := t0.Add(time.Hour)
	dep := Resource{Name: "external-validator", Kind: KindDependency, LatencyMS: 350, SlowFactor: 8}
	cred := Resource{Name: "partner-api-cert", Kind: KindCredential, Of: "external-validator", CredentialType: CredentialServerCertificate}
	rule := Resource{Name: "completeness-rule", Kind: KindRule, RejectShare: 0.35}
	s := set(dep, cred, rule)
	checkState(t, stateAfterOneStep(t, s, newClock(), dep.Name), want{lf: 1})
	checkState(t, stateAfterOneStep(t, s, newClock().on(ModeDependencySlow, dep.Name, t0, far, 0.5), dep.Name),
		want{lf: 1 + 7*0.5, mode: ModeDependencySlow})
	// Default slow factor is 4 (the latency_storm magnitude).
	plain := Resource{Name: "plain-dep", Kind: KindDependency}
	checkState(t, stateAfterOneStep(t, set(plain), newClock().on(ModeDependencySlow, plain.Name, t0, far, 1), plain.Name),
		want{lf: 4, mode: ModeDependencySlow})
	// Both active: the stronger one is the dominant mode.
	both := newClock().on(ModeDependencySlow, dep.Name, t0, far, 0.2).on(ModeDependencyUnavailable, dep.Name, t0, far, 0.3)
	checkState(t, stateAfterOneStep(t, s, both, dep.Name), want{lf: 1 + 7*0.2, reject: 0.3, mode: ModeDependencyUnavailable})
	both = newClock().on(ModeDependencySlow, dep.Name, t0, far, 0.9).on(ModeDependencyUnavailable, dep.Name, t0, far, 0.3)
	checkState(t, stateAfterOneStep(t, s, both, dep.Name), want{lf: 1 + 7*0.9, reject: 0.3, mode: ModeDependencySlow})
	checkState(t, stateAfterOneStep(t, s, newClock().on(ModeCredentialExpired, cred.Name, t0, far, 1), cred.Name),
		want{reject: 1, lf: 1, mode: ModeCredentialExpired})
	checkState(t, stateAfterOneStep(t, s, newClock().on(ModeRuleRegression, rule.Name, t0, far, 0.5), rule.Name),
		want{reject: 0.5 * 0.35, lf: 1, mode: ModeRuleRegression})
}

func TestQueueFormulas(t *testing.T) {
	far := t0.Add(time.Hour)
	// 4 consumers x 1000 / 400 ms = 10 msg/s service against 8 msg/s arrivals.
	q := Resource{Name: "validation-queue", Kind: KindQueue, Size: 4, HoldMS: 400, Demand: constant(8)}
	checkState(t, stateAfterOneStep(t, set(q), newClock(), q.Name),
		want{demand: 8, capacity: 10, util: 0.8, lf: 1, inUse: 4 * 0.8})
	// consumer_slowdown 0.5: mu 5, backlog grows (8 - 5) x 5 = 15 per step.
	m := mustCompile(t, set(q))
	clk := newClock().on(ModeConsumerSlowdown, q.Name, t0, far, 0.5)
	m.Advance(t0, clk)
	// One step: 40 arrive uniformly over the step, 25 depart, so the oldest queued message is
	// arrival number 25 of 40, which arrived 3.125 s into the step: 1.875 s old (FIFO, fluid).
	if st, _ := m.State(q.Name, t0); !near(st.Backlog, 15) || !near(st.OldestAgeSec, 1.875) {
		t.Fatalf("first slowdown step %+v", st)
	}
	m.Advance(at(3), clk)
	st, _ := m.State(q.Name, at(3))
	if !near(st.Backlog, 60) || !near(st.Capacity, 5) || !near(st.WaitMS, 60.0/5*1000) || !near(st.InUse, 4) || st.Mode != ModeConsumerSlowdown {
		t.Fatalf("slowdown state %+v", st)
	}
	// Held across a 400 ms dependency: dependency_slow at x2 makes h = 400 + 400 = 800 ms.
	dep := Resource{Name: "external-validator", Kind: KindDependency, LatencyMS: 400, SlowFactor: 2}
	q2 := q
	q2.HeldAcross = dep.Name
	st = stateAfterOneStep(t, set(dep, q2), newClock().on(ModeDependencySlow, dep.Name, t0, far, 1), q.Name)
	if !near(st.Capacity, 5) || !near(st.LatencyFactor, 2) || !near(st.Backlog, 15) {
		t.Fatalf("held-across state %+v", st)
	}
	// max_depth: overflow is rejected.
	q3 := q
	q3.MaxDepth = 20
	m = mustCompile(t, set(q3))
	m.Advance(t0, clk)    // backlog 15
	m.Advance(at(1), clk) // raw 30 -> clamp 20 with 10 rejected
	st, _ = m.State(q.Name, at(1))
	if !near(st.Backlog, 20) || !near(st.RejectShare, 10.0/40) {
		t.Fatalf("max_depth state %+v", st)
	}
	w, _ := m.Window(q.Name, at(-1), at(1))
	if !near(w.Arrivals, 80) || !near(w.Rejected, 10) || !near(w.Admitted, 70) || !near(w.Served, 50) {
		t.Fatalf("max_depth window %+v", w)
	}
}

func TestDemand(t *testing.T) {
	r := Resource{Name: "q", Kind: KindQuota, LimitRPS: 1000, Demand: &Demand{OffPeakRPS: 10, PeakRPS: 50}}
	clk := newClock()
	clk.factor = func(time.Time) float64 { return 0.5 }
	s := Set{Envs: []Declaration{{Env: "dev", Weight: 0.5, NonProd: true, Resources: []Resource{r}}}}
	// business: off + (peak - off) x clamp(Factor x weight) = 10 + 40 x 0.25.
	if st := stateAfterOneStep(t, s, clk, "q"); !near(st.Demand, 20) {
		t.Fatalf("business demand %v", st.Demand)
	}
	clk.factor = func(time.Time) float64 { return 5 } // clamped to 1
	s.Envs[0].Weight = 1
	if st := stateAfterOneStep(t, s, clk, "q"); !near(st.Demand, 50) {
		t.Fatalf("clamped business demand %v", st.Demand)
	}
	r.Demand.Load = LoadConstant
	clk.vm = 2
	if st := stateAfterOneStep(t, set(r), clk, "q"); !near(st.Demand, 100) {
		t.Fatalf("constant demand %v", st.Demand)
	}
	clk.vm = 0 // paused: no demand at all, not the off-peak floor
	r.Demand.Load = ""
	if st := stateAfterOneStep(t, set(r), clk, "q"); st.Demand != 0 {
		t.Fatalf("paused demand %v", st.Demand)
	}
}

// A fault on a base name reaches every `<base>-<env>` resource; an exact name reaches one.
func TestEnvAliasFaults(t *testing.T) {
	far := t0.Add(time.Hour)
	s := Set{Envs: []Declaration{
		{Env: "PROD", Weight: 1, Resources: []Resource{{Name: "store-prod", Kind: KindDependency}}},
		{Env: "dev", Weight: 1, Resources: []Resource{{Name: "store-dev", Kind: KindDependency}, {Name: "other", Kind: KindDependency}}},
	}}
	clk := newClock().on(ModeDependencySlow, "store", t0, far, 1).on(ModeDependencySlow, "store-dev", t0, far, 0.5)
	m := mustCompile(t, s)
	m.Advance(t0, clk)
	for name, lf := range map[string]float64{"store-prod": 4, "store-dev": 4, "other": 1} {
		if st, _ := m.State(name, t0); st.LatencyFactor != lf {
			t.Errorf("%s latency factor %v, want %v", name, st.LatencyFactor, lf)
		}
	}
	if EnvAlias("store-prod", "PROD") != "store" || EnvAlias("prod", "prod") != "" || EnvAlias("other", "dev") != "" {
		t.Fatal("EnvAlias rule")
	}
}

// cycleSet covers every kind with references, for the cadence and determinism checks.
func cycleSet() Set {
	return Set{Envs: []Declaration{{Env: "prod", Weight: 1, Resources: []Resource{
		{Name: "records-db", Kind: KindDependency, LatencyMS: 15, SlowFactor: 12},
		{Name: "external-validator", Kind: KindDependency, LatencyMS: 350, SlowFactor: 8},
		{Name: "partner-api-cert", Kind: KindCredential, Of: "external-validator", CredentialType: CredentialServerCertificate},
		{Name: "completeness-rule", Kind: KindRule, RejectShare: 0.35},
		{Name: "orders-db-pool", Kind: KindPool, Size: 10, HoldMS: 20, HeldAcross: "records-db", AcquireTimeoutMS: 3000, Demand: &Demand{OffPeakRPS: 10, PeakRPS: 60}},
		{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 20, HoldMS: 900, Demand: &Demand{OffPeakRPS: 2, PeakRPS: 15}},
		{Name: "intake-quota", Kind: KindQuota, LimitRPS: 80, Demand: &Demand{OffPeakRPS: 5, PeakRPS: 60}},
		{Name: "validation-queue", Kind: KindQueue, Size: 4, HoldMS: 400, HeldAcross: "external-validator", MaxDepth: 5000, Demand: &Demand{OffPeakRPS: 1, PeakRPS: 8}},
	}}}}
}

// cycleClock: a load curve and a repeating fault cycle over two hours, as a pure function of t.
func cycleClock() *fakeClock {
	c := newClock()
	c.factor = func(t time.Time) float64 {
		h := float64(t.Sub(t0)) / float64(time.Hour)
		return 0.5 + 0.5*math.Sin(h*math.Pi)
	}
	for cyc := 0; cyc < 4; cyc++ {
		base := t0.Add(time.Duration(cyc) * 30 * time.Minute)
		c.on(ModeDependencySlow, "records-db", base.Add(2*time.Minute+2*time.Second), base.Add(12*time.Minute), 1)
		c.on(ModeDependencySlow, "external-validator", base.Add(5*time.Minute), base.Add(20*time.Minute), 0.6)
		c.on(ModeConsumerSlowdown, "validation-queue", base.Add(8*time.Minute), base.Add(14*time.Minute+3*time.Second), 1)
		c.on(ModeConcurrencyReduced, "enrich-fn-concurrency", base.Add(15*time.Minute), base.Add(25*time.Minute), 0.7)
		c.on(ModeQuotaReduced, "intake-quota", base.Add(18*time.Minute), base.Add(22*time.Minute), 0.5)
		c.on(ModeCredentialExpired, "partner-api-cert", base.Add(24*time.Minute), base.Add(27*time.Minute), 1)
		c.on(ModeRuleRegression, "completeness-rule", base.Add(10*time.Minute), base.Add(16*time.Minute), 0.8)
		c.on(ModePoolExhausted, "orders-db-pool", base.Add(26*time.Minute), base.Add(29*time.Minute), 0.5)
	}
	return c
}

// Advancing every 5 s and every 60 s gives identical State and Window at every shared grid
// point over a two-hour fault cycle, starting mid-step so the first call's grid alignment is
// exercised too.
func TestCadenceInvariance(t *testing.T) {
	s := cycleSet()
	fast, slow := mustCompile(t, s), mustCompile(t, s)
	clk := cycleClock()
	start := t0.Add(1300 * time.Millisecond)
	fast.Advance(start, clk)
	slow.Advance(start, clk)
	sawSaturation, sawBacklog := false, false
	for sec := 5; sec <= 2*3600; sec += 5 {
		now := start.Add(time.Duration(sec) * time.Second)
		fast.Advance(now, clk)
		if sec%60 != 0 {
			continue
		}
		slow.Advance(now, clk)
		for _, d := range s.Envs {
			for _, r := range d.Resources {
				a, okA := fast.State(r.Name, now)
				b, okB := slow.State(r.Name, now)
				if !okA || !okB || !reflect.DeepEqual(a, b) {
					t.Fatalf("%s at +%ds: 5s cadence %+v, 60s cadence %+v", r.Name, sec, a, b)
				}
				wa, _ := fast.Window(r.Name, now.Add(-time.Minute), now)
				wb, _ := slow.Window(r.Name, now.Add(-time.Minute), now)
				if !reflect.DeepEqual(wa, wb) {
					t.Fatalf("%s window at +%ds: %+v vs %+v", r.Name, sec, wa, wb)
				}
				if wa.Steps != 12 || wa.Truncated {
					t.Fatalf("%s window at +%ds has %d steps (truncated %v)", r.Name, sec, wa.Steps, wa.Truncated)
				}
				if r.Name == "orders-db-pool" && a.RejectShare > 0 {
					sawSaturation = true
				}
				if r.Name == "validation-queue" && a.Backlog > 0 {
					sawBacklog = true
				}
			}
		}
	}
	if !sawSaturation || !sawBacklog {
		t.Fatalf("fault cycle did not exercise saturation (%v) and backlog (%v)", sawSaturation, sawBacklog)
	}
}

func TestDeterminism(t *testing.T) {
	run := func() []State {
		m := mustCompile(t, cycleSet())
		clk := cycleClock()
		var out []State
		for sec := 0; sec <= 3600; sec += 15 {
			now := t0.Add(time.Duration(sec) * time.Second)
			m.Advance(now, clk)
			for _, name := range []string{"orders-db-pool", "validation-queue", "enrich-fn-concurrency", "records-db"} {
				st, _ := m.State(name, now)
				out = append(out, st)
			}
		}
		return out
	}
	if a, b := run(), run(); !reflect.DeepEqual(a, b) {
		t.Fatal("two runs over the same set and clock differ")
	}
}

// Under a full consumer stall the oldest message's age is exact FIFO: at the n-th stalled grid
// point it is n steps old (it arrived at the last healthy grid point), and the backlog is n
// steps of arrivals. After the stall clears the queue drains back to zero age.
func TestFIFOAgeUnderFullStall(t *testing.T) {
	q := Resource{Name: "validation-queue", Kind: KindQueue, Size: 4, HoldMS: 400, Demand: constant(2)}
	stallFrom, stallTo := at(10), at(10+360) // 30 minutes stalled
	clk := newClock().on(ModeConsumerSlowdown, q.Name, stallFrom, stallTo, 1)
	m := mustCompile(t, set(q))
	for k := 0; k < 10+360; k++ {
		m.Advance(at(k), clk)
		st, _ := m.State(q.Name, at(k))
		if k < 10 {
			if st.Backlog != 0 || st.OldestAgeSec != 0 {
				t.Fatalf("healthy step %d: %+v", k, st)
			}
			continue
		}
		n := float64(k - 10 + 1)
		if st.OldestAgeSec != n*5 || st.Backlog != n*10 {
			t.Fatalf("stalled step %d: age %v backlog %v, want %v and %v", k, st.OldestAgeSec, st.Backlog, n*5, n*10)
		}
		if st.WaitMS != float64(Horizon/time.Millisecond) {
			t.Fatalf("stalled wait %v", st.WaitMS)
		}
	}
	// First drained step: 50 served at 10/s covers 25 s of arrivals at 2/s.
	m.Advance(stallTo, clk)
	st, _ := m.State(q.Name, stallTo)
	const n = 360
	if !near(st.Backlog, n*10+10-50) || !near(st.OldestAgeSec, (n+1)*5-25) {
		t.Fatalf("first drain step: %+v", st)
	}
	prev := st.OldestAgeSec
	for k := 1; ; k++ {
		now := stallTo.Add(time.Duration(k) * Step)
		m.Advance(now, clk)
		st, _ = m.State(q.Name, now)
		if st.OldestAgeSec > prev+eps {
			t.Fatalf("age grew while draining: %v -> %v", prev, st.OldestAgeSec)
		}
		prev = st.OldestAgeSec
		if st.Backlog == 0 {
			if st.OldestAgeSec != 0 {
				t.Fatalf("empty queue with age %v", st.OldestAgeSec)
			}
			break
		}
		if k > 1000 {
			t.Fatal("queue never drained")
		}
	}
}

func TestAgeCappedAtHorizonAndGapRestart(t *testing.T) {
	q := Resource{Name: "q", Kind: KindQueue, Size: 1, HoldMS: 100, Demand: constant(1)}
	clk := newClock().on(ModeConsumerSlowdown, "q", t0, t0.Add(96*time.Hour), 1)
	m := mustCompile(t, set(q))
	m.Advance(t0, clk)
	m.Advance(t0.Add(Horizon), clk) // a 24 h gap is still integrated
	st, _ := m.State("q", t0.Add(Horizon))
	if st.OldestAgeSec != Horizon.Seconds() || !near(st.Backlog, Horizon.Seconds()+5) {
		t.Fatalf("after 24h stall: %+v", st)
	}
	m.Advance(t0.Add(Horizon+time.Hour), clk)
	st, _ = m.State("q", t0.Add(Horizon+time.Hour))
	if st.OldestAgeSec != Horizon.Seconds() {
		t.Fatalf("age not capped: %v", st.OldestAgeSec)
	}
	// A gap longer than the horizon restarts with a warm start: the state equals that of a model
	// first advanced at the same instant (state depends only on time and scheduled inputs).
	later := t0.Add(Horizon + time.Hour + Horizon + time.Minute)
	m.Advance(later, clk)
	fresh := mustCompile(t, set(q))
	fresh.Advance(later, clk)
	a, _ := m.State("q", later)
	b, _ := fresh.State("q", later)
	if !reflect.DeepEqual(a, b) || a.OldestAgeSec != Horizon.Seconds() {
		t.Fatalf("restart state %+v, fresh %+v", a, b)
	}
}

func TestWindowAndStateBounds(t *testing.T) {
	quota := Resource{Name: "intake-quota", Kind: KindQuota, LimitRPS: 80, Demand: constant(60)}
	m := mustCompile(t, set(quota))
	clk := newClock()
	if _, ok := m.State("intake-quota", t0); ok {
		t.Fatal("state before any Advance")
	}
	m.Advance(t0, clk)
	m.Advance(at(12), clk) // a 60 s catch-up
	w, ok := m.Window("intake-quota", at(0), at(12))
	if !ok || w.Steps != 12 || !near(w.Arrivals, 60*60) || !near(w.Admitted, 60*60) || w.Truncated {
		t.Fatalf("window %+v", w)
	}
	// Idempotent for now <= last.
	m.Advance(at(5), clk)
	if w2, _ := m.Window("intake-quota", at(0), at(12)); !reflect.DeepEqual(w, w2) {
		t.Fatal("Advance backwards changed state")
	}
	// A window longer than the retained ring is truncated to it.
	if w, _ := m.Window("intake-quota", at(-200), at(0)); !w.Truncated || w.Steps != int(ringSteps)-12 { // ring holds at(-167)..at(12)
		t.Fatalf("over-long window %+v", w)
	}
	// History older than the 15-minute ring is truncated.
	m.Advance(at(12+200), clk)
	if w, _ := m.Window("intake-quota", at(0), at(12)); !w.Truncated || w.Steps != 0 {
		t.Fatalf("old window %+v", w)
	}
	if _, ok := m.Window("nope", at(0), at(1)); ok {
		t.Fatal("unknown resource window")
	}
	// State after the last Advance is the newest grid point.
	if st, ok := m.State("intake-quota", at(10000)); !ok || !st.At.Equal(at(212)) {
		t.Fatalf("state beyond last: %+v %v", st, ok)
	}
}

// The model reads only the clock's fault, factor and volume surface: an evaluation pass reads a
// bounded number of Evals per step, never per request.
func TestEvalReadsPerStep(t *testing.T) {
	m := mustCompile(t, cycleSet())
	clk := cycleClock()
	m.Advance(t0, clk)
	clk.evals = 0
	m.Advance(at(10), clk)
	// 2 dependencies x 2 modes + 6 single-mode resources, per step.
	if clk.evals != 10*10 {
		t.Fatalf("evals = %d, want %d", clk.evals, 10*10)
	}
	before := clk.evals
	for i := 0; i < 100; i++ {
		m.Apply("orders-db-pool", at(10), UnitDraw("x"))
	}
	if clk.evals != before {
		t.Fatal("Apply read the clock")
	}
}

// A window over a model that was never advanced has no history: it is empty and Truncated, as
// documented, not a clean empty window.
func TestWindowUnadvancedIsTruncated(t *testing.T) {
	m := mustCompile(t, set(Resource{Name: "intake-quota", Kind: KindQuota, LimitRPS: 80, Demand: constant(60)}))
	w, ok := m.Window("intake-quota", at(0), at(12))
	if !ok || w.Steps != 0 || !w.Truncated {
		t.Fatalf("un-advanced window %+v ok=%v, want empty and Truncated", w, ok)
	}
	// An empty or inverted interval stays a clean empty window.
	if w, ok := m.Window("intake-quota", at(12), at(12)); !ok || w.Truncated {
		t.Fatalf("empty interval %+v ok=%v", w, ok)
	}
}

// A warm start applies a live fault to the final stretch only. A live consumer_slowdown 0.5 on a
// queue fed 8 msg/s and served 10 msg/s used to integrate over the whole Horizon: 3 msg/s x 24 h
// = 259k messages on the first tick, oldest age 9 h. It builds at most the retained history's
// worth (15 minutes), and a scheduled window earlier in the Horizon still counts exactly.
func TestWarmStartLiveFaultFinalStretchOnly(t *testing.T) {
	q := Resource{Name: "jobs-queue", Kind: KindQueue, Size: 5, HoldMS: 500, Demand: constant(8)} // 10 msg/s
	live := func() *fakeClock {
		c := newClock()
		c.live = map[string]float64{ModeConsumerSlowdown + "\x00jobs-queue": 0.5}
		return c
	}
	maxLive := 3.0 * float64(liveSteps) * stepSec // deficit 3 msg/s over the live stretch

	m := mustCompile(t, set(q))
	m.Advance(t0, live())
	st, _ := m.State("jobs-queue", t0)
	if st.Backlog < maxLive-1 || st.Backlog > maxLive+1 {
		t.Errorf("live backlog %.0f, want %.0f (the final stretch only)", st.Backlog, maxLive)
	}
	if want := float64(liveSteps) * stepSec; st.OldestAgeSec > want+1 {
		t.Errorf("oldest age %.0f s exceeds the live stretch %.0f s", st.OldestAgeSec, want)
	}

	// A scheduled stall (service rate 0) from 3 h to 2 h before now still counts exactly under the
	// same live fault: 720 steps x 8 msg/s x 5 s = 28800 queued, drained at 2 msg/s (10 per step) over
	// the 1261 steps from 2 h before now up to the live stretch, then 3 msg/s of the live deficit for
	// 180 steps (2700).
	c := live().on(ModeConsumerSlowdown, "jobs-queue", t0.Add(-3*time.Hour), t0.Add(-2*time.Hour), 1)
	m = mustCompile(t, set(q))
	m.Advance(t0, c)
	st, _ = m.State("jobs-queue", t0)
	if want := 28800.0 - 1261*10 + maxLive; math.Abs(st.Backlog-want) > 1 {
		t.Errorf("backlog %.0f with a scheduled stall, want %.0f", st.Backlog, want)
	}
}

// A Clock that cannot separate scheduled windows from live state keeps the plain Eval for every
// step (live state included), as before.
func TestWarmStartWithoutScheduledClock(t *testing.T) {
	q := Resource{Name: "jobs-queue", Kind: KindQueue, Size: 5, HoldMS: 500, Demand: constant(8)}
	c := newClock()
	c.live = map[string]float64{ModeConsumerSlowdown + "\x00jobs-queue": 0.5}
	m := mustCompile(t, set(q))
	m.Advance(t0, struct{ Clock }{c}) // hides EvalScheduled
	st, _ := m.State("jobs-queue", t0)
	if want := 3.0 * stepSec * float64(horizonSteps+1); math.Abs(st.Backlog-want) > 1 {
		t.Errorf("backlog %.0f, want %.0f (live over the whole Horizon)", st.Backlog, want)
	}
}

// A catch-up after a gap longer than the retained history (liveSteps) but shorter than the Horizon
// splits like a warm start: a live fault says nothing about when it began, so it is applied to the
// final liveSteps only and the gap before that reads scheduled windows alone. A blueprint disabled
// for 12 h and re-enabled under a live consumer_slowdown must not invent a 12 h backlog.
func TestCatchUpGapLiveFaultFinalStretchOnly(t *testing.T) {
	q := Resource{Name: "jobs-queue", Kind: KindQueue, Size: 5, HoldMS: 500, Demand: constant(8)} // 10 msg/s
	live := func() *fakeClock {
		c := newClock()
		c.live = map[string]float64{ModeConsumerSlowdown + "\x00jobs-queue": 0.5}
		return c
	}
	maxLive := 3.0 * float64(liveSteps) * stepSec // deficit 3 msg/s over the live stretch

	m := mustCompile(t, set(q))
	m.Advance(t0, newClock())
	gap := t0.Add(12 * time.Hour)
	m.Advance(gap, live())
	st, _ := m.State("jobs-queue", gap)
	if st.Backlog < maxLive-1 || st.Backlog > maxLive+1 {
		t.Errorf("12 h catch-up live backlog %.0f, want %.0f (the final stretch only)", st.Backlog, maxLive)
	}
	if want := float64(liveSteps) * stepSec; st.OldestAgeSec > want+1 {
		t.Errorf("oldest age %.0f s exceeds the live stretch %.0f s", st.OldestAgeSec, want)
	}
	// The same state a warm start at that instant reaches.
	fresh := mustCompile(t, set(q))
	fresh.Advance(gap, live())
	if b, _ := fresh.State("jobs-queue", gap); !reflect.DeepEqual(st, b) {
		t.Errorf("catch-up state %+v, warm start %+v", st, b)
	}

	// A scheduled stall inside the gap (service rate 0 from 1 h to 2 h) counts exactly, under the
	// same live fault: 720 steps x 8 msg/s x 5 s = 28800 queued, drained at 2 msg/s (10 per step)
	// from the stall's end up to the live stretch, then 3 msg/s of the live deficit for 180 steps.
	stall := func() *fakeClock {
		return live().on(ModeConsumerSlowdown, "jobs-queue", t0.Add(time.Hour), t0.Add(2*time.Hour), 1)
	}
	now := t0.Add(3 * time.Hour)
	m = mustCompile(t, set(q))
	m.Advance(t0, newClock())
	m.Advance(now, stall())
	st, _ = m.State("jobs-queue", now)
	drain := float64(720-liveSteps) * 10 // steps from 2 h to 3 h, less the live stretch
	if want := 28800.0 - drain + maxLive; math.Abs(st.Backlog-want) > 20 {
		t.Errorf("backlog %.0f with a scheduled stall in the gap, want %.0f", st.Backlog, want)
	}
	fresh = mustCompile(t, set(q))
	fresh.Advance(now, stall())
	if b, _ := fresh.State("jobs-queue", now); !reflect.DeepEqual(st, b) {
		t.Errorf("catch-up state %+v, warm start %+v", st, b)
	}
}

// Continuous advance is unchanged: a live fault integrates over every step when each call
// covers at most liveSteps, including a call of exactly liveSteps.
func TestContinuousAdvanceIntegratesLiveFault(t *testing.T) {
	q := Resource{Name: "jobs-queue", Kind: KindQueue, Size: 5, HoldMS: 500, Demand: constant(8)} // 10 msg/s
	c := newClock()
	m := mustCompile(t, set(q))
	m.Advance(t0, c)
	c.live = map[string]float64{ModeConsumerSlowdown + "\x00jobs-queue": 0.5}
	now := t0
	for range 8 { // 8 calls of exactly liveSteps (15 min) each: 2 h of live fault
		now = now.Add(time.Duration(liveSteps) * Step)
		m.Advance(now, c)
	}
	st, _ := m.State("jobs-queue", now)
	if want := 3.0 * 2 * 3600; math.Abs(st.Backlog-want) > 1 {
		t.Errorf("backlog %.0f after 2 h of continuous live fault, want %.0f", st.Backlog, want)
	}
}
