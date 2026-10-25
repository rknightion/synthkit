// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/script"
)

// Engine timing constants.
const (
	// Coalesce is how long the engine waits after the first wake reason before consulting the
	// director, so a burst of events costs one call.
	Coalesce = 1500 * time.Millisecond
	// WindDownGrace bounds a run after its SynthKit scenario is deactivated.
	WindDownGrace = 3 * time.Minute
	// RecentWindow and LedgerWindow bound what the director sees.
	RecentWindow = 40
	LedgerWindow = 30
	// AddressTTL is how long an addressed persona may still reply off-beat.
	AddressTTL = 2 * time.Minute
	// DirectorRetryAfter is the re-wake delay after a failed director call while beats are open.
	DirectorRetryAfter = 5 * time.Second
)

// Effect is something the runner must do for the machine.
type Effect interface{ isEffect() }

// CallDirector asks the runner to consult the director. At most one is in flight.
type CallDirector struct{ Turn Turn }

// SendPost asks the runner to post now. The runner reports back through PostDone.
type SendPost struct{ Post Post }

// JoinChannel asks the runner to join (and backfill) a newly bound channel.
type JoinChannel struct{ ChannelID string }

// Notify asks the runner to tell the operator something privately (the control DM).
type Notify struct{ Text string }

// SetScenario asks the runner to switch the SynthKit scenario on or off. The runner reports
// back through ScenarioDone.
type SetScenario struct{ Active bool }

func (CallDirector) isEffect() {}
func (SendPost) isEffect()     {}
func (JoinChannel) isEffect()  {}
func (Notify) isEffect()       {}
func (SetScenario) isEffect()  {}

// Options wires a machine to one workspace.
type Options struct {
	// Channels maps each static logical channel to its transport channel id.
	Channels map[string]string
	// OperatorID is the operator's transport user id (mention lint).
	OperatorID string
	Journal    *Journal
}

type beatStatus int

const (
	beatPending beatStatus = iota
	beatOpen
	beatDone
	beatExpired
)

func (s beatStatus) String() string {
	return [...]string{"pending", "open", "done", "expired"}[s]
}

type beatState struct {
	def        *script.Beat
	status     beatStatus
	openedAt   time.Time
	turns      int
	opRe       *regexp.Regexp
	slackRe    *regexp.Regexp
	afterRef   string
	afterDelay time.Duration
	byDeact    bool
	thenFired  bool
	evLogical  string // channel of the triggering event, the default for a channel-less beat
	adhoc      bool   // made at run time; its notice comes from whoever made it
}

type personaState struct {
	def      *script.Persona
	present  map[string]bool
	narrate  map[string]string // logical channel -> summoner, pending a narrator line
	lastDue  time.Time
	accepted []time.Time
}

type queued struct {
	seq         int
	due         time.Time
	post        Post
	persona     string
	addressedAt time.Time
}

type pendingSummon struct {
	persona, channel, by string
	due                  time.Time
}

type wakeState struct {
	pending bool
	at      time.Time
	reasons []string
	idle    bool
}

// Machine is the deterministic scenario state machine. It is not safe for concurrent use:
// one runner goroutine owns it.
type Machine struct {
	sc  *script.Scenario
	opt Options
	j   *Journal

	armed, started, ended, paused bool
	armedAt, t0                   time.Time
	runID, endReason              string
	rng                           *rand.Rand

	finalClosing bool
	windDown     bool
	windDownAt   time.Time

	beats    []*beatState
	beatByID map[string]*beatState
	personas map[string]*personaState
	order    []string

	bound map[string]string // logical -> channel id
	byID  map[string]string // channel id -> logical

	ledger  []LedgerEntry
	evSeq   int
	recent  []Message
	queue   []*queued
	sent    map[string]*queued
	postSeq int
	buckets map[string]*TokenBucket
	summons []pendingSummon
	effects []Effect

	wake        wakeState
	inflight    bool
	callIdle    bool
	callFloor   string
	calls       int
	budgetSpent bool
	retried     bool
	feedback    []string
	turnSeq     int

	addressed      map[string]time.Time
	addressedIn    string
	floorIn        string
	floorAt        time.Time
	adhoc          int
	startRe        *regexp.Regexp
	fireRe         *regexp.Regexp
	fired          bool
	lastOperatorAt time.Time
	lastActivity   time.Time
	nudged         bool
	accepted       []time.Time
}

