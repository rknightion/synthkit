// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// The causal request model end to end (internal/causal; codex design §8 lane L11, acceptance 1):
// e2e/fixtures/causal-stories.yaml declares resources and one scheduled fault per story; each test
// loads it through the real loader and catalog, runs one minute of master ticks and a RunOnce
// cycle through the runner into capture sinks, and checks the failing node, its callers and the
// bound constructs against each other in that one window.

const causalStoriesPath = "../../e2e/fixtures/causal-stories.yaml"

// storyDay is the fixture's incident day; each story runs the minute ending at a time inside its
// window, and healthy runs the minute ending at 08:30, before any incident.
var storyDay = time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

func storyAt(hhmm string) time.Time {
	d, err := time.ParseDuration(strings.Replace(hhmm, ":", "h", 1) + "m")
	if err != nil {
		panic(err)
	}
	return storyDay.Add(d)
}

// span is one captured span with the service that emitted it.
type span struct {
	otlp.Span
	service string
}

// storyLine is one captured log line of an app service.
type storyLine struct {
	service, level string
	t              time.Time
	body           map[string]string
}

type storyRun struct {
	t       *testing.T
	end     time.Time
	mc      *coretest.MetricCapture
	spans   map[string]span   // by span id
	traces  map[string][]span // by trace id
	lines   []storyLine
	byOwner map[string][]storyLine // log lines by the span id they carry
}

