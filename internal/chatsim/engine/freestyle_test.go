// SPDX-License-Identifier: AGPL-3.0-only

package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/director"
	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

const freestyleScenario = `apiVersion: synthkit.chatsim/v1
kind: ChatScenario
name: freestyle
seed: 3
trigger: { synthkit_scenario: bp/storm, delay: 0s, start_phrase: "(?i)good morning", fire_phrase: "(?i)quiet one today" }
operator: { name: Casey Lee, role: on-call engineer }
channels:
  general: {}
  ops: {}
personas:
  - { id: alex, name: Alex Moreno, avatar: { emoji: ":a:" }, present: [general, ops] }
  - { id: jordan, name: Jordan Pike, avatar: { emoji: ":b:" }, present: [] }
  - { id: sam, name: Sam Ortiz, avatar: { emoji: ":c:" }, present: [general] }
beats:
  - { id: chatter, at: "+0s", channel: general, speakers: [sam], goal: say morning, max_turns: 1 }
  - { id: arrive, when: { summoned: jordan }, speakers: [jordan], goal: say hi, max_turns: 1 }
end: { after: 30m }
guardrails: { quiet_after_operator: 3s, idle_nudge_after: 0s, max_messages_per_minute: 30, max_per_persona_per_minute: 10 }
`

type capture struct {
	clock   *engine.FakeClock
	posts   []engine.Post
	at      []time.Time
	notices []string
}

func (c *capture) Post(_ context.Context, p engine.Post) (string, error) {
	c.posts = append(c.posts, p)
	c.at = append(c.at, c.clock.Now())
	return "1.1", nil
}
func (c *capture) Join(context.Context, string) error { return nil }
func (c *capture) Notify(_ context.Context, text string) error {
	c.notices = append(c.notices, text)
	return nil
}

type activator struct{ calls []bool }

func (a *activator) SetActive(_ context.Context, active bool) error {
	a.calls = append(a.calls, active)
	return nil
}

