// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

// fakeAPI is a tiny workspace: channels with messages, some threaded, and a log of calls.
type fakeAPI struct {
	who      string
	channels []slack.Channel
	history  map[string][]slack.Message
	replies  map[string][]slack.Message // "<channel>/<parent ts>"
	refuse   map[string]bool            // ts this token may not delete
	limitOne bool                       // rate-limit the first delete once
	log      *[]string
	posts    int
}

func (f *fakeAPI) GetConversationsForUserContext(context.Context, *slack.GetConversationsForUserParameters) ([]slack.Channel, string, error) {
	return f.channels, "", nil
}

func (f *fakeAPI) GetConversationHistoryContext(_ context.Context, p *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error) {
	return &slack.GetConversationHistoryResponse{Messages: f.history[p.ChannelID]}, nil
}

func (f *fakeAPI) GetConversationRepliesContext(_ context.Context, p *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error) {
	return f.replies[p.ChannelID+"/"+p.Timestamp], false, "", nil
}

func (f *fakeAPI) DeleteMessageContext(_ context.Context, ch, ts string) (string, string, error) {
	if f.limitOne {
		f.limitOne = false
		return "", "", &slack.RateLimitedError{RetryAfter: time.Second}
	}
	if f.refuse[ts] {
		return "", "", slack.SlackErrorResponse{Err: "cant_delete_message"}
	}
	*f.log = append(*f.log, fmt.Sprintf("%s delete %s %s", f.who, ch, ts))
	return ch, ts, nil
}

func (f *fakeAPI) ArchiveConversationContext(_ context.Context, ch string) error {
	*f.log = append(*f.log, fmt.Sprintf("%s archive %s", f.who, ch))
	return nil
}

func (f *fakeAPI) JoinConversationContext(_ context.Context, ch string) (*slack.Channel, string, []string, error) {
	*f.log = append(*f.log, fmt.Sprintf("%s join %s", f.who, ch))
	return nil, "", nil, nil
}

func (f *fakeAPI) PostMessageContext(_ context.Context, ch string, opts ...slack.MsgOption) (string, string, error) {
	_, vals, err := slack.UnsafeApplyMsgOptions("x", ch, "https://slack.test/api/", opts...)
	if err != nil {
		return "", "", err
	}
	f.posts++
	ts := fmt.Sprintf("%s.%d", f.who, f.posts)
	line := fmt.Sprintf("%s post %s %q as=%q", f.who, ch, vals.Get("text"), vals.Get("username"))
	if th := vals.Get("thread_ts"); th != "" {
		line += " thread=" + th
	}
	*f.log = append(*f.log, line)
	return ch, ts, nil
}

func msg(ts string, replies int) slack.Message {
	return slack.Message{Msg: slack.Msg{Timestamp: ts, ReplyCount: replies}}
}

func resetFixture(t *testing.T) (*fakeAPI, *fakeAPI, *[]string, *script.Seed, ResetConfig) {
	t.Helper()
	var log []string
	user := &fakeAPI{who: "user", log: &log, limitOne: true,
		channels: []slack.Channel{
			{GroupConversation: slack.GroupConversation{Name: "ops", Conversation: slack.Conversation{ID: "C1"}}},
			{GroupConversation: slack.GroupConversation{Name: "incident-old", Conversation: slack.Conversation{ID: "C2"}}},
			{GroupConversation: slack.GroupConversation{Name: "events", Conversation: slack.Conversation{ID: "C3"}}},
			{GroupConversation: slack.GroupConversation{Name: "random", Conversation: slack.Conversation{ID: "C4"}}},
		},
		history: map[string][]slack.Message{
			"C1": {msg("300.0", 0), msg("200.0", 2), msg("100.0", 0)},
			"C3": {msg("50.0", 0)},
			"C4": {msg("9.0", 0)},
		},
		replies: map[string][]slack.Message{"C1/200.0": {msg("200.0", 2), msg("210.0", 0), msg("220.0", 0)}},
		refuse:  map[string]bool{"100.0": true},
	}
	bot := &fakeAPI{who: "bot", log: &log}
	seed := &script.Seed{Name: "morning", Pace: script.Range{Min: time.Second, Max: 3 * time.Second},
		Wipe: script.SeedWipe{ArchiveMatch: "^incident-", Keep: []string{"#random"}},
		Messages: []script.SeedMessage{
			{Channel: "ops", From: "alex", Text: "handover", Key: "h"},
			{Channel: "ops", From: script.SeedFromOperator, Text: "thanks", ReplyTo: "h"},
			{Channel: "#events", From: script.SeedFromApp, Text: "notice"},
		}}
	cfg := ResetConfig{User: user, Bot: bot, Channels: map[string]string{"ops": "C1"},
		Personas: map[string]engine.Identity{"alex": {PersonaID: "alex", Name: "Alex Moreno", Emoji: ":wave:"}},
		AppName:  "Bobby Tables",
		Sleep:    func(context.Context, time.Duration) error { return nil }}
	return user, bot, &log, seed, cfg
}

// TestResetWipesThenSeeds: thread replies go before their parents, newest first; what the user
// token cannot delete falls back to the bot; incident rooms are archived, kept channels are left
// alone, and channels go in name order; then the seed posts in order, the operator's line as the user in the persona's thread.
func TestResetWipesThenSeeds(t *testing.T) {
	_, _, log, seed, cfg := resetFixture(t)
	rep, err := Reset(context.Background(), cfg, seed)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"user delete C3 50.0",
		"user archive C2",
		"user delete C1 220.0", "user delete C1 210.0", "user delete C1 300.0", "user delete C1 200.0",
		"bot delete C1 100.0",
		"bot join C1", "bot join C3",
		`bot post C1 "handover" as="Alex Moreno"`,
		`user post C1 "thanks" as="" thread=bot.1`,
		`bot post C3 "notice" as="Bobby Tables"`,
	}
	got := *log
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if rep.Wiped != 2 || rep.Deleted != 6 || rep.Archived != 1 || rep.Posted != 3 || rep.Undeletable != 0 {
		t.Fatalf("report %+v", rep)
	}
}

// TestResetRefusesABadSeedBeforeDeleting: a seed line in a channel the workspace lacks stops
// the reset before any message is touched.
func TestResetRefusesABadSeedBeforeDeleting(t *testing.T) {
	_, _, log, seed, cfg := resetFixture(t)
	seed.Messages = append(seed.Messages, script.SeedMessage{Channel: "#nowhere", From: "alex", Text: "x"})
	if _, err := Reset(context.Background(), cfg, seed); err == nil || !strings.Contains(err.Error(), "#nowhere") {
		t.Fatalf("err = %v", err)
	}
	if len(*log) != 0 {
		t.Fatalf("calls before refusing: %v", *log)
	}
}

func TestResetDryRunChangesNothing(t *testing.T) {
	_, _, log, seed, cfg := resetFixture(t)
	cfg.DryRun = true
	rep, err := Reset(context.Background(), cfg, seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(*log) != 0 || rep.Deleted != 6 || rep.Archived != 1 || rep.Posted != 3 {
		t.Fatalf("dry run: calls %v report %+v", *log, rep)
	}
}
