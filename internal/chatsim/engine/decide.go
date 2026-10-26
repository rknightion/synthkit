// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// buildCall assembles the director turn for the pending wake, or clears the wake when there is
// nothing the director may do.
func (m *Machine) buildCall(now time.Time) (Effect, bool) {
	w := m.wake
	m.wake = wakeState{}
	if m.finalClosing || m.budgetSpent {
		return nil, false
	}
	for pid, at := range m.addressed {
		if now.Sub(at) > AddressTTL {
			delete(m.addressed, pid)
		}
	}
	m.turnSeq++
	t := Turn{
		Seq: m.turnSeq, Elapsed: m.elapsed(now), Reasons: w.reasons,
		Operator:     OperatorView{Name: m.sc.Operator.Name, Role: m.sc.Operator.Role},
		Channels:     m.sc.ChannelIDs(),
		Idle:         w.idle,
		MaxLines:     m.sc.Guardrails.MaxLinesPerTurn,
		MaxLineChars: m.sc.Guardrails.MaxLineChars,
		Feedback:     m.feedback,
		Recent:       slices.Clone(m.recent),
	}
	for _, pid := range m.order {
		p := m.personas[pid].def
		t.Personas = append(t.Personas, PersonaView{
			ID: p.ID, Name: p.Name, Role: p.Role, Personality: p.Personality, Style: p.Style,
			Examples: p.Examples, PresentIn: m.presentIn(pid),
		})
		if _, ok := m.addressed[pid]; ok {
			t.Addressed = append(t.Addressed, pid)
			t.AddressedIn = m.addressedIn
		}
	}
	if m.floorIn != "" && now.Sub(m.floorAt) > AddressTTL {
		m.floorIn = ""
	}
	if m.floorIn != "" {
		for _, pid := range m.order {
			if m.personas[pid].present[m.floorIn] {
				t.Floor = append(t.Floor, pid)
			}
		}
		if len(t.Floor) > 0 {
			t.FloorIn = m.floorIn
		}
	}
	for _, b := range m.beats {
		if b.status != beatOpen || b.turns >= b.def.MaxTurns {
			continue
		}
		var speakers []string
		for _, s := range b.def.Speakers {
			if m.canSpeakIn(s, b.def.Channel) {
				speakers = append(speakers, s)
			}
		}
		if len(speakers) == 0 {
			continue
		}
		bv := BeatView{ID: b.def.ID, Goal: b.def.Goal, Channel: b.def.Channel, Speakers: speakers, TurnsUsed: b.turns, TurnsLeft: b.def.MaxTurns - b.turns}
		for i, c := range b.def.MayClaim {
			bv.Claims = append(bv.Claims, Claim{ID: fmt.Sprintf("%s.claim%d", b.def.ID, i+1), Text: c})
		}
		t.OpenBeats = append(t.OpenBeats, bv)
	}
	if len(t.OpenBeats) == 0 && len(t.Addressed) == 0 && len(t.Floor) == 0 && !t.Idle {
		m.feedback, m.retried = nil, false
		return nil, false
	}
	for _, e := range m.ledger {
		if e.Kind == "fact" {
			t.Ledger = append(t.Ledger, e)
		}
	}
	var events []LedgerEntry
	for _, e := range m.ledger {
		if e.Kind != "fact" {
			events = append(events, e)
		}
	}
	if len(events) > LedgerWindow {
		events = events[len(events)-LedgerWindow:]
	}
	t.Ledger = append(t.Ledger, events...)
	m.inflight, m.callIdle, m.callFloor = true, t.Idle, t.FloorIn
	m.log(now, "director_call", map[string]any{"seq": t.Seq, "reasons": t.Reasons, "open_beats": len(t.OpenBeats), "addressed": t.Addressed, "floor": t.Floor, "idle": t.Idle, "retry": len(t.Feedback) > 0}, "")
	return CallDirector{Turn: t}, true
}

// canSpeakIn reports whether persona pid may speak in logical channel ch ("" = anywhere it is).
func (m *Machine) canSpeakIn(pid, ch string) bool {
	ps := m.personas[pid]
	if ps == nil {
		return false
	}
	if ch == "" {
		return len(m.presentIn(pid)) > 0
	}
	return ps.present[ch]
}

