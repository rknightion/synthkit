// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
)

const drawDecision = `{key: decision, enum: [{value: APPROVED, weight: 7}, {value: REJECTED, weight: 3}]}`

// gatedBundleDoc is a three-line bundle: every request logs "received", then exactly one of the
// "applied" (APPROVED) or "rejected" (REJECTED) lines its drawn decision selects. gate is the
// when clause of the two outcome lines ("" declares none).
func gatedBundleDoc(gateApplied, gateRejected string) string {
	line := func(source, msg, gate string) string {
		s := `
      - source: ` + source + `
        body:
          msg: {const_str: ` + msg + `}
          order_ref: ` + drawOrderRef + `
          decision: {request_draw: ` + drawDecision + `}
          pick: {enum: [{value: a, weight: 1}, {value: b, weight: 1}, {value: c, weight: 1}, {value: d, weight: 1}, {value: e, weight: 1}, {value: f, weight: 1}, {value: g, weight: 1}, {value: h, weight: 1}]}
          trace_id: {ref: trace_id}`
		if gate != "" {
			s += `
        when: ` + gate
		}
		return s
	}
	return `
traffic: {off_peak_rps: 100, peak_rps: 100}
services:
  - name: api
    type: web
    entry: true
    logs:` + line("app.received", "received", "") + line("app.applied", "applied", gateApplied) + line("app.rejected", "rejected", gateRejected) + "\n"
}

var (
	gateApproved = `{request_draw: ` + drawDecision + `, in: [APPROVED]}`
	gateRejected = `{request_draw: ` + drawDecision + `, in: [REJECTED]}`
)

// TestLogWhenGatesBundleLines: a bundle whose outcome lines are gated by the drawn decision writes
// exactly the received line plus the drawn outcome's line for each request, every line sharing the
// request's one order reference; the decision mix follows its 7:3 weights.
func TestLogWhenGatesBundleLines(t *testing.T) {
	order, byReq, _ := projectBundles(t, gatedBundleDoc(gateApproved, gateRejected), drawNow)
	decisions := map[string]int{}
	ids := map[string]bool{}
	for _, trace := range order {
		lines := byReq[trace]
		if len(lines) != 2 {
			t.Fatalf("request %s wrote %d lines, want received plus one outcome line: %v", trace, len(lines), lines)
		}
		if lines[0].source != "app.received" && lines[1].source != "app.received" {
			t.Fatalf("request %s has no received line: %v", trace, lines)
		}
		decision := lines[0].body["decision"]
		wantOutcome := map[string]string{"APPROVED": "app.applied", "REJECTED": "app.rejected"}[decision]
		if wantOutcome == "" {
			t.Fatalf("request %s drew decision %q", trace, decision)
		}
		var outcome string
		for _, l := range lines {
			if l.source != "app.received" {
				outcome = l.source
			}
			if l.body["order_ref"] != lines[0].body["order_ref"] || l.body["decision"] != decision {
				t.Fatalf("request %s lines disagree: %v", trace, lines)
			}
		}
		if outcome != wantOutcome {
			t.Fatalf("request %s drew %s but wrote %s", trace, decision, outcome)
		}
		if ids[lines[0].body["order_ref"]] {
			t.Fatalf("order_ref %q repeats across requests", lines[0].body["order_ref"])
		}
		ids[lines[0].body["order_ref"]] = true
		decisions[decision]++
	}
	if got := float64(decisions["APPROVED"]) / float64(len(order)); math.Abs(got-0.7) > 0.03 {
		t.Errorf("APPROVED share = %.3f, want 0.70 (%v)", got, decisions)
	}
}

// TestLogWhenGatedLineShiftsNoDraws: a gated-out line still evaluates its body, so the lines a
// request does write carry exactly the values the ungated bundle gives them.
func TestLogWhenGatedLineShiftsNoDraws(t *testing.T) {
	// Trace IDs are fresh per run, so the two runs pair requests by mint order (same seed, clock).
	gatedOrder, gated, _ := projectBundles(t, gatedBundleDoc(gateApproved, gateRejected), drawNow)
	ungatedOrder, ungated, _ := projectBundles(t, gatedBundleDoc("", ""), drawNow)
	if len(gatedOrder) != len(ungatedOrder) {
		t.Fatalf("minted %d gated and %d ungated requests", len(gatedOrder), len(ungatedOrder))
	}
	key := func(l bundleLine) string { b, _ := json.Marshal(l.body); return l.source + string(b) }
	for i, trace := range gatedOrder {
		base := ungated[ungatedOrder[i]]
		if len(base) != 3 {
			t.Fatalf("ungated request %d wrote %d lines, want 3", i, len(base))
		}
		all := map[string]bool{}
		for _, l := range base {
			all[key(l)] = true
		}
		for _, l := range gated[trace] {
			if !all[key(l)] {
				t.Fatalf("request %d gated line %s differs from the ungated bundle %v", i, key(l), base)
			}
		}
	}
}

// retryDoc is a failing entry (every failure a 503) that logs its request's drawn order reference.
const retryDoc = `
traffic:
  off_peak_rps: 20
  peak_rps: 20
  error_mix: [{status: 503, code: unavailable, weight: 1}]
services:
  - name: api
    type: web
    entry: true
    error_rate: 0.4
    logs:
      - source: app.request
        body:
          order_ref: ` + drawOrderRef + `
          trace_id: {ref: trace_id}
`

