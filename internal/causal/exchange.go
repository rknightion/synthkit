// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"sync"
	"time"
)

// MaxPendingMessages bounds a queue exchange's unconsumed messages (a few MB): several ticks of a
// busy publisher, far more than a queue that keeps up ever holds. Beyond it the oldest messages
// are dropped unconsumed, as a backlog that deep would expire them.
const MaxPendingMessages = 32768

// Message is one message published into a queue: the producing request's start and trace, and the
// span that published it. The package owns no span or trace naming; the strings are opaque ids.
// Client is the publishing hop's caller-side span ("" when the caller emits none) and CallStart and
// CallEnd its window on the producer's timeline, which a synchronously invoked consumer runs inside.
type Message struct {
	Start time.Time
	Trace string
	Span  string

	Client             string
	CallStart, CallEnd time.Time
}

// Exchange is one declared queue's published messages, handed first in, first out to the
// consumers of that queue. It is the only state consumers of one blueprint share, and only through
// this rendezvous: each consumer's model stays its own (cross-consumer agreement on resource
// state still comes from determinism). It is reached through Set.QueueExchange, so it belongs to
// the blueprint whose Set was injected and to no other, whatever names two blueprints declare.
// Its messages are held oldest first.
type Exchange struct {
	mu        sync.Mutex
	consumers int
	msgs      []Message
}

// QueueExchange returns the exchange of the declared queue named name (a resolved resource name,
// as Resource.Name), creating it on first use. It reports false for an unknown name or a resource
// that is not a queue. Every consumer holding the same Set gets the same exchange. This is the one
// place a Set's resources are written after resolution: a lazily created, atomically published
// pointer, never a declared value.
func (s Set) QueueExchange(name string) (*Exchange, bool) {
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			if r.Name != name {
				continue
			}
			if r.Kind != KindQueue || r.Demand == nil {
				return nil, false
			}
			if x := r.Demand.exchange.Load(); x != nil {
				return x, true
			}
			r.Demand.exchange.CompareAndSwap(nil, &Exchange{})
			return r.Demand.exchange.Load(), true
		}
	}
	return nil, false
}

// AddConsumer registers a consuming workload; an exchange with no consumer keeps nothing.
func (x *Exchange) AddConsumer() {
	x.mu.Lock()
	x.consumers++
	x.mu.Unlock()
}

// Publish keeps m for a consumer, dropping the oldest messages past MaxPendingMessages.
func (x *Exchange) Publish(m Message) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.consumers == 0 {
		return
	}
	if len(x.msgs) >= MaxPendingMessages {
		// Reslice rather than copy: append reallocates only when capacity runs out, so a
		// sustained full backlog costs amortised O(1) per publish.
		x.msgs = x.msgs[len(x.msgs)-MaxPendingMessages+1:]
	}
	x.msgs = append(x.msgs, m)
}

// Take removes and returns the oldest message whose producer started no later than notAfter.
func (x *Exchange) Take(notAfter time.Time) (Message, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.msgs) == 0 || x.msgs[0].Start.After(notAfter) {
		return Message{}, false
	}
	m := x.msgs[0]
	x.msgs = x.msgs[1:]
	return m, true
}

// Pending reports the unconsumed message count.
func (x *Exchange) Pending() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.msgs)
}
