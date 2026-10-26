// SPDX-License-Identifier: AGPL-3.0-only

package script

import (
	"strings"
	"testing"
	"time"
)

func TestParseSeed(t *testing.T) {
	sc, err := Parse([]byte(seedTestScenario))
	if err != nil {
		t.Fatal(err)
	}
	good := `apiVersion: synthkit.chatsim/v1
kind: ChatSeed
name: morning
wipe: {archive_match: "^incident-", keep: ["#random"]}
messages:
  - {channel: ops, from: alex, text: handover, key: h}
  - {channel: "#general", from: operator, text: thanks, reply_to: h, after: 2s}
  - {channel: ops, from: app, text: notice}
`
	s, err := ParseSeed([]byte(good), sc)
	if err != nil {
		t.Fatal(err)
	}
	if s.Pace.Min != 4*time.Second || s.Pace.Max != 10*time.Second || s.Messages[1].After.Duration != 2*time.Second {
		t.Fatalf("defaults: pace %+v after %v", s.Pace, s.Messages[1].After)
	}
	bad := `apiVersion: synthkit.chatsim/v1
kind: ChatSeed
name: morning
wipe: {archive_match: "("}
messages:
  - {channel: ops, from: nobody, text: hi, reply_to: later}
  - {channel: "", from: alex, text: "", key: later}
`
	_, err = ParseSeed([]byte(bad), sc)
	for _, want := range []string{"archive_match", `from "nobody"`, `reply_to "later"`, "channel is required", "text is required"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v, want it to mention %s", err, want)
		}
	}
}

const seedTestScenario = `apiVersion: synthkit.chatsim/v1
kind: ChatScenario
name: s
operator: {name: Op}
channels: {ops: {}}
personas:
  - {id: alex, name: Alex Moreno, role: lead, avatar: {emoji: ":wave:"}, present: [ops]}
beats:
  - {id: hello, at: +0s, speakers: [alex], goal: say hi}
`
