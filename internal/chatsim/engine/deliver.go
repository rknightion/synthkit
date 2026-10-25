// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/script"
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
			m.notify(now, "SynthKit reports %s active; the chat starts in %s.", m.sc.Trigger.SynthkitScenario, m.sc.Trigger.Delay.Duration)
		} else if m.started {
			m.fired = true
			m.notify(now, "SynthKit confirms %s is active. The page follows once the alert's pending period passes.", m.sc.Trigger.SynthkitScenario)
		}
		return
	case EvSynthkitDeactivated:
		if m.armed && !m.started {
			m.armed = false
			m.log(now, "disarmed", nil, "synthkit scenario deactivated before chat t0")
			m.notify(now, "SynthKit scenario deactivated before the chat started; disarmed.")
			return
		}
		if m.started && !m.windDown {
			m.notify(now, "SynthKit reports %s inactive; winding the chat down.", m.sc.Trigger.SynthkitScenario)
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
	if !m.started && m.startRe != nil && ev.Kind == EvMessage && ev.From == FromOperator && m.byID[ev.ChannelID] != "" && m.startRe.MatchString(ev.Text) {
		m.start(now)
		m.notify(now, "Chat started by your start phrase (%s).", m.runID)
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
		if m.fireRe != nil && !m.fired && m.fireRe.MatchString(ev.Text) {
			m.fire(now, "your fire phrase")
		}
		summoned := m.autoSummon(now, logical, ev.Text, opened)
		if len(opened) == 0 && !summoned && !m.namesPresent(logical, ev.Text) {
			m.floorIn, m.floorAt = logical, now
		} else {
			m.floorIn = ""
		}
		m.requestWake(now, "operator message")
	} else if len(opened) == 0 && ev.Kind != EvChannelCreated && m.hasOpenBeats() {
		m.requestWake(now, string(ev.Kind)+" from "+string(ev.From))
	}
}

// fire asks the runner to activate the SynthKit scenario, once per run.
func (m *Machine) fire(now time.Time, why string) {
	m.fired = true
	m.log(now, "fire", map[string]string{"scenario": m.sc.Trigger.SynthkitScenario, "why": why}, "")
	m.effects = append(m.effects, SetScenario{Active: true})
}

// ScenarioDone records the runner's answer for a SetScenario.
func (m *Machine) ScenarioDone(now time.Time, active bool, err error) {
	verb := "activate"
	if !active {
		verb = "deactivate"
	}
	if err != nil {
		if active {
			m.fired = false
		}
		m.log(now, "scenario_error", nil, "%s: %v", verb, err)
		m.notify(now, "Could not %s %s: %v", verb, m.sc.Trigger.SynthkitScenario, err)
		return
	}
	m.log(now, "scenario_set", map[string]bool{"active": active}, "")
	if active {
		m.notify(now, "Kicked off %s.", m.sc.Trigger.SynthkitScenario)
	} else {
		m.notify(now, "Switched %s off; the chat winds down when SynthKit reports it inactive.", m.sc.Trigger.SynthkitScenario)
	}
}

// namePattern matches a persona's id, first name or full name as a word.
func namePattern(id, first, full string) *regexp.Regexp {
	alts := []string{regexp.QuoteMeta(id), regexp.QuoteMeta(first), regexp.QuoteMeta(full)}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(alts, "|") + `)\b`)
}

func (m *Machine) named(pid, text string) bool {
	p := m.personas[pid].def
	return namePattern(p.ID, p.FirstName(), p.Name).MatchString(text)
}

// namesPresent reports whether text names any persona present in logical.
func (m *Machine) namesPresent(logical, text string) bool {
	for _, pid := range m.order {
		if m.personas[pid].present[logical] && m.named(pid, text) {
			return true
		}
	}
	return false
}

// summonCue is a loose request cue: naming an absent persona together with one of these
// words brings them into the channel.
var summonCue = regexp.MustCompile(`(?i)\b(get|grab|page|paging|pull|bring|loop|ping|add|invite|call|need|want|ask|involve|rope|shout|fetch|have|where)\b`)

