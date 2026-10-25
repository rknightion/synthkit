// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

var drawNow = time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC)

// bundleDoc is a two-line log bundle: both streams of the entry carry the request's order
// reference and path. The declared form draws them once per request (`request_draw`), the
// legacy form repeats a fixed specimen.
func bundleDoc(orderRef, path string) string {
	return `
traffic: {off_peak_rps: 100, peak_rps: 100}
services:
  - name: api
    type: web
    entry: true
    logs:
      - source: app.request
        body:
          msg: {const_str: received}
          order_ref: ` + orderRef + `
          path: ` + path + `
          trace_id: {ref: trace_id}
      - source: app.audit
        body:
          msg: {const_str: audited}
          order_ref: ` + orderRef + `
          path: ` + path + `
          trace_id: {ref: trace_id}
`
}

const (
	drawOrderRef = `{request_draw: {key: order, format: "ORD-{digits:10}"}}`
	drawPath     = `{request_draw: {key: path, enum: [{value: /v1/orders, weight: 6}, {value: /v1/items, weight: 3}, {value: /v1/health, weight: 1}]}}`
)

// bundleLine is one projected log line of a request's bundle.
type bundleLine struct {
	source string
	body   map[string]string
}

// projectBundles mints one 30 s tick (3000 requests) and groups the projected lines by request
// (the trace id rides as structured metadata), in request order.
func projectBundles(t *testing.T, doc string, at time.Time) (order []string, byReq map[string][]bundleLine, eng *shape.Engine) {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	lc := &coretest.LogCapture{}
	world := coretest.World(&coretest.MetricCapture{}, lc, &coretest.TraceCapture{})
	reqs := w.m.Mint(at, 30, world.Shape)
	if len(reqs) < 2000 {
		t.Fatalf("minted %d requests, want about 3000", len(reqs))
	}
	if err := w.ProjectBatch(context.Background(), at, world, reqs); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	byReq = map[string][]bundleLine{}
	for _, r := range reqs {
		order = append(order, r.TraceID)
	}
	for _, st := range lc.Streams {
		for _, ln := range st.Lines {
			var raw map[string]any
			if err := json.Unmarshal([]byte(ln.Body), &raw); err != nil {
				t.Fatalf("log body %q: %v", ln.Body, err)
			}
			body := map[string]string{}
			for k, v := range raw {
				body[k], _ = v.(string)
			}
			byReq[ln.Meta["trace_id"]] = append(byReq[ln.Meta["trace_id"]], bundleLine{source: st.Labels["source"], body: body})
		}
	}
	return order, byReq, world.Shape
}

var orderRefShape = regexp.MustCompile(`^ORD-[0-9]{10}$`)

// TestRequestDrawSharedAcrossBundle: every line of one request's bundle carries the same freshly
// formatted ID and the same drawn path; different requests carry different IDs; the path
// frequencies follow the declared 6:3:1 mix.
func TestRequestDrawSharedAcrossBundle(t *testing.T) {
	order, byReq, _ := projectBundles(t, bundleDoc(drawOrderRef, drawPath), drawNow)
	ids := map[string]bool{}
	paths := map[string]int{}
	for _, trace := range order {
		lines := byReq[trace]
		if len(lines) != 2 {
			t.Fatalf("request %s has %d bundle lines, want 2", trace, len(lines))
		}
		a, b := lines[0].body, lines[1].body
		if a["msg"] == b["msg"] {
			t.Fatalf("the two lines are the same stream: %v", lines)
		}
		if !orderRefShape.MatchString(a["order_ref"]) {
			t.Fatalf("order_ref %q does not match ORD-<10 digits>", a["order_ref"])
		}
		if a["order_ref"] != b["order_ref"] || a["path"] != b["path"] {
			t.Fatalf("bundle lines disagree: %v vs %v", a, b)
		}
		if ids[a["order_ref"]] {
			t.Fatalf("order_ref %q repeats across requests", a["order_ref"])
		}
		ids[a["order_ref"]] = true
		paths[a["path"]]++
	}
	total := float64(len(order))
	for path, want := range map[string]float64{"/v1/orders": 0.6, "/v1/items": 0.3, "/v1/health": 0.1} {
		if got := float64(paths[path]) / total; math.Abs(got-want) > 0.03 {
			t.Errorf("path %s share = %.3f, want %.2f (%v)", path, got, want, paths)
		}
	}
	if len(paths) != 3 {
		t.Errorf("drawn paths %v, want exactly the declared three", paths)
	}
}

// TestRequestDrawDeterministic: the same seed and clock give the same draws; a later clock (a
// restarted process) gives fresh ones, so an ID never repeats across restarts.
func TestRequestDrawDeterministic(t *testing.T) {
	doc := bundleDoc(drawOrderRef, drawPath)
	pairs := func(at time.Time) []string {
		order, byReq, _ := projectBundles(t, doc, at)
		out := make([]string, 0, len(order))
		for _, trace := range order {
			l := byReq[trace][0].body
			out = append(out, l["order_ref"]+" "+l["path"])
		}
		return out
	}
	first, second := pairs(drawNow), pairs(drawNow)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("request %d: run 1 drew %q, run 2 drew %q", i, first[i], second[i])
		}
	}
	later := pairs(drawNow.Add(time.Hour))
	seen := map[string]bool{}
	for _, v := range first {
		seen[strings.Fields(v)[0]] = true
	}
	for _, v := range later {
		if id := strings.Fields(v)[0]; seen[id] {
			t.Fatalf("order_ref %q repeats after a restart at a later clock", id)
		}
	}
}

