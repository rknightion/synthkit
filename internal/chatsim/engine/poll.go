// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"fmt"
	"sort"
	"time"
)

func (m *Machine) start(now time.Time) {
	m.started, m.armed, m.t0 = true, false, now
	m.runID = fmt.Sprintf("%s-%s", m.sc.Name, now.UTC().Format("20060102T150405Z"))
	m.rng = NewRNG(m.sc.Seed, m.runID)
	m.lastActivity = now
	for _, f := range m.sc.Facts {
		m.addLedger(now, f.ID, "fact", f.Text)
	}
	m.log(now, "start", map[string]any{"scenario": m.sc.Name, "seed": m.sc.Seed}, "chat t0")
	for _, b := range m.beats {
		if w := b.def.When; w != nil && w.Synthkit == "activated" {
			m.openBeat(now, b, nil)
		}
	}
}

func (m *Machine) finish(now time.Time, reason string) {
	if m.ended {
		return
	}
	m.ended, m.endReason = true, reason
	m.log(now, "end", map[string]any{"dropped_lines": len(m.queue), "llm_calls": m.calls}, "%s", reason)
	m.notify(now, "Run ended: %s (%d LLM calls).", reason, m.calls)
	m.queue = nil
}

func (m *Machine) openBeat(now time.Time, b *beatState, ev *Event) {
	b.status, b.openedAt = beatOpen, now
	if ev != nil {
		b.evLogical = m.byID[ev.ChannelID]
	}
	m.log(now, "beat", map[string]string{"beat": b.def.ID, "state": "open"}, "")
	if !b.adhoc {
		m.notify(now, "Beat %s open.", b.def.ID)
	}
	if r := b.def.Record; r != nil && ev != nil {
		m.addLedger(now, r.Fact, "fact", ev.Text)
	}
	m.requestWake(now, "beat "+b.def.ID+" opened")
}

// fireThen runs a beat's then-actions once: when its first line is posted, or when it closes
// without one (never when the operator skips it).
func (m *Machine) fireThen(now time.Time, b *beatState, by string) {
	if b.thenFired {
		return
	}
	b.thenFired = true
	if by == "" {
		by = m.personas[b.def.Speakers[0]].def.FirstName()
	}
	for _, a := range b.def.Then {
		if a.Summon == "" {
			continue
		}
		due := now.Add(Between(m.rng, a.After.Min, a.After.Max))
		m.summons = append(m.summons, pendingSummon{persona: a.Summon, channel: a.Channel, by: by, due: due})
		m.log(now, "summon_scheduled", map[string]any{"persona": a.Summon, "channel": a.Channel, "in": due.Sub(now).String()}, "")
	}
}

func (m *Machine) closeBeat(now time.Time, b *beatState, st beatStatus, why string) {
	if b.status != beatOpen && b.status != beatPending {
		return
	}
	b.status = st
	m.log(now, "beat", map[string]string{"beat": b.def.ID, "state": st.String(), "why": why}, "")
	if why != "skipped by operator" && !m.beatHasQueued(b.def.ID) {
		m.fireThen(now, b, "")
	}
	if b.def.Final && st == beatDone {
		m.finalClosing = true
	}
}

func (m *Machine) beatHasQueued(id string) bool {
	for _, q := range m.queue {
		if q.post.Beat == id {
			return true
		}
	}
	for _, q := range m.sent {
		if q.post.Beat == id {
			return true
		}
	}
	return false
}

func (m *Machine) arrive(now time.Time, s pendingSummon) {
	ps := m.personas[s.persona]
	if ps.present[s.channel] {
		return
	}
	ps.present[s.channel] = true
	ps.narrate[s.channel] = s.by
	m.addLedger(now, "summon_"+s.persona, "summon", fmt.Sprintf("%s joined the %s channel (pulled in by %s)", ps.def.Name, s.channel, s.by))
	m.log(now, "summoned", map[string]string{"persona": s.persona, "channel": s.channel}, "")
	m.notify(now, "%s joined %s.", ps.def.FirstName(), s.channel)
	for _, b := range m.beats {
		if w := b.def.When; b.status == beatPending && w != nil && w.Summoned == s.persona {
			m.openBeat(now, b, nil)
		}
	}
}

func (m *Machine) beatDue(b *beatState) (time.Time, bool) {
	if b.status != beatPending {
		return time.Time{}, false
	}
	if b.def.At != nil {
		return m.t0.Add(b.def.At.Duration), true
	}
	if b.afterRef != "" {
		if ref := m.beatByID[b.afterRef]; ref != nil && ref.status != beatPending {
			return ref.openedAt.Add(b.afterDelay), true
		}
	}
	return time.Time{}, false
}

