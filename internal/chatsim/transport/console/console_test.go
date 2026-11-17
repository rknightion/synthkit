// SPDX-License-Identifier: AGPL-3.0-only

package console

import (
	"context"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

func TestReaderTranslatesLines(t *testing.T) {
	in := strings.Join([]string{
		":irm alert", ":irm ack", "hey alex, what do you think?", ":channel incident-7", ":in incident-7",
		"status", "/chatsim skip get_owner", "say alex hello there", ":synthkit off", ":quit", "never read",
	}, "\n")
	var out strings.Builder
	r := &Reader{In: strings.NewReader(in), Out: &Transport{Out: &out}, Channel: "ops", Operator: "Casey", Scenario: "bp/storm"}
	ch := make(chan engine.Event, 32)
	if err := r.Run(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	close(ch)
	var got []string
	for ev := range ch {
		got = append(got, string(ev.Kind)+"|"+ev.ChannelID+"|"+string(ev.From)+"|"+ev.Command+"|"+ev.Args+"|"+ev.Text)
	}
	want := []string{
		"message|ops|irm|||[FIRING:1] Alert group #1: bp/storm (console-injected alert)",
		"message_changed|ops|irm|||Alert group #1 acknowledged by Casey",
		"message|ops|operator|||hey alex, what do you think?",
		"channel_created|incident-7||||",
		"control|incident-7||status||",
		"control|incident-7||skip|get_owner|",
		"control|incident-7||say|alex hello there|",
		"synthkit_deactivated|||||",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestControlOnlyIgnoresChat(t *testing.T) {
	var out strings.Builder
	r := &Reader{In: strings.NewReader("hello room\npause\n"), Out: &Transport{Out: &out}, ControlOnly: true}
	ch := make(chan engine.Event, 4)
	if err := r.Run(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || (<-ch).Command != "pause" {
		t.Fatalf("control-only reader forwarded chat")
	}
	if !strings.Contains(out.String(), "run mode accepts") {
		t.Fatalf("no hint printed: %q", out.String())
	}
}

func TestParseControl(t *testing.T) {
	for in, want := range map[string]string{
		"/chatsim pause": "pause", "STOP": "stop", "skip": "", "say alex": "say", "stop the pods": "",
		"start now": "", "/chatsim": "",
	} {
		v, _, ok := engine.ParseControl(in)
		if (want == "") == ok || v != want {
			t.Errorf("ParseControl(%q) = %q,%v want %q", in, v, ok, want)
		}
	}
}