// autoSummon brings in every absent persona the operator asks for, unless a beat this message
// opened already summons them. A persona present in the channel says they are fetching them;
// with nobody there, the operator adds them directly. It reports whether anyone was summoned.
func (m *Machine) autoSummon(now time.Time, logical, text string, opened []*beatState) bool {
	if !summonCue.MatchString(text) {
		return false
	}
	scripted := map[string]bool{}
	for _, b := range opened {
		for _, a := range b.def.Then {
			scripted[a.Summon] = true
		}
	}
	for _, s := range m.summons {
		scripted[s.persona] = true
	}
	did := false
	for _, pid := range m.order {
		ps := m.personas[pid]
		if ps.present[logical] || scripted[pid] || !m.named(pid, text) {
			continue
		}
		did = true
		by := ""
		for _, other := range m.order {
			if other != pid && m.personas[other].present[logical] {
				by = other
				break
			}
		}
		if by == "" {
			m.summons = append(m.summons, pendingSummon{persona: pid, channel: logical, by: m.operatorFirstName(), due: now.Add(5 * time.Second)})
			m.notify(now, "Adding %s to %s.", ps.def.FirstName(), logical)
			continue
		}
		m.adhoc++
		b := &script.Beat{
			ID: fmt.Sprintf("fetch_%s_%d", pid, m.adhoc), Channel: logical, Speakers: []string{by}, MaxTurns: 1,
			Goal:         fmt.Sprintf("%s tells %s in one short line that they're pulling %s in now.", m.personas[by].def.FirstName(), m.operatorFirstName(), ps.def.FirstName()),
			ExpiresAfter: script.Duration{Duration: 45 * time.Second},
			Then:         []script.Action{{Summon: pid, Channel: logical, After: script.Range{Min: 20 * time.Second, Max: 40 * time.Second}}},
		}
		m.addBeat(now, b)
		m.notify(now, "%s is fetching %s into %s.", m.personas[by].def.FirstName(), ps.def.FirstName(), logical)
	}
	return did
}

