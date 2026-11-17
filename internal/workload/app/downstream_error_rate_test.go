// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// downstreamDoc is a three-hop chain: an entry with its own rate, an instrumented web service
// (it has a SERVER span) and a db leaf. Rates are replaced per test.
const downstreamDoc = `
traffic: {off_peak_rps: 100, peak_rps: 100}
services:
  - name: api
    type: web
    entry: true
    error_rate: 0.2
    calls: [orders]
  - name: orders
    type: web
    error_rate: 0.1
    calls: [store]
  - name: store
    type: db
    error_rate: 0.05
`

var downstreamNow = time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)

func mintMany(w *Workload, eng *shape.Engine, ticks int) []*ledger.Request {
	var out []*ledger.Request
	for range ticks {
		out = append(out, w.m.Mint(downstreamNow, 60, eng)...)
	}
	return out
}

func requestFailFraction(reqs []*ledger.Request) float64 {
	var failed float64
	for _, r := range reqs {
		if r.Outcome != ledger.OutcomeSuccess {
			failed++
		}
	}
	return failed / float64(len(reqs))
}

// TestDownstreamErrorRateFailsItsOwnHop: a downstream error_rate is the fraction of requests whose
// hop to that service fails, independent of the entry; the request fails with 1-(1-a)(1-b)(1-c)
// overall, and a request only a hop failed is an upstream 5xx.
func TestDownstreamErrorRateFailsItsOwnHop(t *testing.T) {
	w := buildApp(t, decodeStrict(t, downstreamDoc))
	reqs := mintMany(w, shape.New("UTC", nil), 20) // 120,000 requests
	if len(reqs) < 100000 {
		t.Fatalf("minted %d requests, want about 120000", len(reqs))
	}
	if got, want := hopFailRate(reqs, "orders"), 0.1; math.Abs(got-want) > 0.005 {
		t.Errorf("orders hop failed fraction = %.4f, want %.2f", got, want)
	}
	if got, want := hopFailRate(reqs, "store"), 0.05; math.Abs(got-want) > 0.004 {
		t.Errorf("store hop failed fraction = %.4f, want %.2f", got, want)
	}
	if got, want := requestFailFraction(reqs), 1-0.8*0.9*0.95; math.Abs(got-want) > 0.006 {
		t.Errorf("request failure fraction = %.4f, want %.4f", got, want)
	}
	for _, r := range reqs {
		hopFailed := false
		for _, c := range r.Calls {
			hopFailed = hopFailed || c.Failed
		}
		if hopFailed && r.Outcome == ledger.OutcomeSuccess {
			t.Fatalf("request with a failed hop is a success: %+v", r.Calls)
		}
		if hopFailed && r.Outcome == ledger.OutcomeServerError && r.ErrorKind != "upstream_5xx" {
			t.Fatalf("5xx with a failed hop has error kind %q, want upstream_5xx", r.ErrorKind)
		}
	}
}

// TestDownstreamErrorRateSurfacesInSpansAndMetrics: the failed hop's CLIENT and SERVER spans are
// errors and the callee's span metrics and service graph count them, not just the entry.
func TestDownstreamErrorRateSurfacesInSpansAndMetrics(t *testing.T) {
	doc := strings.Replace(downstreamDoc, "error_rate: 0.2", "error_rate: 0", 1)
	doc = strings.Replace(doc, "error_rate: 0.05", "error_rate: 0.3", 1)
	w := buildApp(t, decodeStrict(t, doc))
	eng := shape.New("UTC", nil)
	reqs := mintMany(w, eng, 1)
	world := coretest.World(&coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{})
	world.Shape = eng
	if err := w.ProjectBatch(context.Background(), downstreamNow, world, reqs); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	status := map[string]otlp.StatusCode{}
	for _, res := range world.Traces.(*coretest.TraceCapture).Resources {
		for _, sp := range res.Spans {
			status[sp.SpanID] = sp.Status
		}
	}
	var failedOrders, failedStore int
	for _, r := range reqs {
		for _, c := range r.Calls {
			want := otlp.StatusUnset
			if c.Failed {
				want = otlp.StatusError
			}
			if got := status[c.SpanID]; got != want {
				t.Fatalf("hop %s client span status %v, want %v", c.Target, got, want)
			}
			if c.PeerSpanID != "" {
				if got := status[c.PeerSpanID]; got != want {
					t.Fatalf("hop %s server span status %v, want %v", c.Target, got, want)
				}
			}
			if c.Failed && c.Target == "orders" {
				failedOrders++
			}
			if c.Failed && c.Target == "store" {
				failedStore++
			}
		}
	}
	if failedOrders == 0 || failedStore == 0 {
		t.Fatalf("expected failed hops on both downstream services, got orders=%d store=%d", failedOrders, failedStore)
	}
}

