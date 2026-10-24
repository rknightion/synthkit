// SPDX-License-Identifier: AGPL-3.0-only

package director

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

const sc = `apiVersion: synthkit.chatsim/v1
kind: ChatScenario
name: d
operator: { name: Casey, role: on-call }
channels: { ops: {} }
personas:
  - { id: alex, name: Alex Moreno, role: IC, avatar: { emoji: ":a:" }, present: [ops] }
facts: [ { id: svc, text: "the service exists" } ]
beats:
  - { id: hello, at: "+0s", speakers: [alex], goal: greet }
`

type canned struct {
	replies []string
	prompts []Prompt
}

func (c *canned) Complete(_ context.Context, p Prompt) (Completion, error) {
	c.prompts = append(c.prompts, p)
	r := c.replies[0]
	if len(c.replies) > 1 {
		c.replies = c.replies[1:]
	}
	return Completion{Content: r}, nil
}

func newDirector(t *testing.T, llm LLM) *Director {
	t.Helper()
	s, err := script.Parse([]byte(sc))
	if err != nil {
		t.Fatal(err)
	}
	return New(s, llm)
}

var turn = engine.Turn{MaxLines: 2, OpenBeats: []engine.BeatView{{ID: "hello", Speakers: []string{"alex"}, TurnsLeft: 1}}}

func TestDecideRetriesOnceWithReason(t *testing.T) {
	llm := &canned{replies: []string{
		`{"lines":[{"persona":"nobody","channel":"ops","beat":"hello","text":"hi","cites":[]}],"beats_done":[]}`,
		"```json\n" + `{"lines":[{"persona":"alex","channel":"ops","beat":"hello","text":"hi","cites":[]}],"beats_done":["hello"]}` + "\n```",
	}}
	d, err := newDirector(t, llm).Decide(context.Background(), turn)
	if err != nil {
		t.Fatal(err)
	}
	if d.Attempts != 2 || len(d.Lines) != 1 || d.BeatsDone[0] != "hello" {
		t.Fatalf("decision = %+v", d)
	}
	if !strings.Contains(llm.prompts[1].User, `"nobody" is not a persona id`) {
		t.Fatalf("retry prompt lacks the reason: %s", llm.prompts[1].User)
	}
}

func TestDecideFailsAfterSecondInvalid(t *testing.T) {
	llm := &canned{replies: []string{`{"lines":[],"beats_done":[],"extra":1}`}}
	if _, err := newDirector(t, llm).Decide(context.Background(), turn); err == nil || len(llm.prompts) != 2 {
		t.Fatalf("err=%v prompts=%d, want an error after exactly two attempts", err, len(llm.prompts))
	}
}

func TestParseRejectsClosedBeatAndTruncates(t *testing.T) {
	d := newDirector(t, &canned{})
	if _, err := d.Parse(`{"lines":[{"persona":"alex","channel":"ops","beat":"gone","text":"x","cites":[]}],"beats_done":[]}`, turn); err == nil {
		t.Fatal("line for a beat that is not open was accepted")
	}
	three := `{"lines":[` + strings.Repeat(`{"persona":"alex","channel":"ops","beat":"","text":"x","cites":[]},`, 2) +
		`{"persona":"alex","channel":"ops","beat":"","text":"x","cites":[]}],"beats_done":[]}`
	out, err := d.Parse(three, turn)
	if err != nil || len(out.Lines) != 2 {
		t.Fatalf("got %d lines, err %v; want truncation to max_lines", len(out.Lines), err)
	}
}

func TestSchemaAndPromptShape(t *testing.T) {
	d := newDirector(t, &canned{})
	b, _ := json.Marshal(d.schema)
	for _, want := range []string{`"additionalProperties":false`, `"enum":["alex"]`, `"required":["persona","channel","beat","text","cites"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("schema lacks %s: %s", want, b)
		}
	}
	for _, want := range []string{"Alex Moreno", "svc: the service exists", "Grafana IRM", "Casey"} {
		if !strings.Contains(d.system, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	u, err := UserPrompt(turn)
	if err != nil || !strings.Contains(u, `"open_beats"`) {
		t.Fatalf("user prompt = %s, %v", u, err)
	}
}

func TestFakeLLMIsValid(t *testing.T) {
	f := &FakeLLM{}
	d, err := newDirector(t, f).Decide(context.Background(), turn)
	if err != nil || len(d.Lines) != 1 || d.Lines[0].Beat != "hello" || d.Attempts != 1 {
		t.Fatalf("fake decision = %+v, %v", d, err)
	}
}
