// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"reflect"
	"time"
)

// Run states a Snapshot reports.
const (
	StateWaiting  = "waiting"
	StateArmed    = "armed"
	StateChatting = "chatting"
	StatePaused   = "paused"
	StateWinding  = "winding down"
	StateEnded    = "ended"
)

// Snapshot is the run's state for the operator's control panel. It holds no wall-clock reading
// that changes on its own, so two snapshots are equal exactly when something happened.
type Snapshot struct {
	Scenario string `json:"scenario"`
	// SynthkitScenario is the failure the run fires, empty when the scenario names none.
	SynthkitScenario string          `json:"synthkit_scenario,omitempty"`
	State            string          `json:"state"`
	RunID            string          `json:"run_id,omitempty"`
	StartedAt        time.Time       `json:"started_at,omitzero"`
	Fired            bool            `json:"fired"`
	Paused           bool            `json:"paused"`
	OpenBeats        []string        `json:"open_beats,omitempty"`
	Personas         []PersonaStatus `json:"personas"`
	// SummonTo is the logical channel a bare "summon <persona>" lands in, empty before start.
	SummonTo   string `json:"summon_to,omitempty"`
	Queued     int    `json:"queued"`
	LLMCalls   int    `json:"llm_calls"`
	MaxCalls   int    `json:"max_llm_calls"`
	LastNotice string `json:"last_notice,omitempty"`
	EndReason  string `json:"end_reason,omitempty"`
}

// PersonaStatus is one persona and the channels it is in.
type PersonaStatus struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	In   []string `json:"in,omitempty"`
}

// Paneller is implemented by a transport that keeps a live control panel for the operator.
// Panel is called with the latest snapshot whenever it changes, never concurrently.
type Paneller interface {
	Panel(ctx context.Context, s Snapshot) error
}

// PanelInterval is the least time between two panel updates; changes inside it coalesce.
const PanelInterval = 2 * time.Second

var panelInterval = PanelInterval // tests shorten it

// Snapshot reports the run's state.
func (m *Machine) Snapshot() Snapshot {
	s := Snapshot{
		Scenario: m.sc.Name, SynthkitScenario: m.sc.Trigger.SynthkitScenario,
		RunID: m.runID, StartedAt: m.t0, Fired: m.fired, Paused: m.paused,
		Queued: len(m.queue), LLMCalls: m.calls, MaxCalls: m.sc.LLM.MaxLLMCalls,
		LastNotice: m.lastNotice, EndReason: m.endReason,
	}
	switch {
	case m.ended:
		s.State = StateEnded
	case !m.started && m.armed:
		s.State = StateArmed
	case !m.started:
		s.State = StateWaiting
	case m.paused:
		s.State = StatePaused
	case m.windDown:
		s.State = StateWinding
	default:
		s.State = StateChatting
	}
	for _, b := range m.beats {
		if b.status == beatOpen {
			s.OpenBeats = append(s.OpenBeats, b.def.ID)
		}
	}
	for _, pid := range m.order {
		s.Personas = append(s.Personas, PersonaStatus{ID: pid, Name: m.personas[pid].def.Name, In: m.presentIn(pid)})
	}
	if m.started && len(m.order) > 0 {
		s.SummonTo = m.summonTarget()
	}
	return s
}

// panelFeed delivers snapshots to a Paneller from its own goroutine, at most one call in
// flight and at most one per PanelInterval; a newer snapshot replaces one still waiting.
type panelFeed struct {
	p    Paneller
	in   chan Snapshot
	done chan struct{}
	last Snapshot // last offered, owned by the runner goroutine
	sent bool
}

func newPanelFeed(ctx context.Context, p Paneller, onErr func(error)) *panelFeed {
	f := &panelFeed{p: p, in: make(chan Snapshot, 1), done: make(chan struct{})}
	go func() {
		defer close(f.done)
		var lastAt time.Time
		for s := range f.in {
			if wait := panelInterval - time.Since(lastAt); !lastAt.IsZero() && wait > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(wait):
				}
				select { // a newer snapshot may have arrived while waiting
				case n, ok := <-f.in:
					if ok {
						s = n
					}
				default:
				}
			}
			if err := p.Panel(ctx, s); err != nil {
				onErr(err)
			}
			lastAt = time.Now()
		}
	}()
	return f
}

// offer queues s when it differs from the last snapshot offered.
func (f *panelFeed) offer(s Snapshot) {
	if f.sent && reflect.DeepEqual(s, f.last) {
		return
	}
	f.last, f.sent = s, true
	select {
	case <-f.in: // drop the stale one still waiting
	default:
	}
	f.in <- s
}

// final stops the feed and sends the closing snapshot synchronously under ctx.
func (f *panelFeed) final(ctx context.Context, s Snapshot) error {
	close(f.in)
	select {
	case <-f.done:
	case <-ctx.Done():
	}
	return f.p.Panel(ctx, s)
}
