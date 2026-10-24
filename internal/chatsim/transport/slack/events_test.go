// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// Payloads are invented, shaped like Events API inner events. No network is used.
var ids = Identities{OwnBotID: "B0OURS", OwnUserID: "U0OURBOT", OperatorID: "U0OP", IRMBotIDs: []string{"B0IRM"}}

func TestTranslate(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"operator", `{"type":"message","channel":"C1","user":"U0OP","text":"get jordan","ts":"1.1"}`, "message|C1|operator|get jordan"},
		{"our echo dropped", `{"type":"message","channel":"C1","bot_id":"B0OURS","username":"Alex Moreno","text":"x","ts":"1.2"}`, ""},
		{"irm blocks flattened", `{"type":"message","channel":"C1","bot_id":"B0IRM","text":"fallback","ts":"1.3",
			"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"*Alert group #12* firing"}}],
			"attachments":[{"title":"p95 latency high","fields":[{"title":"Severity","value":"critical"}]}]}`,
			"message|C1|irm|fallback\n*Alert group #12* firing\np95 latency high\nSeverity\ncritical"},
		{"irm edit", `{"type":"message","subtype":"message_changed","channel":"C1","message":{"bot_id":"B0IRM","text":"Acknowledged by Casey","ts":"1.3"}}`, "message_changed|C1|irm|Acknowledged by Casey"},
		{"our edit dropped", `{"type":"message","subtype":"message_changed","channel":"C1","message":{"bot_id":"B0OURS","text":"x"}}`, ""},
		{"other human", `{"type":"message","channel":"C1","user":"U0X","text":"hi","ts":"1.4"}`, "message|C1|other|hi"},
		{"join row ignored", `{"type":"message","subtype":"channel_join","channel":"C1","user":"U0X","text":"joined"}`, ""},
		{"channel created", `{"type":"channel_created","channel":{"id":"C9","name":"incident-12-latency","creator":"U0X"}}`, "channel_created|C9||"},
		{"member joined", `{"type":"member_joined_channel","channel":"C9","user":"U0OP"}`, "member_joined_channel|C9|operator|"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evs := ids.Translate(json.RawMessage(tc.raw))
			got := ""
			if len(evs) == 1 {
				e := evs[0]
				got = strings.Join([]string{string(e.Kind), e.ChannelID, string(e.From), e.Text}, "|")
				if e.Kind == engine.EvChannelCreated && e.ChannelName != "incident-12-latency" {
					t.Fatalf("channel name = %q", e.ChannelName)
				}
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestLRU(t *testing.T) {
	l := newLRU(2)
	if l.Seen("a") || !l.Seen("a") {
		t.Fatal("first sighting must be new, second seen")
	}
	l.Seen("b")
	l.Seen("c") // evicts a
	if l.Seen("a") {
		t.Fatal("a should have been evicted")
	}
}

// Every edit of one message shares its ts; only plain messages are deduplicated by ts, so an
// acknowledge edit and a later resolve edit both reach the engine.
func TestSuccessiveEditsAreNotDeduplicated(t *testing.T) {
	ch := make(chan engine.Event, 4)
	tr := &Transport{seen: newLRU(16), out: ch}
	edit := engine.Event{Kind: engine.EvMessageChanged, ChannelID: "C1", From: engine.FromIRM, TS: "1.3"}
	msg := engine.Event{Kind: engine.EvMessage, ChannelID: "C1", From: engine.FromIRM, TS: "1.4"}
	tr.emit(context.Background(), []engine.Event{edit, edit, msg, msg})
	if len(ch) != 3 {
		t.Fatalf("forwarded %d events, want both edits and one copy of the message", len(ch))
	}
}
