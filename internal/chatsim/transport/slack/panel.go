// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/slack-go/slack"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// Block and action ids of the control panel. PanelBlockID marks a panel message, so a restart
// can retire the previous run's pinned panel.
const (
	PanelBlockID     = "chatsim_panel"
	actionPrefix     = "chatsim_"
	actionSummon     = "chatsim_summon"
	actionDirect     = "chatsim_direct"
	directionModalID = "chatsim_direction"
	directionBlockID = "direction"
	directionInputID = "text"
)

// panelVerbs are the buttons that map one to one onto a control verb.
var panelVerbs = map[string]bool{"start": true, "fire": true, "heal": true, "pause": true, "resume": true, "stop": true}

func mrkdwn(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.MarkdownType, s, false, false)
}

func plain(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.PlainTextType, s, false, false)
}

func button(verb, label string) *slack.ButtonBlockElement {
	return slack.NewButtonBlockElement(actionPrefix+verb, verb, plain(label))
}

func firstName(name string) string {
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}
	return name
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// PanelText is the panel's notification and fallback text.
func PanelText(s engine.Snapshot) string {
	return fmt.Sprintf("Control panel for %s: %s", s.Scenario, s.State)
}

// PanelBlocks renders a snapshot as the control panel. Buttons follow the run's state: the
// panel only offers what would do something now.
func PanelBlocks(s engine.Snapshot) []slack.Block {
	head := fmt.Sprintf("*%s* · `%s`", s.Scenario, s.State)
	if !s.StartedAt.IsZero() {
		u := s.StartedAt.Unix()
		head += fmt.Sprintf(" · started <!date^%d^{time_secs} ({ago})|%s>", u, s.StartedAt.UTC().Format("15:04:05Z"))
	}
	var fields []*slack.TextBlockObject
	if s.SynthkitScenario != "" {
		f := "not fired"
		if s.Fired {
			f = "fired"
		}
		fields = append(fields, mrkdwn(fmt.Sprintf("*Failure*\n`%s` %s", s.SynthkitScenario, f)))
	}
	fields = append(fields, mrkdwn(fmt.Sprintf("*LLM calls*\n%d of %d", s.LLMCalls, s.MaxCalls)))
	beats := "none"
	if len(s.OpenBeats) > 0 {
		beats = clip(strings.Join(s.OpenBeats, ", "), 300)
	}
	fields = append(fields, mrkdwn("*Open beats*\n"+beats))
	if s.Queued > 0 {
		fields = append(fields, mrkdwn(fmt.Sprintf("*Queued lines*\n%d", s.Queued)))
	}
	blocks := []slack.Block{slack.NewSectionBlock(mrkdwn(head), fields, nil, slack.SectionBlockOptionBlockID(PanelBlockID))}

	rooms := map[string][]string{}
	var order, away []string
	for _, p := range s.Personas {
		if len(p.In) == 0 {
			away = append(away, firstName(p.Name))
		}
		for _, c := range p.In {
			if rooms[c] == nil {
				order = append(order, c)
			}
			rooms[c] = append(rooms[c], firstName(p.Name))
		}
	}
	var cast []string
	for _, c := range order {
		cast = append(cast, fmt.Sprintf("*%s*: %s", c, strings.Join(rooms[c], ", ")))
	}
	if len(away) > 0 {
		cast = append(cast, "*away*: "+strings.Join(away, ", "))
	}
	if len(cast) > 0 {
		blocks = append(blocks, slack.NewContextBlock("chatsim_panel_cast", mrkdwn(clip(strings.Join(cast, "  ·  "), 2000))))
	}
	if s.LastNotice != "" {
		blocks = append(blocks, slack.NewContextBlock("chatsim_panel_last", mrkdwn("Last: "+clip(s.LastNotice, 500))))
	}
	if s.State == engine.StateEnded {
		blocks = append(blocks, slack.NewContextBlock("chatsim_panel_end",
			mrkdwn(fmt.Sprintf("Run ended: %s. Restart synthkit-chat for a new run.", s.EndReason))))
		return blocks
	}

	started := s.RunID != ""
	var run []slack.BlockElement
	if !started {
		run = append(run, button("start", "Start chat").WithStyle(slack.StylePrimary))
	}
	if s.SynthkitScenario != "" && !s.Fired {
		run = append(run, button("fire", "Fire failure").WithStyle(slack.StyleDanger))
	}
	if s.SynthkitScenario != "" && s.Fired {
		run = append(run, button("heal", "Heal"))
	}
	if started && s.Paused {
		run = append(run, button("resume", "Resume").WithStyle(slack.StylePrimary))
	} else if started {
		run = append(run, button("pause", "Pause"))
	}
	run = append(run, button("stop", "Stop").WithStyle(slack.StyleDanger).WithConfirm(slack.NewConfirmationBlockObject(
		plain("End the run?"), plain("The cast goes silent and the engine exits. Restart synthkit-chat for a new run."),
		plain("Stop"), plain("Keep going"))))
	blocks = append(blocks, slack.NewActionBlock("chatsim_panel_run", run...))

	if started {
		var cast []slack.BlockElement
		var opts []*slack.OptionBlockObject
		for _, p := range s.Personas {
			if s.SummonTo != "" && !contains(p.In, s.SummonTo) && len(opts) < 100 {
				opts = append(opts, slack.NewOptionBlockObject(p.ID, plain(clip(p.Name, 75)), nil))
			}
		}
		if len(opts) > 0 {
			cast = append(cast, slack.NewOptionsSelectBlockElement(slack.OptTypeStatic, plain("Summon to "+clip(s.SummonTo, 50)), actionSummon, opts...))
		}
		cast = append(cast, slack.NewButtonBlockElement(actionDirect, "direct", plain("Stage direction…")))
		blocks = append(blocks, slack.NewActionBlock("chatsim_panel_cast_actions", cast...))
	}
	return blocks
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// directionModal asks for one stage direction.
func directionModal() slack.ModalViewRequest {
	in := slack.NewPlainTextInputBlockElement(plain("Have Sam mention the coffee machine is broken again"), directionInputID)
	in = in.WithMultiline(true)
	return slack.ModalViewRequest{
		Type: slack.VTModal, CallbackID: directionModalID,
		Title: plain("Stage direction"), Submit: plain("Direct"), Close: plain("Cancel"),
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			slack.NewInputBlock(directionBlockID, plain("What should happen?"),
				plain("Name a persona to have them do it; #channel pins the room."), in),
		}},
	}
}

