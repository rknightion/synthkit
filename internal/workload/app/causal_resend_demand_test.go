// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// resendResources and resendApp are a test-only request path built to make every resend the app
// makes visible at a capacity resource: front resends mid's 5xx twice, mid meets its pool and
// calls fn under a 1500 ms timeout resent once, fn meets its concurrency limit (held across a
// dependency a slowdown stretches past the timeout) and fails on its own at a declared 20%
// error_rate. The rates and sizes are test values chosen so each effect clears the sampling
// tolerance, not a modelled deployment.
const resendResources = `
- {name: mid-pool, kind: pool, size: 1000, hold_ms: 50, held_across: mid-db, acquire_timeout_ms: 3000,
   demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: mid-db, kind: dependency, latency_ms: 10}
- {name: fn-conc, kind: concurrency, size: 1000, hold_ms: 400, held_across: slow-dep,
   demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: slow-dep, kind: dependency, latency_ms: 300, slow_factor: 5}
`

const resendApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [mid]
    call_policies:
      mid: {retries: 2, retry_on: [5xx]}
  - name: mid
    uses: [mid-pool]
    calls: [fn]
    call_policies:
      fn: {timeout_ms: 1500, retries: 1, retry_on: [timeout]}
  - {name: fn, uses: [fn-conc], error_rate: 0.2}
