// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// causalLogBody is a log body reading every causal ref (design §5.2) plus the hop facts.
const causalLogBody = `
    logs:
      - source: app
        body:
          span_id: {ref: span_id}
          status: {ref: status}
          outcome: {ref: outcome}
          error_type: {ref: error_type}
          error_message: {ref: error_message}
          exception_type: {ref: exception_type}
          fault_resource: {ref: fault_resource}
          fault_mode: {ref: fault_mode}
          attempt: {ref: attempt}
          upstream: {ref: upstream}
          error_code: {ref: error_code}`

// loggedIntakeApp is intakeApp (the five-story fixture) with every service logging the causal refs.
var loggedIntakeApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: intake-api
    entry: true
    uses: [intake-quota, intake-db-pool, completeness-rule]
    calls: [records-db, enrich-fn, validation-publisher]
    call_policies:
      enrich-fn: {timeout_ms: 10000, retries: 2, backoff_ms: 200, retry_on: [throttle, 5xx]}` + causalLogBody + `
  - {name: records-db, type: db, uses: [records-db]}
  - name: enrich-fn
    type: function
    external: true
    uses: [enrich-fn-concurrency]
    calls: [partner-gateway]
    call_policies:
      partner-gateway: {timeout_ms: 2000, retries: 1, retry_on: [5xx, timeout, connect]}` + causalLogBody + `
  - name: partner-gateway
    external: true
    uses: [partner-api]` + causalLogBody + `
  - {name: validation-publisher, uses: [validation-queue]}
`

type projected struct {
	spans     map[string]otlp.Span        // by span id
	owner     map[string]string           // span id -> service.name of its resource
	lines     map[string][]logLine        // service_name -> lines in emission order
	lineIndex map[string]map[string][]int // service_name -> effective span_id -> emission positions
	all       []otlp.Resource             // raw resources
	logs      []loki.Stream               // raw streams
	byReq     map[string][]otlp.Span      // trace id -> spans
}

type logLine struct {
	level string
	t     time.Time
	body  map[string]string
	meta  map[string]string
}

// field reads a body or structured-metadata value.
func (l logLine) field(k string) string {
	if v, ok := l.meta[k]; ok {
		return v
	}
	return l.body[k]
}

func project(t *testing.T, w *Workload, eng *shape.Engine, reqs []*ledger.Request) projected {
	t.Helper()
	tc, lc := &coretest.TraceCapture{}, &coretest.LogCapture{}
	world := coretest.World(nil, lc, tc)
	world.Shape = eng
	if err := w.ProjectBatch(context.Background(), causalNow, world, reqs); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	p := projected{spans: map[string]otlp.Span{}, owner: map[string]string{}, lines: map[string][]logLine{}, lineIndex: map[string]map[string][]int{}, all: tc.Resources, logs: lc.Streams, byReq: map[string][]otlp.Span{}}
	for _, res := range tc.Resources {
		for _, sp := range res.Spans {
			p.spans[sp.SpanID] = sp
			p.owner[sp.SpanID] = res.Attrs["service.name"].(string)
			p.byReq[sp.TraceID] = append(p.byReq[sp.TraceID], sp)
		}
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
			svc := st.Labels["service_name"]
			p.appendLine(svc, logLine{level: st.Labels["level"], t: ln.T, body: body, meta: ln.Meta})
		}
	}
	return p
}

// appendLine indexes each decoded captured row once. field retains metadata presence
// precedence, including an explicitly empty metadata value overriding the body.
func (p *projected) appendLine(svc string, line logLine) {
	position := len(p.lines[svc])
	p.lines[svc] = append(p.lines[svc], line)
	if p.lineIndex[svc] == nil {
		p.lineIndex[svc] = map[string][]int{}
	}
	id := line.field("span_id")
	p.lineIndex[svc][id] = append(p.lineIndex[svc][id], position)
}

func (p projected) linesAt(svc string, positions []int) []logLine {
	var out []logLine
	for _, position := range positions {
		out = append(out, p.lines[svc][position])
	}
	return out
}

// linesForSpan keeps root-only callers distinct from request-wide membership.
func (p projected) linesForSpan(svc, id string) []logLine {
	return p.linesAt(svc, p.lineIndex[svc][id])
}

// linesFor are a service's log lines for one request, matched by the request's span ids.
func (p projected) linesFor(svc string, r *ledger.Request) []logLine {
	ids := map[string]bool{r.SpanID: true}
	for _, c := range r.Calls {
		ids[c.SpanID] = true
		if c.PeerSpanID != "" {
			ids[c.PeerSpanID] = true
		}
	}
	var positions []int
	for id := range ids {
		positions = append(positions, p.lineIndex[svc][id]...)
	}
	// Different request spans may interleave in a service's captured stream. Merge
	// positions, not rows keyed by payload: duplicate rows must remain distinct.
	sort.Ints(positions)
	return p.linesAt(svc, positions)
}

// Compare actual ProjectBatch output with the former full-scan predicate. Copied
// captured rows add interleaving/duplicates and metadata-presence edge cases without
// changing any existing proof's fixture, request population or assertions.
func TestProjectedLogLookupPreservesCapturedCorpus(t *testing.T) {
	w := causalApps(t, `- {name: lookup-dep, kind: dependency, latency_ms: 10}`, `
