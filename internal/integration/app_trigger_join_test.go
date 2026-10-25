// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// appTriggerJoinYAML is a test-only declaration (never a deployable blueprint): a caller app whose
// hop publishes into a queue, a worker app that consumes the same queue, and an unrelated app that
// touches neither. %[1]d is the queue's consumer count (40 drains every message as it arrives; 1 is far slower than
// 5 rps, so a backlog builds) and %[2]s the caller's rate in requests per second, which the queue's
// declared demand matches.
const appTriggerJoinYAML = `
name: app-trigger-join
label: app-trigger-join
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-fixture
      cloudwatch: {albs: 0, s3_buckets: 0, nlb: false, ebs: false, nat_gateway: false, eks: false, firehose: false, private_link: false}
    cluster:
      type: eks
      name: trigger-join-eks
      node_groups:
        - { name: general, instance_type: m6i.xlarge, desired: 2 }
    resources:
      - { name: work-dependency, kind: dependency, latency_ms: 100, slow_factor: 4 }
      - name: work-queue
        kind: queue
        size: %[1]d
        hold_ms: 300
        held_across: work-dependency
        demand: { off_peak_rps: %[2]s, peak_rps: %[2]s, load: constant }
workloads:
  - type: app
    name: caller-app
    runs_on: trigger-join-eks
    traffic: { off_peak_rps: %[2]s, peak_rps: %[2]s }
    services:
      - name: caller-api
        entry: true
        calls: [work-publisher]
        logs:
          - source: app
            body: { trace_id: { ref: trace_id }, span_id: { ref: span_id } }
      - name: work-publisher
        uses: [work-queue]
  - type: app
    name: worker-app
    runs_on: trigger-join-eks
    traffic: { from_resource: work-queue }
    services:
      - name: work-consumer
        entry: true
        worker: true
        logs:
          - source: app
            body: { trace_id: { ref: trace_id }, span_id: { ref: span_id } }
  - type: app
    name: unrelated-app
    runs_on: trigger-join-eks
    traffic: { off_peak_rps: 2, peak_rps: 2 }
    services:
      - name: unrelated-api
        entry: true
        logs:
          - source: app
            body: { trace_id: { ref: trace_id }, span_id: { ref: span_id } }
`

// joinMessage is a message the reference model tracks: a published hop of a caller request.
type joinMessage struct {
	start time.Time
	trace string
	span  string
}

// joinRun is everything a run of appTriggerJoinYAML recorded.
type joinRun struct {
	// requests by workload, in mint order.
	requests map[string][]*ledger.Request
	// paired maps a worker request to the message the reference model paired it with (nil = none).
	paired map[*ledger.Request]*joinMessage
	// spans by span id, and the owning resource's service.name.
	spans map[string]otlp.Span
	owner map[string]string
	// logTrace is the trace_id structured metadata of each log line, by workload then span_id.
	logTrace map[string]map[string]string
}