// TestDownstreamErrorRateSpanMetricsFollowHops: the callee's RED error count comes from the same
// failed hops the traces carry.
func TestDownstreamErrorRateSpanMetricsFollowHops(t *testing.T) {
	doc := strings.Replace(downstreamDoc, "error_rate: 0.2", "error_rate: 0", 1)
	w := buildApp(t, decodeStrict(t, doc))
	eng := shape.New("UTC", nil)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	batch := led.Mint(downstreamNow)
	var hops, failed float64
	for _, r := range batch {
		for _, c := range r.Calls {
			if c.Target == "orders" {
				hops++
				if c.Failed {
					failed++
				}
			}
		}
	}
	if failed == 0 {
		t.Fatal("no failed orders hops minted")
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape = eng
	world.Ledger = led
	if err := w.Tick(context.Background(), downstreamNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var calls, errs float64
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels["service_name"] != "orders" || s.Labels["span_kind"] != spanKindServer {
			continue
		}
		calls += s.Value
		if s.Labels["status_code"] == statusCodeError {
			errs += s.Value
		}
	}
	if calls == 0 {
		t.Fatal("no orders server span-metric calls")
	}
	if got, want := errs/calls, failed/hops; math.Abs(got-want) > 1e-9 {
		t.Fatalf("orders span-metric error fraction = %v, want failed-hop fraction %v", got, want)
	}
}

// TestDownstreamErrorRateOptIn: with no downstream rate, a zero downstream rate or one that is
// absent leaves every minted value untouched; a downstream error_rate is the only thing that
// changes the draws.
func TestDownstreamErrorRateOptIn(t *testing.T) {
	entryOnly := strings.Replace(strings.Replace(downstreamDoc, "    error_rate: 0.1\n", "", 1), "    error_rate: 0.05\n", "", 1)
	zero := strings.Replace(strings.Replace(downstreamDoc, "error_rate: 0.1", "error_rate: 0", 1), "error_rate: 0.05", "error_rate: 0", 1)
	a := buildApp(t, decodeStrict(t, entryOnly))
	b := buildApp(t, decodeStrict(t, zero))
	ra, rb := mintMany(a, shape.New("UTC", nil), 3), mintMany(b, shape.New("UTC", nil), 3)
	if len(ra) != len(rb) || len(ra) == 0 {
		t.Fatalf("minted %d vs %d requests", len(ra), len(rb))
	}
	sig := func(r *ledger.Request) [5]any {
		fails := 0
		var hopDur time.Duration
		for _, c := range r.Calls {
			hopDur += c.Duration
			if c.Failed {
				fails++
			}
		}
		return [5]any{r.Outcome, r.StatusCode, r.Duration, hopDur, fails}
	}
	for i := range ra {
		if sig(ra[i]) != sig(rb[i]) {
			t.Fatalf("request %d differs: %v vs %v", i, sig(ra[i]), sig(rb[i]))
		}
	}
	if got := hopFailRate(ra, "store") + hopFailRate(ra, "orders"); got != 0 {
		t.Fatalf("entry-only hops failed at %v, want 0", got)
	}
	if got, want := requestFailFraction(ra), 0.2; math.Abs(got-want) > 0.02 {
		t.Fatalf("entry-only request failure = %v, want about %v", got, want)
	}
}

// TestDownstreamErrorRateComposesWithErrorSpike: an active error_spike on the downstream hop
// still fails it through its own draw, on top of the declared rate.
func TestDownstreamErrorRateComposesWithErrorSpike(t *testing.T) {
	doc := strings.Replace(downstreamDoc, "error_rate: 0.2", "error_rate: 0", 1)
	spike := "error_spike@2026-06-10T13:00:00Z/1h@store"
	eng := shape.New("UTC", []string{spike})
	_, inten := eng.Eval(downstreamNow, "error_spike", "store")
	if inten <= 0 {
		t.Fatalf("error_spike@store intensity %v, want > 0", inten)
	}
	w := buildApp(t, decodeStrict(t, doc))
	got := hopFailRate(mintMany(w, eng, 20), "store")
	want := 1 - (1-0.05)*(1-0.6*inten)
	if math.Abs(got-want) > 0.006 {
		t.Fatalf("store hop failed fraction with spike = %.4f, want %.4f", got, want)
	}
	// The same incident without a declared rate is the unchanged legacy hop draw.
	bare := strings.Replace(doc, "    error_rate: 0.05\n", "", 1)
	w = buildApp(t, decodeStrict(t, bare))
	got = hopFailRate(mintMany(w, shape.New("UTC", []string{spike}), 20), "store")
	if want := 0.6 * inten; math.Abs(got-want) > 0.006 {
		t.Fatalf("store hop failed fraction, spike only = %.4f, want %.4f", got, want)
	}
}

// TestDownstreamErrorRateValidation: a rate out of range, on an unreachable service, or on a flow
// that mints no graph calls is rejected rather than silently ignored.
func TestDownstreamErrorRateValidation(t *testing.T) {
	cases := map[string]struct{ doc, want string }{
		"out of range": {strings.Replace(downstreamDoc, "error_rate: 0.1", "error_rate: 1.5", 1), `service "orders": error_rate 1.5 out of [0,1]`},
		"unreachable": {downstreamDoc + `  - name: island
    type: web
    error_rate: 0.1
`, `service "island": error_rate needs a service the entry can reach`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := build(decodeStrict(t, tc.doc), coretestBinding()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}
