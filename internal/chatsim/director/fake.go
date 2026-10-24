// SPDX-License-Identifier: AGPL-3.0-only

package director

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// FakeLLM is a deterministic LLM for tests and rehearsal (-llm fake). It reads the structured
// turn (never the prompt text) and answers in the real response schema, so the director's
// parse and validation path runs exactly as it does against a model: for each open beat, the
// first eligible speaker who did not speak last says the beat's goal; an addressed persona
// acknowledges the operator; an idle turn gets one nudge.
type FakeLLM struct {
	// Calls counts Complete calls.
	Calls int
}

// Complete implements LLM.
func (f *FakeLLM) Complete(_ context.Context, p Prompt) (Completion, error) {
	f.Calls++
	t := p.Turn
	if t == nil {
		return Completion{}, errors.New("fake llm: prompt carries no turn")
	}
	last := ""
	if n := len(t.Recent); n > 0 {
		last = t.Recent[n-1].Speaker
	}
	names := map[string]string{}
	for _, pv := range t.Personas {
		names[pv.ID] = pv.Name
	}
	out := engine.Decision{Lines: []engine.Line{}, BeatsDone: []string{}}
	spoken := map[string]bool{}
	for _, b := range t.OpenBeats {
		if len(out.Lines) >= t.MaxLines {
			break
		}
		speaker := b.Speakers[0]
		for _, s := range b.Speakers {
			if names[s] != last && !spoken[s] {
				speaker = s
				break
			}
		}
		spoken[speaker] = true
		cites := []string{}
		if len(b.Claims) > 0 {
			cites = append(cites, b.Claims[0].ID)
		}
		out.Lines = append(out.Lines, engine.Line{Persona: speaker, Channel: b.Channel, Beat: b.ID, Text: "(" + b.ID + ") " + firstClause(b.Goal), Cites: cites})
	}
	op := strings.Fields(t.Operator.Name + " operator")[0]
	for _, a := range t.Addressed {
		if len(out.Lines) >= t.MaxLines || spoken[a] {
			continue
		}
		spoken[a] = true
		out.Lines = append(out.Lines, engine.Line{Persona: a, Channel: t.AddressedIn, Text: "heard you, " + op + ".", Cites: []string{}})
	}
	if t.Idle && len(out.Lines) == 0 {
		for _, pv := range t.Personas {
			if len(pv.PresentIn) > 0 {
				out.Lines = append(out.Lines, engine.Line{Persona: pv.ID, Channel: pv.PresentIn[0], Text: "anything new on your side, " + op + "?", Cites: []string{}})
				break
			}
		}
	}
	b, err := json.Marshal(out)
	return Completion{Content: string(b)}, err
}

// firstClause shortens a goal to its first clause so fake lines stay chat-sized.
func firstClause(goal string) string {
	if i := strings.IndexAny(goal, ";."); i > 0 {
		goal = goal[:i]
	}
	return strings.TrimSpace(goal)
}
