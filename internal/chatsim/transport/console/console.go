// SPDX-License-Identifier: AGPL-3.0-only

// Package console is the terminal transport: persona lines print to stdout and stdin lines
// become operator messages, injected platform events or kill-switch commands. In console mode a
// channel's id is its name. In run mode the same reader accepts kill-switch commands only, so
// stdin mirrors the Slack /chatsim command.
package console

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// Transport prints posts.
type Transport struct {
	mu    sync.Mutex
	Out   io.Writer
	Color bool
	Clock engine.Clock
	start time.Time
}

var palette = []string{"\x1b[36m", "\x1b[33m", "\x1b[35m", "\x1b[32m", "\x1b[34m", "\x1b[31m"}

func (t *Transport) stamp() string {
	if t.Clock == nil {
		return ""
	}
	now := t.Clock.Now()
	if t.start.IsZero() {
		t.start = now
	}
	d := now.Sub(t.start).Round(time.Second)
	return fmt.Sprintf("[%02d:%02d] ", int(d.Minutes()), int(d.Seconds())%60)
}

// Post implements engine.Transport.
// Notify implements engine.Notifier: control-DM notices print with a [dm] tag.
func (t *Transport) Notify(_ context.Context, text string) error {
	t.Say("[dm] " + text)
	return nil
}

func (t *Transport) Post(_ context.Context, p engine.Post) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	who, on, off := "~", "", ""
	if p.Identity != nil {
		who = p.Identity.Emoji + " " + p.Identity.Name + ":"
		if t.Color {
			h := 0
			for _, r := range p.Identity.PersonaID {
				h += int(r)
			}
			on, off = palette[h%len(palette)], "\x1b[0m"
		}
	} else if t.Color {
		on, off = "\x1b[2m", "\x1b[0m"
	}
	_, err := fmt.Fprintf(t.Out, "%s#%-10s %s%s %s%s\n", t.stamp(), p.ChannelID, on, who, p.Text, off)
	return p.UtteranceID, err
}

// Join implements engine.Transport.
func (t *Transport) Join(_ context.Context, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := fmt.Fprintf(t.Out, "%s(chatsim joined #%s)\n", t.stamp(), id)
	return err
}

// Say prints a note from the console itself.
func (t *Transport) Say(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintf(t.Out, "%s%s\n", t.stamp(), s)
}

// Reader turns stdin lines into events.
type Reader struct {
	In          io.Reader
	Out         *Transport
	ControlOnly bool   // run mode: only kill-switch commands
	Channel     string // current channel id for operator messages (console mode)
	Operator    string // operator display name, used in injected IRM edits
	Scenario    string // synthkit scenario id, used in an injected alert
}

const help = `console: type to speak as the operator in the current channel.
  :in <channel>        switch the current channel
  :irm alert|ack|resolve|<text>   inject a Grafana IRM post (ack/resolve arrive as edits)
  :edit <text>         inject an IRM message edit
  :channel <name>      inject channel_created (an incident channel)
  :synthkit on|off     inject the SynthKit scenario edge
  :dm <text>           message the control DM (a command or a stage direction)
  start | fire | heal | pause | resume | stop | status | skip <beat> | say <persona> <text>
  summon <persona> [channel] | direct <stage direction>
  :quit`

// Run reads until EOF or ctx ends.
func (r *Reader) Run(ctx context.Context, out chan<- engine.Event) error {
	sc := bufio.NewScanner(r.In)
	reply := func(s string) { r.Out.Say(s) }
	alert := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line == ":quit" {
			return nil
		}
		var ev *engine.Event
		if verb, args, ok := engine.ParseControl(line); ok {
			if verb == "help" {
				reply(help)
				continue
			}
			ev = &engine.Event{Kind: engine.EvControl, Command: verb, Args: args, ChannelID: r.Channel, Reply: reply}
		} else if r.ControlOnly {
			reply("chatsim: run mode accepts " + engine.ControlHelp)
			continue
		} else if text, ok := strings.CutPrefix(line, ":dm "); ok {
			verb, args := engine.ParseDM(text)
			if verb == "help" {
				reply(help)
				continue
			}
			ev = &engine.Event{Kind: engine.EvControl, Command: verb, Args: args, Reply: reply}
		} else {
			ev = r.parse(line, &alert)
		}
		if ev == nil {
			continue
		}
		select {
		case out <- *ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sc.Err()
}

func (r *Reader) parse(line string, alert *int) *engine.Event {
	cmd, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	irm := func(kind engine.EventKind, text string) *engine.Event {
		return &engine.Event{Kind: kind, ChannelID: r.Channel, From: engine.FromIRM, Text: text}
	}
	switch cmd {
	case ":help":
		r.Out.Say(help)
		return nil
	case ":in":
		r.Channel = strings.TrimPrefix(rest, "#")
		r.Out.Say("(console: now speaking in #" + r.Channel + ")")
		return nil
	case ":irm":
		switch rest {
		case "alert":
			*alert++
			return irm(engine.EvMessage, fmt.Sprintf("[FIRING:1] Alert group #%d: %s (console-injected alert)", *alert, r.Scenario))
		case "ack":
			return irm(engine.EvMessageChanged, fmt.Sprintf("Alert group #%d acknowledged by %s", max(*alert, 1), r.Operator))
		case "resolve":
			return irm(engine.EvMessageChanged, fmt.Sprintf("Alert group #%d resolved by %s", max(*alert, 1), r.Operator))
		}
		return irm(engine.EvMessage, rest)
	case ":edit":
		return irm(engine.EvMessageChanged, rest)
	case ":channel":
		name := strings.TrimPrefix(rest, "#")
		return &engine.Event{Kind: engine.EvChannelCreated, ChannelID: name, ChannelName: name}
	case ":synthkit":
		if rest == "off" {
			return &engine.Event{Kind: engine.EvSynthkitDeactivated}
		}
		return &engine.Event{Kind: engine.EvSynthkitActivated}
	}
	if strings.HasPrefix(cmd, ":") {
		r.Out.Say("console: unknown command " + cmd + " (:help lists them)")
		return nil
	}
	return &engine.Event{Kind: engine.EvMessage, ChannelID: r.Channel, From: engine.FromOperator, User: "console-operator", Text: line}
}

// Loopback is an engine.Activator for rehearsal without a control plane: fire and heal inject
// the SynthKit edge the poller would have seen.
type Loopback struct {
	Events   chan<- engine.Event
	Scenario string
}

// SetActive implements engine.Activator.
func (l Loopback) SetActive(ctx context.Context, active bool) error {
	kind := engine.EvSynthkitDeactivated
	if active {
		kind = engine.EvSynthkitActivated
	}
	go func() {
		select {
		case l.Events <- engine.Event{Kind: kind, Text: l.Scenario}:
		case <-ctx.Done():
		}
	}()
	return nil
}