// Interaction is what one interactive payload asks of the transport: control events to
// emit, or a trigger id to open the stage-direction modal with.
type Interaction struct {
	Events    []engine.Event
	OpenModal string
}

// TranslateInteraction maps a block action or modal submission from the operator onto
// control events. Anyone else's clicks are ignored. It is pure so it can be tested.
func (id Identities) TranslateInteraction(cb slack.InteractionCallback) Interaction {
	if id.OperatorID == "" || cb.User.ID != id.OperatorID {
		return Interaction{}
	}
	ctl := func(verb, args string) engine.Event {
		return engine.Event{Kind: engine.EvControl, From: engine.FromOperator, User: cb.User.ID, ChannelID: cb.Channel.ID, Command: verb, Args: args}
	}
	var out Interaction
	switch cb.Type {
	case slack.InteractionTypeBlockActions:
		for _, a := range cb.ActionCallback.BlockActions {
			switch {
			case a.ActionID == actionDirect:
				out.OpenModal = cb.TriggerID
			case a.ActionID == actionSummon && a.SelectedOption.Value != "":
				out.Events = append(out.Events, ctl("summon", a.SelectedOption.Value))
			case strings.HasPrefix(a.ActionID, actionPrefix) && panelVerbs[strings.TrimPrefix(a.ActionID, actionPrefix)]:
				out.Events = append(out.Events, ctl(strings.TrimPrefix(a.ActionID, actionPrefix), ""))
			}
		}
	case slack.InteractionTypeViewSubmission:
		if cb.View.CallbackID != directionModalID || cb.View.State == nil {
			return out
		}
		if text := strings.TrimSpace(cb.View.State.Values[directionBlockID][directionInputID].Value); text != "" {
			out.Events = append(out.Events, ctl("direct", text))
		}
	}
	return out
}

