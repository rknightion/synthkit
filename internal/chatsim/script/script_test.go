// SPDX-License-Identifier: AGPL-3.0-only

package script

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExampleLoads(t *testing.T) {
	sc, err := Load(filepath.Join("..", "..", "..", "examples", "chat", "backend-latency.chat.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Trigger.SynthkitScenario != "acme-ai-platform/backend_latency_storm" || sc.Trigger.Delay.Duration != 20*time.Second {
		t.Fatalf("trigger = %+v", sc.Trigger)
	}
	if got := sc.Persona("jordan"); got == nil || got.FirstName() != "Jordan" {
		t.Fatalf("persona jordan = %+v", got)
	}
	if sc.Beats[0].MaxTurns != 2 || sc.Beats[0].At.Duration != 0 {
		t.Fatalf("beat 0 = %+v", sc.Beats[0])
	}
	if a := sc.Beats[3].Then[0].After; a.Min != 20*time.Second || a.Max != 35*time.Second {
		t.Fatalf("summon range = %+v", a)
	}
}

const minimal = `apiVersion: synthkit.chatsim/v1
kind: ChatScenario
name: t
operator: { name: Op }
channels: { ops: {} }
personas:
  - { id: a, name: Ann One, avatar: { emoji: ":smile:" }, present: [ops] }
beats:
  - { id: b1, at: "+0s", speakers: [a], goal: say hi }
`

func TestParseDefaults(t *testing.T) {
	sc, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if sc.LLM.Model != DefaultModel || sc.LLM.MaxLLMCalls != DefaultMaxLLMCalls || sc.Beats[0].MaxTurns != DefaultMaxTurns {
		t.Fatalf("defaults not applied: %+v %+v", sc.LLM, sc.Beats[0])
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(string) string
		want   string
	}{
		"unknown field":   {func(s string) string { return s + "bogus: 1\n" }, "field bogus not found"},
		"bad kind":        {func(s string) string { return strings.Replace(s, "ChatScenario", "Other", 1) }, "kind must be"},
		"unknown speaker": {func(s string) string { return strings.Replace(s, "speakers: [a]", "speakers: [zz]", 1) }, `speaker "zz" is not a persona`},
		"two triggers":    {func(s string) string { return strings.Replace(s, `at: "+0s",`, `at: "+0s", after: b1,`, 1) }, "exactly one of at, after or when"},
		"bad offset":      {func(s string) string { return strings.Replace(s, `"+0s"`, `"0s"`, 1) }, "must start with '+'"},
		"bad emoji":       {func(s string) string { return strings.Replace(s, `":smile:"`, `"smile"`, 1) }, "avatar.emoji"},
		"intent cut": {func(s string) string {
			return strings.Replace(s, `at: "+0s"`, `when: { operator: { match: "x", intent: "y" } }`, 1)
		}, "intent is not supported"},
		"bad regex": {func(s string) string {
			return strings.Replace(s, `at: "+0s"`, `when: { operator: { match: "(" } }`, 1)
		}, "valid, non-empty regex"},
		"second doc":  {func(s string) string { return s + "---\nname: x\n" }, "exactly one YAML document"},
		"bad channel": {func(s string) string { return strings.Replace(s, "present: [ops]", "present: [nope]", 1) }, `present channel "nope"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.mutate(minimal)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestAfterRef(t *testing.T) {
	b, d, err := AfterRef("owner_arrives+90s")
	if err != nil || b != "owner_arrives" || d != 90*time.Second {
		t.Fatalf("got %q %v %v", b, d, err)
	}
}
