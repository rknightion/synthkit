// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"math"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// faultRowsResources declares one dependency the slowed service waits on. latency_ms is the
// service's bound base duration; slow_factor 4 makes dependency_slow at intensity 1 stretch it 4x.
const faultRowsResources = `
- {name: slow-dep, kind: dependency, latency_ms: 200, slow_factor: 4}
`

// faultRowsApp: the entry calls a service bound to slow-dep and a sibling the fault never reaches.
const faultRowsApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [slow-svc, steady-svc]
  - name: slow-svc
    uses: [slow-dep]
  - name: steady-svc
`

// faultRun is one run's cumulative series after the warm-up tick (warm; empty without one) and
// after the tick under test (mc), the engine's next draw and the requests minted under test.
type faultRun struct {
	warm, mc *coretest.MetricCapture
	next     float64
	reqs     []*ledger.Request
}

// faultRowsRun mints and ticks one healthy warm-up window (before the fault) and then the window
// under test.
func faultRowsRun(t *testing.T, eng *shape.Engine, warm bool) faultRun {
	t.Helper()
	w := causalApps(t, faultRowsResources, faultRowsApp)[0]
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	run := faultRun{warm: &coretest.MetricCapture{}}
	if warm {
		before := causalNow.Add(-90 * time.Minute) // the incident starts at 12:00
		led.Mint(before)
		world := coretest.World(run.warm, nil, nil)
		world.Shape, world.Ledger = eng, led
		if err := w.Tick(context.Background(), before, world); err != nil {
			t.Fatalf("warm-up Tick: %v", err)
		}
	}
	reqs := led.Mint(causalNow)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	run.mc, run.next, run.reqs = mc, eng.Float64(), reqs
	return run
}

// rowSeries renders every span-metric and service-graph series of the rows keep selects.
func rowSeries(mc *coretest.MetricCapture, keep func(map[string]string) bool) []string {
	var out []string
	for _, s := range mc.All() {
		if len(s.Name) < 7 || s.Name[:7] != "traces_" || !keep(s.Labels) {
			continue
		}
		out = append(out, s.Name+fmtLabels(s.Labels)+"="+strconv.FormatFloat(s.Value, 'g', -1, 64))
	}
	sort.Strings(out)
	return out
}

// rowMeanLatency is the mean traces_spanmetrics_latency the tick under test observed on one
// service's rows of one span kind (the cumulative series less the warm-up tick's).
func rowMeanLatency(run faultRun, service, kind, spanName string) float64 {
	totals := func(mc *coretest.MetricCapture) (sum, n float64) {
		for _, s := range mc.All() {
			l := s.Labels
			if l["service_name"] != service || l["span_kind"] != kind || (spanName != "" && l["span_name"] != spanName) {
				continue
			}
			switch s.Name {
			case "traces_spanmetrics_latency_sum":
				sum += s.Value
			case "traces_spanmetrics_latency_count":
				n += s.Value
			}
		}
		return sum, n
	}
	s1, n1 := totals(run.mc)
	s0, n0 := totals(run.warm)
	return (s1 - s0) / math.Max(n1-n0, 1)
}

// SKT-0168.09.35: in a faulted window only the rows whose hops carry the fault leave the legacy
// latency path. The sibling service's rows (its SERVER row, the entry's CLIENT row to it and the
// service-graph edge) are byte-identical to the same window without the fault; the slowed
// service's latency rises by the declared factor over its legacy baseline; and the engine's later
// draws do not shift.
func TestCausalFaultedWindowKeepsUntouchedRowsLegacy(t *testing.T) {
	const intensity, factor = 1.0, 4.0 // dependency_slow: 1 + (slow_factor - 1) x intensity
	faulted := faultRowsRun(t, incident(causal.ModeDependencySlow, "slow-dep", intensity), true)
	healthy := faultRowsRun(t, shape.New("UTC", nil), true)

	slowed := 0
	for _, r := range faulted.reqs {
		if causalFailing(r) {
			slowed++
		}
	}
	if slowed == 0 {
		t.Fatal("the fault slowed no request: the window under test is not gated")
	}
	if faulted.next != healthy.next {
		t.Fatalf("the faulted window shifted the engine's later draws: %v vs %v", faulted.next, healthy.next)
	}

	steady := func(l map[string]string) bool {
		return l["service_name"] == "steady-svc" || l["span_name"] == "call steady-svc" || l["server"] == "steady-svc"
	}
	a, b := rowSeries(faulted.mc, steady), rowSeries(healthy.mc, steady)
	if len(a) == 0 {
		t.Fatal("no steady-svc rows emitted")
	}
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			t.Fatalf("an untouched row differs in the faulted window:\n faulted %s\n healthy %s", a[i], b[i])
		}
	}
	if len(a) != len(b) {
		t.Fatalf("untouched rows: %d series faulted vs %d healthy", len(a), len(b))
	}

	base := rowMeanLatency(healthy, "slow-svc", spanKindServer, "")
	got := rowMeanLatency(faulted, "slow-svc", spanKindServer, "")
	if base <= 0 || base > 0.2 {
		t.Fatalf("slow-svc healthy baseline %.3f s, want the legacy 20 to 200 ms draw", base)
	}
	if ratio := got / base; math.Abs(ratio-factor) > 0.25*factor {
		t.Fatalf("slow-svc SERVER latency %.3f s over a %.3f s legacy baseline: x%.2f, want about the declared x%.0f", got, base, ratio, factor)
	}
	t.Logf("slow-svc SERVER mean: healthy %.3f s, faulted %.3f s (x%.2f, declared x%.0f)", base, got, got/base, factor)
	for _, row := range [][3]string{{"front", spanKindClient, "call slow-svc"}, {"front", spanKindServer, ""}} {
		h, f := rowMeanLatency(healthy, row[0], row[1], row[2]), rowMeanLatency(faulted, row[0], row[1], row[2])
		if f <= h {
			t.Fatalf("%v carries the fault but its latency did not rise: %.3f -> %.3f s", row, h, f)
		}
		t.Logf("%v mean: healthy %.3f s, faulted %.3f s (x%.2f)", row, h, f, f/h)
	}
}

// With no healthy window observed yet (a fault active from the first tick), a faulted row has no
// healthy reference to scale against and keeps the legacy draw rather than jumping to the real
// span durations' scale.
func TestCausalFaultedWindowWithoutReferenceStaysLegacy(t *testing.T) {
	faulted := faultRowsRun(t, incident(causal.ModeDependencySlow, "slow-dep", 1), false)
	healthy := faultRowsRun(t, shape.New("UTC", nil), false)
	all := func(map[string]string) bool { return true }
	a, b := rowSeries(faulted.mc, all), rowSeries(healthy.mc, all)
	if len(a) != len(b) {
		t.Fatalf("%d series faulted vs %d healthy", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("a cold faulted window left the legacy draw:\n faulted %s\n healthy %s", a[i], b[i])
		}
	}
}