traffic: {off_peak_rps: 2, peak_rps: 2}
services:
  - name: lookup-front
    entry: true
    error_rate: 0
    calls: [lookup-child]`+causalLogBody+`
  - name: lookup-child
    error_rate: 0
    uses: [lookup-dep]`+causalLogBody)[0]
	eng := shape.New("UTC", nil)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	if len(reqs) < 2 || len(p.lines["lookup-front"]) == 0 || len(p.lines["lookup-child"]) == 0 {
		t.Fatal("missing real request/log corpus")
	}
	// Build the oracle independently from raw LogCapture streams, not from the
	// appendLine-populated corpus: dropping a row from both corpus and index
	// must not make their agreement a false pass.
	captured := map[string][]logLine{}
	for _, stream := range p.logs {
		for _, line := range stream.Lines {
			var raw map[string]any
			if err := json.Unmarshal([]byte(line.Body), &raw); err != nil {
				t.Fatal(err)
			}
			body := map[string]string{}
			for k, value := range raw {
				body[k], _ = value.(string)
			}
			svc := stream.Labels["service_name"]
			captured[svc] = append(captured[svc], logLine{level: stream.Labels["level"], t: line.T, body: body, meta: line.Meta})
		}
	}
	if !reflect.DeepEqual(p.lines, captured) {
		t.Fatal("decoded corpus differs from raw captured rows")
	}
	r := reqs[0]
	if len(r.Calls) == 0 || r.Calls[0].PeerSpanID == "" {
		t.Fatal("missing real child span membership")
	}
	front, child := p.lines["lookup-front"][0], p.lines["lookup-child"][0]
	withSpanMetadata := func(line logLine, id string) logLine {
		meta := map[string]string{}
		for k, v := range line.meta {
			meta[k] = v
		}
		meta["span_id"] = id
		line.meta = meta
		return line
	}
	appendCaptured := func(svc string, line logLine) {
		captured[svc] = append(captured[svc], line)
		p.appendLine(svc, line)
	}
	appendCaptured("lookup-front", front)
	appendCaptured("lookup-front", child)
	appendCaptured("lookup-front", withSpanMetadata(front, r.Calls[0].PeerSpanID))
	appendCaptured("lookup-front", withSpanMetadata(front, ""))
	appendCaptured("lookup-front", front)
	appendCaptured("lookup-child", front) // same span key under another service

	// Repeated IDs in a request are membership, not multiplicity. Empty root IDs
	// still match empty effective IDs, as the former full scan did.
	repeated := *r
	repeated.Calls = append(append([]ledger.Call(nil), r.Calls...), r.Calls...)
	probes := append(append([]*ledger.Request(nil), reqs...), &repeated, &ledger.Request{})
	scan := func(svc string, ids map[string]bool) []logLine {
		var out []logLine
		for _, line := range captured[svc] {
			if ids[line.field("span_id")] {
				out = append(out, line)
			}
		}
		return out
	}
	for _, svc := range []string{"lookup-front", "lookup-child", "absent-service"} {
		for i, req := range probes {
			ids := map[string]bool{req.SpanID: true}
			for _, call := range req.Calls {
				ids[call.SpanID] = true
				if call.PeerSpanID != "" {
					ids[call.PeerSpanID] = true
				}
			}
			if got, want := p.linesFor(svc, req), scan(svc, ids); !reflect.DeepEqual(got, want) {
				t.Fatalf("service %s request %d: indexed corpus differs from full scan (%d/%d rows)", svc, i, len(got), len(want))
			}
			if got, want := p.linesForSpan(svc, req.SpanID), scan(svc, map[string]bool{req.SpanID: true}); !reflect.DeepEqual(got, want) {
				t.Fatalf("service %s root %d: indexed corpus differs from root-only full scan (%d/%d rows)", svc, i, len(got), len(want))
			}
		}
	}
	t.Logf("compared %d real requests plus repeated/empty membership controls over %d captured/copied rows", len(reqs), len(p.lines["lookup-front"])+len(p.lines["lookup-child"]))
}

func noEvents(t *testing.T, p projected) {
	t.Helper()
	for _, sp := range p.spans {
		if len(sp.Events) != 0 {
			t.Fatalf("span %q carries events %+v; the exception span event is deprecated in semconv v1.44.0 and rides the error log instead", sp.Name, sp.Events)
		}
	}
}

// Story 1 projected: the certificate fault's origin is the enrich-fn CLIENT span to partner-gateway
// (no server span, the cause message, the exception class as error.type); every upstream span
// carries the mapped 502; retries are separate CLIENT spans with http.request.resend_count, placed
// by their offsets; the callee that was never reached logs nothing.
func TestCausalProjectCertificateStory(t *testing.T) {
	w := causalApps(t, causalResources, loggedIntakeApp)[0]
	eng := incident(causal.ModeCredentialExpired, "partner-api-cert", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	noEvents(t, p)
	seen := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "partner-api-cert" {
			continue
		}
		seen++
		base := r.RenderStart()
		root := p.spans[r.SpanID]
		if root.Status != otlp.StatusError || root.Attrs["error.type"] != "502" || root.Attrs["http.response.status_code"] != 502 || root.StatusMsg != "" {
			t.Fatalf("root span: status %v attrs %v msg %q", root.Status, root.Attrs, root.StatusMsg)
		}
		enrich := callsTo(r, "enrich-fn")
		if len(enrich) != 3 {
			t.Fatalf("enrich-fn attempts %d", len(enrich))
		}
		var prevStart time.Time
		for k, c := range enrich {
			cs := p.spans[c.SpanID]
			if p.owner[c.SpanID] != "intake-api" || cs.Status != otlp.StatusError || cs.Attrs["http.response.status_code"] != 502 || cs.Attrs["error.type"] != "502" {
				t.Fatalf("enrich attempt %d client span: owner %s status %v attrs %v", k, p.owner[c.SpanID], cs.Status, cs.Attrs)
			}
			rc, has := cs.Attrs["http.request.resend_count"]
			if k == 0 && has || k > 0 && rc != k {
				t.Fatalf("enrich attempt %d resend_count %v (present %v)", k, rc, has)
			}
			if !cs.Start.Equal(base.Add(c.Offset)) || !cs.Start.After(prevStart) {
				t.Fatalf("enrich attempt %d starts %v, want base+offset %v after %v", k, cs.Start, base.Add(c.Offset), prevStart)
			}
			prevStart = cs.Start
			ss := p.spans[c.PeerSpanID]
			if ss.Status != otlp.StatusError || ss.Attrs["http.response.status_code"] != 502 || ss.Attrs["error.type"] != "502" {
				t.Fatalf("enrich attempt %d server span: status %v attrs %v", k, ss.Status, ss.Attrs)
			}
		}
		for i, c := range r.Calls {
			if c.Target != "partner-gateway" {
				continue
			}
			if c.PeerSpanID != "" {
				t.Fatalf("partner-gateway call has a server span")
			}
			cs := p.spans[c.SpanID]
			if p.owner[c.SpanID] != "enrich-fn" || cs.Status != otlp.StatusError {
				t.Fatalf("partner-gateway client span %d: owner %s status %v", i, p.owner[c.SpanID], cs.Status)
			}
			if cs.Attrs["error.type"] != "SSLHandshakeException" || !strings.Contains(cs.StatusMsg, "certificate expired") {
				t.Fatalf("origin client span: error.type %v msg %q", cs.Attrs["error.type"], cs.StatusMsg)
			}
			if _, has := cs.Attrs["http.response.status_code"]; has {
				t.Fatalf("a transport failure carries an HTTP status: %v", cs.Attrs)
			}
		}
		// Logs: the entry once, enrich-fn once per attempt (its own server span, the origin
		// exception), partner-gateway never.
		in := p.linesFor("intake-api", r)
		if len(in) != 1 {
			t.Fatalf("intake-api lines %d, want 1", len(in))
		}
		l := in[0]
		if l.level != "error" || l.field("status") != "502" || l.field("upstream") != "enrich-fn" || l.field("error_type") != "502" ||
			l.field("exception_type") != "" || l.field("fault_resource") != "partner-api-cert" || l.field("error_code") != r.ErrorKind || l.field("span_id") != r.SpanID {
			t.Fatalf("intake-api line %+v", l)
		}
		en := p.linesFor("enrich-fn", r)
		if len(en) != 3 {
			t.Fatalf("enrich-fn lines %d, want one per attempt (3)", len(en))
		}
		for k, l := range en {
			c := enrich[k]
			end := base.Add(c.Offset + c.Duration)
			if l.field("span_id") != c.PeerSpanID || l.field("attempt") != strconv.Itoa(k) || l.field("status") != "502" || l.field("upstream") != "partner-gateway" ||
				l.field("exception_type") != "SSLHandshakeException" || l.field("error_type") != "SSLHandshakeException" ||
				!strings.Contains(l.field("error_message"), "certificate expired") || l.field("error_code") != "" || !l.t.Equal(end) {
				t.Fatalf("enrich-fn line %d %+v (want end %v)", k, l, end)
			}
		}
		if got := p.linesFor("partner-gateway", r); len(got) != 0 {
			t.Fatalf("unreached partner-gateway logged %d lines", len(got))
		}
	}
	if seen == 0 {
		t.Fatal("no certificate requests minted")
	}
}

// Story 5 projected: the pool rejection originates on the entry, whose SERVER span is ERROR (503)
// with the pool's message; Story 3: a rule 422 at the entry keeps the SERVER span UNSET (M10) and
// the cause message rides the log.
func TestCausalProjectEntryOrigins(t *testing.T) {
	w := causalApps(t, causalResources, loggedIntakeApp)[0]
	eng := incident(causal.ModeDependencySlow, "records-db", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	pool := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "intake-db-pool" || r.StatusCode != 503 {
			continue
		}
		pool++
		root := p.spans[r.SpanID]
		if root.Status != otlp.StatusError || root.Attrs["error.type"] != "SQLTransientConnectionException" || !strings.Contains(root.StatusMsg, "connection is not available") {
			t.Fatalf("pool root: status %v attrs %v msg %q", root.Status, root.Attrs, root.StatusMsg)
		}
		in := p.linesFor("intake-api", r)
		if len(in) != 1 || in[0].field("exception_type") != "SQLTransientConnectionException" || in[0].field("upstream") != "" {
			t.Fatalf("pool entry lines %+v", in)
		}
		if n := len(p.linesFor("enrich-fn", r)) + len(p.linesFor("partner-gateway", r)); n != 0 {
			t.Fatalf("an entry rejection logged %d downstream lines", n)
		}
	}
	if pool == 0 {
		t.Fatal("no pool rejections minted")
	}

	w = causalApps(t, causalResources, loggedIntakeApp)[0]
	eng = incident(causal.ModeRuleRegression, "completeness-rule", 1)
	reqs = mintAt(w, eng, causalNow)
	p = project(t, w, eng, reqs)
	rule := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Kind != string(causal.KindRule) {
			continue
		}
		rule++
		root := p.spans[r.SpanID]
		_, hasType := root.Attrs["error.type"]
		if root.Status != otlp.StatusUnset || root.Attrs["http.response.status_code"] != 422 || hasType || root.StatusMsg != "" {
			t.Fatalf("rule root: status %v attrs %v msg %q", root.Status, root.Attrs, root.StatusMsg)
		}
		in := p.linesFor("intake-api", r)
		if len(in) != 1 || in[0].level != "warn" || in[0].field("status") != "422" || !strings.Contains(in[0].field("error_message"), "rule completeness") {
			t.Fatalf("rule entry lines %+v", in)
		}
	}
	if rule == 0 {
		t.Fatal("no rule rejections minted")
	}
}

// Story 2 projected: a throttled enrich-fn answers 429; its SERVER span stays UNSET (a 4xx) with
// the status code and no error.type, the caller's CLIENT span is ERROR with the 429 it saw.
func TestCausalProjectThrottleStatusRules(t *testing.T) {
	w := causalApps(t, causalResources, loggedIntakeApp)[0]
	eng := incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	seen := 0
	for _, r := range reqs {
		for _, c := range callsTo(r, "enrich-fn") {
			if c.StatusCode != 429 {
				continue
			}
			seen++
			cs, ss := p.spans[c.SpanID], p.spans[c.PeerSpanID]
			if cs.Status != otlp.StatusError || cs.Attrs["http.response.status_code"] != 429 {
				t.Fatalf("throttled client span: %v %v", cs.Status, cs.Attrs)
			}
			// A 4xx HTTP SERVER answer stays UNSET and carries no error.type (semconv v1.44.0
			// http-spans note [4]); the cause rides the log.
			_, hasType := ss.Attrs["error.type"]
			if ss.Status != otlp.StatusUnset || ss.Attrs["http.response.status_code"] != 429 || hasType || ss.StatusMsg != "" {
				t.Fatalf("throttled server span: %v %v %q", ss.Status, ss.Attrs, ss.StatusMsg)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no throttled enrich-fn calls minted")
	}
}

// A worker entry roots a CONSUMER span, and a from_resource app's span-metric volume is the
// queue's served rate, not its declared traffic.
func TestCausalProjectWorkerConsumerAndServedVolume(t *testing.T) {
	ws := causalApps(t, causalResources, intakeApp, validatorApp)
	w := ws[1]
	eng := incident(causal.ModeDependencySlow, "external-validator", 1)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	reqs := led.Mint(causalNow)
	if len(reqs) == 0 {
		t.Fatal("no consumer requests")
	}
	p := project(t, w, eng, reqs)
	for _, r := range reqs {
		if k := p.spans[r.SpanID].Kind; k != otlp.KindConsumer {
			t.Fatalf("worker root span kind %v, want CONSUMER", k)
		}
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var calls float64
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels["service_name"] == "validation-worker" && !strings.HasPrefix(s.Labels["span_name"], "call ") {
			if s.Labels["span_kind"] != spanKindConsumer {
				t.Fatalf("worker entry row span_kind %q", s.Labels["span_kind"])
			}
			calls += s.Value
		}
	}
	want := math.Floor(w.m.causal.servedRPS(causalNow) * interval.Seconds())
	if math.Abs(calls-want) > 1 || want <= 0 {
		t.Fatalf("worker entry calls %.0f, want served rate x 60 s = %.0f", calls, want)
	}
}

// A worker entry's CONSUMER root is named for the messaging operation and destination
// (semantic-conventions v1.44.0 messaging-spans.md "Span name": `{messaging.operation.name}
// {destination}`), the destination being the bound queue resource. Every other root keeps the
// route, and the derived span-metric span_name tracks the emitted root name.
func TestCausalProjectWorkerRootNamedForQueueOperation(t *testing.T) {
	ws := causalApps(t, causalResources, intakeApp, validatorApp)
	intake, worker := ws[0], ws[1]

	wreqs := mintAt(worker, shape.New("UTC", nil), causalNow)
	if len(wreqs) == 0 {
		t.Fatal("no consumer requests")
	}
	wp := project(t, worker, shape.New("UTC", nil), wreqs)
	for _, r := range wreqs {
		root := wp.spans[r.SpanID]
		if root.Kind != otlp.KindConsumer || root.Name != "process validation-queue" {
			t.Fatalf("worker root %q (%v), want CONSUMER %q (route was %q)", root.Name, root.Kind, "process validation-queue", r.Route)
		}
	}

	ireqs := mintAt(intake, shape.New("UTC", nil), causalNow)
	if len(ireqs) == 0 {
		t.Fatal("no intake requests")
	}
	ip := project(t, intake, shape.New("UTC", nil), ireqs)
	for _, r := range ireqs {
		if root := ip.spans[r.SpanID]; root.Name != r.Route || strings.HasPrefix(root.Name, "process ") {
			t.Fatalf("non-worker root %q, want the route %q", root.Name, r.Route)
		}
	}

	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	led := ledger.New(shape.New("UTC", nil), 0, 0)
	led.AddMinter(worker.Minter())
	led.Mint(causalNow)
	world.Ledger = led
	if err := worker.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var rows int
	for _, s := range mc.Find("traces_spanmetrics_calls_total") {
		if s.Labels["service_name"] == "validation-worker" && s.Labels["span_kind"] == spanKindConsumer {
			rows++
			if s.Labels["span_name"] != "process validation-queue" {
				t.Fatalf("worker entry span_name %q, want %q", s.Labels["span_name"], "process validation-queue")
			}
		}
	}
	if rows == 0 {
		t.Fatal("no worker entry span-metric rows")
	}
}

