// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/script"
)

// TestResetEndsTheRunOnlyWhenResettable: without a seed, reset is refused and the run goes on;
// with one, it ends the run at once with EndReasonReset (no wind-down) for the host to reseed.
func TestResetEndsTheRunOnlyWhenResettable(t *testing.T) {
	for _, resettable := range []bool{false, true} {
		sc, err := script.Parse([]byte(testScenario))
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(sc, Options{Channels: map[string]string{"ops": "C1"}, OperatorID: "U1", Resettable: resettable})
		if err != nil {
			t.Fatal(err)
		}
		base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := NewFakeClock(base)
		var replies []string
		reply := func(r string) { replies = append(replies, r) }
		run(t, m, clock, &scripted{}, &sink{clock: clock, base: base},
			TimedEvent{At: 0, Event: Event{Kind: EvControl, Command: "start", Reply: reply}},
			TimedEvent{At: 10 * time.Second, Event: Event{Kind: EvControl, Command: "reset", Reply: reply}})
		if got := m.Snapshot().Resettable; got != resettable {
			t.Fatalf("snapshot resettable = %v, want %v", got, resettable)
		}
		if resettable && (!m.Ended() || m.EndReason() != EndReasonReset) {
			t.Fatalf("resettable: ended=%v reason=%q", m.Ended(), m.EndReason())
		}
		if !resettable && m.EndReason() == EndReasonReset {
			t.Fatalf("reset without a seed ended the run")
		}
		if last := replies[len(replies)-1]; resettable != strings.Contains(last, "resetting") {
			t.Fatalf("resettable=%v: last reply %q", resettable, last)
		}
	}
}

func TestParseControlKnowsReset(t *testing.T) {
	if v, _, ok := ParseControl("reset"); !ok || v != "reset" {
		t.Fatalf("reset = %q %v", v, ok)
	}
	if _, _, ok := ParseControl("reset the pods"); ok {
		t.Fatal("reset with arguments must stay ordinary chat")
	}
}
