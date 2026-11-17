// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"errors"
	"reflect"
	"time"
)

type dirResult struct {
	d   Decision
	err error
}

type scenResult struct {
	active bool
	err    error
}

func setActive(ctx context.Context, act Activator, active bool) error {
	if act == nil {
		return errors.New("no SynthKit control plane configured")
	}
	return act.SetActive(ctx, active)
}

// Run drives a machine in real time until the run ends or ctx is cancelled. Inbound events
// arrive on in; the director is consulted on its own goroutine (one call in flight) so event
// intake and the kill switch never wait on the model. Posts are sent from the loop goroutine.
//
// act, which may be nil, switches the SynthKit scenario for fire and heal. A transport that
// implements Notifier receives the operator's private notices, and one that implements
// Paneller keeps the operator's control panel current.
func Run(ctx context.Context, m *Machine, clock Clock, in <-chan Event, dir Director, tr Transport, act Activator) error {
	results := make(chan dirResult, 1)
	scen := make(chan scenResult, 4)
	nt, _ := tr.(Notifier)
	send := func(ctx context.Context, text string) {
		if err := nt.Notify(ctx, text); err != nil {
			m.j.Write(Entry{At: clock.Now(), Kind: "notify_error", Message: err.Error()})
		}
	}
	var panel *panelFeed
	if p, ok := tr.(Paneller); ok {
		panel = newPanelFeed(ctx, p, func(err error) {
			m.j.Write(Entry{At: clock.Now(), Kind: "panel_error", Message: err.Error()})
		})
	}
	// flush sends the last notices and the closing panel synchronously: the process exits
	// once Run returns.
	flush := func() {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		for _, eff := range m.Poll(clock.Now()) {
			if n, ok := eff.(Notify); ok && nt != nil {
				send(fctx, n.Text)
			}
		}
		if panel != nil {
			if err := panel.final(fctx, m.Snapshot()); err != nil {
				m.j.Write(Entry{At: clock.Now(), Kind: "panel_error", Message: err.Error()})
			}
		}
	}
	for {
		for _, eff := range m.Poll(clock.Now()) {
			switch e := eff.(type) {
			case Notify:
				if nt != nil && m.Ended() {
					fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
					send(fctx, e.Text)
					cancel()
				} else if nt != nil {
					text := e.Text
					go send(ctx, text)
				}
			case SetScenario:
				active := e.Active
				go func() { scen <- scenResult{active, setActive(ctx, act, active)} }()
			case CallDirector:
				go func(t Turn) {
					d, err := dir.Decide(ctx, t)
					results <- dirResult{d, err}
				}(e.Turn)
			case SendPost:
				ts, err := tr.Post(ctx, e.Post)
				m.PostDone(clock.Now(), e.Post, ts, err)
			case JoinChannel:
				id := e.ChannelID
				go func() {
					if err := tr.Join(ctx, id); err != nil {
						m.j.Write(Entry{At: clock.Now(), Kind: "join_error", Message: err.Error(), Detail: id})
					}
				}()
			}
		}
		if m.Ended() {
			flush()
			return m.j.Err()
		}
		if panel != nil {
			panel.offer(m.Snapshot())
		}
		var timer Timer
		var timerC <-chan time.Time
		if d, ok := m.NextDeadline(clock.Now()); ok {
			timer = clock.NewTimer(max(0, d.Sub(clock.Now())))
			timerC = timer.C()
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			m.finish(clock.Now(), "context cancelled")
			flush()
			return ctx.Err()
		case ev, ok := <-in:
			if !ok {
				in = nil
			} else {
				m.Deliver(clock.Now(), ev)
			}
		case r := <-results:
			m.DirectorDone(clock.Now(), r.d, r.err)
		case r := <-scen:
			m.ScenarioDone(clock.Now(), r.active, r.err)
		case <-timerC:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// TimedEvent is one recorded inbound event at an offset from the simulation start.
type TimedEvent struct {
	At    time.Duration `json:"at"`
	Event Event         `json:"event"`
}

// Simulate drives a machine on a fake clock against a recorded event log, calling the
// director and the transport synchronously. It is fully deterministic for a deterministic
// director, and never sleeps. It stops when the run ends, when nothing is left to do, or at
// horizon after the clock's start. A Paneller transport sees each changed snapshot.
func Simulate(ctx context.Context, m *Machine, clock *FakeClock, events []TimedEvent, dir Director, tr Transport, act Activator, horizon time.Duration) error {
	nt, _ := tr.(Notifier)
	pn, _ := tr.(Paneller)
	var shown Snapshot
	panel := func() {
		if s := m.Snapshot(); pn != nil && !reflect.DeepEqual(s, shown) {
			shown = s
			_ = pn.Panel(ctx, s)
		}
	}
	base := clock.Now()
	i := 0
	for step := 0; step < 1_000_000; step++ {
		now := clock.Now()
		for i < len(events) && !base.Add(events[i].At).After(now) {
			m.Deliver(now, events[i].Event)
			i++
		}
		for {
			effs := m.Poll(now)
			if len(effs) == 0 {
				break
			}
			for _, eff := range effs {
				switch e := eff.(type) {
				case CallDirector:
					d, err := dir.Decide(ctx, e.Turn)
					m.DirectorDone(now, d, err)
				case SendPost:
					ts, err := tr.Post(ctx, e.Post)
					m.PostDone(now, e.Post, ts, err)
				case Notify:
					if nt != nil {
						_ = nt.Notify(ctx, e.Text)
					}
				case SetScenario:
					m.ScenarioDone(now, e.Active, setActive(ctx, act, e.Active))
				case JoinChannel:
					if err := tr.Join(ctx, e.ChannelID); err != nil {
						return err
					}
				}
			}
		}
		panel()
		if m.Ended() {
			return nil
		}
		var next time.Time
		if i < len(events) {
			next = base.Add(events[i].At)
		}
		if d, ok := m.NextDeadline(now); ok && (next.IsZero() || d.Before(next)) {
			next = d
		}
		if next.IsZero() || next.After(base.Add(horizon)) {
			return nil
		}
		if !next.After(now) {
			next = now.Add(time.Millisecond)
		}
		clock.Set(next)
	}
	return errors.New("simulate: step limit reached (busy loop)")
}