type tentative struct {
	turns     map[string]int
	addressed map[string]bool
	idleUsed  bool
	floorUsed bool
	persona   map[string]int
	total     int
}

// DirectorDone hands the director's answer back to the machine.
func (m *Machine) DirectorDone(now time.Time, d Decision, err error) {
	m.inflight = false
	m.calls += max(d.Attempts, 1)
	if m.calls >= m.sc.LLM.MaxLLMCalls && !m.budgetSpent {
		m.budgetSpent = true
		m.log(now, "budget", nil, "max_llm_calls %d reached; the director is no longer consulted", m.sc.LLM.MaxLLMCalls)
		m.notify(now, "LLM budget spent (%d calls): the cast is silent now. Use say <persona> <text> to post lines by hand.", m.sc.LLM.MaxLLMCalls)
	}
	if m.ended {
		return
	}
	if err != nil {
		m.log(now, "director_error", nil, "%v", err)
		m.notify(now, "Director call failed (%v); retrying.", err)
		m.feedback, m.retried = nil, false
		if m.hasOpenBeats() && !m.budgetSpent {
			m.wakeAt(now.Add(DirectorRetryAfter), "retry after director error")
		}
		return
	}
	m.log(now, "director_result", d, "")
	tent := tentative{turns: map[string]int{}, addressed: map[string]bool{}, persona: map[string]int{}}
	type verdict struct {
		line    Line
		logical string
		err     error
	}
	var verdicts []verdict
	var reasons []string
	for _, l := range d.Lines {
		logical, err := m.checkLine(now, &l, &tent)
		verdicts = append(verdicts, verdict{l, logical, err})
		var be *budgetError
		switch {
		case errors.As(err, &be):
			if !be.freeAt.IsZero() {
				m.wakeAt(be.freeAt, "rate cap frees")
			}
		case err != nil:
			reasons = append(reasons, fmt.Sprintf("line by %q (%q) rejected: %v", l.Persona, l.Text, err))
		}
	}
	if len(reasons) > 0 && !m.retried && !m.budgetSpent {
		m.log(now, "retry", reasons, "rejected lines; retrying once with feedback")
		m.retried, m.feedback = true, reasons
		m.wake.pending, m.wake.at = true, now
		m.wake.reasons = append(m.wake.reasons, "retry after rejected lines")
		return
	}
	m.retried, m.feedback = false, nil
	at := now
	for _, v := range verdicts {
		if v.err != nil {
			m.log(now, "line_rejected", v.line, "%v", v.err)
			var be *budgetError
			if !errors.As(v.err, &be) {
				m.notify(now, "Dropped a line by %s: %v", v.line.Persona, v.err)
			}
			continue
		}
		if m.callFloor != "" && v.line.Beat == "" && v.logical == m.callFloor {
			if _, addressed := m.addressed[v.line.Persona]; !addressed {
				m.floorIn = ""
			}
		}
		addrAt := time.Time{}
		if a, ok := m.addressed[v.line.Persona]; ok {
			addrAt = a
			delete(m.addressed, v.line.Persona)
		}
		// Personas type in parallel: each line is due after its own read-and-type delay, kept
		// in the director's order (at least a second apart), and after the same persona's
		// previous line.
		ps := m.personas[v.line.Persona]
		delay := TypingDelay(m.rng, v.line.Text, ps.def.TypingWPM, *ps.def.Responsiveness)
		due := now.Add(delay)
		if prev := at.Add(time.Second); at.After(now) && due.Before(prev) {
			due = prev
		}
		if due.Before(ps.lastDue) {
			due = ps.lastDue.Add(delay / 2)
		}
		at = due
		m.enqueue(now, v.line.Persona, v.logical, v.line.Beat, v.line.Text, due, addrAt)
		// Count the turn only once the line is queued, so closing the beat sees it pending and
		// its then-actions wait for the line to post.
		if v.line.Beat != "" {
			b := m.beatByID[v.line.Beat]
			b.turns++
			if b.turns >= b.def.MaxTurns {
				m.closeBeat(now, b, beatDone, "max_turns")
			}
		}
	}
	for _, id := range d.BeatsDone {
		if b := m.beatByID[id]; b != nil && b.status == beatOpen {
			m.closeBeat(now, b, beatDone, "director")
		}
	}
}

