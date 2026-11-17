// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
)

// timedLine is one projected line of a request's bundle with its own event time.
type timedLine struct {
	source string
	t      time.Time
	body   map[string]string
}

// projectTimed mints one tick and groups the projected lines by request (trace id), keeping each
// line's event time, alongside the minted requests so the test can name each completion time.
func projectTimed(t *testing.T, doc string) (reqs []*ledger.Request, byReq map[string][]timedLine) {
	t.Helper()
	w := buildApp(t, decodeStrict(t, doc))
	lc := &coretest.LogCapture{}
	world := coretest.World(&coretest.MetricCapture{}, lc, &coretest.TraceCapture{})
	reqs = w.m.Mint(drawNow, 30, world.Shape)
	if len(reqs) < 2000 {
		t.Fatalf("minted %d requests, want about 3000", len(reqs))
	}
	if err := w.ProjectBatch(context.Background(), drawNow, world, reqs); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	byReq = map[string][]timedLine{}
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
			byReq[ln.Meta["trace_id"]] = append(byReq[ln.Meta["trace_id"]], timedLine{source: st.Labels["source"], t: ln.T, body: body})
		}
	}
	return reqs, byReq
}

func completionOf(r *ledger.Request) time.Time {
	return r.RenderStart().Add(max(r.Duration, time.Millisecond))
}

// dwellDoc is a three-line bundle of one entry: each line has the dwell it declares ("" none).
func dwellDoc(first, second, third string) string {
	line := func(source, dwell string) string {
		s := `
      - source: ` + source
		if dwell != "" {
			s += `
        dwell_seconds: ` + dwell
		}
		return s + `
        body:
          msg: {const_str: ` + source + `}
          trace_id: {ref: trace_id}`
	}
	return `
traffic: {off_peak_rps: 100, peak_rps: 100}
services:
  - name: api
    type: web
    entry: true
    logs:` + line("app.one", first) + line("app.two", second) + line("app.three", third) + "\n"
}

func bySource(lines []timedLine) map[string]time.Time {
	out := map[string]time.Time{}
	for _, l := range lines {
		out[l.source] = l.t
	}
	return out
}

// TestLogDwellAdvancesLineTime: a bundle whose lines declare dwells writes strictly ordered
// timestamps whose gaps are the dwells (the first line's dwell has no predecessor and places
// nothing), the last line at the request's completion, so earlier lines are backdated and none is
// later than the completion.
func TestLogDwellAdvancesLineTime(t *testing.T) {
	reqs, byReq := projectTimed(t, dwellDoc("{const: 99}", "{const: 30}", "{const: 10}"))
	for _, r := range reqs {
		lines := byReq[r.TraceID]
		if len(lines) != 3 {
			t.Fatalf("request %s wrote %d lines, want 3", r.TraceID, len(lines))
		}
		at := bySource(lines)
		done := completionOf(r)
		if !at["app.three"].Equal(done) {
			t.Fatalf("last line at %v, want the completion %v", at["app.three"], done)
		}
		if got := at["app.three"].Sub(at["app.two"]); got != 10*time.Second {
			t.Fatalf("gap two to three = %v, want 10s", got)
		}
		if got := at["app.two"].Sub(at["app.one"]); got != 30*time.Second {
			t.Fatalf("gap one to two = %v, want 30s", got)
		}
		for _, l := range lines {
			if l.t.After(done) {
				t.Fatalf("line %s at %v is after the completion %v", l.source, l.t, done)
			}
		}
	}
}

// TestLogDwellDrawsPerRequest: a drawn dwell differs between requests, and each request's own
// gaps equal the dwells it drew (read back from the body field that reports the same figure).
func TestLogDwellDrawsPerRequest(t *testing.T) {
	doc := dwellDoc("", "{int_range: {min: 5, max: 600}}", "{const: 1}")
	reqs, byReq := projectTimed(t, doc)
	gaps := map[time.Duration]bool{}
	for _, r := range reqs {
		at := bySource(byReq[r.TraceID])
		gap := at["app.three"].Sub(at["app.two"])
		if gap != time.Second {
			t.Fatalf("gap two to three = %v, want 1s", gap)
		}
		g := at["app.two"].Sub(at["app.one"])
		if g < 5*time.Second || g > 600*time.Second || g%time.Second != 0 {
			t.Fatalf("drawn dwell %v outside the declared 5..600 whole seconds", g)
		}
		gaps[g] = true
	}
	if len(gaps) < 50 {
		t.Fatalf("only %d distinct drawn dwells across %d requests", len(gaps), len(reqs))
	}
}

