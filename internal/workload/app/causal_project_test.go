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
	spans map[string]otlp.Span   // by span id
	owner map[string]string      // span id -> service.name of its resource
	lines map[string][]logLine   // service_name -> lines in emission order
	all   []otlp.Resource        // raw resources
	logs  []loki.Stream          // raw streams
	byReq map[string][]otlp.Span // trace id -> spans
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
	p := projected{spans: map[string]otlp.Span{}, owner: map[string]string{}, lines: map[string][]logLine{}, all: tc.Resources, logs: lc.Streams, byReq: map[string][]otlp.Span{}}
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
			p.lines[svc] = append(p.lines[svc], logLine{level: st.Labels["level"], t: ln.T, body: body, meta: ln.Meta})
		}
	}
	return p
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
	var out []logLine
	for _, l := range p.lines[svc] {
		if ids[l.field("span_id")] {
			out = append(out, l)
		}
	}
	return out
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

// A healthy causal request (no fault, nothing touched) projects exactly as the same request
// marked legacy: spans and logs are byte-identical (design §4.6 M6).
func TestCausalProjectHealthyIdentity(t *testing.T) {
	w := causalApps(t, causalResources, loggedIntakeApp)[0]
	reqs := mintAt(w, shape.New("UTC", nil), causalNow)
	var healthy, legacy []*ledger.Request
	for _, r := range reqs {
		if !r.Causal || r.Fault != nil || r.Outcome != ledger.OutcomeSuccess {
			continue
		}
		healthy = append(healthy, r)
		cp := *r
		cp.Causal = false
		legacy = append(legacy, &cp)
	}
	if len(healthy) == 0 {
		t.Fatal("no healthy causal requests")
	}
	a := project(t, w, shape.New("UTC", nil), healthy)
	b := project(t, w, shape.New("UTC", nil), legacy)
	if !reflect.DeepEqual(a.all, b.all) {
		t.Fatal("healthy causal spans differ from their legacy projection")
	}
	if !reflect.DeepEqual(a.logs, b.logs) {
		t.Fatal("healthy causal logs differ from their legacy projection")
	}
}

// Span metrics in a faulted window: the entry's error row observes the real request durations
// (pool rejections last at least the 3 s acquire timeout), not the 20 to 200 ms legacy draw; and
// a retried edge counts its attempts.
func TestCausalSpanMetricsFollowHops(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	eng := incident(causal.ModeDependencySlow, "records-db", 1)
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	led.Mint(causalNow)
	mc := &coretest.MetricCapture{}
	world := coretest.World(mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var sum, count float64
	for _, s := range mc.Find("traces_spanmetrics_latency_sum") {
		if s.Labels["service_name"] == "intake-api" && s.Labels["span_kind"] == spanKindServer && s.Labels["status_code"] == statusCodeError {
			sum += s.Value
		}
	}
	for _, s := range mc.Find("traces_spanmetrics_latency_count") {
		if s.Labels["service_name"] == "intake-api" && s.Labels["span_kind"] == spanKindServer && s.Labels["status_code"] == statusCodeError {
			count += s.Value
		}
	}
	if count == 0 || sum/count < 1 {
		t.Fatalf("entry error-row mean latency %.3f s over %v observations, want the real (>= 3 s) rejection durations", sum/math.Max(count, 1), count)
	}

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

// A gRPC callee follows the generic span-status rule, not the HTTP one: its SERVER span is ERROR
// for any failed answer and carries error.type, and no hop to it carries http.response.status_code.
func TestCausalProjectGRPCHopHasNoHTTPStatus(t *testing.T) {
	doc := strings.Replace(loggedIntakeApp, "    type: function\n", "    type: grpc\n", 1)
	w := causalApps(t, causalResources, doc)[0]
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
			if _, has := ss.Attrs["http.response.status_code"]; has || ss.Status != otlp.StatusError || ss.Attrs["error.type"] != "TooManyRequestsException" {
				t.Fatalf("grpc server span: %v %v", ss.Status, ss.Attrs)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no throttled grpc calls minted")
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