// checkLine applies every deterministic guardrail to one line against tentative counters. It
// fills in the line's channel and returns the logical channel it will post to.
func (m *Machine) checkLine(now time.Time, l *Line, tent *tentative) (string, error) {
	ps := m.personas[l.Persona]
	if ps == nil {
		return "", fmt.Errorf("unknown persona")
	}
	logical := l.Channel
	if l.Beat != "" {
		b := m.beatByID[l.Beat]
		switch {
		case b == nil:
			return "", fmt.Errorf("unknown beat %q", l.Beat)
		case b.status != beatOpen:
			return "", fmt.Errorf("beat %q is not open", l.Beat)
		case !slices.Contains(b.def.Speakers, l.Persona):
			return "", fmt.Errorf("%s is not a speaker of beat %q", l.Persona, l.Beat)
		case b.turns+tent.turns[l.Beat] >= b.def.MaxTurns:
			return "", &budgetError{msg: fmt.Sprintf("beat %q has no turns left", l.Beat)}
		}
		if b.def.Channel != "" {
			logical = b.def.Channel
		} else if logical == "" && b.evLogical != "" && ps.present[b.evLogical] {
			logical = b.evLogical
		}
	} else {
		_, addressed := m.addressed[l.Persona]
		switch {
		case addressed && !tent.addressed[l.Persona]:
			if logical == "" {
				logical = m.addressedIn
			}
		case m.floorOpen(l.Persona) && !tent.floorUsed:
			logical = m.callFloor
		case m.callIdle && !tent.idleUsed:
		default:
			return "", fmt.Errorf("off-beat line: speak only for an open beat, when addressed, as the single floor reply, or as the single idle nudge")
		}
	}
	if logical == "" {
		logical = m.defaultChannel(l.Persona)
	}
	if _, ok := m.sc.Channels[logical]; !ok {
		return "", fmt.Errorf("unknown channel %q", logical)
	}
	if !ps.present[logical] {
		return "", fmt.Errorf("%s is not in the %s channel", l.Persona, logical)
	}
	if m.channelFor(logical) == "" {
		return "", fmt.Errorf("channel %q is not bound", logical)
	}
	g := m.sc.Guardrails
	if countSince(m.accepted, now)+tent.total >= g.MaxMessagesPerMinute {
		return "", &budgetError{"max_messages_per_minute reached", freeAt(m.accepted)}
	}
	if countSince(ps.accepted, now)+tent.persona[l.Persona] >= g.MaxPerPersonaPerMinute {
		return "", &budgetError{"max_per_persona_per_minute reached for " + l.Persona, freeAt(ps.accepted)}
	}
	cited := false
	for _, c := range l.Cites {
		if !m.citable(c, l.Beat) {
			return "", fmt.Errorf("unknown citation %q", c)
		}
		cited = true
	}
	if err := LintLine(l.Text, m.lintContext(cited)); err != nil {
		return "", err
	}
	l.Channel = logical
	if l.Beat != "" {
		tent.turns[l.Beat]++
	} else if _, ok := m.addressed[l.Persona]; ok && !tent.addressed[l.Persona] {
		tent.addressed[l.Persona] = true
	} else if m.floorOpen(l.Persona) && !tent.floorUsed {
		tent.floorUsed = true
	} else {
		tent.idleUsed = true
	}
	tent.persona[l.Persona]++
	tent.total++
	return logical, nil
}

// floorOpen reports whether pid may take the open floor of the current director call.
func (m *Machine) floorOpen(pid string) bool {
	return m.callFloor != "" && m.floorIn == m.callFloor && m.personas[pid].present[m.callFloor]
}

func (m *Machine) citable(id, beat string) bool {
	for _, e := range m.ledger {
		if e.ID == id {
			return true
		}
	}
	if b := m.beatByID[beat]; b != nil {
		for i := range b.def.MayClaim {
			if id == fmt.Sprintf("%s.claim%d", beat, i+1) {
				return true
			}
		}
	}
	return false
}