// runStory runs the minute (end-60s, end]: master ticks at end-55s..end-5s and RunOnce at end,
// twelve 5 s mints, with span metrics enabled for the blueprint.
func runStory(t *testing.T, end time.Time) *storyRun {
	t.Helper()
	data, err := os.ReadFile(causalStoriesPath)
	if err != nil {
		t.Fatal(err)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	mc, lc, tc := &coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{}
	r := runner.New(runner.Sinks{Metrics: mc, Logs: lc, Traces: tc}, reg, runner.Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	cs := control.DefaultState()
	cs.SpanMetricsBlueprints = []string{res.Name}
	r.ApplyControl(cs)
	ctx := context.Background()
	for i := 11; i >= 1; i-- {
		if err := r.MasterTick(ctx, end.Add(-time.Duration(i)*5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RunOnce(ctx, end); err != nil {
		t.Fatal(err)
	}
	run := &storyRun{t: t, end: end, mc: mc, spans: map[string]span{}, traces: map[string][]span{}, byOwner: map[string][]storyLine{}}
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		for _, sp := range res.Spans {
			s := span{Span: sp, service: svc}
			run.spans[sp.SpanID] = s
			run.traces[sp.TraceID] = append(run.traces[sp.TraceID], s)
		}
	}
	for _, st := range lc.Streams {
		svc := st.Labels["service_name"]
		if svc == "" {
			continue
		}
		for _, ln := range st.Lines {
			var raw map[string]any
			if err := json.Unmarshal([]byte(ln.Body), &raw); err != nil {
				continue // not an app body
			}
			body := map[string]string{}
			for k, v := range raw {
				if s, ok := v.(string); ok {
					body[k] = s
				}
			}
			for k, v := range ln.Meta {
				body[k] = v
			}
			l := storyLine{service: svc, level: st.Labels["level"], t: ln.T, body: body}
			run.lines = append(run.lines, l)
			run.byOwner[body["span_id"]] = append(run.byOwner[body["span_id"]], l)
		}
	}
	return run
}

// metric sums every sample of name whose labels include match.
func (r *storyRun) metric(name string, match map[string]string) (float64, bool) {
	var v float64
	found := false
next:
	for _, s := range r.mc.Find(name) {
		for k, want := range match {
			if s.Labels[k] != want {
				continue next
			}
		}
		v += s.Value
		found = true
	}
	return v, found
}

// parent is a span's parent in the same trace.
func (r *storyRun) parent(s span) (span, bool) {
	p, ok := r.spans[s.ParentID]
	return p, ok && p.TraceID == s.TraceID
}

// root is the root span of a trace.
func (r *storyRun) root(traceID string) (span, bool) {
	for _, s := range r.traces[traceID] {
		if s.ParentID == "" {
			return s, true
		}
	}
	return span{}, false
}

// where lists spans matching keep, sorted by start for stable failure output.
func (r *storyRun) where(keep func(span) bool) []span {
	var out []span
	for _, s := range r.spans {
		if keep(s) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Start.Before(out[j].Start) || out[i].Start.Equal(out[j].Start) && out[i].SpanID < out[j].SpanID
	})
	return out
}

func attrStr(s span, k string) (string, bool) {
	v, ok := s.Attrs[k]
	if !ok {
		return "", false
	}
	return fmt.Sprint(v), true
}

func (r *storyRun) linesOf(spanID string) []storyLine { return r.byOwner[spanID] }

func (r *storyRun) servicesIn(traceID string) map[string]int {
	out := map[string]int{}
	for _, s := range r.traces[traceID] {
		out[s.service]++
	}
	return out
}

// sameCount fails unless app and construct counts agree within four binomial standard deviations
// (2 x sqrt(n) bounds 4 x sqrt(n p (1-p)) for any share p), and at least one event: the app mints
// whole requests and draws each one's outcome, while the model's window sums the fluid volumes of
// the same constant rates, so the two agree in expectation (codex design §5.6 "counts agree").
func sameCount(t *testing.T, what string, app, construct float64) {
	t.Helper()
	if tol := math.Max(1, 2*math.Sqrt(math.Max(app, construct))); math.Abs(app-construct) > tol || construct <= 0 {
		t.Errorf("%s: app %v, construct %v; want them within %.0f", what, app, construct, tol)
	}
}

func (r *storyRun) mustMetric(name string, match map[string]string) float64 {
	r.t.Helper()
	v, ok := r.metric(name, match)
	if !ok {
		r.t.Fatalf("%s %v not emitted", name, match)
	}
	return v
}

// spanMetricErrors sums traces_spanmetrics_calls_total ERROR rows of one service and span kind.
func (r *storyRun) spanMetricErrors(service, kind string) float64 {
	v, _ := r.metric("traces_spanmetrics_calls_total", map[string]string{"service_name": service, "span_kind": kind, "status_code": "STATUS_CODE_ERROR"})
	return v
}

// statusCount is the entry's http_server_request_duration_seconds observations with one status.
func (r *storyRun) statusCount(code string) float64 {
	v, _ := r.metric("http_server_request_duration_seconds_count", map[string]string{"http_response_status_code": code})
	return v
}

// upstreamGated checks that an upstream failure that stops intake's calls also lowers the bound
// constructs' volume (SKT-0168.09.33: a bound resource's demand follows the admitted calls of the
// path that reaches it): Lambda executions are intake's calls to enrich-fn and SQS sends are the
// validation-publisher's spans.
func upstreamGated(t *testing.T, r *storyRun) {
	t.Helper()
	calls := float64(len(r.where(func(s span) bool { return s.service == "intake-api" && s.Name == "call enrich-fn" })))
	inv, _ := r.metric("aws_lambda_invocations_sum", nil)
	thr, _ := r.metric("aws_lambda_throttles_sum", nil)
	sameCount(t, "intake calls to enrich-fn vs Lambda Invocations+Throttles", calls, inv+thr)
	publishes := float64(len(r.where(func(s span) bool { return s.service == "validation-publisher" && s.Kind == otlp.KindServer })))
	sent, _ := r.metric("aws_sqs_number_of_messages_sent_sum", nil)
	if publishes == 0 && sent == 0 {
		return
	}
	sameCount(t, "validation-publisher spans vs SQS NumberOfMessagesSent", publishes, sent)
}

func meanDuration(spans []span) time.Duration {
	if len(spans) == 0 {
		return 0
	}
	var sum time.Duration
	for _, s := range spans {
		sum += s.End.Sub(s.Start)
	}
	return sum / time.Duration(len(spans))
}

const (
	certMessage = "partner-api: PKIX path validation failed: certificate expired"
	poolMessage = "intake-db-pool: connection is not available, request timed out after 3000ms"
	ruleMessage = "rule completeness rejected record: mandatory field missing"
)

// TestCausalStories runs each story's window and a healthy window before any incident, and checks
// acceptance 1 of the causal model: the declared fault is visible and consistent in the traces,
// logs and metrics of the failing node and its callers, with an error message naming the cause,
// and the bound Lambda, SQS and Solace series move in the same window with counts that agree.
// Span status follows M10 (codex design §5.1): a SERVER span is ERROR only for a 5xx, a CLIENT
// span for any 4xx, 5xx or transport failure, and the cause message rides an ERROR origin span;
// a 4xx origin (a throttle, a rule) keeps its SERVER span UNSET and the message rides its log line.
func TestCausalStories(t *testing.T) {
	healthy := runStory(t, storyAt("08:30"))
	enrichCalls := func(r *storyRun) []span {
		return r.where(func(s span) bool { return s.service == "intake-api" && s.Name == "call enrich-fn" })
	}

	t.Run("healthy baseline", func(t *testing.T) {
		r := healthy
		for _, s := range r.spans {
			if s.StatusMsg != "" {
				t.Fatalf("healthy window: span %s/%s carries a status message %q", s.service, s.Name, s.StatusMsg)
			}
		}
		if v, _ := r.metric("aws_lambda_throttles_sum", nil); v != 0 {
			t.Errorf("healthy Lambda throttles %v", v)
		}
		if v, _ := r.metric("aws_lambda_errors_sum", nil); v != 0 {
			t.Errorf("healthy Lambda errors %v", v)
		}
		if v := r.mustMetric("aws_sqs_approximate_number_of_messages_visible_maximum", nil); v != 0 {
			t.Errorf("healthy SQS backlog %v", v)
		}
		if v := r.mustMetric("solace_queue_spool_usage_msgs", map[string]string{"queue_name": "partner-feed-q"}); v != 0 {
			t.Errorf("healthy Solace depth %v", v)
		}
		// Every request reaches the function and the publisher, and the validator consumes the
		// queue's served rate: each app count equals its construct's.
		sameCount(t, "intake calls to enrich-fn vs Lambda Invocations+Throttles", float64(len(enrichCalls(r))),
			r.mustMetric("aws_lambda_invocations_sum", nil))
		publishes := r.where(func(s span) bool { return s.service == "validation-publisher" && s.Kind == otlp.KindServer })
		sameCount(t, "validation-publisher spans vs SQS NumberOfMessagesSent", float64(len(publishes)), r.mustMetric("aws_sqs_number_of_messages_sent_sum", nil))
		consumed := r.where(func(s span) bool { return s.service == "validation-worker" && s.Kind == otlp.KindConsumer })
		sameCount(t, "validation-worker requests vs SQS NumberOfMessagesReceived", float64(len(consumed)), r.mustMetric("aws_sqs_number_of_messages_received_sum", nil))
		if v := r.spanMetricErrors("enrich-fn", "SPAN_KIND_SERVER"); v != 0 {
			t.Errorf("healthy enrich-fn span-metric errors %v", v)
		}
	})

	t.Run("story 1 dependency TLS certificate expired", func(t *testing.T) {
		r := runStory(t, storyAt("09:05"))
		origins := r.where(func(s span) bool { return s.StatusMsg == certMessage })
		handshakes := r.where(func(s span) bool { return s.service == "enrich-fn" && s.Name == "call partner-gateway" })
		if len(origins) == 0 || len(origins) != len(handshakes) {
			t.Fatalf("%d spans carry the certificate message, %d enrich-fn calls to partner-gateway; want every handshake to fail with it", len(origins), len(handshakes))
		}
		for _, o := range origins {
			// The origin: enrich-fn's CLIENT span (a transport failure, no server span, no status).
			if o.service != "enrich-fn" || o.Kind != otlp.KindClient || o.Status != otlp.StatusError {
				t.Fatalf("origin %s %s kind %v status %v", o.service, o.Name, o.Kind, o.Status)
			}
			if et, _ := attrStr(o, "error.type"); et != "SSLHandshakeException" {
				t.Fatalf("origin error.type %q", et)
			}
			if _, has := o.Attrs["http.response.status_code"]; has {
				t.Fatalf("a TLS handshake failure carries an HTTP status: %v", o.Attrs)
			}
			// Callers: enrich-fn answers 502, intake-api's call sees 502 and answers 502.
			fn, ok := r.parent(o)
			if !ok || fn.service != "enrich-fn" || fn.Kind != otlp.KindServer || fn.Status != otlp.StatusError || fn.StatusMsg != "" {
				t.Fatalf("enrich-fn server span %+v", fn.Span)
			}
			call, ok := r.parent(fn)
			if !ok || call.Name != "call enrich-fn" || call.Status != otlp.StatusError {
				t.Fatalf("intake-api call span %+v", call.Span)
			}
			root, ok := r.parent(call)
			if !ok || root.ParentID != "" || root.Status != otlp.StatusError || root.StatusMsg != "" {
				t.Fatalf("intake-api root %+v", root.Span)
			}
			for _, s := range []span{fn, call, root} {
				code, _ := attrStr(s, "http.response.status_code")
				et, _ := attrStr(s, "error.type")
				if code != "502" || et != "502" {
					t.Fatalf("%s %s: status_code %q error.type %q, want the mapped 502", s.service, s.Name, code, et)
				}
			}
			if n := r.servicesIn(o.TraceID)["partner-gateway"]; n != 0 {
				t.Fatalf("partner-gateway emitted %d spans for a request that never reached it", n)
			}
			// Logs: the failing node's line carries the cause message, its own span id and the
			// exception class; the entry's line names the same cause; the unreached node is silent.
			fl := r.linesOf(fn.SpanID)
			if len(fl) != 1 || fl[0].service != "enrich-fn" || fl[0].level != "error" || fl[0].body["error_message"] != certMessage ||
				fl[0].body["error_type"] != "SSLHandshakeException" || fl[0].body["exception_type"] != "SSLHandshakeException" ||
				fl[0].body["status"] != "502" || fl[0].body["fault_resource"] != "partner-api-cert" || fl[0].body["fault_mode"] != "credential_expired" ||
				fl[0].body["upstream"] != "partner-gateway" {
				t.Fatalf("enrich-fn log lines %+v", fl)
			}
			el := r.linesOf(root.SpanID)
			if len(el) != 1 || el[0].level != "error" || el[0].body["status"] != "502" || el[0].body["error_message"] != certMessage ||
				el[0].body["error_type"] != "502" || el[0].body["upstream"] != "enrich-fn" || el[0].body["fault_resource"] != "partner-api-cert" {
				t.Fatalf("intake-api log lines %+v", el)
			}
		}
		for _, l := range r.lines {
			if l.service == "partner-gateway" {
				t.Fatalf("unreached partner-gateway logged %+v", l)
			}
		}
		// Metrics: the entry's status histogram and span metrics show the 502s; the bound function
		// fails every execution it admits (the certificate is held across each one) in the same
		// window, and its executions are exactly intake's calls.
		if got := r.statusCount("502"); got != float64(len(origins)) {
			t.Errorf("http_server_request_duration_seconds_count{502} %v, want %d", got, len(origins))
		}
		if r.spanMetricErrors("enrich-fn", "SPAN_KIND_SERVER") <= 0 || r.spanMetricErrors("intake-api", "SPAN_KIND_CLIENT") <= 0 {
			t.Error("span metrics show no enrich-fn server or intake-api client errors")
		}
		inv, errs := r.mustMetric("aws_lambda_invocations_sum", nil), r.mustMetric("aws_lambda_errors_sum", nil)
		if errs != inv {
			t.Errorf("Lambda Errors %v of %v Invocations, want every execution failed", errs, inv)
		}
		sameCount(t, "intake calls to enrich-fn vs Lambda Invocations", float64(len(enrichCalls(r))), inv)
		sameCount(t, "certificate failures vs Lambda Errors", float64(len(origins)), errs)
		// No request reached the publisher, and SQS received nothing from it.
		if sent, _ := r.metric("aws_sqs_number_of_messages_sent_sum", nil); sent != 0 {
			t.Errorf("SQS NumberOfMessagesSent %v while every request failed before publishing", sent)
		}
		upstreamGated(t, r)
	})

	t.Run("story 2 function reserved-concurrency throttling", func(t *testing.T) {
		r := runStory(t, storyAt("09:25"))
		throttled := r.where(func(s span) bool {
			code, _ := attrStr(s, "http.response.status_code")
			return s.service == "enrich-fn" && s.Kind == otlp.KindServer && code == "429"
		})
		if len(throttled) == 0 {
			t.Fatal("no throttled enrich-fn executions")
		}
		for _, s := range throttled {
			// M10: the 429 answer keeps the SERVER span UNSET; the caller's CLIENT span is ERROR.
			code, _ := attrStr(s, "http.response.status_code")
			if s.Status != otlp.StatusUnset || code != "429" || s.StatusMsg != "" {
				t.Fatalf("throttled server span status %v code %q msg %q", s.Status, code, s.StatusMsg)
			}
			call, ok := r.parent(s)
			ccode, _ := attrStr(call, "http.response.status_code")
			cet, _ := attrStr(call, "error.type")
			if !ok || call.Name != "call enrich-fn" || call.Status != otlp.StatusError || ccode != "429" || cet != "429" {
				t.Fatalf("caller span of a throttle %+v", call.Span)
			}
			ls := r.linesOf(s.SpanID)
			rc, _ := attrStr(call, "http.request.resend_count")
			if rc == "" {
				rc = "0"
			}
			if len(ls) != 1 || ls[0].body["status"] != "429" || ls[0].body["error_message"] != "Rate Exceeded." ||
				ls[0].body["exception_type"] != "TooManyRequestsException" || ls[0].body["error_type"] != "TooManyRequestsException" ||
				ls[0].body["fault_resource"] != "enrich-fn-concurrency" || ls[0].body["attempt"] != rc {
				t.Fatalf("throttled enrich-fn log lines %+v (attempt %s)", ls, rc)
			}
		}
		// Retries: each request's calls to enrich-fn are attempts 0..k, carrying
		// http.request.resend_count from 1, each starting after the previous attempt's end plus
		// the 200 ms x 2^(k-1) backoff. A request whose three attempts all throttle answers 503.
		byTrace := map[string][]span{}
		for _, c := range enrichCalls(r) {
			byTrace[c.TraceID] = append(byTrace[c.TraceID], c)
		}
		var resent, exhausted, recovered int
		for traceID, calls := range byTrace {
			sort.Slice(calls, func(i, j int) bool { return calls[i].Start.Before(calls[j].Start) })
			for k, c := range calls {
				rc, has := attrStr(c, "http.request.resend_count")
				if k == 0 && has || k > 0 && rc != strconv.Itoa(k) {
					t.Fatalf("attempt %d resend_count %q (present %v)", k, rc, has)
				}
				if k > 0 {
					resent++
					backoff := time.Duration(200*math.Pow(2, float64(k-1))) * time.Millisecond
					if c.Start.Before(calls[k-1].End.Add(backoff - time.Millisecond)) {
						t.Fatalf("attempt %d starts %v, before the previous end %v plus backoff %v", k, c.Start, calls[k-1].End, backoff)
					}
				}
			}
			root, _ := r.root(traceID)
			last := calls[len(calls)-1]
			code, _ := attrStr(root, "http.response.status_code")
			switch {
			case last.Status == otlp.StatusError:
				exhausted++
				if len(calls) != 3 || root.Status != otlp.StatusError || code != "503" {
					t.Fatalf("exhausted retries: %d attempts, root status %v code %q; want 3 attempts and a 503", len(calls), root.Status, code)
				}
				el := r.linesOf(root.SpanID)
				if len(el) != 1 || el[0].body["status"] != "503" || el[0].body["error_message"] != "Rate Exceeded." ||
					el[0].body["upstream"] != "enrich-fn" || el[0].body["fault_resource"] != "enrich-fn-concurrency" {
					t.Fatalf("intake-api line after exhausted retries %+v", el)
				}
			case len(calls) > 1:
				recovered++ // an absorbed throttle: the failed attempts stay ERROR, the request goes on
				if calls[0].Status != otlp.StatusError {
					t.Fatalf("a recovered request's first attempt is %v", calls[0].Status)
				}
			}
		}
		if resent == 0 || exhausted == 0 || recovered == 0 {
			t.Fatalf("resent %d, exhausted %d, recovered %d; want all three", resent, exhausted, recovered)
		}
		if got := r.statusCount("503"); got < float64(exhausted) {
			t.Errorf("http_server_request_duration_seconds_count{503} %v, want at least %d", got, exhausted)
		}
		// The bound function throttles in the same window, at the reduced concurrency, and its
		// counts are intake's attempts: throttled ones are Throttles, admitted ones Invocations.
		inv, thr := r.mustMetric("aws_lambda_invocations_sum", nil), r.mustMetric("aws_lambda_throttles_sum", nil)
		admitted := len(enrichCalls(r)) - len(throttled)
		sameCount(t, "throttled attempts vs Lambda Throttles", float64(len(throttled)), thr)
		sameCount(t, "admitted attempts vs Lambda Invocations", float64(admitted), inv)
		if c := r.mustMetric("aws_lambda_concurrent_executions_maximum", nil); c != 24 {
			t.Errorf("Lambda ConcurrentExecutions %v, want the reduced limit 24 (80 x 0.3)", c)
		}
		if h, _ := healthy.metric("aws_lambda_throttles_sum", nil); h != 0 || thr <= 0 {
			t.Errorf("Lambda Throttles healthy %v, story %v", h, thr)
		}
	})

	t.Run("story 3 data-quality rule regression", func(t *testing.T) {
		r := runStory(t, storyAt("09:45"))
		var rejected []span
		for _, l := range r.lines {
			if l.service == "intake-api" && l.body["fault_resource"] == "completeness-rule" {
				s, ok := r.spans[l.body["span_id"]]
				if !ok {
					t.Fatalf("rule log line names span %q, which was not emitted", l.body["span_id"])
				}
				rejected = append(rejected, s)
				if l.level != "warn" || l.body["status"] != "422" || l.body["error_message"] != ruleMessage || l.body["error_type"] != "422" || l.body["fault_mode"] != "rule_regression" {
					t.Fatalf("rule log line %+v", l)
				}
			}
		}
		share := float64(len(rejected)) / float64(len(r.where(func(s span) bool { return s.service == "intake-api" && s.ParentID == "" })))
		if math.Abs(share-0.35) > 0.03 {
			t.Fatalf("rule rejected %.3f of requests, want the declared 0.35", share)
		}
		for _, s := range rejected {
			code, _ := attrStr(s, "http.response.status_code")
			if s.ParentID != "" || s.Status != otlp.StatusUnset || code != "422" || s.StatusMsg != "" {
				t.Fatalf("rule origin root: parent %q status %v code %q msg %q", s.ParentID, s.Status, code, s.StatusMsg)
			}
			if svc := r.servicesIn(s.TraceID); len(svc) != 1 {
				t.Fatalf("a rejected record reached %v", svc)
			}
		}
		if got := r.statusCount("422"); got != float64(len(rejected)) {
			t.Errorf("http_server_request_duration_seconds_count{422} %v, want %d", got, len(rejected))
		}
		upstreamGated(t, r)
	})

	t.Run("story 4 external validator slowdown with backlog ageing", func(t *testing.T) {
		r := runStory(t, storyAt("11:30"))
		roots := r.where(func(s span) bool { return s.service == "validation-worker" && s.Kind == otlp.KindConsumer })
		if len(roots) == 0 {
			t.Fatal("no validation-worker requests")
		}
		// The worker's entry is a CONSUMER span named for its queue (SKT-0168.09.34); a message
		// published by a traced intake request continues that trace (SKT-0168.09.30), so the
		// entry is either a trace root or a child of the publishing span.
		for _, s := range roots {
			if s.Name != "process validation-queue" {
				t.Fatalf("worker entry span %q, want %q", s.Name, "process validation-queue")
			}
			if p, ok := r.parent(s); s.ParentID != "" && ok && p.service != "validation-publisher" && p.service != "intake-api" {
				t.Fatalf("worker entry parented to %s/%s", p.service, p.Name)
			}
		}
		// The worker's client timeout (1.5 s) catches the slowed validator: the origin is the
		// worker's CLIENT span, cut at the timeout and naming the slow dependency; the worker
		// answers 504.
		timeouts := r.where(func(s span) bool {
			et, _ := attrStr(s, "error.type")
			return s.service == "validation-worker" && s.Kind == otlp.KindClient && et == "timeout"
		})
		if len(timeouts) < 100 {
			t.Fatalf("%d validator timeouts, want the slowed tail past 1.5 s", len(timeouts))
		}
		for _, c := range timeouts {
			if c.Status != otlp.StatusError || c.StatusMsg != "external-validator: dependency slow" || c.End.Sub(c.Start) != 1500*time.Millisecond {
				t.Fatalf("timed-out call status %v msg %q duration %v", c.Status, c.StatusMsg, c.End.Sub(c.Start))
			}
			root, _ := r.parent(c)
			et, _ := attrStr(root, "error.type")
			if root.Status != otlp.StatusError || et != "504" {
				t.Fatalf("worker root after a timeout: status %v error.type %q", root.Status, et)
			}
			el := r.linesOf(root.SpanID)
			if len(el) != 1 || el[0].level != "error" || el[0].body["status"] != "504" || el[0].body["error_type"] != "timeout" ||
				el[0].body["error_message"] != "external-validator: dependency slow" || el[0].body["upstream"] != "external-validator-node" {
				t.Fatalf("worker line after a timeout %+v", el)
			}
		}
		// The validator's own spans are slowed 3x, as the dependency's latency factor says.
		node := func(r *storyRun) []span {
			return r.where(func(s span) bool { return s.service == "external-validator-node" && s.Kind == otlp.KindServer })
		}
		if slow, base := meanDuration(node(r)), meanDuration(node(healthy)); slow < 25*base/10 {
			t.Errorf("external-validator-node mean span %v, healthy %v; want about 3x", slow, base)
		}
		// Backlog ageing: messages the worker took on time name the queue's oldest-message age,
		// which the bound SQS queue reports in the same window.
		age := r.mustMetric("aws_sqs_approximate_age_of_oldest_message_maximum", nil)
		maxAge := -1
		for _, l := range r.lines {
			if l.service != "validation-worker" || l.body["fault_resource"] != "validation-queue" {
				continue
			}
			var n int
			if _, err := fmt.Sscanf(l.body["error_message"], "validation-queue: queue backlog (oldest message %d s)", &n); err != nil {
				t.Fatalf("backlog message %q: %v", l.body["error_message"], err)
			}
			maxAge = max(maxAge, n)
		}
		if maxAge < 0 || math.Abs(float64(maxAge)-age) > 5 {
			t.Fatalf("worker logs name an oldest-message age up to %d s, SQS ApproximateAgeOfOldestMessage %v", maxAge, age)
		}
		visible := r.mustMetric("aws_sqs_approximate_number_of_messages_visible_maximum", nil)
		if visible < 10000 || age < 600 {
			t.Errorf("SQS backlog %v, age %v after 30 minutes slowed; want tens of thousands and over 10 minutes", visible, age)
		}
		// Counts: the worker consumes exactly what SQS reports received, and the intake publisher
		// sent what SQS reports sent.
		sameCount(t, "validation-worker requests vs SQS NumberOfMessagesReceived", float64(len(roots)), r.mustMetric("aws_sqs_number_of_messages_received_sum", nil))
		publishes := r.where(func(s span) bool { return s.service == "validation-publisher" && s.Kind == otlp.KindServer })
		sameCount(t, "validation-publisher spans vs SQS NumberOfMessagesSent", float64(len(publishes)), r.mustMetric("aws_sqs_number_of_messages_sent_sum", nil))
		// The Solace partner feed shares the validator: its depth grows at 15 - 8/1.1 msg/s
		// over the 30 slowed minutes.
		depth := r.mustMetric("solace_queue_spool_usage_msgs", map[string]string{"queue_name": "partner-feed-q"})
		want := (15 - 8*1000.0/1100) * 30 * 60
		if math.Abs(depth-want) > 0.02*want {
			t.Errorf("Solace partner-feed-q depth %v, want about %.0f", depth, want)
		}
		if r.spanMetricErrors("validation-worker", "SPAN_KIND_CLIENT") <= 0 {
			t.Error("span metrics show no validation-worker client errors")
		}
	})

	t.Run("story 5 intake pool exhaustion", func(t *testing.T) {
		r := runStory(t, storyAt("10:05"))
		rejected := r.where(func(s span) bool { return s.StatusMsg == poolMessage })
		total := len(r.where(func(s span) bool { return s.service == "intake-api" && s.ParentID == "" }))
		// h = 20 + 15 x 11 = 185 ms; 11.1 connections offered to 10: 1 - 1/1.11 rejected.
		if share := float64(len(rejected)) / float64(total); math.Abs(share-(1-1/1.11)) > 0.02 {
			t.Fatalf("pool rejected %.3f of %d requests, want about %.3f", share, total, 1-1/1.11)
		}
		for _, s := range rejected {
			code, _ := attrStr(s, "http.response.status_code")
			et, _ := attrStr(s, "error.type")
			if s.ParentID != "" || s.Status != otlp.StatusError || code != "503" || et != "SQLTransientConnectionException" {
				t.Fatalf("pool origin: parent %q status %v code %q error.type %q", s.ParentID, s.Status, code, et)
			}
			if d := s.End.Sub(s.Start); d < 3*time.Second {
				t.Fatalf("a pool rejection lasted %v, under the 3 s acquire timeout", d)
			}
			if svc := r.servicesIn(s.TraceID); len(svc) != 1 {
				t.Fatalf("a rejected request reached %v", svc)
			}
			el := r.linesOf(s.SpanID)
			if len(el) != 1 || el[0].level != "error" || el[0].body["status"] != "503" || el[0].body["error_message"] != poolMessage ||
				el[0].body["exception_type"] != "SQLTransientConnectionException" || el[0].body["fault_resource"] != "intake-db-pool" {
				t.Fatalf("pool rejection log lines %+v", el)
			}
		}
		// The admitted requests waited for a connection and say so; the slowed database's calls
		// stretch 12x.
		waited := 0
		for _, l := range r.lines {
			if l.service == "intake-api" && l.body["status"] == "200" && l.body["error_message"] == "intake-db-pool: connection pool saturated" {
				waited++
			}
		}
		if waited == 0 {
			t.Error("no admitted request names the saturated pool")
		}
		db := func(r *storyRun) []span { return r.where(func(s span) bool { return s.Name == "call records-db" }) }
		if slow, base := meanDuration(db(r)), meanDuration(db(healthy)); slow < 8*base {
			t.Errorf("records-db calls mean %v, healthy %v; want the 12x lock contention", slow, base)
		}
		if got := r.statusCount("503"); got < float64(len(rejected)) {
			t.Errorf("http_server_request_duration_seconds_count{503} %v, want at least %d", got, len(rejected))
		}
		if fast, _ := r.metric("http_server_request_duration_seconds_bucket", map[string]string{"http_response_status_code": "503", "le": "2.5"}); fast != 0 {
			t.Errorf("%v pool rejections observed under 2.5 s", fast)
		}
		if r.spanMetricErrors("intake-api", "SPAN_KIND_SERVER") <= healthy.spanMetricErrors("intake-api", "SPAN_KIND_SERVER") {
			t.Error("intake-api span-metric errors did not rise")
		}
		upstreamGated(t, r)
	})

	t.Run("story 5 intake rate quota 429", func(t *testing.T) {
		r := runStory(t, storyAt("10:25"))
		var throttled []span
		for _, l := range r.lines {
			if l.service == "intake-api" && l.body["fault_resource"] == "intake-quota" {
				if l.level != "error" || l.body["status"] != "429" || l.body["error_message"] != "intake-quota: rate quota reduced" || l.body["fault_mode"] != "quota_reduced" {
					t.Fatalf("quota log line %+v", l)
				}
				throttled = append(throttled, r.spans[l.body["span_id"]])
			}
		}
		total := len(r.where(func(s span) bool { return s.service == "intake-api" && s.ParentID == "" }))
		// 40 rps admitted of 60: a third throttled.
		if share := float64(len(throttled)) / float64(total); math.Abs(share-1.0/3) > 0.03 {
			t.Fatalf("quota throttled %.3f of %d requests, want a third", share, total)
		}
		for _, s := range throttled {
			code, _ := attrStr(s, "http.response.status_code")
			if s.ParentID != "" || s.Status != otlp.StatusUnset || code != "429" || s.StatusMsg != "" {
				t.Fatalf("quota origin root: status %v code %q msg %q", s.Status, code, s.StatusMsg)
			}
			if svc := r.servicesIn(s.TraceID); len(svc) != 1 {
				t.Fatalf("a throttled request reached %v", svc)
			}
		}
		if got := r.statusCount("429"); got < float64(len(throttled)) {
			t.Errorf("http_server_request_duration_seconds_count{429} %v, want at least %d", got, len(throttled))
		}
		upstreamGated(t, r)
	})
}

// TestCausalStoriesServer4xxCarryNoErrorType pins the semconv rule for SERVER spans (root note
// D85, the l4b projection repair): a SERVER span answering a causal 4xx (a rule's 422, a quota or
// concurrency 429) stays UNSET and carries no error.type, which the pinned HTTP span conventions
// set only on an errored (5xx) server span; the cause rides the log line instead.
func TestCausalStoriesServer4xxCarryNoErrorType(t *testing.T) {
	for _, at := range []string{"09:25", "09:45", "10:25"} {
		r := runStory(t, storyAt(at))
		n := 0
		for _, l := range r.lines {
			// Only causal answers: a legacy baseline-draw failure projects as legacy (D85).
			if l.body["fault_resource"] == "" || !strings.HasPrefix(l.body["status"], "4") {
				continue
			}
			s := r.spans[l.body["span_id"]]
			code, _ := attrStr(s, "http.response.status_code")
			if s.Kind != otlp.KindServer {
				continue
			}
			n++
			if et, has := attrStr(s, "error.type"); has || s.Status != otlp.StatusUnset {
				t.Fatalf("%s: %s SERVER span answering %s: status %v error.type %q", at, s.service, code, s.Status, et)
			}
		}
		if n == 0 {
			t.Fatalf("%s: no SERVER span answered a 4xx", at)
		}
	}
}

// idPattern masks request-scoped identifiers (trace and span ids, uuid request and session ids)
// in span attributes, log bodies and metadata. With crypto/rand seeded both runs draw the same ids;
// the mask keeps the comparison about emitted facts, not id bookkeeping.
var idPattern = regexp.MustCompile(`\b[0-9a-f]{32}\b|\b[0-9a-f]{16}\b|\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

// appWire renders one fixed-clock run of a blueprint to sorted, id-masked lines for the given
// services: their spans (ids dropped, parent kept as the parent's service and name), log lines,
// span-metric rows and native OTLP metric points.
func appWire(t *testing.T, text string, services map[string]bool) []string {
	t.Helper()
	// Request ids come from crypto/rand, and each request's render start is offset by a hash of
	// its trace id; one seed per run makes both runs draw the same ids and times.
	cryptotest.SetGlobalRandom(t, 0x0168_0915)
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatal(err)
	}
	mc, lc, tc, om := &coretest.MetricCapture{}, &coretest.LogCapture{}, &coretest.TraceCapture{}, &proofMetrics{}
	r := runner.New(runner.Sinks{Metrics: mc, Logs: lc, Traces: tc, OTLPMetrics: om}, reg, runner.Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatal(err)
	}
	cs := control.DefaultState()
	cs.SpanMetricsBlueprints = []string{res.Name}
	r.ApplyControl(cs)
	end := time.Date(2026, 6, 17, 11, 1, 0, 0, time.UTC)
	for i := 11; i >= 1; i-- {
		if err := r.MasterTick(context.Background(), end.Add(-time.Duration(i)*5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RunOnce(context.Background(), end); err != nil {
		t.Fatal(err)
	}
	var out []string
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	byID := map[string]span{}
	for _, res := range tc.Resources {
		svc, _ := res.Attrs["service.name"].(string)
		for _, sp := range res.Spans {
			byID[sp.SpanID] = span{Span: sp, service: svc}
		}
	}
	for _, s := range byID {
		if !services[s.service] {
			continue
		}
		p := byID[s.ParentID]
		sp := s.Span
		sp.TraceID, sp.SpanID, sp.ParentID = "", "", ""
		out = append(out, "span "+s.service+" parent="+p.service+"/"+p.Name+" "+idPattern.ReplaceAllString(enc(sp), "<id>"))
	}
	for _, st := range lc.Streams {
		if !services[st.Labels["service_name"]] {
			continue
		}
		for _, ln := range st.Lines {
			out = append(out, idPattern.ReplaceAllString("log "+enc(st.Labels)+" "+ln.T.String()+" "+ln.Body+" "+enc(ln.Meta), "<id>"))
		}
	}
	for _, s := range mc.All() {
		if services[s.Labels["service_name"]] || services[s.Labels["client"]] || services[s.Labels["server"]] {
			out = append(out, idPattern.ReplaceAllString("series "+s.Name+" "+enc(s.Labels)+" "+strconv.FormatFloat(s.Value, 'g', -1, 64), "<id>"))
		}
	}
	for _, mr := range om.r {
		if svc, _ := mr.Attrs["service.name"].(string); services[svc] {
			out = append(out, idPattern.ReplaceAllString("otlp "+enc(mr), "<id>"))
		}
	}
	sort.Strings(out)
	return out
}

func sameWire(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatalf("%s: the legacy run emitted nothing to compare", what)
	}
	if strings.Join(got, "\n") == strings.Join(want, "\n") {
		return
	}
	g, w := map[string]int{}, map[string]int{}
	for _, l := range got {
		g[l]++
	}
	for _, l := range want {
		w[l]++
	}
	var diff []string
	for l, n := range w {
		if g[l] < n && len(diff) < 6 {
			diff = append(diff, "- "+l)
		}
	}
	for l, n := range g {
		if w[l] < n && len(diff) < 12 {
			diff = append(diff, "+ "+l)
		}
	}
	sort.Strings(diff)
	t.Errorf("%s: opted-healthy output differs from legacy (%d vs %d lines):\n%s", what, len(got), len(want), strings.Join(diff, "\n"))
}

// spanTimes is the Start and End of a rendered span line.
var spanTimes = regexp.MustCompile(`"Start":"[^"]*","End":"[^"]*"`)

// untimedSpans drops each span line's Start and End, keeping every other emitted fact.
func untimedSpans(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if strings.HasPrefix(l, "span ") {
			l = spanTimes.ReplaceAllString(l, `"Start":"","End":""`)
		}
		out[i] = l
	}
	sort.Strings(out)
	return out
}

// TestCausalOptedHealthyIdentity is the opted-healthy guard (codex design §10.5, §4.6): a shipped
// app blueprint that declares resources and binds them, with no fault active, emits the same app
// wire output as without them for every node not bound to a capacity or dependency resource,
// apart from span timing: a successful causal request is laid out on the engine's timeline
// (SKT-0200), so span Start and End are left out of the comparison.
// A rule binds no duration, so binding one on the entry must leave the whole app unchanged; a
// dependency bound on a leaf may change only that leaf and the hop cover of its caller.
func TestCausalOptedHealthyIdentity(t *testing.T) {
	data, err := os.ReadFile("../../blueprints/profiling-demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	legacy := string(data)
	opt := func(resource, node, nodeLine string) string {
		s := strings.Replace(legacy, "      vpc_id: vpc-0prof01\n", "      vpc_id: vpc-0prof01\n    resources:\n      - "+resource+"\n", 1)
		out := strings.Replace(s, nodeLine, nodeLine+"        uses: ["+node+"]\n", 1)
		if out == s || s == legacy {
			t.Fatal("the opt-in edit did not apply")
		}
		return out
	}
	all := map[string]bool{"checkout": true, "catalog": true, "cart-store": true}
	ruleOpted := opt("{name: checkout-rule, kind: rule, reject_share: 0.2}", "checkout-rule", "        entry: true\n")
	sameWire(t, "rule bound on the entry", untimedSpans(appWire(t, ruleOpted, all)), untimedSpans(appWire(t, legacy, all)))

	depOpted := opt("{name: cart-store-db, kind: dependency, latency_ms: 4}", "cart-store-db", "        controller: statefulset\n")
	unbound := map[string]bool{"catalog": true}
	sameWire(t, "dependency bound on cart-store, catalog", untimedSpans(appWire(t, depOpted, unbound)), untimedSpans(appWire(t, legacy, unbound)))
}
