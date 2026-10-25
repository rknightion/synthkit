// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/script"
)

const testScenario = `apiVersion: synthkit.chatsim/v1
kind: ChatScenario
name: unit
seed: 1
trigger: { synthkit_scenario: bp/storm, delay: 10s }
operator: { name: Casey }
channels: { ops: {} }
personas:
  - { id: alex, name: Alex Moreno, avatar: { emoji: ":a:" }, present: [ops] }
  - { id: jordan, name: Jordan Pike, avatar: { emoji: ":b:" }, present: [] }
facts:
  - { id: lat, text: "p95 is 2400ms" }
beats:
  - { id: hello, at: "+0s", channel: ops, speakers: [alex], goal: greet, max_turns: 1 }
  - { id: get, when: { operator: { match: "(?i)get jordan" } }, speakers: [alex], goal: ping jordan, max_turns: 1,
      then: [ { summon: jordan, channel: ops, after: 5s } ] }
  - { id: arrive, when: { summoned: jordan }, speakers: [jordan], goal: say hi, max_turns: 1 }
guardrails: { quiet_after_operator: 6s, max_per_persona_per_minute: 5 }
`

// scripted answers each call with the next canned decision (empty once exhausted).
type scripted struct {
	answers []Decision
	turns   []Turn
}

func (s *scripted) Decide(_ context.Context, t Turn) (Decision, error) {
	s.turns = append(s.turns, t)
	if len(s.answers) == 0 {
		return Decision{}, nil
	}
	d := s.answers[0]
	s.answers = s.answers[1:]
	return d, nil
}

type sink struct {
	clock *FakeClock
	posts []Post
	at    []time.Duration
	base  time.Time
}

func (s *sink) Post(_ context.Context, p Post) (string, error) {
	s.posts = append(s.posts, p)
	s.at = append(s.at, s.clock.Now().Sub(s.base))
	return "1.1", nil
}
func (s *sink) Join(context.Context, string) error { return nil }

