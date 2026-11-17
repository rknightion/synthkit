// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// triggerPair builds a blueprint's producing intake app and consuming validator app over one
// resource set (so they share one queue), with the publisher failing the given share of calls.
func triggerPair(t *testing.T, publisherErrorRate float64) (producer, worker *Workload) {
	t.Helper()
	doc := strings.Replace(intakeApp, `- {name: validation-publisher, uses: [validation-queue]}`,
		fmt.Sprintf(`- {name: validation-publisher, uses: [validation-queue], error_rate: %v}`, publisherErrorRate), 1)
	ws := causalApps(t, causalResources, doc, validatorApp)
	return ws[0], ws[1]
}

// mintTick mints one tick of both apps, producer first, and returns each app's requests.
func mintTick(producer, worker *Workload, eng *shape.Engine, now time.Time) (made, taken []*ledger.Request) {
	producer.AdvanceCausal(now, eng)
	worker.AdvanceCausal(now, eng)
	return producer.m.Mint(now, 60, eng), worker.m.Mint(now, 60, eng)
}

// publishedSpans are the publisher server spans of a producer batch, split by whether the hop
// succeeded.
func publishedSpans(reqs []*ledger.Request) (ok, failed map[string]string) {
	ok, failed = map[string]string{}, map[string]string{}
	for _, r := range reqs {
		for _, c := range callsTo(r, "validation-publisher") {
			if c.Failed {
				failed[c.PeerSpanID] = r.TraceID
			} else {
				ok[c.PeerSpanID] = r.TraceID
			}
		}
	}
	return ok, failed
}

// Only a hop that succeeded publishes: a worker request continues the trace of a request whose
// publisher answered, never one whose publisher failed, and takes each message once, oldest first.
func TestWorkerContinuesOnlySuccessfulPublishes(t *testing.T) {
	producer, worker := triggerPair(t, 0.4)
	eng := shape.New("UTC", nil)
	published := map[string]string{}
	failed := map[string]string{}
	var order []string // publisher spans in publish order
	used := map[string]bool{}
	joined, free := 0, 0
	for k := range 6 {
		now := causalNow.Add(time.Duration(k) * time.Minute)
		made, taken := mintTick(producer, worker, eng, now)
		for _, r := range taken {
			tr, ok := worker.triggerOf(r)
			if !ok {
				free++
				continue
			}
			joined++
			if published[tr.span] != tr.trace || r.TraceID != tr.trace {
				t.Fatalf("worker request joined trace %s under span %s, which no successful publisher hop published (published trace %q)", r.TraceID, tr.span, published[tr.span])
			}
			if _, bad := failed[tr.span]; bad || used[tr.span] {
				t.Fatalf("worker request took span %s that failed or was already consumed", tr.span)
			}
			if want := order[len(used)]; tr.span != want {
				t.Fatalf("worker request took %s, want the oldest unconsumed message %s", tr.span, want)
			}
			used[tr.span] = true
		}
		ok, bad := publishedSpans(made)
		// Publish order within a tick is mint order.
		for _, r := range made {
			for _, c := range callsTo(r, "validation-publisher") {
				if !c.Failed {
					order = append(order, c.PeerSpanID)
				}
			}
		}
		for s, tr := range ok {
			published[s] = tr
		}
		for s, tr := range bad {
			failed[s] = tr
		}
	}
	if len(failed) == 0 || joined == 0 {
		t.Fatalf("%d publisher hops failed and %d worker requests joined; the test needs both", len(failed), joined)
	}
	if free == 0 {
		t.Fatal("the first tick, with nothing published yet, must start its own traces")
	}
}