// A healthy causal request (no fault, nothing touched), successful or failed only by the entry's
// baseline draw, projects as the same request marked legacy except for where its hops sit: every
// span keeps its name, ids, parent, kind, status and attributes and every log line is
// byte-identical, while the hop spans are laid out on the engine's timeline, each CLIENT span
// starting at its hop's Offset (SKT-0200, SKT-0206; design §4.6 M6, root ruling D85).
func TestCausalProjectHealthyIdentity(t *testing.T) {
	w := causalApps(t, causalResources, loggedIntakeApp)[0]
	reqs := mintAt(w, shape.New("UTC", nil), causalNow)
	var healthy, legacy []*ledger.Request
	baseline := 0
	for _, r := range reqs {
		if !r.Causal || r.Fault != nil || r.Outcome != ledger.OutcomeSuccess && !baselineFailure(r) {
			continue
		}
		if r.Outcome != ledger.OutcomeSuccess {
			baseline++
		}
		healthy = append(healthy, r)
		cp := *r
		cp.Causal = false
		legacy = append(legacy, &cp)
	}
	if len(healthy) == 0 || baseline == 0 {
		t.Fatalf("%d healthy causal requests, %d of them baseline-failed; want both kinds", len(healthy), baseline)
	}
	a := project(t, w, shape.New("UTC", nil), healthy)
	b := project(t, w, shape.New("UTC", nil), legacy)
	untimed := func(rs []otlp.Resource) []otlp.Resource {
		out := make([]otlp.Resource, len(rs))
		for i, r := range rs {
			out[i] = r
			out[i].Spans = make([]otlp.Span, len(r.Spans))
			for j, sp := range r.Spans {
				sp.Start, sp.End = time.Time{}, time.Time{}
				out[i].Spans[j] = sp
			}
		}
		return out
	}
	if !reflect.DeepEqual(untimed(a.all), untimed(b.all)) {
		t.Fatal("healthy causal spans differ from their legacy projection beyond their timing")
	}
	hops := 0
	for _, r := range healthy {
		if !causalLayout(r) {
			continue
		}
		for _, c := range r.Calls {
			hops++
			if got, want := a.spans[c.SpanID].Start, r.RenderStart().Add(c.Offset); !got.Equal(want) {
				t.Fatalf("hop %s CLIENT span starts at %v, want its engine offset %v", c.Target, got, want)
			}
		}
	}
	if hops == 0 {
		t.Fatal("no healthy causal hop was laid out on the engine timeline")
	}
	if !reflect.DeepEqual(a.logs, b.logs) {
		t.Fatal("healthy causal logs differ from their legacy projection")
	}
}