func runTriggerJoin(t *testing.T, consumers int, rate string) *joinRun {
	t.Helper()
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(fmt.Sprintf(appTriggerJoinYAML, consumers, rate)), reg)
	if err != nil {
		t.Fatal(err)
	}
	eng := shape.New("UTC", res.Incidents)
	const tick = 15 * time.Second
	led := ledger.New(eng, 0, 0)
	led.SetTickSeconds(tick.Seconds())

	workloads := map[string]core.Workload{}
	var order []core.Workload
	for _, wi := range res.Workloads {
		wr, _ := reg.Workload(wi.Kind)
		b := core.Binding{Name: wi.Name, Seed: res.Name + ":" + wi.Name, Replicas: wi.Replicas, Env: wi.Env, Cluster: wi.Cluster, Calls: wi.Calls, Databases: wi.Databases}
		w, err := wr.Build(wi.Config, b)
		if err != nil {
			t.Fatal(err)
		}
		workloads[wi.Name] = w
		order = append(order, w)
		led.AddMinter(w.Minter())
	}

	tc, lc := &coretest.TraceCapture{}, &coretest.LogCapture{}
	world := coretest.World(nil, lc, tc)
	world.Shape, world.Ledger = eng, led

	run := &joinRun{requests: map[string][]*ledger.Request{}, paired: map[*ledger.Request]*joinMessage{}, spans: map[string]otlp.Span{}, owner: map[string]string{}, logTrace: map[string]map[string]string{}}
	var pending []*joinMessage
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for now := start; now.Before(start.Add(20 * time.Minute)); now = now.Add(tick) {
		for _, w := range order {
			if a, ok := w.(causal.Advancer); ok {
				a.AdvanceCausal(now, eng)
			}
		}
		batch := led.Mint(now)
		// The reference model of the stated rule: a caller request publishes one message per
		// successful work-publisher hop; a worker request takes the oldest message whose
		// publisher finished at least one worker duration before it.
		for _, r := range batch {
			run.requests[r.Workload] = append(run.requests[r.Workload], r)
			switch r.Workload {
			case "worker-app":
				var msg *joinMessage
				if len(pending) > 0 && !pending[0].start.After(r.Start.Add(-r.Duration)) {
					msg, pending = pending[0], pending[1:]
				}
				run.paired[r] = msg
			}
		}
		// A message published this tick starts at now, so it is never eligible for a worker
		// request starting at now: the reference publishes after the batch's takes.
		for _, r := range batch {
			if r.Workload != "caller-app" {
				continue
			}
			for _, c := range r.Calls {
				if c.Target == "work-publisher" && !c.Failed {
					pending = append(pending, &joinMessage{start: r.Start, trace: r.TraceID, span: c.PeerSpanID})
				}
			}
		}
		for name, w := range workloads {
			var mine []*ledger.Request
			for _, r := range batch {
				if r.Workload == name {
					mine = append(mine, r)
				}
			}
			if err := w.ProjectBatch(context.Background(), now, world, mine); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, rs := range tc.Resources {
		for _, sp := range rs.Spans {
			run.spans[sp.SpanID] = sp
			run.owner[sp.SpanID], _ = rs.Attrs["service.name"].(string)
		}
	}
	for _, st := range lc.Streams {
		svc := st.Labels["service_name"]
		for _, ln := range st.Lines {
			// span_id and trace_id are high-cardinality refs, so they ride structured metadata.
			if run.logTrace[svc] == nil {
				run.logTrace[svc] = map[string]string{}
			}
			run.logTrace[svc][ln.Meta["span_id"]] = ln.Meta["trace_id"]
		}
	}
	return run
}

// A worker app whose queue a caller hop publishes into starts no trace of its own: each worker
// request that takes a published message joins the producing request's trace as a child of the
// publisher's span, the worker's logs carry that joined trace id, and an unrelated app is untouched.
// The pairing is the stated FIFO rule, checked against a reference model.
func TestWorkerAppJoinsTheProducingRequestsTrace(t *testing.T) {
	for _, tc := range []struct {
		name      string
		consumers int
		rate      string
		// backlog: a consumer slower than the publisher keeps a message waiting for every
		// worker request after the first tick.
		backlog bool
	}{
		{"keeps-up", 40, "5", false},
		// A fractional rate makes the producer's per-tick count vary, so a worker request now and
		// then finds nothing eligible; the reference model decides which.
		{"fractional", 40, "0.7", false},
		{"backlog", 1, "5", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runTriggerJoin(t, tc.consumers, tc.rate)
			callerTraces := map[string]bool{}
			for _, r := range run.requests["caller-app"] {
				callerTraces[r.TraceID] = true
			}
			workers := run.requests["worker-app"]
			if len(workers) == 0 || len(run.requests["caller-app"]) == 0 {
				t.Fatalf("minted %d worker and %d caller requests", len(workers), len(run.requests["caller-app"]))
			}
			joined, free := 0, 0
			used := map[string]bool{}
			for i, r := range workers {
				msg := run.paired[r]
				root, ok := run.spans[r.SpanID]
				if !ok {
					t.Fatalf("worker request %d projected no root span", i)
				}
				if root.Kind != otlp.KindConsumer {
					t.Fatalf("worker root kind %v, want CONSUMER", root.Kind)
				}
				if msg == nil {
					free++
					if callerTraces[r.TraceID] || root.ParentID != "" || len(root.Links) != 0 {
						t.Fatalf("worker request %d took no message but joined trace %s (parent %q)", i, r.TraceID, root.ParentID)
					}
					continue
				}
				joined++
				if r.TraceID != msg.trace || root.TraceID != msg.trace {
					t.Fatalf("worker request %d trace %s (root %s), want the producing request's %s", i, r.TraceID, root.TraceID, msg.trace)
				}
				if root.ParentID != msg.span {
					t.Fatalf("worker root parent %q, want the publisher's span %q", root.ParentID, msg.span)
				}
				if len(root.Links) != 1 || root.Links[0].TraceID != msg.trace || root.Links[0].SpanID != msg.span {
					t.Fatalf("worker root links %+v, want one to the message creation context %s/%s", root.Links, msg.trace, msg.span)
				}
				pub, ok := run.spans[msg.span]
				if !ok || pub.TraceID != msg.trace || run.owner[msg.span] != "work-publisher" {
					t.Fatalf("parent span %q is not the producing trace's work-publisher span: %+v (owner %q)", msg.span, pub, run.owner[msg.span])
				}
				if root.Start.Before(pub.End) {
					t.Fatalf("worker root starts %s, before its producer's publisher span ends %s", root.Start, pub.End)
				}
				if used[msg.span] {
					t.Fatalf("message %q consumed twice", msg.span)
				}
				used[msg.span] = true
				if got := run.logTrace["work-consumer"][r.SpanID]; got != msg.trace {
					t.Fatalf("worker log trace_id %q, want the joined %q", got, msg.trace)
				}
			}
			t.Logf("%d worker requests: %d joined a producing trace, %d started their own; %d caller requests", len(workers), joined, free, len(run.requests["caller-app"]))
			if joined == 0 {
				t.Fatalf("no worker request joined a producing trace (%d free)", free)
			}
			if tc.backlog {
				// Only the first tick, before anything was published, may start free.
				first := 0
				for _, r := range workers {
					if !r.Start.After(workers[0].Start) {
						first++
					}
				}
				if free > first {
					t.Errorf("%d of %d worker requests started their own trace, only the first tick's %d may", free, len(workers), first)
				}
			} else if tc.rate == "5" && joined < free {
				t.Errorf("keeping up, %d joined and %d did not", joined, free)
			}

			// Every other app is untouched: its roots have no parent and its traces are its own.
			for _, r := range run.requests["unrelated-app"] {
				root := run.spans[r.SpanID]
				if root.ParentID != "" || len(root.Links) != 0 || callerTraces[r.TraceID] {
					t.Fatalf("unrelated request joined a trace: %+v", root)
				}
			}
			for _, r := range run.requests["caller-app"] {
				if root := run.spans[r.SpanID]; root.ParentID != "" || len(root.Links) != 0 {
					t.Fatalf("caller root has parent %q", root.ParentID)
				}
			}
		})
	}
}

