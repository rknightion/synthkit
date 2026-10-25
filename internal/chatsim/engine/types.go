// SPDX-License-Identifier: AGPL-3.0-only

// Package engine is the deterministic core of the chat-scenario engine: the beat state machine,
// the ledger, guardrails and lint, pacing, and the journal. It owns time and decides when the
// director is consulted; the director decides only who speaks and what they say. It uses the
// standard library and the sibling script package only, and never touches a network itself.
package engine

import (
	"context"
	"time"
)

// EventKind classifies an inbound event.
type EventKind string

// Inbound event kinds.
const (
	EvMessage             EventKind = "message"
	EvMessageChanged      EventKind = "message_changed"
	EvChannelCreated      EventKind = "channel_created"
	EvMemberJoined        EventKind = "member_joined_channel"
	EvSynthkitActivated   EventKind = "synthkit_activated"
	EvSynthkitDeactivated EventKind = "synthkit_deactivated"
	EvControl             EventKind = "control"
)

// Source is who produced a message event. Transports map raw identities onto these.
type Source string

// Message sources.
const (
	FromOperator Source = "operator"
	FromIRM      Source = "irm"
	FromOther    Source = "other"
)

// Event is one inbound event, already translated by a transport. Our own posts never arrive
// here: transports drop their echoes.
type Event struct {
	Kind        EventKind `json:"kind"`
	ChannelID   string    `json:"channel,omitempty"`
	ChannelName string    `json:"channel_name,omitempty"`
	From        Source    `json:"from,omitempty"`
	User        string    `json:"user,omitempty"`
	Text        string    `json:"text,omitempty"`
	TS          string    `json:"ts,omitempty"`
	// Command and Args carry a kill-switch command (Kind == EvControl).
	Command string `json:"command,omitempty"`
	Args    string `json:"args,omitempty"`
	// Reply, when set, answers a control command privately to the operator.
	Reply func(string) `json:"-"`
}

// Identity is how a persona appears in the room.
type Identity struct {
	PersonaID string `json:"persona"`
	Name      string `json:"name"`
	Emoji     string `json:"emoji"`
}

// Post is one outbound message. Narrator posts (Identity == nil) appear in the app's own
// identity; persona posts carry a username and emoji.
type Post struct {
	RunID       string    `json:"run_id"`
	UtteranceID string    `json:"utterance_id"`
	ChannelID   string    `json:"channel"`
	Logical     string    `json:"logical"`
	Identity    *Identity `json:"identity,omitempty"`
	Text        string    `json:"text"`
	Beat        string    `json:"beat,omitempty"`
}

// Transport sends posts and joins channels. Implementations must never edit a persona message
// or use platform-side scheduling.
type Transport interface {
	Post(ctx context.Context, p Post) (ts string, err error)
	Join(ctx context.Context, channelID string) error
}

// Notifier is implemented by a transport that can reach the operator privately (the control
// DM). Transports without one drop notices; the journal still records them.
type Notifier interface {
	Notify(ctx context.Context, text string) error
}

// Activator switches the SynthKit failure scenario on or off.
type Activator interface {
	SetActive(ctx context.Context, active bool) error
}

// Director decides who speaks and what they say for one wake-up.
type Director interface {
	Decide(ctx context.Context, t Turn) (Decision, error)
}

// Turn is everything the director may see for one wake-up.
type Turn struct {
	Seq         int           `json:"seq"`
	Elapsed     time.Duration `json:"elapsed"`
	Reasons     []string      `json:"reasons"`
	Operator    OperatorView  `json:"operator"`
	Personas    []PersonaView `json:"personas"`
	Channels    []string      `json:"channels"`
	OpenBeats   []BeatView    `json:"open_beats"`
	Ledger      []LedgerEntry `json:"ledger"`
	Recent      []Message     `json:"recent"`
	Addressed   []string      `json:"addressed"`
	AddressedIn string        `json:"addressed_in,omitempty"`
	// Floor lists the personas who may answer an operator message that named nobody, in
	// FloorIn; at most one of them replies.
	Floor        []string `json:"floor,omitempty"`
	FloorIn      string   `json:"floor_in,omitempty"`
	Idle         bool     `json:"idle"`
	MaxLines     int      `json:"max_lines"`
	MaxLineChars int      `json:"max_line_chars"`
	// Feedback lists why lines of the previous attempt were rejected (retry only).
	Feedback []string `json:"feedback,omitempty"`
}

// OperatorView describes the human.
type OperatorView struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// PersonaView is a persona card plus where it may currently speak.
type PersonaView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Personality string   `json:"personality"`
	Style       string   `json:"style"`
	Examples    []string `json:"examples,omitempty"`
	PresentIn   []string `json:"present_in"`
}

// BeatView is an open beat with its remaining budget.
type BeatView struct {
	ID        string   `json:"id"`
	Goal      string   `json:"goal"`
	Channel   string   `json:"channel,omitempty"`
	Speakers  []string `json:"speakers"`
	TurnsUsed int      `json:"turns_used"`
	TurnsLeft int      `json:"turns_left"`
	Claims    []Claim  `json:"claims,omitempty"`
}

// Claim is a citable may_claim entry of a beat.
type Claim struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// LedgerEntry is something that actually happened, or a declared fact.
type LedgerEntry struct {
	ID      string        `json:"id"`
	Elapsed time.Duration `json:"elapsed"`
	Kind    string        `json:"kind"`
	Text    string        `json:"text"`
}

// Message is one transcript line as the room saw it.
type Message struct {
	Elapsed time.Duration `json:"elapsed"`
	Channel string        `json:"channel"`
	Speaker string        `json:"speaker"`
	Text    string        `json:"text"`
}

// Decision is the director's answer.
type Decision struct {
	Lines     []Line   `json:"lines"`
	BeatsDone []string `json:"beats_done"`
	// Attempts is how many LLM calls the decision took (budget accounting).
	Attempts int `json:"-"`
}

// Line is one proposed persona line.
type Line struct {
	Persona string   `json:"persona"`
	Channel string   `json:"channel"`
	Beat    string   `json:"beat"`
	Text    string   `json:"text"`
	Cites   []string `json:"cites"`
}

// Clock abstracts time so tests run on a fake clock.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is the subset of *time.Timer the engine needs.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}
