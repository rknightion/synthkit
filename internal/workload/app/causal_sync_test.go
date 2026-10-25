// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// syncResources adds the enrichment function's invocations (its admitted runs) to causalResources.
const syncResources = causalResources + `
- {name: enrich-invocations, kind: queue, size: 80, hold_ms: 1, demand: {from: enrich-fn-concurrency}}
`

// syncIntakeApp is intakeApp whose enrich-fn hop publishes each successful call as an invocation.
var syncIntakeApp = strings.Replace(intakeApp, "    uses: [enrich-fn-concurrency]\n", "    uses: [enrich-fn-concurrency, enrich-invocations]\n", 1)

// syncFunctionApp is the enrichment function itself, invoked synchronously by the intake's hop. Its
// entry type is worker, whose own root kind is CONSUMER, so a SERVER root is the synchronous rule's.
const syncFunctionApp = `
traffic: {from_resource: enrich-invocations, synchronous: true}
services:
  - name: enrich-handler
    type: worker
    entry: true
    logs:
      - source: handler
        body:
          event: {const_str: invoked}
          span_id: {ref: span_id}
`

// syncMint mints one tick of every app through a ledger (so each request carries its render offset)
// and returns each app's requests.
func syncMint(t *testing.T, led *ledger.Ledger, eng *shape.Engine, now time.Time, ws ...*Workload) [][]*ledger.Request {
	t.Helper()
	for _, w := range ws {
		w.AdvanceCausal(now, eng)
	}
	out := make([][]*ledger.Request, len(ws))
	for _, r := range led.Mint(now) {
		for i, w := range ws {
			if r.Workload == w.Minter().Workload() {
				out[i] = append(out[i], r)
			}
		}
	}
	return out
}

func syncLedger(eng *shape.Engine, ws ...*Workload) *ledger.Ledger {
	led := ledger.New(eng, 0, 0)
	led.SetTickSeconds(60)
	for _, w := range ws {
		led.AddMinter(w.Minter())
	}
	return led
}

// A synchronously invoked function's request joins the call that invoked it: its SERVER root shares
// the caller's trace, is parented to the caller's CLIENT span with no link, and the root, every span
// under it and its log lines lie inside that CLIENT span's window, in the same tick as the call.
func TestSynchronousInvocationRunsInsideTheCallingClientSpan(t *testing.T) {
	ws := causalApps(t, syncResources, syncIntakeApp, syncFunctionApp)
	producer, fn := ws[0], ws[1]
	eng := shape.New("UTC", nil)
	led := syncLedger(eng, producer, fn)
	joined, free := 0, 0
	for k := range 3 {
		now := causalNow.Add(time.Duration(k) * time.Minute)
		got := syncMint(t, led, eng, now, producer, fn)
		made, served := got[0], got[1]
		pp := project(t, producer, eng, made)
		fp := project(t, fn, eng, served)
		for _, r := range served {
			root := fp.spans[r.SpanID]
			if root.Kind != otlp.KindServer {
				t.Fatalf("synchronous function root kind %v, want SERVER", root.Kind)
			}
			tr, ok := fn.triggerOf(r)
			if !ok {
				free++
				if root.ParentID != "" {
					t.Fatalf("an unjoined invocation has parent %q", root.ParentID)
				}
				continue
			}
			joined++
			client, found := pp.spans[tr.span]
			if !found || client.Kind != otlp.KindClient || client.TraceID != r.TraceID || pp.owner[tr.span] != "intake-api" {
				t.Fatalf("joined root's parent %s is not an emitted intake-api CLIENT span of trace %s (found %v, kind %v)", tr.span, r.TraceID, found, client.Kind)
			}
			if !strings.HasSuffix(client.Name, "enrich-fn") {
				t.Fatalf("joined root's parent is %q, not the call into enrich-fn", client.Name)
			}
			if root.TraceID != r.TraceID || root.ParentID != tr.span || len(root.Links) != 0 {
				t.Fatalf("root trace %s parent %q links %+v, want trace %s parent %s and no link", root.TraceID, root.ParentID, root.Links, r.TraceID, tr.span)
			}
			for _, sp := range fp.byReq[r.TraceID] {
				if fp.owner[sp.SpanID] != "enrich-handler" {
					continue
				}
				if sp.Start.Before(client.Start) || sp.End.After(client.End) {
					t.Fatalf("span %q [%s, %s] lies outside the calling CLIENT span [%s, %s]", sp.Name, sp.Start, sp.End, client.Start, client.End)
				}
			}
			lines := fp.linesFor("enrich-handler", r)
			if len(lines) == 0 {
				t.Fatal("the joined invocation logged nothing")
			}
			for _, l := range lines {
				if l.t.Before(client.Start) || l.t.After(client.End) {
					t.Fatalf("log line at %s lies outside the calling CLIENT span [%s, %s]", l.t, client.Start, client.End)
				}
			}
		}
		if k == 0 && joined == 0 {
			t.Fatal("no invocation joined a call minted earlier in its own tick")
		}
	}
	if joined == 0 {
		t.Fatal("no invocation joined its caller's trace")
	}
	t.Logf("%d invocations joined, %d kept their own trace", joined, free)
}