// Span metrics in a faulted window: the entry's error row moves off the 20 to 200 ms legacy draw
// by the real rejections' slowdown over the entry's healthy duration (pool rejections last at
// least the 3 s acquire timeout, about 2.5x a healthy request here), once a healthy window has given that reference; and a retried
// edge counts its attempts.
func TestCausalSpanMetricsFollowHops(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	eng := incident(causal.ModeDependencySlow, "records-db", 1)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	before := causalNow.Add(-90 * time.Minute) // the incident starts at 12:00
	led.Mint(before)
	warm := &coretest.MetricCapture{}
	world := coretest.World(warm, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), before, world); err != nil {
		t.Fatalf("warm-up Tick: %v", err)
	}
	led.Mint(causalNow)
	mc := &coretest.MetricCapture{}
	world = coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	errRow := func(mc *coretest.MetricCapture, name string) float64 {
		var v float64
		for _, s := range mc.Find(name) {
			if s.Labels["service_name"] == "intake-api" && s.Labels["span_kind"] == spanKindServer && s.Labels["status_code"] == statusCodeError {
				v += s.Value
			}
		}
		return v
	}
	sum := errRow(mc, "traces_spanmetrics_latency_sum") - errRow(warm, "traces_spanmetrics_latency_sum")
	count := errRow(mc, "traces_spanmetrics_latency_count") - errRow(warm, "traces_spanmetrics_latency_count")
	// Every legacy draw is at most 200 ms, so a mean above it is the rejections' slowdown.
	if count == 0 || sum/count <= 0.2 {
		t.Fatalf("entry error-row mean latency %.3f s over %v observations, want the rejections' slowdown lifting it past the 200 ms legacy ceiling", sum/math.Max(count, 1), count)
	}
	t.Logf("entry error-row mean %.3f s over %v observations (healthy entry reference %.3f s)", sum/count, count, w.healthySpan[serverRefKey("intake-api")])

	w = causalApps(t, causalResources, intakeApp)[0]
	eng = incident(causal.ModeCredentialExpired, "partner-api-cert", 1)
	led = ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	batch := led.Mint(causalNow)
	attempts := 0
	for _, r := range batch {
		attempts += len(callsTo(r, "enrich-fn"))
	}
	mc = &coretest.MetricCapture{}
	world = coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var edge float64
	for _, s := range mc.Find("traces_service_graph_request_total") {
		if s.Labels["client"] == "intake-api" && s.Labels["server"] == "enrich-fn" {
			edge += s.Value
		}
	}
	volume := float64(w.spanMetricVolume(causalNow, world))
	want := volume * float64(attempts) / float64(len(batch))
	if math.Abs(edge-want) > 1e-6 || edge <= volume {
		t.Fatalf("intake-api -> enrich-fn edge volume %.2f, want %.2f (calls %v x %d attempts / %d requests)", edge, want, volume, attempts, len(batch))
	}
}

