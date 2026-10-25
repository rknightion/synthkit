// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"sync"
	"sync/atomic"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
)

// Trigger context across a queue. When a request's hop publishes into a queue that a worker app
// consumes (traffic.from_resource), the worker request it causes continues the producing request's
// trace instead of starting a trace of its own. This follows the messaging span conventions
// (OpenTelemetry semantic-conventions v1.44.0, messaging-spans.md, "Message creation context as
// parent of Process span"): a worker handles exactly one message, which is the single-message
// case where the consumer's "process" span MAY take the message's creation context as its parent,
// and it also links to that context, as the convention directs for every message a process span
// accounts for. Batch or ambient-context cases, where only a link would do, do not arise here:
// the minter creates one request per served message.
//
// The pairing is deterministic and stated, not drawn:
//
//   - A request publishes one message per successful hop into a service that uses the queue (the
//     producer side of a queue, ServiceNode.Uses). The message carries the request's trace id, the
//     publisher's server span (or, when it has none or its spans are off, the caller's client span)
//     and the request's start. Only a queue that a worker app consumes keeps messages.
//   - A worker request takes the oldest unconsumed message whose producing request started no
//     later than this request started minus its duration. Traces are backdated to completion
//     (ledger.Request.RenderStart): a trace ends at its request's start less the render offset,
//     whatever its duration, and a worker that joins the trace shares that offset, so its root span
//     begins at its own start less its duration, after the producer's trace has ended. A message
//     becomes consumable one tick after it was published. Taking is first in, first out in publish
//     order (mint order within a tick).
//   - A worker request that finds no such message mints exactly as before: its own trace, no
//     parent, no link. So does a queue with no hop publishing into it, or a producer with no
//     consuming worker app.
//   - At most causal.MaxPendingMessages wait per queue; beyond that the oldest are dropped unconsumed, as
//     a backlog that deep would expire its oldest messages.
//
// The messages travel through the queue's causal.Exchange, reached from the blueprint's injected
// causal.Set: every app of one blueprint shares it and no other blueprint can, whatever resource
// names they declare.
//
// The worker's trace id is replaced by the message's before any signal reads it, so its logs, its
// spans and its span-metric exemplars all carry the joined trace. No span is added or removed:
// span metrics are driven by each workload's own requests, so the joined pair's spans are counted
// once each under their own services, exactly as when they were two traces.

// maxTrackedTriggers bounds the worker requests of earlier mints whose trigger context is still
// held. The requests of the mint in progress are never evicted: one mint can join more requests than
// the bound (up to causal.MaxPendingMessages, a queue's whole backlog), and each of them still
// projects before the next mint, so the book holds at most the larger of the bound and one mint's
// joined requests.
const maxTrackedTriggers = 16384

// trigger is the message creation context a joined worker request continues.
type trigger struct{ trace, span string }

// trackedTrigger is one joined worker request in the book and the mint (the minter's tick count
// when it joined) that made it.
type trackedTrigger struct {
	r   *ledger.Request
	gen uint64
}

// triggerBook remembers the trigger of each joined worker request from mint to projection (the
// request carries no field for it), bounded so a workload whose traces are never projected does
// not grow it. Eviction is oldest first and draws nothing, so it is deterministic.
type triggerBook struct {
	mu    sync.Mutex
	by    map[*ledger.Request]trigger
	order []trackedTrigger // oldest first from head
	head  int
}

// put records r's trigger for the mint gen. Past the bound it evicts the oldest entries of earlier
// mints, never one of gen's own.
func (b *triggerBook) put(gen uint64, r *ledger.Request, t trigger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.by == nil {
		b.by = map[*ledger.Request]trigger{}
	}
	for len(b.order)-b.head >= maxTrackedTriggers && b.order[b.head].gen < gen {
		delete(b.by, b.order[b.head].r)
		b.order[b.head] = trackedTrigger{}
		b.head++
	}
	if b.head > 0 && b.head >= len(b.order)/2 {
		// Compact once half the slice is evicted: amortised O(1) per put.
		b.order = append(b.order[:0], b.order[b.head:]...)
		b.head = 0
	}
	b.by[r] = t
	b.order = append(b.order, trackedTrigger{r: r, gen: gen})
}

func (b *triggerBook) get(r *ledger.Request) (trigger, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.by[r]
	return t, ok
}

// bindExchanges resolves the app's side of each queue it publishes into or consumes. Called once
// at build, after bindCausal validated the declarations.
func (p *causalPlan) bindExchanges(set causal.Set) {
	for name, cn := range p.nodes {
		if name == p.entry {
			continue // a hop publishes; the entry's own use of a queue is not a caller hop
		}
		for _, u := range cn.uses {
			if x, ok := set.QueueExchange(u); ok {
				if p.publishes == nil {
					p.publishes = map[string][]*causal.Exchange{}
				}
				p.publishes[name] = append(p.publishes[name], x)
			}
		}
	}
	if p.worker {
		if x, ok := set.QueueExchange(p.fromQueue); ok {
			p.consumes = x
			x.AddConsumer()
		}
	}
}

// exchange runs after a request's facts are final: a worker request first takes its message and
// joins the producing trace, then the request publishes its own messages, so a worker that itself
// publishes continues the same trace into the next queue.
func (p *causalPlan) exchange(m *minter, r *ledger.Request) {
	if p.consumes != nil {
		if msg, ok := p.consumes.Take(r.Start.Add(-r.Duration)); ok {
			r.TraceID = msg.Trace
			// The minter's tick count names the mint in progress (Mint advances it first).
			p.triggers.put(atomic.LoadUint64(&m.tick), r, trigger{trace: msg.Trace, span: msg.Span})
		}
	}
	if len(p.publishes) == 0 {
		return
	}
	for i, c := range r.Calls {
		xs := p.publishes[c.Target]
		if len(xs) == 0 || c.Failed {
			continue
		}
		span := m.hopContext(r, i)
		if span == "" {
			continue
		}
		for _, x := range xs {
			x.Publish(causal.Message{Start: r.Start, Trace: r.TraceID, Span: span})
		}
	}
}

// hopContext is the span id of the span that publishes at hop i: the callee's server span when it
// emits one, else the caller's client span; "" when neither emits spans.
func (m *minter) hopContext(r *ledger.Request, i int) string {
	c := r.Calls[i]
	callee := m.graph.byName[c.Target]
	if callee == nil {
		return ""
	}
	if callee.kind.serverSpan && c.PeerSpanID != "" && callee.decl.tracesEnabled() {
		return c.PeerSpanID
	}
	caller := m.graph.entry
	if c.ParentHopIndex >= 0 && c.ParentHopIndex < len(r.Calls) {
		if pn := m.graph.byName[r.Calls[c.ParentHopIndex].Target]; pn != nil {
			caller = pn
		}
	}
	if caller.decl.tracesEnabled() && !caller.kind.leaf {
		return c.SpanID
	}
	return ""
}

// triggerOf is the trigger context of a joined worker request, if it joined one.
func (w *Workload) triggerOf(r *ledger.Request) (trigger, bool) {
	if w.m == nil || w.m.causal == nil {
		return trigger{}, false
	}
	return w.m.causal.triggers.get(r)
}
