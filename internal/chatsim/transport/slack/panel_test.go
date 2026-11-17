// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// actionIDs lists every interactive element's action id, in order.
func actionIDs(blocks []slack.Block) []string {
	var out []string
	for _, b := range blocks {
		a, ok := b.(*slack.ActionBlock)
		if !ok {
			continue
		}
		for _, e := range a.Elements.ElementSet {
			switch x := e.(type) {
			case *slack.ButtonBlockElement:
				out = append(out, x.ActionID)
			case *slack.SelectBlockElement:
				out = append(out, x.ActionID)
			}
		}
	}
	return out
}

func TestPanelBlocksOfferOnlyWhatWorksNow(t *testing.T) {
	cast := []engine.PersonaStatus{{ID: "alex", Name: "Alex Moreno", In: []string{"ops"}}, {ID: "jordan", Name: "Jordan Pike"}}
	t0 := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		s    engine.Snapshot
		want []string
	}{
		{"waiting", engine.Snapshot{State: engine.StateWaiting, SynthkitScenario: "bp/storm", Personas: cast},
			[]string{"chatsim_start", "chatsim_fire", "chatsim_stop"}},
		{"chatting", engine.Snapshot{State: engine.StateChatting, SynthkitScenario: "bp/storm", RunID: "r", StartedAt: t0, SummonTo: "ops", Personas: cast},
			[]string{"chatsim_fire", "chatsim_pause", "chatsim_stop", "chatsim_summon", "chatsim_direct"}},
		{"fired and paused", engine.Snapshot{State: engine.StatePaused, SynthkitScenario: "bp/storm", RunID: "r", StartedAt: t0, Fired: true, Paused: true, SummonTo: "ops", Personas: cast},
			[]string{"chatsim_heal", "chatsim_resume", "chatsim_stop", "chatsim_summon", "chatsim_direct"}},
		{"everyone here", engine.Snapshot{State: engine.StateChatting, RunID: "r", StartedAt: t0, SummonTo: "ops", Personas: cast[:1]},
			[]string{"chatsim_pause", "chatsim_stop", "chatsim_direct"}},
		{"ended", engine.Snapshot{State: engine.StateEnded, RunID: "r", StartedAt: t0, EndReason: "stopped by operator", Personas: cast}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := PanelBlocks(tc.s)
			if got := actionIDs(blocks); !slices.Equal(got, tc.want) {
				t.Fatalf("actions = %v, want %v", got, tc.want)
			}
			if !isPanel(slack.Blocks{BlockSet: blocks}) {
				t.Fatal("panel head block id missing: a restart could not retire this panel")
			}
			raw, err := json.Marshal(blocks)
			if err != nil {
				t.Fatal(err)
			}
			js := string(raw)
			if tc.s.Personas[len(tc.s.Personas)-1].In == nil && !strings.Contains(js, "*away*: Jordan") {
				t.Errorf("absent persona not listed as away: %s", js)
			}
			if tc.name == "ended" && !strings.Contains(js, "stopped by operator") {
				t.Errorf("end reason missing: %s", js)
			}
			if tc.name == "chatting" && (!strings.Contains(js, "{ago}") || strings.Contains(js, `"value":"alex"`)) {
				t.Errorf("want a relative start time and no summon option for alex, who is already in ops: %s", js)
			}
		})
	}
}

func TestStopAsksFirst(t *testing.T) {
	for _, b := range PanelBlocks(engine.Snapshot{State: engine.StateWaiting}) {
		a, ok := b.(*slack.ActionBlock)
		if !ok {
			continue
		}
		for _, e := range a.Elements.ElementSet {
			if btn, ok := e.(*slack.ButtonBlockElement); ok && btn.ActionID == "chatsim_stop" && btn.Confirm == nil {
				t.Fatal("Stop must carry a confirmation dialog")
			}
		}
	}
}

func cb(raw string) slack.InteractionCallback {
	var c slack.InteractionCallback
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		panic(err)
	}
	return c
}