// TestLogDwellUndeclaredKeepsCompletion: with no dwell on any line every line keeps the
// request's completion time, as before the field existed.
func TestLogDwellUndeclaredKeepsCompletion(t *testing.T) {
	reqs, byReq := projectTimed(t, dwellDoc("", "", ""))
	for _, r := range reqs {
		for _, l := range byReq[r.TraceID] {
			if !l.t.Equal(completionOf(r)) {
				t.Fatalf("line %s at %v, want the completion %v", l.source, l.t, completionOf(r))
			}
		}
	}
}

// TestLogDwellGatedLineAddsNone: a gated-out line contributes no dwell and shifts no other line's
// time: for requests that skip the middle line the two written lines are exactly the last dwell
// apart, and for requests that write it the middle line's dwell opens the gap.
func TestLogDwellGatedLineAddsNone(t *testing.T) {
	doc := `
traffic: {off_peak_rps: 100, peak_rps: 100}
services:
  - name: api
    type: web
    entry: true
    logs:
      - source: app.one
        dwell_seconds: {const: 99}
        body:
          decision: {request_draw: ` + drawDecision + `}
          trace_id: {ref: trace_id}
      - source: app.mid
        dwell_seconds: {const: 40}
        when: ` + gateApproved + `
        body:
          decision: {request_draw: ` + drawDecision + `}
          trace_id: {ref: trace_id}
      - source: app.three
        dwell_seconds: {const: 7}
        body:
          decision: {request_draw: ` + drawDecision + `}
          trace_id: {ref: trace_id}
`
	reqs, byReq := projectTimed(t, doc)
	skipped, written := 0, 0
	for _, r := range reqs {
		at := bySource(byReq[r.TraceID])
		if !at["app.three"].Equal(completionOf(r)) {
			t.Fatalf("last line at %v, want the completion %v", at["app.three"], completionOf(r))
		}
		if mid, ok := at["app.mid"]; ok {
			written++
			if at["app.three"].Sub(mid) != 7*time.Second || mid.Sub(at["app.one"]) != 40*time.Second {
				t.Fatalf("written middle line: gaps %v and %v, want 40s then 7s", mid.Sub(at["app.one"]), at["app.three"].Sub(mid))
			}
			continue
		}
		skipped++
		if got := at["app.three"].Sub(at["app.one"]); got != 7*time.Second {
			t.Fatalf("a skipped middle line left a %v gap, want only the last dwell 7s", got)
		}
	}
	if skipped == 0 || written == 0 {
		t.Fatalf("gate wrote %d and skipped %d requests, want both", written, skipped)
	}
}

// dwellApp is loggedIntakeApp whose partner-facing function logs a dwelled bundle per attempt.
func dwellApp() string {
	const marker = "      partner-gateway: {timeout_ms: 2000, retries: 1, retry_on: [5xx, timeout, connect]}" + causalLogBody
	extra := `
      - source: app.detail
        dwell_seconds: {const: 20}
        body:
          kind: {const_str: detail}
          span_id: {ref: span_id}
      - source: app.last
        dwell_seconds: {const: 12}
        body:
          kind: {const_str: last}
          span_id: {ref: span_id}`
	return strings.Replace(loggedIntakeApp, marker, marker+extra, 1)
}

// TestLogDwellCausalBundlePerAttempt: on a failing causal request each attempt's bundle at a node
// is timed on its own: the last line at that attempt's end, the earlier lines the dwells before it.
func TestLogDwellCausalBundlePerAttempt(t *testing.T) {
	w := causalApps(t, causalResources, dwellApp())[0]
	eng := incident(causal.ModeCredentialExpired, "partner-api-cert", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	checked := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "partner-api-cert" {
			continue
		}
		enrich := callsTo(r, "enrich-fn")
		lines := p.linesFor("enrich-fn", r)
		if len(lines) != 3*len(enrich) {
			t.Fatalf("enrich-fn wrote %d lines for %d attempts, want 3 each", len(lines), len(enrich))
		}
		base := r.RenderStart()
		done := completionOf(r)
		for k, c := range enrich {
			end := base.Add(c.Offset + c.Duration)
			got := map[string]time.Time{}
			for _, l := range lines {
				if l.field("span_id") == c.PeerSpanID {
					kind := l.field("kind")
					if kind == "" {
						kind = "first"
					}
					got[kind] = l.t
				}
			}
			if len(got) != 3 {
				t.Fatalf("attempt %d bundle has %d lines, want 3", k, len(got))
			}
			if !got["last"].Equal(end) || got["last"].After(done) {
				t.Fatalf("attempt %d last line at %v, want the attempt end %v (completion %v)", k, got["last"], end, done)
			}
			if got["last"].Sub(got["detail"]) != 12*time.Second || got["detail"].Sub(got["first"]) != 20*time.Second {
				t.Fatalf("attempt %d gaps %v then %v, want 20s then 12s", k, got["detail"].Sub(got["first"]), got["last"].Sub(got["detail"]))
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no certificate requests minted")
	}
}