// Panel implements engine.Paneller: the first call retires earlier runs' pinned panels, then
// posts and pins this run's; later calls edit it in place.
func (t *Transport) Panel(ctx context.Context, s engine.Snapshot) error {
	t.panelMu.Lock()
	defer t.panelMu.Unlock()
	dm, err := t.dmChannel(ctx)
	if err != nil {
		return err
	}
	opts := []slack.MsgOption{slack.MsgOptionText(PanelText(s), false), slack.MsgOptionBlocks(PanelBlocks(s)...)}
	if t.panelTS != "" {
		_, _, _, err := t.api.UpdateMessageContext(ctx, dm, t.panelTS, opts...)
		var se slack.SlackErrorResponse
		switch {
		case err == nil:
			return nil
		case errors.As(err, &se) && (se.Err == "message_not_found" || se.Err == "cant_update_message"):
			t.panelTS = "" // deleted by hand: post a fresh one
		default:
			return fmt.Errorf("slack chat.update of the control panel: %w", err)
		}
	}
	t.retirePanels(ctx, dm)
	if t.cfg.ControlName != "" {
		opts = append(opts, slack.MsgOptionUsername(t.cfg.ControlName))
	}
	if t.cfg.ControlEmoji != "" {
		opts = append(opts, slack.MsgOptionIconEmoji(t.cfg.ControlEmoji))
	}
	_, ts, err := t.api.PostMessageContext(ctx, dm, opts...)
	if err != nil {
		return fmt.Errorf("slack chat.postMessage of the control panel: %w", err)
	}
	t.panelTS = ts
	if err := t.api.AddPinContext(ctx, dm, slack.NewRefToMessage(dm, ts)); err != nil {
		t.cfg.Logf("slack: could not pin the control panel (needs pins:write): %v", err)
	}
	return nil
}

// retirePanels unpins our earlier panels in the DM and strips their buttons, so only the
// live panel can drive the run. Failures are logged: a stale panel is untidy, not fatal.
func (t *Transport) retirePanels(ctx context.Context, dm string) {
	items, _, err := t.api.ListPinsContext(ctx, dm)
	if err != nil {
		t.cfg.Logf("slack: could not list pins (needs pins:read): %v", err)
		return
	}
	for _, it := range items {
		msg := it.Message
		if msg == nil || msg.BotID != t.ids.OwnBotID || !isPanel(msg.Blocks) {
			continue
		}
		ts := msg.Timestamp
		if err := t.api.RemovePinContext(ctx, dm, slack.NewRefToMessage(dm, ts)); err != nil {
			t.cfg.Logf("slack: could not unpin an old panel: %v", err)
		}
		if _, _, _, err := t.api.UpdateMessageContext(ctx, dm, ts, slack.MsgOptionText("Retired control panel", false),
			slack.MsgOptionBlocks(slack.NewContextBlock("chatsim_panel_retired", mrkdwn("_Control panel from an earlier run; the live one is pinned below._")))); err != nil {
			t.cfg.Logf("slack: could not retire an old panel: %v", err)
		}
	}
}

func isPanel(b slack.Blocks) bool {
	for _, blk := range b.BlockSet {
		if s, ok := blk.(*slack.SectionBlock); ok && s.BlockID == PanelBlockID {
			return true
		}
	}
	return false
}

// interact handles one interactive payload: the modal opens at once (its trigger id expires
// in three seconds), and control events go to the engine with replies to the DM.
func (t *Transport) interact(ctx context.Context, cb slack.InteractionCallback) {
	in := t.ids.TranslateInteraction(cb)
	if in.OpenModal != "" {
		if _, err := t.api.OpenViewContext(ctx, in.OpenModal, directionModal()); err != nil {
			t.cfg.Logf("slack: could not open the stage-direction modal: %v", err)
		}
	}
	if len(in.Events) == 0 && in.OpenModal == "" && cb.User.ID != t.ids.OperatorID {
		t.cfg.Logf("slack: ignored a panel click from %s (not the bound operator)", cb.User.ID)
		return
	}
	for i := range in.Events {
		in.Events[i].Reply = t.dmReply(ctx)
	}
	t.emit(ctx, in.Events)
}