// The two value kinds (design §5.4) read the model and the node's window outcomes.
func TestCausalCustomMetricValues(t *testing.T) {
	doc := strings.Replace(intakeApp, `    call_policies:
      enrich-fn:`, `    custom_metrics:
      - name: records_latency_factor
        source: test fixture
        instrument: gauge
        value: {resource: {name: records-db, field: latency_factor}}
      - name: pool_rejected_total
        source: test fixture
        instrument: counter
        value: {resource: {name: intake-db-pool, field: rejected}}
      - name: intake_outcomes_total
        source: test fixture
        instrument: counter
        labels:
          code: {enum: [{value: "503"}, {value: success}]}
        value: {outcomes: {status_label: code}}
      - name: intake_503_share
        source: test fixture
        instrument: gauge
        value: {outcomes: {status: ["503"], measure: share}}
      - name: intake_503_seconds
        source: test fixture
        instrument: histogram
        buckets: [1, 5, 10]
        value: {outcomes: {status: ["503"], measure: latency}}
    call_policies:
      enrich-fn:`, 1)
	w := causalApps(t, causalResources, doc)[0]
	eng := incident(causal.ModeDependencySlow, "records-db", 1)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	batch := led.Mint(causalNow)
	var n503, nOK int
	for _, r := range batch {
		switch r.StatusCode {
		case 503:
			n503++
		case 200:
			nOK++
		}
	}
	if n503 == 0 {
		t.Fatal("no 503s minted")
	}
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	one := func(name string, match map[string]string) float64 {
		t.Helper()
		var v float64
		found := false
	next:
		for _, s := range mc.Find(name) {
			for k, want := range match {
				if s.Labels[k] != want {
					continue next
				}
			}
			v += s.Value
			found = true
		}
		if !found {
			t.Fatalf("%s %v not emitted", name, match)
		}
		return v
	}
	if got := one("records_latency_factor", nil); got != 12 {
		t.Errorf("records_latency_factor %v, want slow_factor 12", got)
	}
	if got := one("pool_rejected_total", nil); got <= 0 {
		t.Errorf("pool_rejected_total %v, want the window's rejections", got)
	}
	if got := one("intake_outcomes_total", map[string]string{"code": "503"}); got != float64(n503) {
		t.Errorf("intake_outcomes_total{code=503} %v, want %d", got, n503)
	}
	if got := one("intake_outcomes_total", map[string]string{"code": "success"}); got != float64(nOK) {
		t.Errorf("intake_outcomes_total{code=success} %v, want %d", got, nOK)
	}
	if got := one("intake_503_share", nil); math.Abs(got-float64(n503)/float64(len(batch))) > 1e-9 {
		t.Errorf("intake_503_share %v, want %v", got, float64(n503)/float64(len(batch)))
	}
	if got := one("intake_503_seconds_count", nil); got != float64(n503) {
		t.Errorf("intake_503_seconds_count %v, want one observation per 503 (%d)", got, n503)
	}
	if got := one("intake_503_seconds_sum", nil); got < 3*float64(n503) {
		t.Errorf("intake_503_seconds_sum %v, want at least 3 s per 503", got)
	}
}