`

// The model's arrivals equal what the app's attempts deliver for every resend the app makes
// (SKT-0205): a legacy hop error draw (fn's declared error_rate, healthy, and an error_spike on
// fn) fails mid, whose 502 front resends with mid's whole subtree; a timeout fault (the slowed
// dependency stretches fn's hold past mid's 1500 ms timeout) is resent by mid and, once spent,
// reaches front as a 504 that it resends too; and a throttling limit's 429 reaches front as mid's
// 503. Each holds at the fault's onset, where the step's own reject shares must be read (the
// pool is solved before the limit it reads), and in steady state.
func TestCausalResendsCountAsDemand(t *testing.T) {
	onset := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) // incident's start
	for _, tc := range []struct {
		name string
		eng  func() *shape.Engine
	}{
		{"hop error rate", func() *shape.Engine { return shape.New("UTC", nil) }},
		{"hop error spike", func() *shape.Engine { return incident("error_spike", "fn", 0.5) }},
		{"timeout", func() *shape.Engine { return incident(causal.ModeDependencySlow, "slow-dep", 1) }},
		{"throttle", func() *shape.Engine { return incident(causal.ModeConcurrencyReduced, "fn-conc", 0.99) }},
	} {
		for _, at := range []struct {
			name string
			now  time.Time
			step bool // compare the onset step's rate, not a minute
		}{{"onset", onset, true}, {"steady", causalNow, false}} {
			t.Run(tc.name+"/"+at.name, func(t *testing.T) {
				w := causalApps(t, resendResources, resendApp)[0]
				reqs := mintAt(w, tc.eng(), at.now)
				got := map[string]float64{}
				for _, r := range reqs {
					got["mid-pool"] += float64(len(callsTo(r, "mid")))
					got["fn-conc"] += float64(len(callsTo(r, "fn")))
				}
				model := w.m.causal.model
				for _, name := range []string{"mid-pool", "fn-conc"} {
					win, _ := model.Window(name, at.now.Add(-time.Minute), at.now)
					want := win.Arrivals
					if at.step {
						// Every request minted at the onset reads the onset step's state, so its
						// 60 s of requests are 12 steps at that step's rate.
						w5, _ := model.Window(name, at.now.Add(-causal.Step), at.now)
						want = w5.Arrivals * float64(time.Minute/causal.Step)
					}
					tol := 3*math.Sqrt(math.Max(want, 1)) + 0.01*want
					if math.Abs(want-got[name]) > tol {
						t.Errorf("%s: model arrivals %.0f, app delivered %.0f (tolerance %.0f)", name, want, got[name], tol)
					}
				}
			})
		}
	}
}

// A timeout on mid measures its sequential child calls, including spent retries and backoff,
// not just mid's short own work. The covered case also exercises a hold supplied by a real call.
func TestCausalParentTimeoutCountsChildElapsed(t *testing.T) {
	for _, tc := range []struct{ name, mid, children, resources string }{
		{"slow child", "uses: [mid-pool], calls: [fn]", "- {name: fn, uses: [slow-dep]}", ""},
		{"sequential children", "uses: [mid-pool], calls: [fn, other]", "- {name: fn, uses: [slow-dep]}\n  - {name: other, uses: [slow-dep]}", ""},
		{"spent child retries", "uses: [mid-pool], calls: [fn], call_policies: {fn: {timeout_ms: 300, retries: 1, backoff_ms: 900, retry_on: [timeout]}}", "- {name: fn, uses: [slow-dep]}", ""},
		{"covered hold", "uses: [mid-pool], calls: [fn]", "- {name: fn, uses: [mid-db]}", "covered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := resendResources
			if tc.resources == "covered" {
				resources = strings.ReplaceAll(resources, "hold_ms: 50", "hold_ms: 800")
				resources = strings.ReplaceAll(resources, "latency_ms: 10", "latency_ms: 600")
			}
			app := `traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [mid]
    call_policies: {mid: {timeout_ms: 700, retries: 2, retry_on: [timeout]}}
  - {name: mid, ` + tc.mid + `}
  ` + tc.children
			for _, at := range []struct {
				name  string
				now   time.Time
				onset bool
			}{
				{"onset", time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC), true},
				{"steady", causalNow, false},
			} {
				t.Run(at.name, func(t *testing.T) {
					w := causalApps(t, resources, app)[0]
					reqs := mintAt(w, incident(causal.ModeDependencySlow, "slow-dep", 0.5), at.now)
					var sum, sumSq float64
					for _, r := range reqs {
						x := float64(len(callsTo(r, "mid")))
						sum += x
						sumSq += x * x
					}
					win, _ := w.m.causal.model.Window("mid-pool", at.now.Add(-time.Minute), at.now)
					want := win.Arrivals
					if at.onset {
						step, _ := w.m.causal.model.Window("mid-pool", at.now.Add(-causal.Step), at.now)
						want = step.Arrivals * float64(time.Minute/causal.Step)
					}
					// Four standard errors of the observed bounded per-request attempt count. No
					// extra percentage allowance for distribution error: report the actual bias.
					se := math.Sqrt(math.Max(0, sumSq-float64(sum*sum)/float64(len(reqs))))
					t.Logf("model %.2f app %.0f bias %.2f sampling SE %.2f", want, sum, want-sum, se)
					if math.Abs(want-sum) > 4*se {
						t.Errorf("parent attempts model %.2f app %.0f, difference exceeds 4 sampling SE (%.2f)", want, sum, se)
					}
				})
			}
		})
	}
}

// A service without durRes still does the legacy work drawn by the public minter. Its
// baseline duration (and the shared request budget behind sibling durations) persists across
// retries; a resend jitters that same baseline, not an independent new duration draw.
func TestCausalLegacyTimeoutResendsCountAsDemand(t *testing.T) {
	for _, tc := range []struct{ name, app, resource, target string }{
		{"leaf", `traffic: {off_peak_rps: 5, peak_rps: 60, request_latency_p95_ms: 3000}
services:
  - name: front
    entry: true
    calls: [legacy]
    call_policies: {legacy: {timeout_ms: 500, retries: 2, retry_on: [timeout]}}
  - {name: legacy, uses: [legacy-quota]}
`, "legacy-quota", "legacy"},
		{"shared sibling budget", `traffic: {off_peak_rps: 5, peak_rps: 60, request_latency_p95_ms: 8000}
services:
  - name: front
    entry: true
    calls: [mid]
    call_policies: {mid: {timeout_ms: 1000, retries: 2, retry_on: [timeout]}}
  - {name: mid, uses: [mid-pool], calls: [one, two]}
  - {name: one}
  - {name: two}
`, "mid-pool", "mid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Large limits isolate timing from admission. Latency/counts here are test-only
			// discriminators for tight client timeouts, never shipped blueprint tuning.
			resources := resendResources + `