// A message is consumable only once its producing request has ended before the worker request
// begins: a worker minted at the producer's own instant starts its own trace, and takes the
// message at the next tick.
func TestWorkerNeverTakesAMessageFromItsOwnTick(t *testing.T) {
	producer, worker := triggerPair(t, 0)
	eng := shape.New("UTC", nil)
	made, taken := mintTick(producer, worker, eng, causalNow)
	if len(made) == 0 || len(taken) == 0 {
		t.Fatal("no requests minted")
	}
	for _, r := range taken {
		if _, ok := worker.triggerOf(r); ok {
			t.Fatal("a worker request took a message published at its own instant")
		}
	}
	_, taken = mintTick(producer, worker, eng, causalNow.Add(time.Minute))
	joined := 0
	for _, r := range taken {
		if _, ok := worker.triggerOf(r); ok {
			joined++
		}
	}
	if joined == 0 {
		t.Fatal("no worker request took a message at the next tick")
	}
}

// Two blueprints that declare the same queue name never exchange messages, and a queue no worker
// consumes keeps none.
func TestMessageChannelsStayWithinTheirBlueprint(t *testing.T) {
	prodA, workA := triggerPair(t, 0)
	_, workB := triggerPair(t, 0)
	eng := shape.New("UTC", nil)
	// Blueprint A publishes; blueprint B's worker, ticking later, must not see it.
	a1, _ := mintTick(prodA, workA, eng, causalNow)
	if len(a1) == 0 {
		t.Fatal("no producer requests")
	}
	workB.AdvanceCausal(causalNow.Add(time.Minute), eng)
	for _, r := range workB.m.Mint(causalNow.Add(time.Minute), 60, eng) {
		if _, ok := workB.triggerOf(r); ok {
			t.Fatal("a worker joined a trace published in another blueprint")
		}
	}

	// A producer whose queue has no consuming app keeps nothing.
	ws := causalApps(t, causalResources, intakeApp)
	ws[0].AdvanceCausal(causalNow, eng)
	if len(ws[0].m.Mint(causalNow, 60, eng)) == 0 {
		t.Fatal("no producer requests")
	}
	for _, xs := range ws[0].m.causal.publishes {
		for _, x := range xs {
			if n := x.Pending(); n != 0 {
				t.Fatalf("an unconsumed queue kept %d messages", n)
			}
		}
	}
}

