// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"testing"
	"time"
)

// certSet is the certificate story's concurrency resource: 900 ms executions held across a 120 ms
// partner API whose server certificate can expire, at a constant 10 rps against 20 slots.
func certSet() Set {
	return set(
		Resource{Name: "partner-api", Kind: KindDependency, LatencyMS: 120},
		Resource{Name: "partner-api-cert", Kind: KindCredential, Of: "partner-api", CredentialType: CredentialServerCertificate},
		Resource{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 20, HoldMS: 900, HeldAcross: "partner-api", Demand: constant(10)},
		Resource{Name: "partner-quota", Kind: KindQuota, LimitRPS: 50, Demand: constant(10)},
	)
}

// Occupancy splits a concurrency resource's served work into completions at the completing hold
// and failures at the failed hold (SKT-0202): a failed handshake ends after PreWorkFraction of the
// 900 ms hold and of the 120 ms partner latency, 102 ms, and the two populations' rate x hold is
// the step's InUse (Little's law). Healthy, the split is all completions at the hold.
func TestOccupancySplit(t *testing.T) {
	far := t0.Add(time.Hour)
	for _, tc := range []struct {
		name                        string
		clk                         *fakeClock
		completed, failed, failHold float64
	}{
		{"healthy", newClock(), 10, 0, 900},
		{"every handshake fails", newClock().on(ModeCredentialExpired, "partner-api-cert", t0, far, 1), 0, 10, 102},
		{"half the handshakes fail", newClock().on(ModeCredentialExpired, "partner-api-cert", t0, far, 0.5), 5, 5, 102},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mustCompile(t, certSet())
			m.Advance(t0, tc.clk)
			o, ok := m.Occupancy("enrich-fn-concurrency", t0)
			if !ok {
				t.Fatal("no occupancy for the concurrency resource")
			}
			st, _ := m.State("enrich-fn-concurrency", t0)
			if !o.At.Equal(st.At) || !near(o.CompletedRPS, tc.completed) || !near(o.FailedRPS, tc.failed) ||
				o.HoldMS != 900 || !near(o.FailedHoldMS, tc.failHold) {
				t.Fatalf("occupancy %+v, want %v completed at 900 ms and %v failed at %v ms", o, tc.completed, tc.failed, tc.failHold)
			}
			if !near((o.CompletedRPS*o.HoldMS+o.FailedRPS*o.FailedHoldMS)/1000, st.InUse) {
				t.Fatalf("rate x hold %v != InUse %v", (o.CompletedRPS*o.HoldMS+o.FailedRPS*o.FailedHoldMS)/1000, st.InUse)
			}
			if tc.failed == 0 && (o.CompletedRPS != st.ServedRPS || o.FailedHoldMS != o.HoldMS) {
				t.Fatalf("healthy split %+v is not exactly the served rate at the hold (state %+v)", o, st)
			}
		})
	}
	m := mustCompile(t, certSet())
	m.Advance(t0, newClock())
	for _, name := range []string{"partner-api", "partner-api-cert", "partner-quota", "nope"} {
		if o, ok := m.Occupancy(name, t0); ok {
			t.Fatalf("%s holds no work but reports occupancy %+v", name, o)
		}
	}
	if o, ok := m.Occupancy("enrich-fn-concurrency", t0.Add(-time.Hour)); ok {
		t.Fatalf("a step outside the history reports occupancy %+v", o)
	}
}
