// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"testing"
	"time"
)

func waitPool(name string) *compiled {
	c := &compiled{r: Resource{Name: name, Kind: KindPool, AcquireTimeoutMS: 1000}, last: rec{valid: true, st: State{WaitMS: 1000}}}
	c.key = c.failKey()
	return c
}

// Independent analytic references for the admitted law and ordered acquisitions.
// W=min(1000*(.5+U),1000): half the mass is an atom at 1000 ms.
func TestFlowAcquisitionWaitTiming(t *testing.T) {
	p, q := waitPool("p"), waitPool("q")
	rule := &compiled{r: Resource{Name: "rule", Kind: KindRule}}
	rule.key = rule.failKey()
	for _, tc := range []struct {
		name                 string
		uses                 []*compiled
		mean, deadline, want float64
		reject               *compiled
	}{
		// E[min(X,250)] differs negligibly from E[X]=1 for this lognormal.
		{"admitted own work", []*compiled{p}, 1, 750, .751, nil},
		{"earlier wait before rejection", []*compiled{p, q}, 0, 1750, .75, q},
		// Two continuous uniforms can fit below 1500 only on a triangle of area .125.
		{"two admitted waits before rule", []*compiled{p, q, rule}, 0, 1500, .875, rule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cf := &cflow{nodes: []flowNode{{uses: tc.uses, mean: tc.mean, storm: 1, covered: -1, policy: CallPolicy{TimeoutMS: tc.deadline}}}}
			rej := func(c *compiled) float64 {
				if c == tc.reject {
					return 1
				}
				return 0
			}
			for _, bins := range []int{elapsedBins, 512} {
				got := 0.0
				for _, e := range cf.elapsedServe(0, rej, bins, false) {
					if e.key.class == classTimeout {
						got += e.p
					}
				}
				t.Logf("bins %d timeout %.12f reference %.12f error %.12g", bins, got, tc.want, math.Abs(got-tc.want))
				if math.Abs(got-tc.want) > 1e-5 {
					t.Errorf("timeout %v want %v within 1e-5", got, tc.want)
				}
			}
		})
	}
}

// Novel wait/storm/reject vectors on every operation prevent steady-input caches
// from disguising the sustained cost. One operation is ONE gate evaluation, not a
// full solver step (which may evaluate many gates/iterates). No latency SLA is implied.
func BenchmarkFlowWaitDynamic(b *testing.B) {
	for _, legacy := range []bool{false, true} {
		name := "resource leaf"
		if legacy {
			name = "legacy siblings"
		}
		b.Run(name, func(b *testing.B) {
			p := waitPool("p")
			p.r.HoldMS = 50
			f := &Flow{Nodes: []FlowNode{
				{Parent: -1},
				{Parent: 0, Uses: []string{"p"}, Duration: "p", Policy: CallPolicy{TimeoutMS: 1000, Retries: 2, RetryOn: []string{RetryTimeout}}},
			}}
			if legacy {
				f.Legacy = &LegacyTiming{P95MS: 8000}
				f.Nodes = append(f.Nodes, FlowNode{Parent: 1}, FlowNode{Parent: 1})
			}
			cf := compileFlow(f, map[string]*compiled{"p": p})
			cf.prepare(newClock(), t0)
			g := cf.gateFor(f, 1, "p")
			r := 0.0
			rej := func(*compiled) float64 { return r }
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p.last.st.WaitMS = 100 + float64(i)/1000
				cf.nodes[1].storm = 1 + float64(i)/10000
				r = float64(i%100) / 1000
				if got := g.arrivals(rej); got < 1 || got > 3 {
					b.Fatalf("invalid arrivals %v", got)
				}
			}
		})
	}
}

func TestAcquisitionWaitDrawAndCDF(t *testing.T) {
	m := mustCompile(t, set(Resource{Name: "pool", Kind: KindPool, Size: 1, HoldMS: 1, AcquireTimeoutMS: 1000, Demand: constant(2000)}))
	m.Advance(t0, newClock())
	for _, tc := range []struct {
		u    float64
		want time.Duration
	}{{0, 500 * time.Millisecond}, {.25, 750 * time.Millisecond}, {.5, time.Second}, {.9, time.Second}} {
		calls := 0
		e := m.Apply("pool", t0, func(purpose string) float64 {
			calls++
			if purpose == "fail" {
				return .99
			}
			return tc.u
		})
		if e.Fail || e.Wait != tc.want || calls != 2 {
			t.Fatalf("u %v got %+v (%d draws), want %v and 2 draws", tc.u, e, calls, tc.want)
		}
	}
	w := acquisitionWait{1000, 1000}
	for _, tc := range []struct{ ms, want float64 }{{499, 0}, {750, .2500000005}, {999.999999, .4999999995}, {1000, 1}} {
		if got := w.cdf(tc.ms); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("cdf(%v)=%v want %v", tc.ms, got, tc.want)
		}
	}
}