- {name: legacy-quota, kind: quota, limit_rps: 100000, demand: {off_peak_rps: 5, peak_rps: 60}}
`
			w := causalApps(t, resources, tc.app)[0]
			reqs := mintAt(w, shape.New("UTC", nil), causalNow)
			var sum, sumSq float64
			for _, r := range reqs {
				x := float64(len(callsTo(r, tc.target)))
				sum += x
				sumSq += x * x
			}
			win, _ := w.m.causal.model.Window(tc.resource, causalNow.Add(-time.Minute), causalNow)
			se := math.Sqrt(math.Max(0, sumSq-float64(sum*sum)/float64(len(reqs))))
			t.Logf("model %.2f app %.0f bias %.2f sampling SE %.2f", win.Arrivals, sum, win.Arrivals-sum, se)
			if math.Abs(win.Arrivals-sum) > 4*se {
				t.Errorf("legacy attempts model %.2f app %.0f, difference exceeds 4 sampling SE (%.2f)", win.Arrivals, sum, se)
			}
		})
	}
}

// Saturation gives admitted acquisitions a clipped uniform wait, not the state's scalar
// WaitMS. Test-only 1000 rps against a one-slot/1 ms pool puts a 750 ms caller deadline
// inside that distribution. The old scalar model resent every request instead of ~87.5%.
func TestCausalPoolWaitTimeoutResendsCountAsDemand(t *testing.T) {
	// Freeze only the process-edge source of live correlation IDs, using the
	// existing parity harness reader. Never normalize away timing or retry facts.
	previousReader := cryptorand.Reader
	cryptorand.Reader = newParityRandomReader("pool-wait-timeout")
	defer func() { cryptorand.Reader = previousReader }()
	w := causalApps(t, `
- {name: wait-pool, kind: pool, size: 1, hold_ms: 1, acquire_timeout_ms: 1000,
   demand: {off_peak_rps: 1000, peak_rps: 1000}}
`, `traffic: {off_peak_rps: 1000, peak_rps: 1000}
services:
  - name: front
    entry: true
    calls: [leaf]
    call_policies: {leaf: {timeout_ms: 750, retries: 1, retry_on: [timeout]}}
  - {name: leaf, uses: [wait-pool]}
`)[0]
	reqs := mintAt(w, shape.New("UTC", nil), causalNow)
	var sum, sumSq float64
	for _, r := range reqs {
		x := float64(len(callsTo(r, "leaf")))
		sum += x
		sumSq += float64(x * x)
	}
	// The cross-architecture proof compares these actual public mint facts, including
	// integer-ns client/server durations, offsets, attempt numbers and outcomes.
	facts, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("WAIT_FACTS_SHA256=%x", sha256.Sum256(facts))
	st, _ := w.m.causal.model.State("wait-pool", causalNow)
	if st.WaitMS < 750 || st.RejectShare <= 0 {
		t.Fatalf("fixture did not exercise saturated waits: %+v", st)
	}
	win, _ := w.m.causal.model.Window("wait-pool", causalNow.Add(-time.Minute), causalNow)
	se := math.Sqrt(math.Max(0, sumSq-float64(sum*sum)/float64(len(reqs))))
	t.Logf("model %.8f app %.0f bias %.8f sampling SE %.8f wait %.8f reject %.8f", win.Arrivals, sum, win.Arrivals-sum, se, st.WaitMS, st.RejectShare)
	if math.Abs(win.Arrivals-sum) > 4*se {
		t.Errorf("pool-wait attempts model %.2f app %.0f, difference exceeds 4 sampling SE (%.2f)", win.Arrivals, sum, se)
	}
}

// fnAttempts is the share of requests with more than one call to fn (a resent attempt).
func fnAttempts(reqs []*ledger.Request) float64 {
	n := 0
	for _, r := range reqs {
		if len(callsTo(r, "fn")) > 1 {
			n++
		}
	}
	return float64(n) / math.Max(1, float64(len(reqs)))
}

// The app does resend in every case above (a guard against a vacuous comparison).
func TestCausalResendFixtureResends(t *testing.T) {
	w := causalApps(t, resendResources, resendApp)[0]
	if share := fnAttempts(mintAt(w, incident(causal.ModeDependencySlow, "slow-dep", 1), causalNow)); share < 0.2 {
		t.Fatalf("slowed: %v of requests call fn more than once, want a large share", share)
	}
	w = causalApps(t, resendResources, resendApp)[0]
	if share := fnAttempts(mintAt(w, shape.New("UTC", nil), causalNow)); share < 0.1 {
		t.Fatalf("healthy: %v of requests call fn more than once (front resends mid's 502), want about 0.2", share)
	}
}
