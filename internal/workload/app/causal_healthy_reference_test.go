// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// All timings are test-only: the root encloses sequential calls (5+40+30 ms),
// while middle's 40 ms hold already contains leaf's 20 ms dependency.
const healthyReferenceResources = `
- {name: root-slot, kind: concurrency, size: 10000, hold_ms: 50, demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: middle-slot, kind: concurrency, size: 10000, hold_ms: 40, held_across: dep, demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: dep, kind: dependency, latency_ms: 20, slow_factor: 12}
- {name: other-slot, kind: concurrency, size: 10000, hold_ms: 30, demand: {off_peak_rps: 5, peak_rps: 60}}
`
const healthyReferenceApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
- {name: root, entry: true, uses: [root-slot], calls: [middle, other], error_rate: 0}
- {name: middle, uses: [middle-slot], calls: [leaf], error_rate: 0}
- {name: leaf, uses: [dep], error_rate: 0}
- {name: other, uses: [other-slot], error_rate: 0}
`

// The explicit reference run is an independent oracle for the declared layout, not
// a fabricated healthy request. Both runs cross Build, ledger Mint, Project and Tick.
func TestCausalDeclaredHealthyReferenceFromFirstTick(t *testing.T) {
	refs := map[string]float64{
		serverRefKey("root"): .075, serverRefKey("middle"): .040,
		serverRefKey("leaf"): .020, serverRefKey("other"): .030,
		clientRefKey("root", "middle"): .040, clientRefKey("middle", "leaf"): .020,
		clientRefKey("root", "other"): .030,
	}
	type result struct {
		metrics []*coretest.MetricCapture
		after   float64
		learned map[string]float64
	}
	run := func(explicit bool) result {
		w := causalApps(t, healthyReferenceResources, healthyReferenceApp)[0]
		if explicit {
			w.healthySpan = map[string]float64{}
			for k, v := range refs {
				w.healthySpan[k] = v
			}
		}
		eng := incident(causal.ModeDependencySlow, "dep", 1)
		led := ledger.New(eng, 0, 0)
		led.AddMinter(w.Minter())
		var out result
		// Fault present from startup, then a healthy window replaces the fallback.
		for i, now := range []time.Time{causalNow, causalNow.Add(2 * time.Hour)} {
			reqs := led.Mint(now)
			if len(reqs) == 0 {
				t.Fatal("empty public Mint")
			}
			before := make([]ledger.Request, len(reqs))
			for j, r := range reqs {
				before[j] = *r
				before[j].Calls = append([]ledger.Call(nil), r.Calls...)
			}
			project(t, w, eng, reqs)
			mc := &coretest.MetricCapture{}
			world := coretest.World(mc, nil, nil)
			world.Shape, world.Ledger = eng, led
			if err := w.Tick(context.Background(), now, world); err != nil {
				t.Fatal(err)
			}
			for j, r := range reqs {
				if !reflect.DeepEqual(before[j], *r) {
					t.Fatal("projection/metrics fabricated ledger observations")
				}
			}
			if i == 0 && !explicit && w.healthySpan[serverRefKey("root")] > 0 {
				t.Fatal("faulted root learned as healthy")
			}
			out.metrics = append(out.metrics, mc)
		}
		out.after = eng.Float64()
		out.learned = w.healthySpan
		// Learned observations must replace, not merely seed the declared fallback.
		key := serverRefKey("root")
		if out.learned[key] <= 0 || out.learned[key] == refs[key] {
			t.Fatal("healthy window did not replace the declared reference")
		}
		cw := &causalSpanWindow{w: w}
		if got := cw.scaled(key, 2*out.learned[key], true); got != 2 {
			t.Fatalf("learned reference factor %v, want 2", got)
		}
		return out
	}
	a, b := run(false), run(true)
	for i := range a.metrics {
		rows := func(mc *coretest.MetricCapture) []string {
			var out []string
			for _, s := range mc.All() {
				out = append(out, fmt.Sprintf("%s%s=%g", s.Name, fmtLabels(s.Labels), s.Value))
			}
			sort.Strings(out)
			return out
		}
		ar, br := rows(a.metrics[i]), rows(b.metrics[i])
		if !reflect.DeepEqual(ar, br) {
			for j := range min(len(ar), len(br)) {
				if ar[j] != br[j] {
					t.Logf("first diff: %s vs %s", ar[j], br[j])
					break
				}
			}
			t.Fatalf("tick %d metrics differ from declared enclosing/held reference oracle", i)
		}
	}
	if a.after != b.after {
		t.Fatal("healthy reference consumed engine draws")
	}
}

// The cold-start fallback must not disturb the sibling's legacy rows or draws.
func TestCausalDeclaredHealthyReferenceKeepsUntouchedRows(t *testing.T) {
	faulted := faultRowsRun(t, incident(causal.ModeDependencySlow, "slow-dep", 1), false)
	healthy := faultRowsRun(t, shape.New("UTC", nil), false)
	steady := func(l map[string]string) bool {
		return l["service_name"] == "steady-svc" || l["span_name"] == "call steady-svc" || l["server"] == "steady-svc"
	}
	a, b := rowSeries(faulted.mc, steady), rowSeries(healthy.mc, steady)
	if len(a) == 0 || !reflect.DeepEqual(a, b) {
		t.Fatal("cold fault changed untouched sibling rows")
	}
	if faulted.next != healthy.next {
		t.Fatal("cold fault shifted later engine draws")
	}
}