func (m *Machine) lintContext(cited bool) LintContext {
	var names []string
	for _, pid := range m.order {
		p := m.personas[pid].def
		names = append(names, p.Name)
	}
	var ev strings.Builder
	for _, e := range m.ledger {
		ev.WriteString(e.Text)
		ev.WriteByte('\n')
	}
	return LintContext{
		MaxChars: m.sc.Guardrails.MaxLineChars, OperatorID: m.opt.OperatorID, OperatorName: m.operatorFirstName(),
		KnownNames: names, Evidence: ev.String(), Cited: cited,
	}
}

// budgetError is a rejection a retry cannot fix (a cap or an exhausted beat). freeAt, when set,
// is when the cap frees again.
type budgetError struct {
	msg    string
	freeAt time.Time
}

func (e *budgetError) Error() string { return e.msg }

func freeAt(ts []time.Time) time.Time {
	if len(ts) == 0 {
		return time.Time{}
	}
	return ts[len(ts)-1].Add(time.Minute)
}

func countSince(ts []time.Time, now time.Time) int {
	n := 0
	for _, t := range ts {
		if now.Sub(t) < time.Minute {
			n++
		}
	}
	return n
}

// enqueue schedules a persona line (and, before a summoned persona's first line in a channel,
// the narrator's "was added to the channel" line).
func (m *Machine) enqueue(now time.Time, pid, logical, beat, text string, due, addressedAt time.Time) {
	ps := m.personas[pid]
	cid := m.channelFor(logical)
	if by, ok := ps.narrate[logical]; ok {
		delete(ps.narrate, logical)
		m.postSeq++
		m.queue = append(m.queue, &queued{seq: m.postSeq, due: due, addressedAt: addressedAt, post: Post{
			RunID: m.runID, UtteranceID: UtteranceID(m.runID, m.postSeq), ChannelID: cid, Logical: logical,
			Text: fmt.Sprintf("%s was added to the channel by %s", ps.def.Name, by),
		}})
	}
	m.postSeq++
	q := &queued{seq: m.postSeq, due: due, persona: pid, addressedAt: addressedAt, post: Post{
		RunID: m.runID, UtteranceID: UtteranceID(m.runID, m.postSeq), ChannelID: cid, Logical: logical, Beat: beat,
		Identity: &Identity{PersonaID: pid, Name: ps.def.Name, Emoji: ps.def.Avatar.Emoji, ImageURL: ps.def.Avatar.ImageURL}, Text: strings.TrimSpace(text),
	}}
	m.queue = append(m.queue, q)
	ps.lastDue = due
	ps.accepted = append(ps.accepted, now)
	m.accepted = append(m.accepted, now)
	m.log(now, "line_accepted", q.post, "due +%s", due.Sub(now).Round(10*time.Millisecond))
}

// PostDone records the transport's answer for a SendPost.
func (m *Machine) PostDone(now time.Time, p Post, ts string, err error) {
	delete(m.sent, p.UtteranceID)
	if err != nil {
		m.log(now, "post_error", p, "%v", err)
		return
	}
	m.log(now, "posted", map[string]string{"utterance_id": p.UtteranceID, "ts": ts}, "")
	speaker := "narrator"
	if p.Identity != nil {
		speaker = p.Identity.Name
	}
	m.addRecent(now, p.Logical, speaker, p.Text)
	m.lastActivity = now
	if p.Beat == "" || m.ended {
		return
	}
	b := m.beatByID[p.Beat]
	if b != nil && p.Identity != nil {
		m.fireThen(now, b, m.personas[p.Identity.PersonaID].def.FirstName())
	}
	if b == nil || b.status != beatOpen || b.turns >= b.def.MaxTurns {
		return
	}
	for _, q := range m.queue {
		if q.post.Beat == p.Beat {
			return
		}
	}
	m.requestWake(now, "beat "+p.Beat+" continues")
}

// defaultChannel is the first channel the persona is present in, in declaration order (summoned
// channels last), skipping channels that are not bound yet.
func (m *Machine) defaultChannel(pid string) string {
	ps := m.personas[pid]
	var order []string
	order = append(order, ps.def.Present...)
	order = append(order, m.presentIn(pid)...)
	for _, c := range order {
		if ps.present[c] && m.bound[c] != "" {
			return c
		}
	}
	for _, c := range order {
		if ps.present[c] {
			return c
		}
	}
	return ""
}