// grpcEntryApp is loggedIntakeApp with a gRPC entry service.
func grpcEntryApp() string {
	return strings.Replace(loggedIntakeApp, "  - name: intake-api\n    entry: true\n", "  - name: intake-api\n    type: grpc\n    entry: true\n", 1)
}

// grpcCalleeApp is loggedIntakeApp with a gRPC enrich-fn.
func grpcCalleeApp() string {
	return strings.Replace(loggedIntakeApp, "    type: function\n", "    type: grpc\n", 1)
}

// A gRPC callee follows the gRPC span-status rule (semconv v1.44.0 gRPC server spans), not the HTTP
// one: a throttle answer (RESOURCE_EXHAUSTED) is not a SERVER error, so the span stays UNSET and
// carries no error.type; the caller's CLIENT span is ERROR on any failed answer. No hop to a gRPC
// node carries http.response.status_code.
func TestCausalProjectGRPCHopHasNoHTTPStatus(t *testing.T) {
	w := causalApps(t, causalResources, grpcCalleeApp())[0]
	eng := incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	seen := 0
	for _, r := range reqs {
		for _, c := range callsTo(r, "enrich-fn") {
			if c.StatusCode != 429 {
				continue
			}
			seen++
			cs, ss := p.spans[c.SpanID], p.spans[c.PeerSpanID]
			if _, has := cs.Attrs["http.response.status_code"]; has || cs.Status != otlp.StatusError || cs.Attrs["error.type"] != "429" {
				t.Fatalf("grpc client span: %v %v", cs.Status, cs.Attrs)
			}
			_, hasStatus := ss.Attrs["http.response.status_code"]
			_, hasType := ss.Attrs["error.type"]
			if hasStatus || hasType || ss.Status != otlp.StatusUnset || ss.StatusMsg != "" {
				t.Fatalf("grpc server span for a throttle: %v %v %q", ss.Status, ss.Attrs, ss.StatusMsg)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no throttled grpc calls minted")
	}
}

// A gRPC callee that answers 5xx (INTERNAL, UNAVAILABLE, ...) is a SERVER error: ERROR with
// error.type and still no HTTP status code.
func TestCausalProjectGRPCServerErrorForServerFailures(t *testing.T) {
	w := causalApps(t, causalResources, grpcCalleeApp())[0]
	eng := incident(causal.ModeCredentialExpired, "partner-api-cert", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	seen := 0
	for _, r := range reqs {
		for _, c := range callsTo(r, "enrich-fn") {
			if c.StatusCode < 500 || c.PeerSpanID == "" {
				continue
			}
			seen++
			ss := p.spans[c.PeerSpanID]
			_, hasStatus := ss.Attrs["http.response.status_code"]
			if hasStatus || ss.Status != otlp.StatusError || ss.Attrs["error.type"] == nil {
				t.Fatalf("grpc server span for %d: %v %v", c.StatusCode, ss.Status, ss.Attrs)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no 5xx grpc calls minted")
	}
}

// A call that timed out against a gRPC callee is DEADLINE_EXCEEDED on its SERVER span, an error;
// the same timeout against an HTTP callee leaves the span UNSET (the server never saw the caller
// give up, http-spans note [4]).
func TestCausalProjectGRPCTimeoutIsServerError(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want otlp.StatusCode
	}{
		{"grpc", grpcCalleeApp(), otlp.StatusError},
		{"http", loggedIntakeApp, otlp.StatusUnset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := causalApps(t, causalResources, tc.doc)[0]
			eng := incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 1)
			reqs := mintAt(w, eng, causalNow)
			var timed *ledger.Request
			var idx int
			for _, r := range reqs {
				for i, c := range r.Calls {
					if c.Target == "enrich-fn" && c.PeerSpanID != "" && timed == nil {
						timed, idx = r, i
					}
				}
			}
			if timed == nil {
				t.Fatal("no enrich-fn call with a server span minted")
			}
			c := &timed.Calls[idx]
			c.Failed, c.TimedOut, c.StatusCode = true, true, 0
			c.ServerDuration = c.Duration * 2
			c.Fault = &ledger.Fault{ErrorType: "timeout"}
			timed.Fault = c.Fault
			timed.Outcome, timed.StatusCode = ledger.OutcomeServerError, 504
			p := project(t, w, eng, []*ledger.Request{timed})
			ss := p.spans[c.PeerSpanID]
			if ss.Status != tc.want {
				t.Fatalf("timed-out server span status %v, want %v (attrs %v)", ss.Status, tc.want, ss.Attrs)
			}
			if tc.want == otlp.StatusError && ss.Attrs["error.type"] != "timeout" {
				t.Fatalf("timed-out grpc server span error.type %v", ss.Attrs["error.type"])
			}
			if cs := p.spans[c.SpanID]; cs.Status != otlp.StatusError {
				t.Fatalf("timed-out client span %v", cs.Status)
			}
		})
	}
}

// A gRPC entry's SERVER root follows the same rule: a pool's 503 (UNAVAILABLE) is an error, a rule
// rejection (INVALID_ARGUMENT, mapped 422) is not; neither carries an HTTP status code.
func TestCausalProjectGRPCEntryRootStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode, res string
		status    int
		want      otlp.StatusCode
	}{
		{"pool 503", causal.ModeDependencySlow, "records-db", 503, otlp.StatusError},
		{"rule 422", causal.ModeRuleRegression, "completeness-rule", 422, otlp.StatusUnset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := causalApps(t, causalResources, grpcEntryApp())[0]
			eng := incident(tc.mode, tc.res, 1)
			reqs := mintAt(w, eng, causalNow)
			p := project(t, w, eng, reqs)
			seen := 0
			for _, r := range reqs {
				if r.Fault == nil || r.StatusCode != tc.status {
					continue
				}
				seen++
				root := p.spans[r.SpanID]
				_, hasStatus := root.Attrs["http.response.status_code"]
				if hasStatus || root.Status != tc.want {
					t.Fatalf("grpc root for %d: status %v attrs %v", tc.status, root.Status, root.Attrs)
				}
				if _, hasType := root.Attrs["error.type"]; hasType != (tc.want == otlp.StatusError) {
					t.Fatalf("grpc root for %d: error.type presence %v, attrs %v", tc.status, hasType, root.Attrs)
				}
			}
			if seen == 0 {
				t.Fatalf("no %d requests minted", tc.status)
			}
		})
	}
}

