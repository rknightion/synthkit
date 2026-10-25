// SPDX-License-Identifier: AGPL-3.0-only

// Package slack is the Slack transport and the only importer of slack-go. It listens over
// Socket Mode (no public URL), posts persona lines with chat.postMessage plus a per-message
// username and icon_emoji, and posts narrator lines in the app's own identity. It never edits
// a persona message, never uses Slack-side scheduling and never sets reply_broadcast. Our own
// echoes are dropped by bot_id. New channels are joined on request and their recent history
// is backfilled as ordinary events.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// MetadataEventType tags every post's Slack message metadata (ids only, never content).
const MetadataEventType = "synthkit_chat_line"

// SlashCommand is the kill-switch command name.
const SlashCommand = "/chatsim"

// Config configures the transport.
type Config struct {
	BotToken   string // xoxb-
	AppToken   string // xapp- with connections:write
	OperatorID string
	IRMBotIDs  []string
	IRMAppIDs  []string
	Logf       func(format string, a ...any)
	// Tap, when set, receives every raw envelope payload as one JSON line.
	Tap io.Writer
	// ControlName and ControlEmoji, when set, are the identity the control DM speaks in
	// (chat:write.customize); otherwise it speaks as the app.
	ControlName  string
	ControlEmoji string
}

// Transport implements engine.Transport over Slack.
type Transport struct {
	api  *slack.Client
	cfg  Config
	ids  Identities
	mu   sync.Mutex
	out  chan<- engine.Event
	seen *lru
	tapM sync.Mutex
	dmMu sync.Mutex
	dm   string // the operator's DM channel with the app, opened on first use
	// panelMu guards panelTS, the live control panel's message in the DM.
	panelMu sync.Mutex
	panelTS string
}

// New authenticates (auth.test) to learn our own bot id.
func New(ctx context.Context, cfg Config) (*Transport, error) {
	if cfg.BotToken == "" || cfg.AppToken == "" {
		return nil, errors.New("slack: bot token and app-level token are both required")
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	api := slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	auth, err := api.AuthTestContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("slack auth.test: %w", err)
	}
	t := &Transport{api: api, cfg: cfg, seen: newLRU(4096), ids: Identities{
		OwnBotID: auth.BotID, OwnUserID: auth.UserID, OperatorID: cfg.OperatorID,
		IRMBotIDs: cfg.IRMBotIDs, IRMAppIDs: cfg.IRMAppIDs,
	}}
	cfg.Logf("slack: connected to %s as %s (bot %s)", auth.Team, auth.User, auth.BotID)
	return t, nil
}

func (t *Transport) tap(kind string, payload any) {
	if t.cfg.Tap == nil {
		return
	}
	t.tapM.Lock()
	defer t.tapM.Unlock()
	b, err := json.Marshal(map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "kind": kind, "payload": payload})
	if err == nil {
		_, _ = t.cfg.Tap.Write(append(b, '\n'))
	}
}

func (t *Transport) emit(ctx context.Context, evs []engine.Event) {
	t.mu.Lock()
	out := t.out
	t.mu.Unlock()
	for _, ev := range evs {
		// A backfilled message may also arrive live. Edits share their message's ts, so only
		// plain messages are deduplicated by ts; envelope redeliveries are caught by event_id.
		if ev.Kind == engine.EvMessage && ev.TS != "" && t.seen.Seen(string(ev.Kind)+"|"+ev.ChannelID+"|"+ev.TS) {
			continue
		}
		if out == nil {
			continue
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return
		}
	}
}

// Run connects over Socket Mode and forwards events until ctx ends. Every envelope is acked
// before it is processed; Slack redelivers what is not acked quickly.
func (t *Transport) Run(ctx context.Context, out chan<- engine.Event) error {
	t.mu.Lock()
	t.out = out
	t.mu.Unlock()
	sm := socketmode.New(t.api)
	errc := make(chan error, 1)
	go func() { errc <- sm.RunContext(ctx) }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			return fmt.Errorf("slack socket mode: %w", err)
		case evt, ok := <-sm.Events:
			if !ok {
				return errors.New("slack socket mode: event stream closed")
			}
			if err := t.handle(ctx, sm, evt); err != nil {
				return err
			}
		}
	}
}