// Joining a trace adds and removes no span, so the worker's span metrics do not move: the same
// worker app ticked alone and ticked beside a publishing app emits the same call counts (summed
// over status, whose split follows the shared engine's draws).
func TestJoinedWorkerSpanMetricsAreNotDoubleCounted(t *testing.T) {
	calls := func(withProducer bool) map[string]float64 {
		var producer, worker *Workload
		eng := shape.New("UTC", nil)
		led := ledger.New(eng, 0, 0)
		led.SetTickSeconds(60)
		if withProducer {
			producer, worker = triggerPair(t, 0)
			led.AddMinter(producer.Minter())
		} else {
			worker = causalApps(t, causalResources, intakeApp, validatorApp)[1]
		}
		led.AddMinter(worker.Minter())
		var joined int
		for k := range 2 {
			now := causalNow.Add(time.Duration(k) * time.Minute)
			if producer != nil {
				producer.AdvanceCausal(now, eng)
			}
			worker.AdvanceCausal(now, eng)
			for _, r := range led.Mint(now) {
				if _, ok := worker.triggerOf(r); ok {
					joined++
				}
			}
		}
		if withProducer && joined == 0 || !withProducer && joined != 0 {
			t.Fatalf("withProducer=%v but %d worker requests joined", withProducer, joined)
		}
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape, world.Ledger = eng, led
		if err := worker.Tick(context.Background(), causalNow.Add(time.Minute), world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		out := map[string]float64{}
		for _, name := range []string{"traces_spanmetrics_calls_total", "traces_service_graph_request_total"} {
			for _, s := range mc.Find(name) {
				out[name+"|"+s.Labels["service_name"]+"|"+s.Labels["span_name"]+"|"+s.Labels["span_kind"]+"|"+s.Labels["client"]+"|"+s.Labels["server"]] += s.Value
			}
		}
		return out
	}
	alone, beside := calls(false), calls(true)
	if len(alone) == 0 {
		t.Fatal("the worker emitted no call counts")
	}
	keys := make([]string, 0, len(alone))
	for k := range alone {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if alone[k] != beside[k] {
			t.Errorf("%s: %v alone, %v beside a publishing app", k, alone[k], beside[k])
		}
	}
	if len(alone) != len(beside) {
		t.Errorf("%d call-count series alone, %d beside a publishing app", len(alone), len(beside))
	}
}

// The parent a worker continues is always a span that was emitted: the publisher's server span, or
// the caller's client span when the publisher emits none. The worker's root never begins before the
// parent ends, even when the producer is slow: a trace ends at its request's start (traces are
// backdated to completion), so the producer's own duration does not move that bound.
func TestWorkerParentIsAnEmittedSpan(t *testing.T) {
	for _, tc := range []struct {
		name      string
		publisher string
		kind      string // span kind of the expected parent
		eng       *shape.Engine
	}{
		{"publisher traced", `{name: validation-publisher, uses: [validation-queue]}`, "SERVER", shape.New("UTC", nil)},
		{"publisher untraced", `{name: validation-publisher, uses: [validation-queue], signals: {traces: false}}`, "CLIENT", shape.New("UTC", nil)},
		// A slow records database stretches every intake request to seconds (pool rejections last
		// the 3 s acquire timeout), far past a worker's own duration.
		{"slow producer", `{name: validation-publisher, uses: [validation-queue]}`, "SERVER", incident(causal.ModeDependencySlow, "records-db", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := strings.Replace(intakeApp, `- {name: validation-publisher, uses: [validation-queue]}`, "- "+tc.publisher, 1)
			ws := causalApps(t, causalResources, doc, validatorApp)
			eng := tc.eng
			made, _ := mintTick(ws[0], ws[1], eng, causalNow)
			pp := project(t, ws[0], eng, made)
			_, taken := mintTick(ws[0], ws[1], eng, causalNow.Add(time.Minute))
			wp := project(t, ws[1], eng, taken)
			joined := 0
			for _, r := range taken {
				tr, ok := ws[1].triggerOf(r)
				if !ok {
					continue
				}
				joined++
				parent, found := pp.spans[tr.span]
				if !found || parent.TraceID != tr.trace {
					t.Fatalf("parent span %s was not emitted in trace %s", tr.span, tr.trace)
				}
				if got := map[otlp.SpanKind]string{otlp.KindServer: "SERVER", otlp.KindClient: "CLIENT"}[parent.Kind]; got != tc.kind {
					t.Fatalf("parent span kind %s, want %s", got, tc.kind)
				}
				root := wp.spans[r.SpanID]
				if root.ParentID != tr.span || len(root.Links) != 1 || root.Links[0].TraceID != tr.trace || root.Links[0].SpanID != tr.span {
					t.Fatalf("worker root parent %q links %+v", root.ParentID, root.Links)
				}
				if root.Start.Before(parent.End) {
					t.Fatalf("worker root starts %s, before its parent span ends %s", root.Start, parent.End)
				}
			}
			if joined == 0 {
				t.Fatal("no worker request joined")
			}
		})
	}
}

// bulkResources, bulkProducerApp and bulkWorkerApp are one busy queue: a producer publishing about
// 400 messages a second (24,000 a 60 s tick) and a worker that keeps up with them, so one tick's
// worker batch joins more requests than maxTrackedTriggers.
const bulkResources = `
- {name: bulk-queue, kind: queue, size: 2000, hold_ms: 100, demand: {off_peak_rps: 400, peak_rps: 400}}
`

const bulkProducerApp = `
traffic: {off_peak_rps: 400, peak_rps: 400}
services:
  - {name: bulk-api, entry: true, calls: [bulk-publisher]}
  - {name: bulk-publisher, uses: [bulk-queue]}
`

const bulkWorkerApp = `
traffic: {from_resource: bulk-queue}
services:
  - {name: bulk-worker, entry: true, worker: true}
`

// A worker tick that joins more requests than maxTrackedTriggers keeps every joined request's
// trigger: each request that took a message (it carries the producer's trace id) projects a root
// parented to and linked to the span that published it, never a second root inside the producer
// trace. Across further ticks the book stays bounded by the larger of the bound and one tick's
// joins.
func TestWorkerJoiningMoreThanTheTriggerBoundKeepsEveryTrigger(t *testing.T) {
	ws := causalApps(t, bulkResources, bulkProducerApp, bulkWorkerApp)
	producer, worker := ws[0], ws[1]
	eng := shape.New("UTC", nil)
	publisher := map[string]string{} // producer trace -> the span that published its message
	largest := 0
	for k := range 4 {
		now := causalNow.Add(time.Duration(k) * time.Minute)
		// The worker mints first (a blueprint may declare it before its producer), so it takes the
		// whole previous tick's messages before this tick's publishes press on the pending bound.
		producer.AdvanceCausal(now, eng)
		worker.AdvanceCausal(now, eng)
		taken := worker.m.Mint(now, 60, eng)
		made := producer.m.Mint(now, 60, eng)
		joined := 0
		if k == 1 {
			wp := project(t, worker, eng, taken)
			for _, r := range taken {
				span, ok := publisher[r.TraceID]
				if !ok {
					continue
				}
				joined++
				root := wp.spans[r.SpanID]
				if root.TraceID != r.TraceID || root.ParentID != span || len(root.Links) != 1 || root.Links[0].TraceID != r.TraceID || root.Links[0].SpanID != span {
					t.Fatalf("joined worker request %d of %d: root trace %s parent %q links %+v, want parent and link %s in %s",
						joined, len(taken), root.TraceID, root.ParentID, root.Links, span, r.TraceID)
				}
			}
			if joined <= maxTrackedTriggers {
				t.Fatalf("%d worker requests joined in one tick; the test needs more than %d", joined, maxTrackedTriggers)
			}
		} else {
			for _, r := range taken {
				if _, ok := worker.triggerOf(r); ok {
					joined++
				}
			}
		}
		largest = max(largest, joined)
		for _, r := range made {
			for i, c := range r.Calls {
				if c.Target == "bulk-publisher" && !c.Failed {
					if span := producer.m.hopContext(r, i); span != "" {
						publisher[r.TraceID] = span
					}
				}
			}
		}
		b := &worker.m.causal.triggers
		b.mu.Lock()
		held := len(b.by)
		b.mu.Unlock()
		if limit := max(maxTrackedTriggers, joined); held > limit {
			t.Fatalf("tick %d: the trigger book holds %d entries, more than %d", k, held, limit)
		}
	}
	if largest <= maxTrackedTriggers {
		t.Fatalf("at most %d worker requests joined in a tick; the test needs more than %d", largest, maxTrackedTriggers)
	}
}

// Past the bound the book still evicts earlier mints' entries oldest first, as before, but never an
// entry of the mint in progress.
func TestTriggerBookEvictsOnlyEarlierMints(t *testing.T) {
	var b triggerBook
	first := make([]*ledger.Request, maxTrackedTriggers)
	for i := range first {
		first[i] = &ledger.Request{}
		b.put(1, first[i], trigger{span: fmt.Sprint(i)})
	}
	const later = 10
	for range later {
		b.put(2, &ledger.Request{}, trigger{})
	}
	for i, r := range first {
		if _, ok := b.get(r); ok != (i >= later) {
			t.Fatalf("entry %d of the first mint held=%v after %d later puts", i, ok, later)
		}
	}
	// One mint larger than the bound keeps every entry and evicts every earlier one.
	big := make([]*ledger.Request, maxTrackedTriggers+later)
	for i := range big {
		big[i] = &ledger.Request{}
		b.put(3, big[i], trigger{span: fmt.Sprint(i)})
	}
	for i, r := range big {
		if tr, ok := b.get(r); !ok || tr.span != fmt.Sprint(i) {
			t.Fatalf("entry %d of the large mint was evicted", i)
		}
	}
	if len(b.by) != len(big) || len(b.order)-b.head != len(big) {
		t.Fatalf("book holds %d entries (%d ordered), want %d", len(b.by), len(b.order)-b.head, len(big))
	}
}