// TestRequestDrawConsumesNoEngineRandomness: the per-request draw hashes the seed unit, so the
// shape engine's sequence is the same whether a field is drawn per request or is a constant (the
// declared form never shifts a neighbour's draws).
func TestRequestDrawConsumesNoEngineRandomness(t *testing.T) {
	neighbours := `
          pick: {enum: [{value: a, weight: 1}, {value: b, weight: 1}]}
          ms: {int_range: {min: 1, max: 1000}}`
	next := func(orderRef, path string) float64 {
		doc := strings.ReplaceAll(bundleDoc(orderRef, path), "          trace_id:", neighbours+"\n          trace_id:")
		_, _, eng := projectBundles(t, doc, drawNow)
		return eng.Float64()
	}
	declared := next(drawOrderRef, drawPath)
	fixed := next(`{const_str: ORD-0000000001}`, `{const_str: /v1/orders}`)
	if declared != fixed {
		t.Fatalf("engine sequence moved: next draw %v with request_draw, %v with constants", declared, fixed)
	}
}

// specimenLines projects one hand-built request through a blueprint that declares no
// request_draw and returns its raw body lines in emission order.
func specimenLines(t *testing.T) []string {
	t.Helper()
	doc := strings.ReplaceAll(bundleDoc(`{const_str: ORD-0000000001}`, `{const_str: /v1/orders}`),
		"          trace_id:", `          pick: {enum: [{value: a, weight: 1}, {value: b, weight: 1}]}
          ms: {int_range: {min: 1, max: 1000}}
          trace_id:`)
	w := buildApp(t, decodeStrict(t, doc))
	r := &ledger.Request{
		Correlation: ledger.NewCorrelationFromSeed("bundle-specimen"),
		Workload:    "demo", Env: "prod", Cluster: "c", Route: "GET /v1/orders",
		Start: drawNow, Duration: 120 * time.Millisecond, StatusCode: 200,
	}
	lc := &coretest.LogCapture{}
	world := coretest.World(&coretest.MetricCapture{}, lc, nil)
	if err := w.ProjectBatch(context.Background(), drawNow, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	var out []string
	for _, st := range lc.Streams {
		for _, ln := range st.Lines {
			out = append(out, st.Labels["source"]+" "+ln.Body)
		}
	}
	return out
}

// TestRequestDrawUndeclaredOutputUnchanged pins the exact lines of a blueprint that declares no
// request_draw (captured on the base before the model existed).
func TestRequestDrawUndeclaredOutputUnchanged(t *testing.T) {
	want := []string{
		`app.request {"ms":345,"msg":"received","order_ref":"ORD-0000000001","path":"/v1/orders","pick":"a"}`,
		`app.audit {"ms":524,"msg":"audited","order_ref":"ORD-0000000001","path":"/v1/orders","pick":"a"}`,
	}
	got := specimenLines(t)
	if len(got) != len(want) {
		t.Fatalf("lines %d, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

// TestRequestDrawCausalBundleSharesAcrossNodesAndAttempts: in a failing causal request every
// node's lines, including each retried attempt's, carry the request's one order reference.
func TestRequestDrawCausalBundleSharesAcrossNodesAndAttempts(t *testing.T) {
	doc := strings.ReplaceAll(loggedIntakeApp, "error_code: {ref: error_code}",
		"error_code: {ref: error_code}\n          order_ref: "+drawOrderRef)
	w := causalApps(t, causalResources, doc)[0]
	eng := incident(causal.ModeCredentialExpired, "partner-api-cert", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	checked := 0
	ids := map[string]bool{}
	for _, r := range reqs {
		if r.Fault == nil {
			continue
		}
		var want string
		n := 0
		for _, svc := range []string{"intake-api", "enrich-fn", "partner-gateway"} {
			for _, l := range p.linesFor(svc, r) {
				n++
				id := l.field("order_ref")
				if !orderRefShape.MatchString(id) {
					t.Fatalf("%s order_ref %q does not match ORD-<10 digits>", svc, id)
				}
				if want == "" {
					want = id
				}
				if id != want {
					t.Fatalf("request %s: %s logged order_ref %q, bundle has %q", r.TraceID, svc, id, want)
				}
			}
		}
		if n < 3 {
			t.Fatalf("failing request logged %d lines, want the multi-node, multi-attempt bundle", n)
		}
		if ids[want] {
			t.Fatalf("order_ref %q shared by two requests", want)
		}
		ids[want] = true
		checked++
	}
	if checked == 0 {
		t.Fatal("no faulted requests minted")
	}
}
