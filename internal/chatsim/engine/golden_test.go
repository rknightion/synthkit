// SPDX-License-Identifier: AGPL-3.0-only

package engine_test

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/director"
	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

var update = flag.Bool("update", false, "rewrite golden files")

type recorder struct {
	clock *engine.FakeClock
	base  time.Time
	lines []string
	posts []engine.Post
	times []time.Time
	joins []string
}

func (r *recorder) Post(_ context.Context, p engine.Post) (string, error) {
	now := r.clock.Now()
	el := now.Sub(r.base)
	who := "* narrator:"
	if p.Identity != nil {
		who = fmt.Sprintf("%s %s:", p.Identity.Emoji, p.Identity.Name)
	}
	r.lines = append(r.lines, fmt.Sprintf("+%06.2fs #%-8s %s %s", el.Seconds(), p.Logical, who, p.Text))
	r.posts = append(r.posts, p)
	r.times = append(r.times, now)
	return fmt.Sprintf("%d.%06d", now.Unix(), len(r.posts)), nil
}

func (r *recorder) Join(_ context.Context, id string) error {
	r.joins = append(r.joins, id)
	return nil
}

func loadEvents(t *testing.T, path string) []engine.TimedEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []engine.TimedEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var raw struct {
			At    string       `json:"at"`
			Event engine.Event `json:"event"`
		}
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		d, err := time.ParseDuration(raw.At)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, engine.TimedEvent{At: d, Event: raw.Event})
	}
	return out
}

// TestGoldenBackendLatency runs the public example scenario against a recorded event log with
// the fake LLM and a fake clock, and asserts the exact posted transcript.
func TestGoldenBackendLatency(t *testing.T) {
	sc, err := script.Load(filepath.Join("..", "..", "..", "examples", "chat", "backend-latency.chat.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var journal strings.Builder
	m, err := engine.NewMachine(sc, engine.Options{
		Channels:   map[string]string{"ops": "C0OPS"},
		OperatorID: "U0OPERATOR",
		Journal:    engine.NewJournal(&journal),
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2030, 1, 2, 18, 0, 0, 0, time.UTC)
	clock := engine.NewFakeClock(base)
	rec := &recorder{clock: clock, base: base}
	llm := &director.FakeLLM{}
	events := loadEvents(t, filepath.Join("testdata", "backend-latency.events.jsonl"))
	if err := engine.Simulate(context.Background(), m, clock, events, director.New(sc, llm), rec, time.Hour); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rec.lines, "\n") + "\n"
	got += fmt.Sprintf("-- end: %s; joins %v; llm calls %d\n", m.EndReason(), rec.joins, llm.Calls)

	golden := filepath.Join("testdata", "backend-latency.golden.txt")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Fatalf("transcript differs from %s (run with -update to accept)\n--- got\n%s--- want\n%s", golden, got, want)
	}

	// Invariants the transcript must hold whatever the golden says.
	last := map[string]time.Time{}
	sawNarrator := false
	for i, p := range rec.posts {
		if prev, ok := last[p.ChannelID]; ok && rec.times[i].Sub(prev) < time.Second {
			t.Errorf("post %d in %s only %s after the previous one", i, p.ChannelID, rec.times[i].Sub(prev))
		}
		last[p.ChannelID] = rec.times[i]
		if p.Identity == nil {
			sawNarrator = true
		}
		if p.Identity != nil && p.Identity.PersonaID == "jordan" && !sawNarrator {
			t.Errorf("jordan spoke before the narrator announced him")
		}
		if strings.Contains(p.Text, "@") {
			t.Errorf("post %d contains an @: %q", i, p.Text)
		}
	}
	if err := journalHasIntentBeforeResult(journal.String()); err != nil {
		t.Error(err)
	}
}

func journalHasIntentBeforeResult(j string) error {
	intents := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(j), "\n") {
		var e struct {
			Kind   string          `json:"kind"`
			Detail json.RawMessage `json:"detail"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return fmt.Errorf("journal line is not JSON: %v", err)
		}
		var d struct {
			ID string `json:"utterance_id"`
		}
		_ = json.Unmarshal(e.Detail, &d)
		id := d.ID
		switch e.Kind {
		case "post_intent":
			intents[id] = true
		case "posted":
			if !intents[id] {
				return fmt.Errorf("posted %s without a prior post_intent", id)
			}
		}
	}
	if len(intents) == 0 {
		return fmt.Errorf("journal recorded no post intents")
	}
	return nil
}