// A run longer than the call it would join does not fit inside it: it takes the message (one
// invocation per call) but keeps its own trace, parentless, as it would with no message at all.
func TestSynchronousRunLongerThanItsCallKeepsItsOwnTrace(t *testing.T) {
	slow := strings.Replace(syncFunctionApp, "synchronous: true}", "synchronous: true, request_latency_p95_ms: 600000}", 1)
	ws := causalApps(t, syncResources, syncIntakeApp, slow)
	producer, fn := ws[0], ws[1]
	eng := shape.New("UTC", nil)
	led := syncLedger(eng, producer, fn)
	served, pending := 0, 0
	for k := range 2 {
		got := syncMint(t, led, eng, causalNow.Add(time.Duration(k)*time.Minute), producer, fn)
		// The intake mints first, so every call of this tick is consumable by this tick's runs.
		for _, r := range got[0] {
			for _, c := range callsTo(r, "enrich-fn") {
				if !c.Failed {
					pending++
				}
			}
		}
		pending = max(0, pending-len(got[1]))
		fp := project(t, fn, eng, got[1])
		for _, r := range got[1] {
			served++
			if _, ok := fn.triggerOf(r); ok {
				t.Fatal("a run longer than every call joined one")
			}
			if root := fp.spans[r.SpanID]; root.ParentID != "" || len(root.Links) != 0 {
				t.Fatalf("an unjoined run has parent %q links %+v", root.ParentID, root.Links)
			}
		}
	}
	if served == 0 {
		t.Fatal("the function served no invocations")
	}
	// Each served run took one message, joined or not: the backlog holds only the calls not served.
	if n := fn.m.causal.consumes.Pending(); n != pending {
		t.Fatalf("%d invocations pending, want %d after %d runs each took one", n, pending, served)
	}
}

// Without traffic.synchronous a function minting from the same invocations is unchanged: it consumes
// no message, joins nothing and keeps its service type's root kind; and the publisher keeps nothing
// for it.
func TestUnboundFunctionIsUnchanged(t *testing.T) {
	plain := strings.Replace(syncFunctionApp, ", synchronous: true}", "}", 1)
	ws := causalApps(t, syncResources, syncIntakeApp, plain)
	producer, fn := ws[0], ws[1]
	eng := shape.New("UTC", nil)
	led := syncLedger(eng, producer, fn)
	for k := range 2 {
		got := syncMint(t, led, eng, causalNow.Add(time.Duration(k)*time.Minute), producer, fn)
		fp := project(t, fn, eng, got[1])
		for _, r := range got[1] {
			if _, ok := fn.triggerOf(r); ok {
				t.Fatal("an unbound function joined a trace")
			}
			if root := fp.spans[r.SpanID]; root.Kind != otlp.KindConsumer || root.ParentID != "" {
				t.Fatalf("unbound function root kind %v parent %q, want its worker type's CONSUMER root and no parent", root.Kind, root.ParentID)
			}
		}
	}
	for _, xs := range producer.m.causal.publishes {
		for _, x := range xs {
			if n := x.Pending(); n != 0 {
				t.Fatalf("invocations no synchronous function serves kept %d messages", n)
			}
		}
	}
}