func freestyle(t *testing.T) (*engine.Machine, *engine.FakeClock, engine.Director, *capture, time.Time) {
	t.Helper()
	sc, err := script.Parse([]byte(freestyleScenario))
	if err != nil {
		t.Fatal(err)
	}
	m, err := engine.NewMachine(sc, engine.Options{Channels: map[string]string{"general": "C1", "ops": "C2"}, OperatorID: "U1"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	clock := engine.NewFakeClock(base)
	return m, clock, director.New(sc, &director.FakeLLM{}), &capture{clock: clock}, base
}

func op(at time.Duration, channel, text string) engine.TimedEvent {
	return engine.TimedEvent{At: at, Event: engine.Event{Kind: engine.EvMessage, ChannelID: channel, From: engine.FromOperator, User: "U1", Text: text}}
}

// TestFreestyleNeverLeavesTheOperatorUnanswered sends off-script operator messages that name
// nobody, or name someone who is not in the room, and requires a persona post in that channel
// after each one.
func TestFreestyleNeverLeavesTheOperatorUnanswered(t *testing.T) {
	m, clock, dir, c, base := freestyle(t)
	msgs := []struct{ ch, text string }{
		{"C1", "good morning team"},
		{"C2", "anyone know if the publisher was deployed last night?"},
		{"C2", "is it just me or are things slow"},
		{"C1", "what's everyone up to today"},
		{"C2", "seeing errors on the intake API, not sure why"},
		{"C2", "can we ping Jordan please"},
		{"C2", "ok what do we know so far"},
		{"C1", "lunch plans?"},
		{"C2", "who owns the queue consumer"},
		{"C2", "right, any theories"},
	}
	var evs []engine.TimedEvent
	for i, mm := range msgs {
		evs = append(evs, op(time.Duration(i)*90*time.Second, mm.ch, mm.text))
	}
	if err := engine.Simulate(context.Background(), m, clock, evs, dir, c, &activator{}, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	for i, mm := range msgs {
		from, to := base.Add(time.Duration(i)*90*time.Second), base.Add(time.Duration(i+1)*90*time.Second)
		answered := false
		for j, p := range c.posts {
			if p.ChannelID == mm.ch && p.Identity != nil && c.at[j].After(from) && c.at[j].Before(to) {
				answered = true
			}
		}
		if !answered {
			t.Errorf("operator message %d %q in %s got no persona reply; posts: %+v", i, mm.text, mm.ch, c.posts)
		}
	}
}

// TestAskingForAnAbsentPersonaSummonsThem checks a loose request ("can we ping Jordan") brings
// Jordan in through a fetch line by someone present, then Jordan's own arrival beat.
func TestAskingForAnAbsentPersonaSummonsThem(t *testing.T) {
	m, clock, dir, c, _ := freestyle(t)
	evs := []engine.TimedEvent{op(0, "C1", "good morning"), op(30*time.Second, "C2", "can we ping Jordan please")}
	if err := engine.Simulate(context.Background(), m, clock, evs, dir, c, nil, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	var fetch, narr, hi bool
	for _, p := range c.posts {
		switch {
		case p.Identity != nil && p.Identity.PersonaID == "alex" && strings.HasPrefix(p.Beat, "fetch_jordan"):
			fetch = true
		case p.Identity == nil && strings.Contains(p.Text, "Jordan Pike was added to the channel by Alex"):
			narr = true
		case p.Identity != nil && p.Identity.PersonaID == "jordan" && p.Beat == "arrive" && p.ChannelID == "C2":
			hi = fetch && narr
		}
	}
	if !fetch || !narr || !hi {
		t.Fatalf("want alex fetch line, narrator join, then jordan's arrival in ops; got %+v", c.posts)
	}
}

// TestStartAndFirePhrases starts the chat from the operator's greeting and fires the SynthKit
// scenario once from the secret phrase, telling the operator privately.
func TestStartAndFirePhrases(t *testing.T) {
	m, clock, dir, c, _ := freestyle(t)
	act := &activator{}
	evs := []engine.TimedEvent{
		op(0, "C1", "hello all"),
		op(10*time.Second, "C1", "Good morning team"),
		op(40*time.Second, "C1", "should be a quiet one today"),
		op(80*time.Second, "C1", "famous last words, quiet one today"),
	}
	if err := engine.Simulate(context.Background(), m, clock, evs, dir, c, act, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if !m.Started() {
		t.Fatal("start phrase did not start the chat")
	}
	if len(act.calls) != 1 || !act.calls[0] {
		t.Fatalf("want exactly one activate call, got %v", act.calls)
	}
	joined := strings.Join(c.notices, "\n")
	for _, want := range []string{"Chat started by your start phrase", "Kicked off bp/storm"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notices missing %q:\n%s", want, joined)
		}
	}
	if c.posts[0].Beat != "chatter" {
		t.Errorf("first post should be the chatter beat that the start opened, got %+v", c.posts[0])
	}
}

// TestControlDMDirectionsAndFire drives the run from the control DM alone: fire starts the chat
// and activates the scenario; a stage direction naming a persona makes that persona speak.
func TestControlDMDirectionsAndFire(t *testing.T) {
	m, clock, dir, c, _ := freestyle(t)
	act := &activator{}
	var replies []string
	dm := func(at time.Duration, text string) engine.TimedEvent {
		verb, args := engine.ParseDM(text)
		return engine.TimedEvent{At: at, Event: engine.Event{Kind: engine.EvControl, Command: verb, Args: args,
			Reply: func(s string) { replies = append(replies, s) }}}
	}
	evs := []engine.TimedEvent{
		dm(0, "fire"),
		dm(30*time.Second, "have Sam mention the coffee machine is broken again"),
		dm(60*time.Second, "summon jordan ops"),
		dm(90*time.Second, "status"),
		dm(100*time.Second, "heal"),
		dm(120*time.Second, "stop"),
	}
	if err := engine.Simulate(context.Background(), m, clock, evs, dir, c, act, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(act.calls) != 2 || !act.calls[0] || act.calls[1] {
		t.Fatalf("want activate then deactivate, got %v", act.calls)
	}
	var sam, jordan bool
	for _, p := range c.posts {
		if p.Identity != nil && p.Identity.PersonaID == "sam" && strings.HasPrefix(p.Beat, "direction_") {
			sam = true
		}
		if p.Identity != nil && p.Identity.PersonaID == "jordan" && p.ChannelID == "C2" {
			jordan = true
		}
	}
	if !sam || !jordan {
		t.Fatalf("want sam's directed line and jordan in ops; replies %q posts %+v", replies, c.posts)
	}
	if !strings.Contains(strings.Join(replies, "\n"), "chatsim: run freestyle-") {
		t.Errorf("status reply missing: %q", replies)
	}
	if n := c.notices[len(c.notices)-1]; !strings.HasPrefix(n, "Run ended: stopped by operator") {
		t.Errorf("the closing notice must reach the control DM; last notice %q", n)
	}
}

func TestParseDM(t *testing.T) {
	for in, want := range map[string]string{
		"status":                      "status|",
		"Fire!":                       "fire|",
		"summon ben incident":         "summon|ben incident",
		"have Tom chase his ticket":   "direct|have Tom chase his ticket",
		"stop the pods":               "direct|stop the pods",
		"direct Lukas finds the cert": "direct|Lukas finds the cert",
		"say sandra morning all":      "say|sandra morning all",
		"pause":                       "pause|",
	} {
		v, a := engine.ParseDM(in)
		if got := v + "|" + a; got != want {
			t.Errorf("ParseDM(%q) = %q, want %q", in, got, want)
		}
	}
}