func setup(t *testing.T, answers ...Decision) (*Machine, *FakeClock, *scripted, *sink) {
	t.Helper()
	sc, err := script.Parse([]byte(testScenario))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(sc, Options{Channels: map[string]string{"ops": "C1"}, OperatorID: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := NewFakeClock(base)
	return m, clock, &scripted{answers: answers}, &sink{clock: clock, base: base}
}

func line(p, beat, text string, cites ...string) Line {
	return Line{Persona: p, Channel: "ops", Beat: beat, Text: text, Cites: cites}
}

func run(t *testing.T, m *Machine, c *FakeClock, d Director, s *sink, evs ...TimedEvent) {
	t.Helper()
	if err := Simulate(context.Background(), m, c, evs, d, s, nil, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
}

var activate = TimedEvent{At: 0, Event: Event{Kind: EvSynthkitActivated}}

func TestRisingEdgeDelayAndRetryOnLint(t *testing.T) {
	m, c, d, s := setup(t,
		Decision{Lines: []Line{line("alex", "hello", "i acked the alert already")}},
		Decision{Lines: []Line{line("alex", "hello", "morning. casey, can you take a look?")}},
	)
	run(t, m, c, d, s, activate)
	if len(d.turns) != 2 || len(d.turns[1].Feedback) == 0 || !strings.Contains(d.turns[1].Feedback[0], "irm_claim") {
		t.Fatalf("want one retry with irm_claim feedback, got turns %+v", d.turns)
	}
	if len(s.posts) != 1 || s.posts[0].Text != "morning. casey, can you take a look?" {
		t.Fatalf("posts = %+v", s.posts)
	}
	if s.at[0] < 10*time.Second+Coalesce {
		t.Fatalf("posted at %s, before trigger delay plus coalesce", s.at[0])
	}
}

func TestSecondFailureIsDroppedNotPosted(t *testing.T) {
	m, c, d, s := setup(t,
		Decision{Lines: []Line{line("alex", "hello", "I restarted the pods")}},
		Decision{Lines: []Line{line("alex", "hello", "I restarted the pods")}},
	)
	run(t, m, c, d, s, activate)
	if len(s.posts) != 0 {
		t.Fatalf("uncited claim was posted: %+v", s.posts)
	}
}

func TestOffBeatAndAbsentPersonaRejected(t *testing.T) {
	m, c, d, s := setup(t,
		Decision{Lines: []Line{line("jordan", "", "hi all"), line("alex", "", "random thought")}},
		Decision{Lines: []Line{line("jordan", "", "hi all"), line("alex", "", "random thought")}},
	)
	run(t, m, c, d, s, activate)
	if len(s.posts) != 0 {
		t.Fatalf("off-beat lines were posted: %+v", s.posts)
	}
}

func TestSummonNarratesAndQuietPeriodHolds(t *testing.T) {
	m, c, d, s := setup(t,
		Decision{Lines: []Line{line("alex", "hello", "morning")}},
		Decision{Lines: []Line{line("alex", "get", "pinging jordan")}},
		Decision{Lines: []Line{line("jordan", "arrive", "hey, what's up?")}},
	)
	var journal strings.Builder
	m.j = NewJournal(&journal)
	op := TimedEvent{At: 40 * time.Second, Event: Event{Kind: EvMessage, ChannelID: "C1", From: FromOperator, User: "U1", Text: "can we get jordan in?"}}
	run(t, m, c, d, s, activate, op)
	posted, summonedAfter := 0, -1
	for _, l := range strings.Split(journal.String(), "\n") {
		switch {
		case strings.Contains(l, `"kind":"posted"`):
			posted++
		case strings.Contains(l, `"kind":"summon_scheduled"`) && summonedAfter < 0:
			summonedAfter = posted
		}
	}
	if summonedAfter < 2 {
		t.Fatalf("summon scheduled after %d posts; it must wait for the get beat's line (the 2nd post)", summonedAfter)
	}
	var texts []string
	for _, p := range s.posts {
		texts = append(texts, p.Text)
	}
	want := []string{"morning", "pinging jordan", "Jordan Pike was added to the channel by Alex", "hey, what's up?"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("posts = %q, want %q", texts, want)
	}
	if s.at[1] < 40*time.Second+6*time.Second {
		t.Fatalf("unaddressed line posted inside the quiet period at %s", s.at[1])
	}
	if gap := s.at[2] - s.at[1]; gap < 5*time.Second {
		t.Fatalf("jordan arrived %s after alex's pinging line; the summon timer must start when that line posts", gap)
	}
	if s.posts[2].Identity != nil || s.posts[3].Identity == nil || s.at[3]-s.at[2] < time.Second {
		t.Fatalf("narrator must precede jordan by at least 1s in the app's own identity: %+v %v", s.posts, s.at)
	}
}

func TestKillSwitch(t *testing.T) {
	m, c, d, s := setup(t, Decision{Lines: []Line{line("alex", "hello", "morning")}})
	var replies []string
	reply := func(r string) { replies = append(replies, r) }
	evs := []TimedEvent{
		{At: 0, Event: Event{Kind: EvControl, Command: "start", Reply: reply}},
		{At: 0, Event: Event{Kind: EvControl, Command: "pause", Reply: reply}},
		{At: 30 * time.Second, Event: Event{Kind: EvControl, Command: "status", Reply: reply}},
		{At: 31 * time.Second, Event: Event{Kind: EvControl, Command: "say", Args: "alex manual line", Reply: reply}},
		{At: 32 * time.Second, Event: Event{Kind: EvControl, Command: "resume", Reply: reply}},
		{At: 90 * time.Second, Event: Event{Kind: EvControl, Command: "stop", Reply: reply}},
	}
	run(t, m, c, d, s, evs...)
	if !m.Ended() || m.EndReason() != "stopped by operator" {
		t.Fatalf("ended=%v reason=%q", m.Ended(), m.EndReason())
	}
	for _, p := range s.posts {
		if p.Text == "morning" && len(d.turns) > 0 && d.turns[0].Elapsed < 32*time.Second {
			t.Fatalf("director consulted while paused at %s", d.turns[0].Elapsed)
		}
	}
	if len(s.posts) != 2 || s.posts[0].Text != "manual line" || s.at[0] < 32*time.Second {
		t.Fatalf("posts = %+v at %v (say must wait for resume)", s.posts, s.at)
	}
	if !strings.Contains(strings.Join(replies, "\n"), "paused=true") {
		t.Fatalf("status reply missing pause state: %q", replies)
	}
}

func TestDeactivateBeforeT0Disarms(t *testing.T) {
	m, c, d, s := setup(t)
	run(t, m, c, d, s, activate, TimedEvent{At: 5 * time.Second, Event: Event{Kind: EvSynthkitDeactivated}})
	if m.Started() || len(d.turns) != 0 {
		t.Fatalf("run started despite deactivation before t0")
	}
}

func TestRateCapDefersAcrossMinute(t *testing.T) {
	src := strings.Replace(testScenario, "max_per_persona_per_minute: 5", "max_per_persona_per_minute: 1", 1)
	src = strings.Replace(src, "goal: greet, max_turns: 1", "goal: greet, max_turns: 3", 1)
	sc, err := script.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(sc, Options{Channels: map[string]string{"ops": "C1"}})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFakeClock(base)
	d := &scripted{answers: []Decision{
		{Lines: []Line{line("alex", "hello", "one"), line("alex", "hello", "two")}},
		{Lines: []Line{line("alex", "hello", "three")}},
	}}
	s := &sink{clock: c, base: base}
	run(t, m, c, d, s, activate)
	if len(s.posts) != 1 {
		t.Fatalf("cap of 1/min let %d lines through", len(s.posts))
	}
	rewoke := false
	for _, tn := range d.turns {
		rewoke = rewoke || tn.Elapsed >= time.Minute
	}
	if !rewoke {
		t.Fatalf("want a re-wake once the cap frees (after a minute), got %d turns", len(d.turns))
	}
}

type failing struct{ calls int }

func (f *failing) Decide(context.Context, Turn) (Decision, error) {
	f.calls++
	return Decision{}, errors.New("upstream timeout")
}

// A failed director call must not strand an open beat in a quiet room.
func TestDirectorErrorRewakes(t *testing.T) {
	m, c, _, s := setup(t)
	f := &failing{}
	if err := Simulate(context.Background(), m, c, []TimedEvent{activate}, f, s, nil, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if f.calls < 2 {
		t.Fatalf("director called %d times; an error must schedule a re-wake while beats are open", f.calls)
	}
}