func TestSynchronousDeclarationIsValidated(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"no from_resource", `
traffic: {synchronous: true}
services:
  - {name: enrich-handler, entry: true}
`, "traffic.synchronous needs traffic.from_resource"},
		{"worker", `
traffic: {from_resource: enrich-invocations, synchronous: true}
services:
  - {name: enrich-handler, entry: true, worker: true}
`, "worker and traffic.synchronous are exclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tryCausalApps(t, syncResources, syncIntakeApp, tc.doc)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("build error %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// chainResources adds a second function, invoked synchronously by the first.
const chainResources = syncResources + `
- {name: score-fn-concurrency, kind: concurrency, size: 80, hold_ms: 50, demand: {from: enrich-invocations}}
- {name: score-invocations, kind: queue, size: 80, hold_ms: 1, demand: {from: score-fn-concurrency}}
`

const chainEnrichApp = `
traffic: {from_resource: enrich-invocations, synchronous: true}
services:
  - {name: enrich-handler, type: worker, entry: true, calls: [score-fn]}
  - {name: score-fn, type: function, external: true, uses: [score-fn-concurrency, score-invocations], signals: {traces: false}}
`

const chainScoreApp = `
traffic: {from_resource: score-invocations, synchronous: true, request_latency_p95_ms: 10}
services:
  - {name: score-handler, entry: true}
`

// A synchronously joined function that invokes another in turn measures that call where its own
// tree is rendered, so the second function's root lies inside the first one's CLIENT span.
func TestChainedSynchronousInvocationsNest(t *testing.T) {
	ws := causalApps(t, chainResources, syncIntakeApp, chainEnrichApp, chainScoreApp)
	eng := shape.New("UTC", nil)
	led := syncLedger(eng, ws...)
	got := syncMint(t, led, eng, causalNow, ws...)
	ep := project(t, ws[1], eng, got[1])
	sp := project(t, ws[2], eng, got[2])
	nested := 0
	for _, r := range got[2] {
		tr, ok := ws[2].triggerOf(r)
		if !ok {
			continue
		}
		client, found := ep.spans[tr.span]
		if !found || client.Kind != otlp.KindClient {
			t.Fatalf("score root's parent %s is not an emitted enrich-handler CLIENT span", tr.span)
		}
		if _, upstream := ws[1].triggerOf(findReq(got[1], r.TraceID)); !upstream {
			continue
		}
		nested++
		root := sp.spans[r.SpanID]
		if root.Start.Before(client.Start) || root.End.After(client.End) {
			t.Fatalf("score root [%s, %s] lies outside the calling CLIENT span [%s, %s]", root.Start, root.End, client.Start, client.End)
		}
	}
	if nested == 0 {
		t.Fatal("no second-level invocation joined a joined first-level one")
	}
}

func findReq(reqs []*ledger.Request, trace string) *ledger.Request {
	for _, r := range reqs {
		if r.TraceID == trace {
			return r
		}
	}
	return &ledger.Request{}
}

// A call the function did not serve at the mint where it was consumable is dropped at the next
// one, never joined: a run is not laid inside a call that had already ended. That includes a call
// made at the previous mint's own instant and left pending after its last take, while a call
// published after that take (its producer minted later in the tick) is still served.
func TestSynchronousConsumerDropsCallsUnservedAtItsPreviousMint(t *testing.T) {
	x := &causal.Exchange{}
	x.AddConsumer()
	p := &causalPlan{consumes: x, sync: true}
	t0, t1, t2, t3 := causalNow, causalNow.Add(time.Minute), causalNow.Add(2*time.Minute), causalNow.Add(3*time.Minute)
	x.Publish(causal.Message{Start: t0, Span: "a"})
	x.Publish(causal.Message{Start: t0, Span: "b"})
	if msg, ok := p.takeCall(t1); !ok || msg.Span != "a" {
		t.Fatalf("first mint took %+v %v, want the oldest call a", msg, ok)
	}
	// b went unserved at t1; c and d are made at t1 itself, c before t1's last take, d after it.
	x.Publish(causal.Message{Start: t1, Span: "c"})
	if msg, ok := p.takeCall(t1); !ok || msg.Span != "b" {
		t.Fatalf("second take at t1 took %+v %v, want b", msg, ok)
	}
	x.Publish(causal.Message{Start: t1, Span: "d"})
	x.Publish(causal.Message{Start: t2, Span: "e"})
	if msg, ok := p.takeCall(t2); !ok || msg.Span != "d" {
		t.Fatalf("mint t2 took %+v %v, want d (c was left unserved at t1's last take)", msg, ok)
	}
	// A call made before the previous mint instant is dropped even when no take saw it pending.
	x.Publish(causal.Message{Start: t1, Span: "f"})
	x.Publish(causal.Message{Start: t3, Span: "g"})
	if msg, ok := p.takeCall(t3); !ok || msg.Span != "g" {
		t.Fatalf("mint t3 took %+v %v, want g (e was left at t2, f predates t2)", msg, ok)
	}
	if n := x.Pending(); n != 0 {
		t.Fatalf("%d calls pending, want 0", n)
	}
}

// Expiry belongs to the messages observed at the previous take, not the queue's length: a
// competing consumer or bounded eviction may remove those messages before fresh calls arrive.
// Use real producer Mint messages and emitted CLIENT spans, then control only exchange scheduling.
func TestSynchronousFreshCallsSurviveSharedExchangeChanges(t *testing.T) {
	for _, eviction := range []bool{false, true} {
		name := "competing consumer"
		if eviction {
			name = "bounded eviction"
		}
		t.Run(name, func(t *testing.T) {
			// A small test-only function duration ensures every served request fits the real
			// caller window; neither shipped rates nor call-retention bounds are changed.
			fast := strings.Replace(syncFunctionApp, "synchronous: true}", "synchronous: true, request_latency_p95_ms: 1}", 1)
			ws := causalApps(t, syncResources, syncIntakeApp, fast)
			producer, fn := ws[0], ws[1]
			eng := shape.New("UTC", nil)
			t1, t2 := causalNow, causalNow.Add(time.Minute)
			made := syncMint(t, syncLedger(eng, producer), eng, t1, producer)[0]
			pp := project(t, producer, eng, made)
			x := fn.m.causal.consumes
			var messages []causal.Message
			for {
				msg, ok := x.Take(t1)
				if !ok {
					break
				}
				messages = append(messages, msg)
			}
			if len(messages) < 3 {
				t.Fatal("producer did not publish three real calls")
			}
			a, b, fresh := messages[0], messages[1], messages[2]
			led := syncLedger(eng, fn)
			first := syncMint(t, led, eng, t1, fn)[0]
			if len(first) < 2 {
				t.Fatal("function minted fewer than two requests")
			}
			x.Publish(a)
			old := 1
			if eviction {
				old = causal.MaxPendingMessages - 1
			}
			for range old {
				x.Publish(b)
			}
			// Consumer A serves one real request at t1 and leaves old messages pending.
			if _, fits := syncPlacement(a, first[0].Duration); !fits {
				t.Fatal("test request does not fit the initial real caller window")
			}
			fn.m.causal.exchange(fn.m, first[0])
			if tr, ok := fn.triggerOf(first[0]); !ok || tr.span != a.Client || x.Pending() != old {
				t.Fatal("first take did not join a and leave the expected old messages")
			}
			freshCount := 1
			if eviction {
				freshCount = causal.MaxPendingMessages
			} else {
				// Consumer B owns b before a later producer in the same tick publishes fresh.
				x.AddConsumer()
				if msg, ok := x.Take(t1); !ok || msg.Trace != b.Trace || msg.Client != b.Client {
					t.Fatal("competing consumer did not take b")
				}
			}
			for range freshCount {
				x.Publish(fresh)
			}
			if x.Pending() != freshCount {
				t.Fatal("fresh publications did not replace the old backlog")
			}
			served := syncMint(t, led, eng, t2, fn)[0]
			fp := project(t, fn, eng, served)
			client, emitted := pp.spans[fresh.Client]
			if !emitted || client.Kind != otlp.KindClient || client.TraceID != fresh.Trace {
				t.Fatal("fresh call has no real emitted CLIENT parent")
			}
			joined := 0
			for _, r := range served {
				if _, fits := syncPlacement(fresh, r.Duration); !fits {
					t.Fatal("test request does not fit the fresh real caller window")
				}
				if tr, ok := fn.triggerOf(r); ok {
					joined++
					root := fp.spans[r.SpanID]
					if tr.span != fresh.Client || r.TraceID != fresh.Trace || root.ParentID != client.SpanID || root.Start.Before(client.Start) || root.End.After(client.End) {
						t.Fatal("fresh invocation did not join inside its real CLIENT parent")
					}
				}
			}
			want := min(freshCount, len(served))
			if joined != want || want == 0 {
				t.Fatalf("joined %d fresh calls, want %d; old pending messages were removed, not replaced by fresh stale calls", joined, want)
			}
			if n, want := x.Pending(), freshCount-want; n != want {
				t.Fatalf("%d fresh calls pending, want %d (no expiry of replacement messages)", n, want)
			}
		})
	}
}

// A function declared before its caller mints first in each tick: it serves the calls of the
// previous tick, which were published after its own previous mint, and still nests inside them.
func TestSynchronousFunctionMintingBeforeItsCallerJoinsTheNextTick(t *testing.T) {
	ws := causalApps(t, syncResources, syncIntakeApp, syncFunctionApp)
	producer, fn := ws[0], ws[1]
	eng := shape.New("UTC", nil)
	led := syncLedger(eng, fn, producer)
	joined := 0
	for k := range 2 {
		got := syncMint(t, led, eng, causalNow.Add(time.Duration(k)*time.Minute), producer, fn)
		for _, r := range got[1] {
			if _, ok := fn.triggerOf(r); ok {
				if k == 0 {
					t.Fatal("a function minting before any call joined one")
				}
				joined++
			}
		}
	}
	if joined == 0 {
		t.Fatal("no invocation joined a call published after the function's previous mint")
	}
}

// failingFunctionApp is the synchronously invoked function with a background error share and a
// rule at its entry, logging every causal ref. The 0.3 rate is a test value chosen so one tick
// holds failures; it models nothing.
var failingFunctionApp = `
traffic: {from_resource: enrich-invocations, synchronous: true}
services:
  - name: enrich-handler
    entry: true
    error_rate: 0.3
    uses: [completeness-rule]` + causalLogBody + `
`

// A synchronous run that fails, by its own background error_rate or by a causal rule rejection,
// still joins the call that invoked it (same trace, parent the CLIENT span, inside its window),
// and keeps the failure's error fields on its root and its log line, which lands inside the call.
func TestFailedSynchronousRunJoinsWithItsErrorFields(t *testing.T) {
	ws := causalApps(t, syncResources, syncIntakeApp, failingFunctionApp)
	producer, fn := ws[0], ws[1]
	// The rule rejects only under its regression; the caller's own use of the rule fails some
	// calls too, which publish nothing.
	eng := incident(causal.ModeRuleRegression, "completeness-rule", 1)
	led := syncLedger(eng, producer, fn)
	got := syncMint(t, led, eng, causalNow, producer, fn)
	pp := project(t, producer, eng, got[0])
	fp := project(t, fn, eng, got[1])
	background, rule := 0, 0
	for _, r := range got[1] {
		if r.Outcome == ledger.OutcomeSuccess || r.Fault == nil {
			continue
		}
		tr, ok := fn.triggerOf(r)
		if !ok {
			continue
		}
		client := pp.spans[tr.span]
		root := fp.spans[r.SpanID]
		if client.Kind != otlp.KindClient || root.TraceID != client.TraceID || root.ParentID != client.SpanID || root.Kind != otlp.KindServer {
			t.Fatalf("failed run root trace %s parent %q kind %v, want trace %s parent CLIENT %s", root.TraceID, root.ParentID, root.Kind, client.TraceID, client.SpanID)
		}
		if root.Start.Before(client.Start) || root.End.After(client.End) {
			t.Fatalf("failed run root [%s, %s] outside its CLIENT span [%s, %s]", root.Start, root.End, client.Start, client.End)
		}
		in := fp.linesFor("enrich-handler", r)
		if len(in) != 1 || in[0].t.Before(client.Start) || in[0].t.After(client.End) {
			t.Fatalf("failed run lines %+v, want one inside [%s, %s]", in, client.Start, client.End)
		}
		switch {
		case r.Fault.Mode == "error_rate":
			background++
			wantType, wantMsg := strconv.Itoa(r.StatusCode), "enrich-handler: "+r.ErrorKind
			if in[0].body["error_type"] != wantType || in[0].body["error_message"] != wantMsg || in[0].body["fault_mode"] != "error_rate" || in[0].body["error_code"] != r.ErrorKind {
				t.Fatalf("background failure line %+v, want error_type %s message %q", in[0].body, wantType, wantMsg)
			}
			if root.Status == otlp.StatusError && (root.Attrs["error.type"] != wantType || root.StatusMsg != wantMsg) {
				t.Fatalf("background ERROR root attrs %v msg %q", root.Attrs, root.StatusMsg)
			}
		case r.Fault.Resource == "completeness-rule":
			rule++
			if in[0].body["error_type"] == "" || in[0].body["error_message"] != r.Fault.Message || in[0].body["fault_resource"] != "completeness-rule" {
				t.Fatalf("rule failure line %+v, want the rule's error fields", in[0].body)
			}
		}
	}
	if background == 0 || rule == 0 {
		t.Fatalf("%d background and %d rule failures joined; the test needs both", background, rule)
	}
}
