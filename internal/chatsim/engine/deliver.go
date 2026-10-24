// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Deliver feeds one inbound event to the machine.
func (m *Machine) Deliver(now time.Time, ev Event) {
	if m.ended {
		if ev.Kind == EvControl && ev.Reply != nil {
			ev.Reply("chatsim: run has ended (" + m.endReason + ")")
		}
		return
	}
	m.log(now, "event", ev, "")
	switch ev.Kind {
	case EvControl:
		m.control(now, ev)
		return
	case EvSynthkitActivated:
		if !m.started && !m.armed {
			m.armed, m.armedAt = true, now.Add(m.sc.Trigger.Delay.Duration)
			m.log(now, "armed", nil, "synthkit scenario activated; chat t0 in %s", m.sc.Trigger.Delay.Duration)
		}
		return
	case EvSynthkitDeactivated:
		if m.armed && !m.started {
			m.armed = false
			m.log(now, "disarmed", nil, "synthkit scenario deactivated before chat t0")
			return
		}
		if m.started && !m.windDown {
			m.windDown, m.windDownAt = true, now
			for _, b := range m.beats {
				w := b.def.When
				if b.status == beatPending && (b.def.ID == m.sc.Trigger.OnDeactivate || (w != nil && w.Synthkit == "deactivated")) {
					b.byDeact = true
					m.openBeat(now, b, nil)
				}
			}
		}
		return
	}
	if !m.started {
		return
	}
	if ev.Kind == EvChannelCreated {
		m.bindCreated(now, ev)
	}
	logical := m.byID[ev.ChannelID]
	if logical == "" {
		return // not one of our rooms
	}
	m.lastActivity = now
	m.nudged = false
	if ev.Kind == EvMessage || ev.Kind == EvMessageChanged {
		m.recordMessage(now, logical, ev)
	}
	opened := m.matchEventBeats(now, logical, ev)
	if ev.Kind == EvMessage && ev.From == FromOperator {
		m.requestWake(now, "operator message")
	} else if !opened && ev.Kind != EvChannelCreated && m.hasOpenBeats() {
		m.requestWake(now, string(ev.Kind)+" from "+string(ev.From))
	}
}

func (m *Machine) recordMessage(now time.Time, logical string, ev Event) {
	speaker := string(ev.From)
	kind := string(ev.From)
	switch ev.From {
	case FromOperator:
		speaker = m.sc.Operator.Name + " (operator)"
		m.lastOperatorAt = now
		m.markAddressed(now, logical, ev.Text)
	case FromIRM:
		speaker = "IRM"
	}
	if ev.Kind == EvMessageChanged {
		kind += "_edit"
		speaker += " (edited)"
	}
	m.addLedger(now, "", kind, speaker+": "+ev.Text)
	m.addRecent(now, logical, speaker, ev.Text)
}

func (m *Machine) markAddressed(now time.Time, logical, text string) {
	for _, pid := range m.order {
		ps := m.personas[pid]
		p := ps.def
		alts := []string{regexp.QuoteMeta(p.ID), regexp.QuoteMeta(p.FirstName()), regexp.QuoteMeta(p.Name)}
		re := regexp.MustCompile(`(?i)\b(?:` + strings.Join(alts, "|") + `)\b`)
		if re.MatchString(text) && ps.present[logical] {
			m.addressed[pid] = now
			m.addressedIn = logical
		}
	}
}

func (m *Machine) bindCreated(now time.Time, ev Event) {
	for _, id := range m.sc.ChannelIDs() {
		ch := m.sc.Channels[id]
		if ch.Bind == nil || m.bound[id] != "" {
			continue
		}
		if ok, _ := regexp.MatchString(ch.Bind.NameMatch, ev.ChannelName); !ok {
			continue
		}
		m.bound[id], m.byID[ev.ChannelID] = ev.ChannelID, id
		m.addLedger(now, "", "channel_created", fmt.Sprintf("#%s was created and is now the %s channel", ev.ChannelName, id))
		m.effects = append(m.effects, JoinChannel{ChannelID: ev.ChannelID})
		m.log(now, "bound", map[string]string{"logical": id, "channel": ev.ChannelID, "name": ev.ChannelName}, "")
		return
	}
}