// Only HTTP kinds stamp http.response.status_code: a worker, stream or job callee answers
// over its own protocol. A capacity refusal is not a handled business-rule rejection: non-HTTP
// operations report ERROR, while an HTTP callee retains its 4xx SERVER rule and stamps the code.
func TestCausalProjectNonHTTPKindsCarryNoHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		http bool
	}{
		{"function", true}, {"web", true}, {"gateway", true}, {"llm", true},
		{"worker", false}, {"stream", false}, {"job", false},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			doc := strings.Replace(loggedIntakeApp, "    type: function\n", "    type: "+tc.typ+"\n", 1)
			w := causalApps(t, causalResources, doc)[0]
			eng := incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 1)
			reqs := mintAt(w, eng, causalNow)
			p := project(t, w, eng, reqs)
			seen := 0
			for _, r := range reqs {
				for _, c := range callsTo(r, "enrich-fn") {
					if c.StatusCode != 429 || c.PeerSpanID == "" {
						continue
					}
					seen++
					for _, sp := range []otlp.Span{p.spans[c.SpanID], p.spans[c.PeerSpanID]} {
						if _, has := sp.Attrs["http.response.status_code"]; has != tc.http {
							t.Fatalf("%s callee span %q: http.response.status_code present=%v, want %v (%v)", tc.typ, sp.Name, has, tc.http, sp.Attrs)
						}
					}
					wantServer := otlp.StatusError
					if tc.http {
						wantServer = otlp.StatusUnset
					}
					cs, ss := p.spans[c.SpanID], p.spans[c.PeerSpanID]
					if cs.Status != otlp.StatusError || ss.Status != wantServer {
						t.Fatalf("%s callee statuses: client %v server %v", tc.typ, cs.Status, ss.Status)
					}
					if _, has := ss.Attrs[attrErrorType]; has != !tc.http {
						t.Fatalf("%s callee error.type presence %v", tc.typ, has)
					}
				}
			}
			if seen == 0 {
				t.Fatal("no throttled calls minted")
			}
		})
	}
}

// Faulted requests project their per-attempt log lines from the request's hash unit, like resend
// spans, so the engine's later draws do not depend on how many attempts a fault produced (design
// §4.1): the projection takes none, and the values are deterministic per line.
func TestCausalProjectionLogsTakeNoEngineDraws(t *testing.T) {
	doc := strings.ReplaceAll(loggedIntakeApp, "          error_code: {ref: error_code}", "          error_code: {ref: error_code}\n          jitter: {float_range: {min: 0, max: 1}}")
	w := causalApps(t, causalResources, doc)[0]
	reqs := mintAt(w, incident(causal.ModeCredentialExpired, "partner-api-cert", 1), causalNow)
	var failing []*ledger.Request
	attempts := 0
	for _, r := range reqs {
		if causalFailing(r) {
			failing = append(failing, r)
			for _, c := range r.Calls {
				if c.Attempt >= 1 {
					attempts++
				}
			}
		}
	}
	if len(failing) == 0 || attempts == 0 {
		t.Fatalf("minted %d failing requests with %d resend attempts", len(failing), attempts)
	}
	logsOf := func() ([]loki.Stream, float64) {
		eng := shape.New("UTC", nil)
		lc := &coretest.LogCapture{}
		world := coretest.World(nil, lc, nil)
		world.Shape = eng
		if err := w.projectLogs(context.Background(), world, failing); err != nil {
			t.Fatalf("projectLogs: %v", err)
		}
		return lc.Streams, eng.Float64()
	}
	a, next := logsOf()
	if want := shape.New("UTC", nil).Float64(); next != want {
		t.Fatalf("faulted log projection took engine draws: next draw %v, untouched engine %v", next, want)
	}
	b, _ := logsOf()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("faulted log values are not deterministic across projections")
	}
	jitters := map[string]bool{}
	lines := 0
	for _, st := range a {
		for _, ln := range st.Lines {
			var body map[string]any
			if err := json.Unmarshal([]byte(ln.Body), &body); err != nil {
				t.Fatalf("log body %q: %v", ln.Body, err)
			}
			j, ok := body["jitter"].(float64)
			if !ok {
				t.Fatalf("log body %q carries no jitter value", ln.Body)
			}
			jitters[strconv.FormatFloat(j, 'g', -1, 64)] = true
			lines++
		}
	}
	if len(jitters) < lines/2 {
		t.Fatalf("%d lines share only %d distinct jitter values; each line draws its own", lines, len(jitters))
	}
}

// loggedLegacyIntakeApp is loggedIntakeApp without any causal field.
var loggedLegacyIntakeApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: intake-api
    entry: true
    calls: [records-db, enrich-fn, validation-publisher]` + causalLogBody + `
  - {name: records-db, type: db}
  - name: enrich-fn
    type: function
    external: true
    calls: [partner-gateway]` + causalLogBody + `
  - name: partner-gateway
    external: true` + causalLogBody + `
  - {name: validation-publisher}
