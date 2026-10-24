// SPDX-License-Identifier: AGPL-3.0-only

package director

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

const rules = `You direct a fictional team chat during a simulated production incident. A real human,
the operator, is in the room. Everyone else is a persona you voice. You never speak as the operator.

Each turn you receive the current state as JSON and answer with ONE JSON object that matches the
response schema exactly: {"lines":[...],"beats_done":[...]}. An empty "lines" array is a good answer
when nobody would naturally speak.

Rules, all mandatory:
1. Only write lines that serve an open beat (set "beat" to its id and use only its listed speakers),
   or reply to a persona the operator just addressed ("addressed"; set "beat" to ""), or, only when
   "idle" is true, at most one short in-character nudge (beat "").
2. A persona speaks only in a channel listed in its "present_in". Use the beat's channel when it has one.
3. The operator alone acts in Grafana IRM: acknowledging, resolving, escalating, silencing, paging or
   declaring. Personas ask him to do these things. They never claim to have done one, never say they
   will do one, and never type a slash command.
4. Never write @here, @channel, @everyone, or an @mention of any app or person. Address people by
   first name in plain text.
5. Never invent metrics, numbers with units, URLs, people or systems. Use only what the ledger and
   facts say. A persona who has not checked something says so.
6. A completed action ("restarted", "rolled back", "fixed", "pinged") is only allowed when the ledger
   or a beat claim records it; put that entry's id in "cites".
7. Write like Slack: short, natural, in each persona's own style. At most %d lines per turn, each
   under %d characters. Never repeat a line already in "recent". Never mention being simulated or AI.
8. Add a beat id to "beats_done" once its goal has been fully achieved.`

// SystemPrompt is the static, cacheable prefix: rules, operator, channels, cast and facts.
func SystemPrompt(sc *script.Scenario) string {
	var b strings.Builder
	fmt.Fprintf(&b, rules, sc.Guardrails.MaxLinesPerTurn, sc.Guardrails.MaxLineChars)
	fmt.Fprintf(&b, "\n\nOperator (the human): %s, %s.\n", sc.Operator.Name, sc.Operator.Role)
	fmt.Fprintf(&b, "Channels: %s.\n\nCast:\n", strings.Join(sc.ChannelIDs(), ", "))
	for _, p := range sc.Personas {
		fmt.Fprintf(&b, "- id %s: %s, %s. Personality: %s. Style: %s.", p.ID, p.Name, p.Role, p.Personality, p.Style)
		if len(p.Examples) > 0 {
			fmt.Fprintf(&b, " Example lines: %q.", p.Examples)
		}
		b.WriteByte('\n')
	}
	if len(sc.Facts) > 0 {
		b.WriteString("\nDeclared facts (citable by id):\n")
		for _, f := range sc.Facts {
			fmt.Fprintf(&b, "- %s: %s\n", f.ID, f.Text)
		}
	}
	return b.String()
}

type userState struct {
	Elapsed     string              `json:"elapsed"`
	Reasons     []string            `json:"wake_reasons"`
	Personas    []presence          `json:"personas"`
	OpenBeats   []engine.BeatView   `json:"open_beats"`
	Ledger      []ledgerLine        `json:"ledger"`
	Recent      []recentLine        `json:"recent"`
	Addressed   []string            `json:"addressed"`
	AddressedIn string              `json:"addressed_in,omitempty"`
	Idle        bool                `json:"idle"`
	MaxLines    int                 `json:"max_lines"`
	Feedback    []string            `json:"previous_attempt_rejected,omitempty"`
	Operator    engine.OperatorView `json:"operator"`
}

type presence struct {
	ID        string   `json:"id"`
	PresentIn []string `json:"present_in"`
}

type ledgerLine struct {
	ID   string `json:"id"`
	At   string `json:"at"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type recentLine struct {
	At      string `json:"at"`
	Channel string `json:"channel"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

func at(d time.Duration) string { return "+" + d.Round(time.Second).String() }

// UserPrompt is the dynamic tail for one turn.
func UserPrompt(t engine.Turn) (string, error) {
	u := userState{
		Elapsed: at(t.Elapsed), Reasons: t.Reasons, OpenBeats: t.OpenBeats, Addressed: t.Addressed,
		AddressedIn: t.AddressedIn, Idle: t.Idle, MaxLines: t.MaxLines, Feedback: t.Feedback, Operator: t.Operator,
	}
	for _, p := range t.Personas {
		u.Personas = append(u.Personas, presence{ID: p.ID, PresentIn: p.PresentIn})
	}
	for _, e := range t.Ledger {
		u.Ledger = append(u.Ledger, ledgerLine{ID: e.ID, At: at(e.Elapsed), Kind: e.Kind, Text: e.Text})
	}
	for _, m := range t.Recent {
		u.Recent = append(u.Recent, recentLine{At: at(m.Elapsed), Channel: m.Channel, Speaker: m.Speaker, Text: m.Text})
	}
	if u.OpenBeats == nil {
		u.OpenBeats = []engine.BeatView{}
	}
	b, err := json.MarshalIndent(u, "", " ")
	if err != nil {
		return "", err
	}
	return "Current state:\n" + string(b), nil
}