// matchEventBeats opens pending beats whose event trigger matches. It reports whether any opened.
func (m *Machine) matchEventBeats(now time.Time, logical string, ev Event) bool {
	opened := false
	for _, b := range m.beats {
		if b.status != beatPending || b.def.When == nil {
			continue
		}
		w := b.def.When
		match := false
		switch {
		case w.Operator != nil:
			match = ev.Kind == EvMessage && ev.From == FromOperator && b.opRe.MatchString(ev.Text)
		case w.Slack != nil:
			s := w.Slack
			kind := s.Kind
			if kind == "" {
				kind = string(EvMessage)
			}
			text := ev.Text
			if ev.Kind == EvChannelCreated {
				text = ev.ChannelName
			}
			match = string(ev.Kind) == kind &&
				(s.From == "" || s.From == "any" || s.From == string(ev.From) || ev.Kind == EvChannelCreated) &&
				(s.Channel == "" || s.Channel == logical) &&
				(b.slackRe == nil || b.slackRe.MatchString(text))
		}
		if match {
			m.openBeat(now, b, &ev)
			opened = true
		}
	}
	return opened
}

func (m *Machine) hasOpenBeats() bool {
	for _, b := range m.beats {
		if b.status == beatOpen {
			return true
		}
	}
	return false
}

func (m *Machine) control(now time.Time, ev Event) {
	reply := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		m.log(now, "control_reply", nil, "%s", msg)
		if ev.Reply != nil {
			ev.Reply(msg)
		}
	}
	switch ev.Command {
	case "start":
		if m.started {
			reply("chatsim: already running (%s)", m.runID)
			return
		}
		m.start(now)
		reply("chatsim: started %s", m.runID)
	case "pause":
		m.paused = true
		reply("chatsim: paused; personas are silent until resume")
	case "resume":
		m.paused = false
		reply("chatsim: resumed")
	case "stop":
		m.finish(now, "stopped by operator")
		reply("chatsim: stopped")
	case "status":
		reply("%s", m.status(now))
	case "skip":
		b := m.beatByID[strings.TrimSpace(ev.Args)]
		if b == nil || (b.status != beatPending && b.status != beatOpen) {
			reply("chatsim: no pending or open beat %q", ev.Args)
			return
		}
		m.closeBeat(now, b, beatDone, "skipped by operator")
		reply("chatsim: skipped %s", b.def.ID)
	case "say":
		m.say(now, ev, reply)
	default:
		reply("chatsim: commands are start | pause | resume | stop | status | skip <beat> | say <persona> <text>")
	}
}

func (m *Machine) say(now time.Time, ev Event, reply func(string, ...any)) {
	pid, text, _ := strings.Cut(strings.TrimSpace(ev.Args), " ")
	ps := m.personas[pid]
	text = strings.TrimSpace(text)
	if ps == nil || text == "" || !m.started {
		reply("chatsim: usage: say <persona> <text> (run must be started)")
		return
	}
	logical := m.byID[ev.ChannelID]
	if logical == "" {
		if logical = m.defaultChannel(pid); logical == "" {
			logical = m.sc.ChannelIDs()[0]
		}
	}
	m.enqueue(now, pid, logical, "", text, now, now)
	reply("chatsim: queued a line for %s in %s", pid, logical)
}

func (m *Machine) status(now time.Time) string {
	if !m.started {
		if m.armed {
			return fmt.Sprintf("chatsim: armed; chat t0 in %s", m.armedAt.Sub(now).Round(time.Second))
		}
		return "chatsim: waiting for trigger"
	}
	var open []string
	for _, b := range m.beats {
		if b.status == beatOpen {
			open = append(open, fmt.Sprintf("%s(%d/%d)", b.def.ID, b.turns, b.def.MaxTurns))
		}
	}
	return fmt.Sprintf("chatsim: run %s at +%s, paused=%t, open beats [%s], queued %d, llm calls %d/%d",
		m.runID, m.elapsed(now).Round(time.Second), m.paused, strings.Join(open, " "), len(m.queue), m.calls, m.sc.LLM.MaxLLMCalls)
}