`

// Design §10.5 (root ruling D85): an opted blueprint with no active fault and the default 1%
// baseline error rate projects its app logs, root span status and attributes, and span-metric
// latency exactly as the same app without resources. A baseline failure carries no Fault, so it
// is not causal.
func TestCausalOptedHealthyMatchesLegacy(t *testing.T) {
	opted := causalApps(t, causalResources, loggedIntakeApp)[0]
	// The same binding as causalApps, so seeded correlation ids agree.
	lw, err := build(decodeStrict(t, loggedLegacyIntakeApp), core.Binding{Name: "appa", Env: &fixture.Env{Name: "prod", Weight: 1}, Cluster: coretest.Cluster()})
	if err != nil {
		t.Fatalf("build legacy: %v", err)
	}
	legacy := lw.(*Workload)
	type run struct {
		reqs  []*ledger.Request
		p     projected
		lat   []string
		after float64
	}
	do := func(w *Workload) run {
		eng := shape.New("UTC", nil)
		led := ledger.New(eng, 0, 0)
		led.AddMinter(w.Minter())
		reqs := led.Mint(causalNow)
		p := project(t, w, eng, reqs)
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape, world.Ledger = eng, led
		if err := w.Tick(context.Background(), causalNow, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		var lat []string
		for _, name := range []string{"traces_spanmetrics_latency_sum", "traces_spanmetrics_latency_count", "traces_service_graph_request_server_seconds_sum"} {
			for _, s := range mc.Find(name) {
				lat = append(lat, name+fmtLabels(s.Labels)+"="+strconv.FormatFloat(s.Value, 'g', -1, 64))
			}
		}
		sort.Strings(lat)
		return run{reqs: reqs, p: p, lat: lat, after: eng.Float64()}
	}
	a, b := do(opted), do(legacy)
	if len(a.reqs) != len(b.reqs) {
		t.Fatalf("minted %d vs %d", len(a.reqs), len(b.reqs))
	}
	failed := 0
	for i, r := range a.reqs {
		if r.Fault != nil {
			t.Fatalf("healthy opted request carries %+v", r.Fault)
		}
		if r.Outcome != ledger.OutcomeSuccess {
			failed++
		}
		ra, rb := a.p.spans[r.SpanID], b.p.spans[b.reqs[i].SpanID]
		if ra.Status != rb.Status || !reflect.DeepEqual(maskIDs(ra.Attrs), maskIDs(rb.Attrs)) || ra.StatusMsg != rb.StatusMsg {
			t.Fatalf("root span differs: opted %v %v vs legacy %v %v", ra.Status, ra.Attrs, rb.Status, rb.Attrs)
		}
	}
	if failed == 0 {
		t.Fatal("no baseline failures minted")
	}
	if !reflect.DeepEqual(logShape(a.p.logs), logShape(b.p.logs)) {
		t.Fatal("opted-healthy app logs differ from the app without resources")
	}
	if !reflect.DeepEqual(a.lat, b.lat) {
		for i := range min(len(a.lat), len(b.lat)) {
			if a.lat[i] != b.lat[i] {
				t.Logf("first diff %d:\n opted  %s\n legacy %s", i, a.lat[i], b.lat[i])
				break
			}
		}
		t.Fatalf("opted-healthy span-metric latency differs from the app without resources (%d vs %d series)", len(a.lat), len(b.lat))
	}
	if a.after != b.after {
		t.Fatal("engine draws diverged")
	}
}

func fmtLabels(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("," + k + "=" + l[k])
	}
	return "{" + b.String() + "}"
}

// staticMinter hands the ledger a prepared batch once.
type staticMinter struct {
	name  string
	batch []*ledger.Request
}

func (m *staticMinter) Workload() string { return m.name }
func (m *staticMinter) Mint(time.Time, float64, *shape.Engine) []*ledger.Request {
	b := m.batch
	m.batch = nil
	return b
}

// Under a fault with retries, a gated span-metric window takes exactly the legacy path's engine
// draws (the same requests marked legacy), and resend spans' DSL values draw nothing from the
// engine (design §4.1): only the first attempts' spans do.
func TestCausalProjectionKeepsEngineDraws(t *testing.T) {
	doc := strings.Replace(intakeApp, `    type: function
    external: true
`, `    type: function
    external: true
    spans:
      - name_template: enrich
        attributes:
          jitter: {float_range: {min: 0, max: 1}}
`, 1)
	w := causalApps(t, causalResources, doc)[0]
	reqs := mintAt(w, incident(causal.ModeCredentialExpired, "partner-api-cert", 1), causalNow)
	resends := 0
	var legacyCopy []*ledger.Request
	for _, r := range reqs {
		cp := *r
		cp.Causal = false
		legacyCopy = append(legacyCopy, &cp)
		for _, c := range r.Calls {
			if c.Attempt >= 1 {
				resends++
			}
		}
	}
	if resends == 0 {
		t.Fatal("no resends minted")
	}

	tick := func(batch []*ledger.Request) float64 {
		eng := shape.New("UTC", nil)
		led := ledger.New(eng, 0, 0)
		led.AddMinter(&staticMinter{name: w.Name(), batch: batch})
		led.Mint(causalNow)
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape, world.Ledger = eng, led
		if err := w.Tick(context.Background(), causalNow, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return eng.Float64()
	}
	if got, want := tick(reqs), tick(legacyCopy); got != want {
		t.Fatalf("gated span metrics shifted the engine: next draw %v, legacy path %v", got, want)
	}

	// Traces: one engine draw per enrich-fn SERVER span off a resend path.
	firsts := 0
	for _, r := range reqs {
		for _, c := range r.Calls {
			if c.Target == "enrich-fn" && c.PeerSpanID != "" && c.Attempt == 0 {
				firsts++
			}
		}
	}
	eng := shape.New("UTC", nil)
	tc := &coretest.TraceCapture{}
	world := coretest.World(nil, nil, tc)
	world.Shape = eng
	if err := w.ProjectBatch(context.Background(), causalNow, world, reqs); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	ref := shape.New("UTC", nil)
	for range firsts {
		ref.Float64()
	}
	if got, want := eng.Float64(), ref.Float64(); got != want {
		t.Fatalf("trace projection took a different number of engine draws than the %d first-attempt spans", firsts)
	}
}

// maskIDs drops the per-request random correlation ids (the mint path's ids are not seeded).
func maskIDs(attrs map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range attrs {
		switch k {
		case "app.correlation_id", "request_id", "session_id":
			continue
		}
		out[k] = v
	}
	return out
}

// logShape is a stream set with per-request ids and timestamps masked: stream labels, each line's
// body and its structured-metadata keys.
func logShape(streams []loki.Stream) []string {
	var out []string
	for _, st := range streams {
		for _, ln := range st.Lines {
			keys := make([]string, 0, len(ln.Meta))
			for k := range ln.Meta {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out = append(out, fmtLabels(st.Labels)+" "+ln.Body+" "+strings.Join(keys, ","))
		}
	}
	return out
}