// TestClientRetryRepeatsLogicalIdentity: a client re-send of a failed request is a new request
// (fresh trace) of the same logical request, logging the first attempt's drawn order reference;
// every distinct logical request draws its own. Re-sends follow the backoff and stop at the limit.
func TestClientRetryRepeatsLogicalIdentity(t *testing.T) {
	w := buildApp(t, decodeStrict(t, retryDoc))
	w.m.retry = &ClientRetry{Retries: 2, BackoffSeconds: 20, RetryOn: []string{"5xx"}}
	lc := &coretest.LogCapture{}
	world := coretest.World(&coretest.MetricCapture{}, lc, &coretest.TraceCapture{})
	var reqs []*ledger.Request
	for i := range 6 {
		at := drawNow.Add(time.Duration(i) * 30 * time.Second)
		batch := w.m.Mint(at, 30, world.Shape)
		if err := w.ProjectBatch(context.Background(), at, world, batch); err != nil {
			t.Fatalf("ProjectBatch: %v", err)
		}
		reqs = append(reqs, batch...)
	}
	refByTrace := map[string]string{}
	for _, st := range lc.Streams {
		for _, ln := range st.Lines {
			var body map[string]any
			if err := json.Unmarshal([]byte(ln.Body), &body); err != nil {
				t.Fatal(err)
			}
			refByTrace[ln.Meta["trace_id"]], _ = body["order_ref"].(string)
		}
	}
	// logical unit -> its attempts in mint order
	attempts := map[string][]*ledger.Request{}
	var units []string
	for _, r := range reqs {
		u := drawUnit(r)
		if attempts[u] == nil {
			units = append(units, u)
		}
		attempts[u] = append(attempts[u], r)
	}
	refOwner := map[string]string{}
	resent := 0
	for _, u := range units {
		as := attempts[u]
		ref := refByTrace[as[0].TraceID]
		if !orderRefShape.MatchString(ref) {
			t.Fatalf("order_ref %q does not match ORD-<10 digits>", ref)
		}
		if other, ok := refOwner[ref]; ok {
			t.Fatalf("logical requests %q and %q share order_ref %q", other, u, ref)
		}
		refOwner[ref] = u
		traces := map[string]bool{}
		for k, a := range as {
			if a.LogicalAttempt != k {
				t.Fatalf("logical request %q attempt %d has LogicalAttempt %d", u, k, a.LogicalAttempt)
			}
			if traces[a.TraceID] {
				t.Fatalf("logical request %q repeats trace %s across attempts", u, a.TraceID)
			}
			traces[a.TraceID] = true
			if got := refByTrace[a.TraceID]; got != ref {
				t.Fatalf("logical request %q attempt %d logged order_ref %q, first attempt %q", u, k, got, ref)
			}
			if k == 0 {
				continue
			}
			resent++
			prev := as[k-1]
			if prev.StatusCode != 503 {
				t.Fatalf("attempt %d re-sent after status %d", k, prev.StatusCode)
			}
			if gap, want := a.Start.Sub(prev.Start), time.Duration(20*math.Pow(2, float64(k-1)))*time.Second; gap < want {
				t.Fatalf("attempt %d sent %v after the previous one, want at least the %v backoff", k, gap, want)
			}
		}
		if len(as) > 3 {
			t.Fatalf("logical request %q made %d attempts, want at most 1 + 2 re-sends", u, len(as))
		}
	}
	if resent == 0 {
		t.Fatal("no request was re-sent")
	}
}

// TestClientRetryValidation rejects every malformed policy and every workload it cannot apply to.
func TestClientRetryValidation(t *testing.T) {
	ok := ClientRetry{Retries: 1, BackoffSeconds: 2, RetryOn: []string{"5xx", "throttle"}}
	if err := ok.validate(decodeStrict(t, retryDoc)); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	var none *ClientRetry
	if err := none.validate(decodeStrict(t, retryDoc)); err != nil {
		t.Fatalf("nil policy rejected: %v", err)
	}
	for name, c := range map[string]ClientRetry{
		"no retries":       {Retries: 0, BackoffSeconds: 2, RetryOn: []string{"5xx"}},
		"too many retries": {Retries: 4, BackoffSeconds: 2, RetryOn: []string{"5xx"}},
		"zero backoff":     {Retries: 1, BackoffSeconds: 0, RetryOn: []string{"5xx"}},
		"nan backoff":      {Retries: 1, BackoffSeconds: math.NaN(), RetryOn: []string{"5xx"}},
		"huge backoff":     {Retries: 1, BackoffSeconds: 1e10, RetryOn: []string{"5xx"}},
		"no classes":       {Retries: 1, BackoffSeconds: 2},
		"unknown class":    {Retries: 1, BackoffSeconds: 2, RetryOn: []string{"timeout"}},
		"repeated class":   {Retries: 1, BackoffSeconds: 2, RetryOn: []string{"5xx", "5xx"}},
	} {
		if err := c.validate(decodeStrict(t, retryDoc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	queue := decodeStrict(t, retryDoc)
	queue.Traffic.FromResource = "q"
	if err := ok.validate(queue); err == nil || !strings.Contains(err.Error(), "from_resource") {
		t.Errorf("queue consumer: err = %v, want from_resource rejection", err)
	}
}

// TestClientRetryUndeclaredMintsNoResends: without a policy no request carries a logical identity,
// so every request draws from its own seed unit exactly as before.
func TestClientRetryUndeclaredMintsNoResends(t *testing.T) {
	w := buildApp(t, decodeStrict(t, retryDoc))
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	for i := range 4 {
		for _, r := range w.m.Mint(drawNow.Add(time.Duration(i)*30*time.Second), 30, world.Shape) {
			if r.Logical != "" || r.LogicalAttempt != 0 {
				t.Fatalf("request %s carries logical identity %q/%d without a policy", r.TraceID, r.Logical, r.LogicalAttempt)
			}
		}
	}
}
