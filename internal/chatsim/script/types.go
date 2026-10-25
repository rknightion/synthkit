// SPDX-License-Identifier: AGPL-3.0-only

// Package script defines the chat-scenario file format (kind: ChatScenario), its strict YAML
// loader and its validation. A scenario carries the cast, logical channels, declared facts,
// beats and guardrails. It never carries workspace bindings (channel IDs, user IDs, tokens):
// those live in a separate bindings file owned by cmd/synthkit-chat.
package script

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// APIVersion and Kind are the only accepted header values.
const (
	APIVersion = "synthkit.chatsim/v1"
	Kind       = "ChatScenario"
)

// Scenario is one chat scenario file.
type Scenario struct {
	APIVersion  string             `yaml:"apiVersion"`
	Kind        string             `yaml:"kind"`
	Name        string             `yaml:"name"`
	Description string             `yaml:"description"`
	Seed        uint64             `yaml:"seed"`
	Trigger     Trigger            `yaml:"trigger"`
	LLM         LLM                `yaml:"llm"`
	Operator    Operator           `yaml:"operator"`
	Channels    map[string]Channel `yaml:"channels"`
	Personas    []Persona          `yaml:"personas"`
	Facts       []Fact             `yaml:"facts"`
	Beats       []Beat             `yaml:"beats"`
	End         End                `yaml:"end"`
	Guardrails  Guardrails         `yaml:"guardrails"`
}

// Trigger ties the chat run to a SynthKit failure scenario.
type Trigger struct {
	// SynthkitScenario is the exact "<blueprint>/<name>" id from GET /control/schema.
	SynthkitScenario string   `yaml:"synthkit_scenario"`
	Delay            Duration `yaml:"delay"`
	OnDeactivate     string   `yaml:"on_deactivate"`
	// StartPhrase, a regexp, starts the chat when the operator posts a match in a static
	// channel before the run has started; the message is then handled as the run's first event.
	StartPhrase string `yaml:"start_phrase"`
	// FirePhrase, a regexp, activates the SynthKit scenario once when the operator posts a
	// match in any of the run's channels after the chat has started.
	FirePhrase string `yaml:"fire_phrase"`
}

// LLM selects the director model and its budgets.
type LLM struct {
	Model           string   `yaml:"model"`
	Temperature     *float64 `yaml:"temperature"`
	MaxTokens       int      `yaml:"max_tokens"`
	MaxLLMCalls     int      `yaml:"max_llm_calls"`
	ReasoningEffort string   `yaml:"reasoning_effort"`
	ProviderOrder   []string `yaml:"provider_order"`
	ZDR             bool     `yaml:"zdr"`
}

// Operator describes the only human in the room. His Slack ID comes from bindings.
type Operator struct {
	Name string `yaml:"name"`
	Role string `yaml:"role"`
}

// Channel is a logical channel. A static channel is bound to an ID in bindings; a dynamic one
// is bound at runtime by an event and posts to Fallback until then.
type Channel struct {
	Bind     *ChannelBind `yaml:"bind"`
	Fallback string       `yaml:"fallback"`
}

// ChannelBind binds a logical channel when a matching channel is created.
type ChannelBind struct {
	On        string `yaml:"on"`
	NameMatch string `yaml:"name_match"`
}

// Persona is one simulated coworker. Personas are data, not agents.
type Persona struct {
	ID             string   `yaml:"id"`
	Name           string   `yaml:"name"`
	Role           string   `yaml:"role"`
	Personality    string   `yaml:"personality"`
	Style          string   `yaml:"style"`
	Examples       []string `yaml:"examples"`
	Avatar         Avatar   `yaml:"avatar"`
	TypingWPM      int      `yaml:"typing_wpm"`
	Responsiveness *float64 `yaml:"responsiveness"`
	Present        []string `yaml:"present"`
}

// FirstName is the persona's first name token.
func (p Persona) FirstName() string {
	if f := strings.Fields(p.Name); len(f) > 0 {
		return f[0]
	}
	return p.ID
}

// Avatar is emoji only in this release.
type Avatar struct {
	Emoji string `yaml:"emoji"`
}

// Fact is declared ground truth the cast may cite.
type Fact struct {
	ID   string `yaml:"id"`
	Text string `yaml:"text"`
}