// addBeat registers and opens a beat made at run time (summons, stage directions).
func (m *Machine) addBeat(now time.Time, b *script.Beat) *beatState {
	bs := &beatState{def: b, adhoc: true}
	m.beats = append(m.beats, bs)
	m.beatByID[b.ID] = bs
	m.openBeat(now, bs, nil)
	return bs
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
		if m.named(pid, text) && m.personas[pid].present[logical] {
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

// matchEventBeats opens pending beats whose event trigger matches and returns them.
func (m *Machine) matchEventBeats(now time.Time, logical string, ev Event) []*beatState {
	var opened []*beatState
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
			opened = append(opened, b)
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
	case "fire":
		switch {
		case m.sc.Trigger.SynthkitScenario == "":
			reply("chatsim: this scenario names no trigger.synthkit_scenario")
		case m.fired:
			reply("chatsim: %s was already fired this run", m.sc.Trigger.SynthkitScenario)
		default:
			if !m.started {
				m.start(now)
			}
			m.fire(now, "operator")
			reply("chatsim: activating %s", m.sc.Trigger.SynthkitScenario)
		}
	case "heal":
		if m.sc.Trigger.SynthkitScenario == "" {
			reply("chatsim: this scenario names no trigger.synthkit_scenario")
			return
		}
		m.effects = append(m.effects, SetScenario{Active: false})
		reply("chatsim: deactivating %s", m.sc.Trigger.SynthkitScenario)
	case "summon":
		m.summonCmd(now, ev.Args, reply)
	case "direct":
		m.direct(now, ev.Args, reply)
	default:
		reply("chatsim: " + ControlHelp)
	}
}

// summonCmd handles "summon <persona> [channel]": the persona arrives at once.
func (m *Machine) summonCmd(now time.Time, args string, reply func(string, ...any)) {
	f := strings.Fields(args)
	if len(f) == 0 || !m.started {
		reply("chatsim: usage: summon <persona> [channel] (run must be started)")
		return
	}
	pid := m.personaByWord(f[0])
	if pid == "" {
		reply("chatsim: no persona %q", f[0])
		return
	}
	logical := m.summonTarget()
	if len(f) > 1 {
		logical = strings.TrimPrefix(f[1], "#")
	}
	if _, ok := m.sc.Channels[logical]; !ok || m.channelFor(logical) == "" {
		reply("chatsim: channel %q is unknown or not bound yet", logical)
		return
	}
	if m.personas[pid].present[logical] {
		reply("chatsim: %s is already in %s", pid, logical)
		return
	}
	m.arrive(now, pendingSummon{persona: pid, channel: logical, by: m.operatorFirstName(), due: now})
	m.requestWake(now, "summoned "+pid)
	reply("chatsim: %s is in %s", pid, logical)
}

// summonTarget is where a bare summon lands: the incident room once it exists, else the
// first persona's usual channel.
func (m *Machine) summonTarget() string {
	logical := ""
	for _, id := range m.sc.ChannelIDs() {
		if m.sc.Channels[id].Bind != nil && m.bound[id] != "" {
			logical = id
		}
	}
	if logical == "" {
		logical = m.defaultChannel(m.order[0])
	}
	return logical
}

// direct turns a stage direction into a one-off beat. Personas it names speak; with none
// named, anyone present may. "#channel" or "in <channel>" pins the room.
func (m *Machine) direct(now time.Time, text string, reply func(string, ...any)) {
	if !m.started {
		reply("chatsim: the chat has not started; say start (or fire) first")
		return
	}
	var speakers, absent []string
	for _, pid := range m.order {
		if !m.named(pid, text) {
			continue
		}
		if len(m.presentIn(pid)) == 0 {
			absent = append(absent, pid)
			continue
		}
		speakers = append(speakers, pid)
	}
	if len(speakers) == 0 && len(absent) > 0 {
		reply("chatsim: %s is not in any channel yet; summon them first", strings.Join(absent, ", "))
		return
	}
	if len(speakers) == 0 {
		for _, pid := range m.order {
			if len(m.presentIn(pid)) > 0 {
				speakers = append(speakers, pid)
			}
		}
	}
	channel := ""
	for _, id := range m.sc.ChannelIDs() {
		re := regexp.MustCompile(`(?i)(#|\bin (the )?)` + regexp.QuoteMeta(id) + `\b`)
		if re.MatchString(text) && m.channelFor(id) != "" {
			channel = id
			break
		}
	}
	if channel != "" {
		var here []string
		for _, pid := range speakers {
			if m.personas[pid].present[channel] {
				here = append(here, pid)
			}
		}
		if len(here) == 0 {
			reply("chatsim: none of %s is in %s", strings.Join(speakers, ", "), channel)
			return
		}
		speakers = here
	}
	m.adhoc++
	turns := 2
	if len(speakers) == 1 {
		turns = 1
	}
	b := &script.Beat{
		ID: fmt.Sprintf("direction_%d", m.adhoc), Channel: channel, Speakers: speakers, MaxTurns: turns,
		Goal:         "Stage direction from the operator, played in character: " + text,
		ExpiresAfter: script.Duration{Duration: 3 * time.Minute},
	}
	m.addBeat(now, b)
	where := channel
	if where == "" {
		where = "their usual channel"
	}
	reply("chatsim: directing %s in %s", strings.Join(speakers, ", "), where)
}

// personaByWord resolves a persona id or first name.
func (m *Machine) personaByWord(w string) string {
	for _, pid := range m.order {
		p := m.personas[pid].def
		if strings.EqualFold(w, p.ID) || strings.EqualFold(w, p.FirstName()) {
			return pid
		}
	}
	return ""
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
