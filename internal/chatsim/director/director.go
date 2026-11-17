// SPDX-License-Identifier: AGPL-3.0-only

// Package director turns an engine.Turn into a prompt, asks an LLM for a strict-JSON answer,
// and validates that answer locally (retrying once) before the engine applies its guardrails.
// Prompts hold rules and the scenario's own cast and facts, never narrative of their own.
package director

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

// SchemaName names the response format.
const SchemaName = "director_turn"

// Prompt is one LLM request. Turn is carried for test doubles; real clients ignore it.
type Prompt struct {
	System     string
	User       string
	SchemaName string
	Schema     map[string]any
	Turn       *engine.Turn
}

// Completion is one LLM answer.
type Completion struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
}

// LLM is a chat-completion backend returning JSON text.
type LLM interface {
	Complete(ctx context.Context, p Prompt) (Completion, error)
}

// Director implements engine.Director over an LLM.
type Director struct {
	llm      LLM
	system   string
	schema   map[string]any
	personas []string
	channels []string
}

// New builds a director for one scenario. The system prompt is assembled once (a stable,
// cacheable prefix).
func New(sc *script.Scenario, llm LLM) *Director {
	d := &Director{llm: llm, channels: sc.ChannelIDs()}
	for _, p := range sc.Personas {
		d.personas = append(d.personas, p.ID)
	}
	d.schema = Schema(d.personas, d.channels)
	d.system = SystemPrompt(sc)
	return d
}

// Decide asks the LLM once, validates, and retries once with the validation error.
func (d *Director) Decide(ctx context.Context, t engine.Turn) (engine.Decision, error) {
	user, err := UserPrompt(t)
	if err != nil {
		return engine.Decision{}, err
	}
	p := Prompt{System: d.system, User: user, SchemaName: SchemaName, Schema: d.schema, Turn: &t}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		c, err := d.llm.Complete(ctx, p)
		if err != nil {
			return engine.Decision{Attempts: attempt}, err
		}
		dec, verr := d.Parse(c.Content, t)
		if verr == nil {
			dec.Attempts = attempt
			return dec, nil
		}
		lastErr = verr
		p.User = user + "\n\nYour previous reply was rejected: " + verr.Error() +
			"\nReply again with one JSON object that matches the schema exactly, and nothing else."
	}
	return engine.Decision{Attempts: 2}, fmt.Errorf("director: invalid response after retry: %w", lastErr)
}

// Parse decodes and structurally validates a response against the turn.
func (d *Director) Parse(content string, t engine.Turn) (engine.Decision, error) {
	s := strings.TrimSpace(content)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var out engine.Decision
	if err := dec.Decode(&out); err != nil {
		return engine.Decision{}, fmt.Errorf("not valid JSON for the schema: %w", err)
	}
	if dec.More() {
		return engine.Decision{}, errors.New("trailing content after the JSON object")
	}
	open := map[string]bool{}
	for _, b := range t.OpenBeats {
		open[b.ID] = true
	}
	var errs []string
	for i, l := range out.Lines {
		if !slices.Contains(d.personas, l.Persona) {
			errs = append(errs, fmt.Sprintf("lines[%d].persona %q is not a persona id", i, l.Persona))
		}
		if l.Channel != "" && !slices.Contains(d.channels, l.Channel) {
			errs = append(errs, fmt.Sprintf("lines[%d].channel %q is not a channel id", i, l.Channel))
		}
		if l.Beat != "" && !open[l.Beat] {
			errs = append(errs, fmt.Sprintf("lines[%d].beat %q is not an open beat", i, l.Beat))
		}
		if strings.TrimSpace(l.Text) == "" {
			errs = append(errs, fmt.Sprintf("lines[%d].text is empty", i))
		}
	}
	for _, b := range out.BeatsDone {
		if !open[b] {
			errs = append(errs, fmt.Sprintf("beats_done %q is not an open beat", b))
		}
	}
	if len(errs) > 0 {
		return engine.Decision{}, errors.New(strings.Join(errs, "; "))
	}
	if t.MaxLines > 0 && len(out.Lines) > t.MaxLines {
		out.Lines = out.Lines[:t.MaxLines]
	}
	return out, nil
}

// Schema is the strict JSON schema of a director answer.
func Schema(personas, channels []string) map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"lines", "beats_done"},
		"properties": map[string]any{
			"lines": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"persona", "channel", "beat", "text", "cites"},
					"properties": map[string]any{
						"persona": map[string]any{"type": "string", "enum": personas},
						"channel": map[string]any{"type": "string", "enum": channels},
						"beat":    str,
						"text":    str,
						"cites":   map[string]any{"type": "array", "items": str},
					},
				},
			},
			"beats_done": map[string]any{"type": "array", "items": str},
		},
	}
}
