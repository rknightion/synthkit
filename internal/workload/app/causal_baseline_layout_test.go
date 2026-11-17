// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// withEntryErrorRate declares the entry front's baseline error_rate in untouchedApp-shaped
// documents. The rates are test values: they size the baseline-failed share under test, not a
// modelled deployment.
func withEntryErrorRate(doc string, rate string) string {
	return strings.Replace(doc, "{name: front, entry: true,", "{name: front, entry: true, error_rate: "+rate+",", 1)
}

// SKT-0206: a request failed only by the entry's baseline draw is laid out on the engine's
// timeline like a successful one, so in a healthy window the spans of its services read the same
// durations as on successful requests (legacy nesting made them about x4 longer).
func TestCausalBaselineFailuresKeepHealthyLayout(t *testing.T) {
	// 20% baseline failures (a test value) gives several hundred baseline-failed requests a window.
	w := causalApps(t, untouchedResources, withEntryErrorRate(untouchedApp, "0.2"))[0]
	eng := shape.New("UTC", nil)
	var ok, failed []*ledger.Request
	for i := range 3 {
		for _, r := range mintAt(w, eng, causalNow.Add(time.Duration(i)*time.Minute)) {
			switch {
			case r.Outcome == ledger.OutcomeSuccess && r.Fault == nil:
				ok = append(ok, r)
			case baselineFailure(r):
				failed = append(failed, r)
			}
		}
	}
	if len(ok) < 1000 || len(failed) < 300 {
		t.Fatalf("too few requests: %d successful, %d baseline-failed", len(ok), len(failed))
	}
	spans := func(reqs []*ledger.Request) map[[3]string][]float64 {
		p := project(t, w, shape.New("UTC", nil), reqs)
		out := map[[3]string][]float64{}
		for id, sp := range p.spans {
			k := [3]string{p.owner[id], kindLabel(sp.Kind), sp.Name}
			out[k] = append(out[k], float64(sp.End.Sub(sp.Start))/float64(time.Millisecond))
		}
		return out
	}
	so, sf := spans(ok), spans(failed)
	for _, k := range [][3]string{
		{"auth", spanKindServer, "auth"},
		{"front", spanKindClient, "call auth"},
		{"auth", spanKindClient, "call auth-db"},
		{"editor", spanKindServer, "editor"},
		{"front", spanKindClient, "call editor"},
		{"editor", spanKindClient, "call editor-db"},
		{"gen", spanKindServer, "gen"},
		{"front", spanKindClient, "call gen"},
	} {
		if len(so[k]) == 0 || len(sf[k]) == 0 {
			t.Fatalf("span %v: %d on successful requests, %d on baseline-failed", k, len(so[k]), len(sf[k]))
		}
		r := meanOf(sf[k]) / meanOf(so[k])
		t.Logf("%v: baseline-failed / successful mean x%.3f", k, r)
		// Several hundred lognormal draws per side: the means agree within sampling noise.
		if math.Abs(r-1) > 0.08 {
			t.Errorf("%v: baseline-failed span mean x%.3f of the successful one, want the same layout (within 8%%)", k, r)
		}
	}
}

// rowFactorResources slows auth-dep, which auth's database call uses: the fault reaches the
// unbound service auth through its leaf, so auth's rows are scaled against a healthy reference
// of auth's own engine-laid-out duration.
const rowFactorResources = untouchedResources + `- {name: auth-dep, kind: dependency, latency_ms: 60, slow_factor: 4}
`

var rowFactorApp = strings.Replace(untouchedApp, "  - {name: auth-db, type: db}\n", "  - {name: auth-db, type: db, uses: [auth-dep]}\n", 1)