// Beat is one scripted moment. Exactly one of At, After or When is set.
type Beat struct {
	ID           string   `yaml:"id"`
	At           *Offset  `yaml:"at"`
	After        string   `yaml:"after"`
	When         *When    `yaml:"when"`
	Channel      string   `yaml:"channel"`
	Speakers     []string `yaml:"speakers"`
	Goal         string   `yaml:"goal"`
	MaxTurns     int      `yaml:"max_turns"`
	ExpiresAfter Duration `yaml:"expires_after"`
	Record       *Record  `yaml:"record"`
	Then         []Action `yaml:"then"`
	MayClaim     []string `yaml:"may_claim"`
	Final        bool     `yaml:"final"`
}

// When is an event trigger. Exactly one field is set.
type When struct {
	Operator *OperatorWhen `yaml:"operator"`
	Slack    *SlackWhen    `yaml:"slack"`
	Synthkit string        `yaml:"synthkit"`
	Summoned string        `yaml:"summoned"`
}

// OperatorWhen fires on an operator message matching Match. Intent classification is not
// implemented in this release; a non-empty Intent is rejected at load.
type OperatorWhen struct {
	Match  string `yaml:"match"`
	Intent string `yaml:"intent"`
}

// SlackWhen fires on a Slack event.
type SlackWhen struct {
	From      string `yaml:"from"`
	Kind      string `yaml:"kind"`
	Channel   string `yaml:"channel"`
	TextMatch string `yaml:"text_match"`
}

// Record turns the triggering event's text into a citable fact.
type Record struct {
	Fact string `yaml:"fact"`
	From string `yaml:"from"`
}

// Action runs when its beat opens.
type Action struct {
	Summon  string `yaml:"summon"`
	Channel string `yaml:"channel"`
	After   Range  `yaml:"after"`
}

// End bounds the whole run.
type End struct {
	After Duration `yaml:"after"`
}

// Guardrails are the deterministic budgets.
type Guardrails struct {
	MaxMessagesPerMinute   int      `yaml:"max_messages_per_minute"`
	MaxPerPersonaPerMinute int      `yaml:"max_per_persona_per_minute"`
	QuietAfterOperator     Duration `yaml:"quiet_after_operator"`
	IdleNudgeAfter         Duration `yaml:"idle_nudge_after"`
	MaxLineChars           int      `yaml:"max_line_chars"`
	MaxLinesPerTurn        int      `yaml:"max_lines_per_turn"`
}

// Duration is a Go duration string ("20s", "1m30s").
type Duration struct{ time.Duration }

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("line %d: duration must be a string like \"20s\"", n.Line)
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	if v < 0 {
		return fmt.Errorf("line %d: duration %q is negative", n.Line, s)
	}
	d.Duration = v
	return nil
}

// Offset is "+Ns" from chat t0.
type Offset struct{ time.Duration }

// UnmarshalYAML parses "+20s" (the leading plus is required).
func (o *Offset) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("line %d: offset must be a string like \"+20s\"", n.Line)
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "+") {
		return fmt.Errorf("line %d: offset %q must start with '+'", n.Line, s)
	}
	v, err := time.ParseDuration(s[1:])
	if err != nil || v < 0 {
		return fmt.Errorf("line %d: bad offset %q", n.Line, s)
	}
	o.Duration = v
	return nil
}

// Range is "25s..45s" or a single duration.
type Range struct {
	Min, Max time.Duration
}

// UnmarshalYAML parses "lo..hi" or "d".
func (r *Range) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("line %d: range must be a string like \"25s..45s\"", n.Line)
	}
	lo, hi, found := strings.Cut(strings.TrimSpace(s), "..")
	a, err := time.ParseDuration(strings.TrimSpace(lo))
	if err != nil || a < 0 {
		return fmt.Errorf("line %d: bad range %q", n.Line, s)
	}
	b := a
	if found {
		if b, err = time.ParseDuration(strings.TrimSpace(hi)); err != nil || b < a {
			return fmt.Errorf("line %d: bad range %q (want lo..hi with lo <= hi)", n.Line, s)
		}
	}
	r.Min, r.Max = a, b
	return nil
}

// AfterRef splits an "after:" value "<beat>[+Ns]".
func AfterRef(s string) (beat string, delay time.Duration, err error) {
	beat, off, found := strings.Cut(strings.TrimSpace(s), "+")
	beat = strings.TrimSpace(beat)
	if found {
		if delay, err = time.ParseDuration(strings.TrimSpace(off)); err != nil || delay < 0 {
			return "", 0, fmt.Errorf("bad after %q (want <beat>[+Ns])", s)
		}
	}
	return beat, delay, nil
}