// effectiveDue applies the quiet period and the per-channel token bucket to a queued line.
func (m *Machine) effectiveDue(now time.Time, q *queued) time.Time {
	d := q.due
	quiet := m.sc.Guardrails.QuietAfterOperator.Duration
	if quiet > 0 && !m.lastOperatorAt.IsZero() && q.addressedAt.Before(m.lastOperatorAt) {
		if until := m.lastOperatorAt.Add(quiet); until.After(d) {
			d = until
		}
	}
	if r := m.bucket(q.post.ChannelID).Ready(now); r.After(d) {
		d = r
	}
	return d
}

// Poll advances the machine to now and returns the effects the runner must perform. Effects
// queued as the run ends (the closing notice) are still returned.
func (m *Machine) Poll(now time.Time) []Effect {
	out := m.poll(now)
	out = append(out, m.effects...)
	m.effects = nil
	return out
}

func (m *Machine) poll(now time.Time) []Effect {
	if m.ended {
		return nil
	}
	if m.armed && !m.started && !now.Before(m.armedAt) {
		m.start(now)
	}
	out := m.effects
	m.effects = nil
	if !m.started {
		return out
	}
	if !now.Before(m.t0.Add(m.sc.End.After.Duration)) {
		m.finish(now, "end.after reached")
		return out
	}
	if m.windDown && !now.Before(m.windDownAt.Add(WindDownGrace)) {
		m.finish(now, "wind-down grace elapsed")
		return out
	}
	if m.paused {
		return out
	}
	for _, b := range m.beats {
		if due, ok := m.beatDue(b); ok && !now.Before(due) {
			m.openBeat(now, b, nil)
		}
		if b.status == beatOpen && b.def.ExpiresAfter.Duration > 0 && !now.Before(b.openedAt.Add(b.def.ExpiresAfter.Duration)) {
			m.closeBeat(now, b, beatExpired, "expires_after")
		}
	}
	kept := m.summons[:0]
	var arriving []pendingSummon
	for _, s := range m.summons {
		if !now.Before(s.due) {
			arriving = append(arriving, s)
		} else {
			kept = append(kept, s)
		}
	}
	m.summons = kept
	for _, s := range arriving {
		m.arrive(now, s)
	}
	if idle := m.sc.Guardrails.IdleNudgeAfter.Duration; idle > 0 && !m.nudged && !now.Before(m.lastActivity.Add(idle)) {
		if !m.hasOpenBeats() && len(m.queue) == 0 && len(m.sent) == 0 && !m.inflight && !m.finalClosing && !m.windDown {
			m.nudged = true
			m.requestWake(now, "idle")
			m.wake.idle, m.wake.at = true, now
		} else {
			m.lastActivity = now
		}
	}
	if m.wake.pending && !now.Before(m.wake.at) && !m.inflight {
		if eff, ok := m.buildCall(now); ok {
			out = append(out, eff)
		}
	}
	sort.SliceStable(m.queue, func(i, j int) bool { return m.queue[i].seq < m.queue[j].seq })
	keptQ := m.queue[:0]
	for _, q := range m.queue {
		if !m.effectiveDue(now, q).After(now) && m.bucket(q.post.ChannelID).Take(now) {
			m.sent[q.post.UtteranceID] = q
			m.log(now, "post_intent", q.post, "")
			out = append(out, SendPost{Post: q.post})
			continue
		}
		keptQ = append(keptQ, q)
	}
	m.queue = keptQ
	if (m.finalClosing || (m.windDown && m.deactBeatsClosed())) && len(m.queue) == 0 && len(m.sent) == 0 && !m.inflight && !m.wake.pending {
		m.finish(now, "scenario complete")
	}
	return out
}

func (m *Machine) deactBeatsClosed() bool {
	for _, b := range m.beats {
		if b.byDeact && b.status == beatOpen {
			return false
		}
	}
	return true
}

// NextDeadline is the earliest instant at which Poll has timed work to do.
func (m *Machine) NextDeadline(now time.Time) (time.Time, bool) {
	if m.ended {
		return time.Time{}, false
	}
	var best time.Time
	add := func(t time.Time) {
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	if len(m.effects) > 0 {
		add(now)
	}
	if m.armed && !m.started {
		add(m.armedAt)
	}
	if !m.started {
		return best, !best.IsZero()
	}
	add(m.t0.Add(m.sc.End.After.Duration))
	if m.windDown {
		add(m.windDownAt.Add(WindDownGrace))
	}
	if m.paused {
		return best, true
	}
	for _, b := range m.beats {
		if due, ok := m.beatDue(b); ok {
			add(due)
		}
		if b.status == beatOpen && b.def.ExpiresAfter.Duration > 0 {
			add(b.openedAt.Add(b.def.ExpiresAfter.Duration))
		}
	}
	for _, s := range m.summons {
		add(s.due)
	}
	if idle := m.sc.Guardrails.IdleNudgeAfter.Duration; idle > 0 && !m.nudged {
		add(m.lastActivity.Add(idle))
	}
	if m.wake.pending && !m.inflight {
		add(m.wake.at)
	}
	for _, q := range m.queue {
		add(m.effectiveDue(now, q))
	}
	if best.Before(now) {
		best = now
	}
	return best, true
}