// SKT-0206: the healthy reference a faulted row is scaled by holds only engine-laid-out hops, so
// at a 1% and a 5% baseline error rate the factors of auth's faulted SERVER and CLIENT rows
// average the engine's real slowdown of auth (its faulted hops' mean over its healthy successful
// hops' mean). Baseline-failed requests on the legacy nesting biased the reference by about
// their share of the window.
func TestCausalFaultedRowFactorsMatchEngine(t *testing.T) {
	if rowFactorApp == untouchedApp {
		t.Fatal("the auth-dep binding did not apply")
	}
	for _, rate := range []string{"0.01", "0.05"} {
		t.Run("error_rate "+rate, func(t *testing.T) {
			w := causalApps(t, rowFactorResources, withEntryErrorRate(rowFactorApp, rate))[0]
			eng := incident(causal.ModeDependencySlow, "auth-dep", 1)
			led := ledger.New(eng, 0, 0)
			led.AddMinter(w.Minter())
			before := causalNow.Add(-90 * time.Minute) // the incident starts at 12:00
			warm := led.Mint(before)
			world := coretest.World(&coretest.MetricCapture{}, nil, nil)
			world.Shape, world.Ledger = eng, led
			if err := w.Tick(context.Background(), before, world); err != nil {
				t.Fatalf("warm-up Tick: %v", err)
			}
			faulted := led.Mint(causalNow)
			world = coretest.World(&coretest.MetricCapture{}, nil, nil)
			world.Shape, world.Ledger = eng, led
			if err := w.Tick(context.Background(), causalNow, world); err != nil {
				t.Fatalf("Tick: %v", err)
			}

			// The engine's real factor: auth's faulted hops against its healthy successful hops.
			hops := func(reqs []*ledger.Request, keep func(*ledger.Request) bool, d func(ledger.Call) float64) []float64 {
				var v []float64
				for _, r := range reqs {
					if !keep(r) {
						continue
					}
					for _, c := range r.Calls {
						if c.Target == "auth" {
							v = append(v, d(c))
						}
					}
				}
				return v
			}
			healthy := func(r *ledger.Request) bool { return r.Outcome == ledger.OutcomeSuccess && r.Fault == nil }
			all := func(*ledger.Request) bool { return true }
			client := func(c ledger.Call) float64 { return seconds(c.Duration) }
			cw := w.causalSpanWindow(causalNow, world)
			if cw == nil {
				t.Fatal("the faulted window is not gated")
			}
			auth, front := w.graph.byName["auth"], w.graph.entry
			for _, c := range []struct {
				row     string
				factors []float64
				dur     func(ledger.Call) float64
			}{
				{"auth SERVER", cw.serverFactors(auth, statusCodeUnset), serverHopSeconds},
				{"front CLIENT call auth", cw.clientFactors(front, "auth", ""), client},
			} {
				ok, warmAll, hurt := hops(warm, healthy, c.dur), hops(warm, all, c.dur), hops(faulted, all, c.dur)
				nf := len(hops(warm, baselineFailure, c.dur))
				if len(ok) < 1000 || len(hurt) < 1000 || nf == 0 || len(ok)+nf != len(warmAll) {
					t.Fatalf("%s: %d healthy successful, %d baseline-failed of %d warm auth hops, %d faulted; want both kinds and nothing else", c.row, len(ok), nf, len(warmAll), len(hurt))
				}
				engine := meanOf(hurt) / meanOf(ok)
				got := meanOf(c.factors)
				// Sampling noise: the reference is the mean of every healthy hop, which sits off the
				// successful hops' mean by the nf baseline-failed hops' own spread, a relative
				// standard error of sqrt(nf) x CV / n. Three standard errors bound it.
				var ss float64
				for _, x := range ok {
					ss += (x - meanOf(ok)) * (x - meanOf(ok))
				}
				cv := math.Sqrt(ss/float64(len(ok)-1)) / meanOf(ok)
				tol := 3 * math.Sqrt(float64(nf)) * cv / float64(len(warmAll))
				t.Logf("%s: %d baseline-failed of %d warm hops; row factor mean x%.4f, engine x%.4f (off by %.3f%%, bound %.3f%%)", c.row, nf, len(warmAll), got, engine, 100*(got/engine-1), 100*tol)
				if len(c.factors) < 1000 || engine < 1.5 {
					t.Fatalf("%s: %d factors, engine x%.3f; want the fault to reach auth", c.row, len(c.factors), engine)
				}
				if math.Abs(got/engine-1) > tol {
					t.Errorf("%s: row factor mean x%.4f, engine x%.4f; want them equal within sampling noise (%.3f%%)", c.row, got, engine, 100*tol)
				}
			}
		})
	}
}

// m4 (rv12): every hop that did not fail answers with the app's declared success status, on a
// healthy, a baseline-failed and a legacy request alike, as the entry does; a served hop's ledger
// 200 does not leak into the hop views the logs and outcomes values read.
func TestCausalHopStatusesFollowSuccessStatus(t *testing.T) {
	doc := strings.Replace(withEntryErrorRate(untouchedApp, "0.2"), "traffic: {", "traffic: {success_status: 201, ", 1)
	w := causalApps(t, untouchedResources, doc)[0]
	reqs := mintAt(w, shape.New("UTC", nil), causalNow)
	var ok, failed int
	for _, r := range reqs {
		switch {
		case r.Outcome == ledger.OutcomeSuccess && r.Fault == nil:
			ok++
		case baselineFailure(r):
			failed++
		default:
			continue
		}
		cp := *r
		cp.Causal = false
		views, legacy := w.hopViews(r), w.hopViews(&cp)
		for i, v := range views {
			if v.index < 0 {
				continue
			}
			if v.status != 201 {
				t.Fatalf("%s request hop %s: status %d, want the success status 201", r.Outcome, v.node, v.status)
			}
			if v.status != legacy[i].status || v.failed != legacy[i].failed || v.reached != legacy[i].reached {
				t.Fatalf("%s request hop %s: view %+v, legacy %+v", r.Outcome, v.node, v, legacy[i])
			}
		}
	}
	if ok == 0 || failed == 0 {
		t.Fatalf("%d healthy and %d baseline-failed requests; want both", ok, failed)
	}
}
