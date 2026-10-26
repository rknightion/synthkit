// SPDX-License-Identifier: AGPL-3.0-only

package script

import (
	"os"
	"path/filepath"
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

// TestSeedReplayedPost: an "app" line can carry another integration's name, picture and Slack
// blocks; LoadSeed reads the blocks file beside the seed and refuses anything but a JSON array.
func TestSeedReplayedPost(t *testing.T) {
	sc, err := Parse([]byte(seedTestScenario))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	seedFile := filepath.Join(dir, "s.seed.yaml")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	head := "apiVersion: synthkit.chatsim/v1\nkind: ChatSeed\nname: m\nmessages:\n"
	write("s.seed.yaml", head+`  - {channel: ops, from: app, text: summary, as: {name: Watcher, icon_url: "https://x.test/i.png"}, blocks_file: post.json}`+"\n")
	write("post.json", `[{"type":"section","text":{"type":"mrkdwn","text":"hi"}}]`)
	s, err := LoadSeed(seedFile, sc)
	if err != nil {
		t.Fatal(err)
	}
	if m := s.Messages[0]; m.As.Name != "Watcher" || !strings.Contains(string(m.Blocks), `"mrkdwn"`) {
		t.Fatalf("line %+v", m)
	}
	write("post.json", `{"type":"section"}`)
	if _, err := LoadSeed(seedFile, sc); err == nil || !strings.Contains(err.Error(), "not a JSON array") {
		t.Fatalf("object blocks: err = %v", err)
	}
	bad := head + `  - {channel: ops, from: alex, text: x, as: {name: W}}
  - {channel: ops, from: app, text: x, as: {name: "", icon_url: "http://x.test/i.png"}, blocks_file: ../post.json}
`
	_, err = ParseSeed([]byte(bad), sc)
	for _, want := range []string{`as is only for from "app"`, "as.name is required", "as.icon_url must be an https URL", "blocks_file must be a path inside"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v, want it to mention %s", err, want)
		}
	}
}