// Two blueprints declaring identical app, service and queue names never pair across each other: a
// worker of blueprint B, minting while blueprint A's caller publishes into its own identically
// named queue, starts only its own traces; the same worker beside A's own caller joins.
func TestWorkerNeverJoinsAnotherBlueprintsTrace(t *testing.T) {
	reg := runner.Catalog()
	build := func() (map[string]core.Workload, *shape.Engine) {
		res, err := blueprint.Load([]byte(fmt.Sprintf(appTriggerJoinYAML, 40, "5")), reg)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]core.Workload{}
		for _, wi := range res.Workloads {
			wr, _ := reg.Workload(wi.Kind)
			b := core.Binding{Name: wi.Name, Seed: res.Name + ":" + wi.Name, Replicas: wi.Replicas, Env: wi.Env, Cluster: wi.Cluster, Calls: wi.Calls, Databases: wi.Databases}
			w, err := wr.Build(wi.Config, b)
			if err != nil {
				t.Fatal(err)
			}
			out[wi.Name] = w
		}
		return out, shape.New("UTC", res.Incidents)
	}
	const tick = 15 * time.Second
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	// run mints ticks of the given workloads on one ledger and returns the caller's trace ids and
	// the worker requests' trace ids.
	run := func(eng *shape.Engine, ws ...core.Workload) (callers map[string]bool, workers []string) {
		led := ledger.New(eng, 0, 0)
		led.SetTickSeconds(tick.Seconds())
		for _, w := range ws {
			led.AddMinter(w.Minter())
		}
		callers = map[string]bool{}
		for now := start; now.Before(start.Add(2 * time.Minute)); now = now.Add(tick) {
			for _, w := range ws {
				if a, ok := w.(causal.Advancer); ok {
					a.AdvanceCausal(now, eng)
				}
			}
			for _, r := range led.Mint(now) {
				switch r.Workload {
				case "caller-app":
					callers[r.TraceID] = true
				case "worker-app":
					workers = append(workers, r.TraceID)
				}
			}
		}
		return callers, workers
	}

	a, engA := build()
	b, engB := build()
	// A's caller publishes on its own ledger; B's worker mints on another, interleaved in time.
	callersA, _ := run(engA, a["caller-app"])
	_, workersB := run(engB, b["worker-app"])
	if len(callersA) == 0 || len(workersB) == 0 {
		t.Fatalf("minted %d caller traces and %d worker requests", len(callersA), len(workersB))
	}
	for _, id := range workersB {
		if callersA[id] {
			t.Fatalf("blueprint B's worker joined blueprint A's trace %s", id)
		}
	}

	// Control: a worker beside its own blueprint's caller joins, so the check above can fail.
	c, engC := build()
	callersC, workersC := run(engC, c["caller-app"], c["worker-app"])
	joined := 0
	for _, id := range workersC {
		if callersC[id] {
			joined++
		}
	}
	if joined == 0 {
		t.Fatal("the control worker joined no trace of its own blueprint's caller")
	}
}