func (t *Transport) handle(ctx context.Context, sm *socketmode.Client, evt socketmode.Event) error {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		t.cfg.Logf("slack: connecting")
	case socketmode.EventTypeConnected:
		t.cfg.Logf("slack: socket mode connected")
	case socketmode.EventTypeConnectionError:
		t.cfg.Logf("slack: connection error (the client retries): %v", evt.Data)
	case socketmode.EventTypeInvalidAuth:
		return errors.New("slack: invalid auth (check the xapp- token and its connections:write scope)")
	case socketmode.EventTypeEventsAPI:
		if evt.Request == nil {
			return nil
		}
		_ = sm.Ack(*evt.Request)
		t.tap("events_api", evt.Request.Payload)
		var env struct {
			EventID string          `json:"event_id"`
			Event   json.RawMessage `json:"event"`
		}
		if json.Unmarshal(evt.Request.Payload, &env) != nil || (env.EventID != "" && t.seen.Seen("evt|"+env.EventID)) {
			return nil
		}
		evs := t.ids.Translate(env.Event)
		for i := range evs {
			if evs[i].Kind == engine.EvControl {
				t.rememberDM(evs[i].ChannelID)
				evs[i].Reply = t.dmReply(ctx)
			}
		}
		t.emit(ctx, evs)
	case socketmode.EventTypeSlashCommand:
		if evt.Request == nil {
			return nil
		}
		_ = sm.Ack(*evt.Request)
		cmd, ok := evt.Data.(slack.SlashCommand)
		if !ok {
			return nil
		}
		t.tap("slash_command", map[string]string{"command": cmd.Command, "user_id": cmd.UserID, "channel_id": cmd.ChannelID, "text": cmd.Text})
		t.slash(ctx, cmd)
	case socketmode.EventTypeInteractive:
		if evt.Request == nil {
			return nil
		}
		_ = sm.Ack(*evt.Request) // an empty ack also closes a submitted modal
		cb, ok := evt.Data.(slack.InteractionCallback)
		if !ok {
			return nil
		}
		t.tap("interactive", evt.Request.Payload)
		t.interact(ctx, cb)
	}
	return nil
}

func (t *Transport) slash(ctx context.Context, cmd slack.SlashCommand) {
	reply := func(text string) {
		if cmd.ResponseURL == "" {
			return
		}
		go func() {
			_ = slack.PostWebhookContext(ctx, cmd.ResponseURL, &slack.WebhookMessage{Text: text, ResponseType: "ephemeral"})
		}()
	}
	if cmd.Command != SlashCommand {
		return
	}
	if t.ids.OperatorID == "" || cmd.UserID != t.ids.OperatorID {
		t.cfg.Logf("slack: ignored %s from %s (not the bound operator)", cmd.Command, cmd.UserID)
		reply("chatsim: only the bound operator can drive this run.")
		return
	}
	verb, args, ok := engine.ParseControl(cmd.Text)
	if !ok || verb == "help" {
		reply("chatsim: /chatsim " + engine.ControlHelp)
		return
	}
	t.emit(ctx, []engine.Event{{Kind: engine.EvControl, Command: verb, Args: args, ChannelID: cmd.ChannelID, User: cmd.UserID, Reply: reply}})
}

func (t *Transport) rememberDM(id string) {
	t.dmMu.Lock()
	defer t.dmMu.Unlock()
	if t.dm == "" && strings.HasPrefix(id, "D") {
		t.dm = id
	}
}

func (t *Transport) dmChannel(ctx context.Context) (string, error) {
	t.dmMu.Lock()
	defer t.dmMu.Unlock()
	if t.dm != "" {
		return t.dm, nil
	}
	if t.ids.OperatorID == "" {
		return "", errors.New("no operator user id bound")
	}
	ch, _, _, err := t.api.OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: []string{t.ids.OperatorID}})
	if err != nil {
		return "", fmt.Errorf("conversations.open (needs im:write): %w", err)
	}
	t.dm = ch.ID
	return t.dm, nil
}