// Payloads are invented, shaped like Slack interactive payloads.
func TestTranslateInteraction(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"fire", `{"type":"block_actions","user":{"id":"U0OP"},"channel":{"id":"D1"},"trigger_id":"tr","actions":[{"block_id":"chatsim_panel_run","action_id":"chatsim_fire","value":"fire"}]}`, "fire|"},
		{"stop", `{"type":"block_actions","user":{"id":"U0OP"},"actions":[{"block_id":"chatsim_panel_run","action_id":"chatsim_stop","value":"stop"}]}`, "stop|"},
		{"not the operator", `{"type":"block_actions","user":{"id":"U0X"},"actions":[{"block_id":"chatsim_panel_run","action_id":"chatsim_fire","value":"fire"}]}`, ""},
		{"unknown action", `{"type":"block_actions","user":{"id":"U0OP"},"actions":[{"block_id":"chatsim_panel_run","action_id":"chatsim_say","value":"say"}]}`, ""},
		{"summon", `{"type":"block_actions","user":{"id":"U0OP"},"actions":[{"block_id":"chatsim_panel_run","action_id":"chatsim_summon","type":"static_select","selected_option":{"value":"jordan"}}]}`, "summon|jordan"},
		{"direction opens the modal", `{"type":"block_actions","user":{"id":"U0OP"},"trigger_id":"tr9","actions":[{"block_id":"chatsim_panel_run","action_id":"chatsim_direct","value":"direct"}]}`, "modal:tr9"},
		{"direction submitted", `{"type":"view_submission","user":{"id":"U0OP"},"view":{"callback_id":"chatsim_direction","state":{"values":{"direction":{"text":{"type":"plain_text_input","value":" have Sam grab coffee "}}}}}}`, "direct|have Sam grab coffee"},
		{"someone else's modal", `{"type":"view_submission","user":{"id":"U0OP"},"view":{"callback_id":"other","state":{"values":{"direction":{"text":{"value":"x"}}}}}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := ids.TranslateInteraction(cb(tc.raw))
			var got []string
			if in.OpenModal != "" {
				got = append(got, "modal:"+in.OpenModal)
			}
			for _, e := range in.Events {
				if e.Kind != engine.EvControl || e.From != engine.FromOperator {
					t.Fatalf("event %+v is not an operator control event", e)
				}
				got = append(got, e.Command+"|"+e.Args)
			}
			if strings.Join(got, ",") != tc.want {
				t.Fatalf("got %q, want %q", strings.Join(got, ","), tc.want)
			}
		})
	}
}

// Every panel verb must be one the engine's control handler knows.
func TestPanelVerbsAreControlVerbs(t *testing.T) {
	for v := range panelVerbs {
		if !slices.Contains(engine.ControlVerbs, v) {
			t.Errorf("panel verb %q is not an engine control verb", v)
		}
	}
	if err := slack.ValidateUniqueBlockID(directionModal()); !err {
		t.Error("direction modal has duplicate block ids")
	}
}

// TestPanelOffersResetOnlyWithASeed: the confirmed Reset button appears when the host can
// reseed, maps onto the reset verb, and an ended reset panel says a fresh run follows.
func TestPanelOffersResetOnlyWithASeed(t *testing.T) {
	s := engine.Snapshot{State: engine.StateWaiting, Resettable: true}
	got := actionIDs(PanelBlocks(s))
	if !slices.Equal(got, []string{"chatsim_start", "chatsim_stop", "chatsim_reset"}) {
		t.Fatalf("actions = %v", got)
	}
	for _, b := range PanelBlocks(s) {
		if a, ok := b.(*slack.ActionBlock); ok {
			last := a.Elements.ElementSet[len(a.Elements.ElementSet)-1].(*slack.ButtonBlockElement)
			if last.Confirm == nil {
				t.Fatal("reset has no confirmation")
			}
		}
	}
	id := Identities{OperatorID: "U1"}
	cb := slack.InteractionCallback{Type: slack.InteractionTypeBlockActions, User: slack.User{ID: "U1"}}
	cb.ActionCallback.BlockActions = []*slack.BlockAction{{ActionID: "chatsim_reset"}}
	if in := id.TranslateInteraction(cb); len(in.Events) != 1 || in.Events[0].Command != "reset" {
		t.Fatalf("interaction = %+v", in)
	}
	ended, _ := json.Marshal(PanelBlocks(engine.Snapshot{State: engine.StateEnded, EndReason: engine.EndReasonReset}))
	if !strings.Contains(string(ended), "fresh run") {
		t.Fatalf("ended reset panel: %s", ended)
	}
}