// NewMachine builds a machine for a validated scenario.
func NewMachine(sc *script.Scenario, opt Options) (*Machine, error) {
	m := &Machine{
		sc: sc, opt: opt, j: opt.Journal,
		beatByID: map[string]*beatState{}, personas: map[string]*personaState{},
		bound: map[string]string{}, byID: map[string]string{}, sent: map[string]*queued{},
		buckets: map[string]*TokenBucket{}, addressed: map[string]time.Time{},
	}
	for _, id := range sc.ChannelIDs() {
		ch := sc.Channels[id]
		if ch.Bind != nil {
			continue
		}
		cid := opt.Channels[id]
		if cid == "" {
			return nil, fmt.Errorf("channel %q has no binding", id)
		}
		if prev, dup := m.byID[cid]; dup {
			return nil, fmt.Errorf("channels %q and %q are bound to the same id", prev, id)
		}
		m.bound[id], m.byID[cid] = cid, id
	}
	for i := range sc.Personas {
		p := &sc.Personas[i]
		ps := &personaState{def: p, present: map[string]bool{}, narrate: map[string]string{}}
		for _, c := range p.Present {
			ps.present[c] = true
		}
		m.personas[p.ID] = ps
		m.order = append(m.order, p.ID)
	}
	if p := sc.Trigger.StartPhrase; p != "" {
		m.startRe = regexp.MustCompile(p)
	}
	if p := sc.Trigger.FirePhrase; p != "" {
		m.fireRe = regexp.MustCompile(p)
	}
	for i := range sc.Beats {
		b := &sc.Beats[i]
		bs := &beatState{def: b}
		if b.After != "" {
			bs.afterRef, bs.afterDelay, _ = script.AfterRef(b.After)
		}
		if w := b.When; w != nil && w.Operator != nil {
			bs.opRe = regexp.MustCompile(w.Operator.Match)
		}
		if w := b.When; w != nil && w.Slack != nil && w.Slack.TextMatch != "" {
			bs.slackRe = regexp.MustCompile(w.Slack.TextMatch)
		}
		m.beats = append(m.beats, bs)
		m.beatByID[b.ID] = bs
	}
	return m, nil
}

// RunID is set once the run starts.
func (m *Machine) RunID() string { return m.runID }

// Started reports whether chat t0 has passed.
func (m *Machine) Started() bool { return m.started }

// Ended reports whether the run is over, and why.
func (m *Machine) Ended() bool { return m.ended }

// EndReason says why the run ended.
func (m *Machine) EndReason() string { return m.endReason }

func (m *Machine) log(now time.Time, kind string, detail any, format string, a ...any) {
	m.j.Write(Entry{At: now, RunID: m.runID, Kind: kind, Detail: detail, Message: fmt.Sprintf(format, a...)})
}

// notify tells the operator privately and journals it.
func (m *Machine) notify(now time.Time, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	m.log(now, "notify", nil, "%s", msg)
	m.effects = append(m.effects, Notify{Text: msg})
}

func (m *Machine) elapsed(now time.Time) time.Duration {
	if !m.started {
		return 0
	}
	return now.Sub(m.t0)
}

func (m *Machine) addLedger(now time.Time, id, kind, text string) string {
	if id == "" {
		m.evSeq++
		id = fmt.Sprintf("e%d", m.evSeq)
	}
	m.ledger = append(m.ledger, LedgerEntry{ID: id, Elapsed: m.elapsed(now), Kind: kind, Text: text})
	return id
}

func (m *Machine) addRecent(now time.Time, logical, speaker, text string) {
	m.recent = append(m.recent, Message{Elapsed: m.elapsed(now), Channel: logical, Speaker: speaker, Text: text})
	if len(m.recent) > RecentWindow {
		m.recent = m.recent[len(m.recent)-RecentWindow:]
	}
}

func (m *Machine) requestWake(now time.Time, reason string) {
	m.wakeAt(now.Add(Coalesce), reason)
}

// wakeAt asks for a director call no later than at.
func (m *Machine) wakeAt(at time.Time, reason string) {
	if !m.wake.pending {
		m.wake = wakeState{pending: true, at: at}
	} else if at.Before(m.wake.at) {
		m.wake.at = at
	}
	m.wake.reasons = append(m.wake.reasons, reason)
}

// channelFor resolves a logical channel to a transport id, following fallback while unbound.
func (m *Machine) channelFor(logical string) string {
	if id := m.bound[logical]; id != "" {
		return id
	}
	if ch, ok := m.sc.Channels[logical]; ok && ch.Fallback != "" {
		return m.bound[ch.Fallback]
	}
	return ""
}

func (m *Machine) bucket(channelID string) *TokenBucket {
	b := m.buckets[channelID]
	if b == nil {
		b = NewTokenBucket(1, 1)
		m.buckets[channelID] = b
	}
	return b
}

func (m *Machine) operatorFirstName() string {
	if f := strings.Fields(m.sc.Operator.Name); len(f) > 0 {
		return f[0]
	}
	return m.sc.Operator.Name
}

func (m *Machine) presentIn(pid string) []string {
	var out []string
	for c, ok := range m.personas[pid].present {
		if ok {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