// dmReply answers a control command in the control DM without blocking the caller.
func (t *Transport) dmReply(ctx context.Context) func(string) {
	return func(text string) {
		go func() {
			if err := t.Notify(ctx, text); err != nil {
				t.cfg.Logf("slack: control DM reply: %v", err)
			}
		}()
	}
}

// Notify implements engine.Notifier: a private line to the operator in the app's DM.
func (t *Transport) Notify(ctx context.Context, text string) error {
	dm, err := t.dmChannel(ctx)
	if err != nil {
		return err
	}
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if t.cfg.ControlName != "" {
		opts = append(opts, slack.MsgOptionUsername(t.cfg.ControlName))
	}
	if t.cfg.ControlEmoji != "" {
		opts = append(opts, slack.MsgOptionIconEmoji(t.cfg.ControlEmoji))
	}
	_, _, err = t.api.PostMessageContext(ctx, dm, opts...)
	if err != nil {
		return fmt.Errorf("slack chat.postMessage to the control DM: %w", err)
	}
	return nil
}

// Hello posts a startup line to the control DM, logging (not failing) when the DM is not
// set up yet.
func (t *Transport) Hello(ctx context.Context, text string) {
	if err := t.Notify(ctx, text); err != nil {
		t.cfg.Logf("slack: control DM unavailable (%v); /chatsim and stdin still work", err)
	}
}

// Post implements engine.Transport. A 429 is retried once after Retry-After.
func (t *Transport) Post(ctx context.Context, p engine.Post) (string, error) {
	persona := ""
	opts := []slack.MsgOption{
		slack.MsgOptionText(p.Text, false),
	}
	if p.Identity != nil {
		persona = p.Identity.PersonaID
		opts = append(opts, slack.MsgOptionUsername(p.Identity.Name), slack.MsgOptionIconEmoji(p.Identity.Emoji))
	} else {
		opts = append(opts, slack.MsgOptionBlocks(slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, "_"+p.Text+"_", false, false))))
	}
	opts = append(opts, slack.MsgOptionMetadata(slack.SlackMetadata{
		EventType:    MetadataEventType,
		EventPayload: map[string]any{"run_id": p.RunID, "utterance_id": p.UtteranceID, "persona": persona},
	}))
	_, ts, err := t.api.PostMessageContext(ctx, p.ChannelID, opts...)
	var rl *slack.RateLimitedError
	if errors.As(err, &rl) {
		wait := min(rl.RetryAfter, 30*time.Second)
		t.cfg.Logf("slack: rate limited; retrying once in %s", wait)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
		_, ts, err = t.api.PostMessageContext(ctx, p.ChannelID, opts...)
	}
	if err != nil {
		return "", fmt.Errorf("slack chat.postMessage: %w", err)
	}
	return ts, nil
}

// Join implements engine.Transport: conversations.join, then backfill recent history so an
// alert posted before the join is still seen. Private channels cannot be joined; invite the
// app instead.
func (t *Transport) Join(ctx context.Context, channelID string) error {
	if _, _, _, err := t.api.JoinConversationContext(ctx, channelID); err != nil {
		return fmt.Errorf("slack conversations.join %s: %w", channelID, err)
	}
	hist, err := t.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channelID, Limit: 20})
	if err != nil {
		return fmt.Errorf("slack conversations.history %s: %w", channelID, err)
	}
	msgs := slices.Clone(hist.Messages)
	slices.Reverse(msgs) // oldest first
	for _, m := range msgs {
		raw, err := json.Marshal(m.Msg)
		if err != nil {
			continue
		}
		var obj map[string]any
		_ = json.Unmarshal(raw, &obj)
		obj["type"], obj["channel"] = "message", channelID
		raw, _ = json.Marshal(obj)
		t.tap("backfill", json.RawMessage(raw))
		t.emit(ctx, t.ids.Translate(raw))
	}
	return nil
}
