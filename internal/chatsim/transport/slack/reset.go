// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/slack-go/slack"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/script"
)

// SeedEventType tags each seeded post's Slack message metadata.
const SeedEventType = "synthkit_chat_seed"

// ResetAPI is the part of the Slack Web API a reset uses; *slack.Client implements it.
type ResetAPI interface {
	GetConversationsForUserContext(ctx context.Context, params *slack.GetConversationsForUserParameters) ([]slack.Channel, string, error)
	GetConversationHistoryContext(ctx context.Context, params *slack.GetConversationHistoryParameters) (*slack.GetConversationHistoryResponse, error)
	GetConversationRepliesContext(ctx context.Context, params *slack.GetConversationRepliesParameters) ([]slack.Message, bool, string, error)
	DeleteMessageContext(ctx context.Context, channel, ts string) (string, string, error)
	ArchiveConversationContext(ctx context.Context, channelID string) error
	JoinConversationContext(ctx context.Context, channelID string) (*slack.Channel, string, []string, error)
	PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error)
}

// ResetConfig configures a workspace reset.
type ResetConfig struct {
	// User is a workspace owner's or admin's user token (xoxp-): it lists, wipes and archives
	// the channels that user is in, and posts the operator's seeded lines. Slack lets owners and
	// admins delete anyone's messages in channels they belong to; a bot token deletes only its
	// own.
	User ResetAPI
	// Bot posts persona and app lines, and deletes its own messages when User cannot.
	Bot ResetAPI
	// Channels maps logical channels to bound channel ids; bound channels are never archived.
	Channels map[string]string
	// Personas maps persona ids to their posting identity.
	Personas map[string]engine.Identity
	// AppName and AppEmoji are the identity for "app" lines; empty means the app's own.
	AppName, AppEmoji string
	// DryRun lists what would be deleted, archived and posted, and changes nothing.
	DryRun bool
	// Progress receives one line per channel and step; Logf receives detail.
	Progress func(string)
	Logf     func(format string, a ...any)
	// Sleep waits between seeded lines and after a rate limit; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// ResetReport says what a reset did.
type ResetReport struct {
	Wiped, Archived, Deleted, Undeletable, Posted int
	Problems                                      []string
}

func (r ResetReport) String() string {
	s := fmt.Sprintf("%d channels wiped (%d messages deleted), %d archived, %d lines seeded", r.Wiped, r.Deleted, r.Archived, r.Posted)
	if r.Undeletable > 0 {
		s += fmt.Sprintf("; %d messages could not be deleted", r.Undeletable)
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type resetter struct {
	cfg ResetConfig
	rep ResetReport
}

// Reset wipes the workspace back to the seed's known state: every message in every channel the
// user token's user belongs to is deleted (thread replies first), channels matching
// wipe.archive_match are archived, then the seed's lines are posted in order, paced. Seed
// channels are resolved before anything is deleted, so a bad seed changes nothing.
func Reset(ctx context.Context, cfg ResetConfig, seed *script.Seed) (ResetReport, error) {
	if cfg.User == nil || cfg.Bot == nil {
		return ResetReport{}, errors.New("reset needs both a user token and the bot token")
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Progress == nil {
		cfg.Progress = func(string) {}
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	r := &resetter{cfg: cfg}
	chans, err := r.listChannels(ctx)
	if err != nil {
		return r.rep, err
	}
	byName := map[string]string{}
	names := map[string]string{}
	for _, c := range chans {
		byName[c.Name], names[c.ID] = c.ID, c.Name
	}
	targets, err := r.resolveSeed(seed, byName)
	if err != nil {
		return r.rep, err
	}
	blocks, err := seedBlocks(seed)
	if err != nil {
		return r.rep, err
	}
	keep := map[string]bool{}
	for _, k := range seed.Wipe.Keep {
		if id := r.channelID(k, byName); id != "" {
			keep[id] = true
		}
	}
	bound := map[string]bool{}
	for _, id := range cfg.Channels {
		bound[id] = true
	}
	var archive *regexp.Regexp
	if seed.Wipe.ArchiveMatch != "" {
		archive = regexp.MustCompile(seed.Wipe.ArchiveMatch) // validated by script.ParseSeed
	}
	for _, c := range chans {
		switch {
		case keep[c.ID]:
			cfg.Progress(fmt.Sprintf("kept #%s", c.Name))
		case archive != nil && archive.MatchString(c.Name) && !bound[c.ID] && !slices.Contains(targets, c.ID):
			if err := r.archive(ctx, c); err != nil {
				return r.rep, err
			}
		default:
			if err := r.wipe(ctx, c); err != nil {
				return r.rep, err
			}
		}
	}
	if err := r.seed(ctx, seed, targets, names, blocks); err != nil {
		return r.rep, err
	}
	return r.rep, nil
}

func (r *resetter) listChannels(ctx context.Context) ([]slack.Channel, error) {
	var all []slack.Channel
	cursor := ""
	for {
		var page []slack.Channel
		var next string
		err := r.retry(ctx, func() error {
			var err error
			page, next, err = r.cfg.User.GetConversationsForUserContext(ctx, &slack.GetConversationsForUserParameters{
				Types: []string{"public_channel", "private_channel"}, ExcludeArchived: true, Limit: 200, Cursor: cursor,
			})
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("slack users.conversations (user token needs channels:read): %w", err)
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	slices.SortFunc(all, func(a, b slack.Channel) int { return strings.Compare(a.Name, b.Name) })
	return all, nil
}

// channelID resolves "#name" or a logical channel.
func (r *resetter) channelID(ref string, byName map[string]string) string {
	if name, ok := strings.CutPrefix(ref, "#"); ok {
		return byName[name]
	}
	return r.cfg.Channels[ref]
}

// resolveSeed returns each seed line's channel id, in order.
func (r *resetter) resolveSeed(seed *script.Seed, byName map[string]string) ([]string, error) {
	out := make([]string, len(seed.Messages))
	var errs []string
	for i, m := range seed.Messages {
		id := r.channelID(m.Channel, byName)
		if id == "" {
			errs = append(errs, fmt.Sprintf("messages[%d]: channel %q is not bound and is not a channel the user token's user is in", i, m.Channel))
		}
		if m.From != script.SeedFromOperator && m.From != script.SeedFromApp {
			if _, ok := r.cfg.Personas[m.From]; !ok {
				errs = append(errs, fmt.Sprintf("messages[%d]: no persona %q", i, m.From))
			}
		}
		out[i] = id
	}
	if len(errs) > 0 {
		return nil, errors.New("seed does not fit this workspace:\n  - " + strings.Join(errs, "\n  - "))
	}
	return out, nil
}

// retry runs fn, waiting out Slack rate limits (at most five times).
func (r *resetter) retry(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		var rl *slack.RateLimitedError
		if !errors.As(err, &rl) || attempt == 5 {
			return err
		}
		wait := max(rl.RetryAfter, time.Second)
		r.cfg.Logf("slack: rate limited; waiting %s", wait)
		if err := r.cfg.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func slackErr(err error) string {
	var se slack.SlackErrorResponse
	if errors.As(err, &se) {
		return se.Err
	}
	return ""
}

func (r *resetter) archive(ctx context.Context, c slack.Channel) error {
	if r.cfg.DryRun {
		r.cfg.Progress(fmt.Sprintf("would archive #%s", c.Name))
		r.rep.Archived++
		return nil
	}
	err := r.retry(ctx, func() error { return r.cfg.User.ArchiveConversationContext(ctx, c.ID) })
	if err != nil && slackErr(err) != "already_archived" {
		r.rep.Problems = append(r.rep.Problems, fmt.Sprintf("archive #%s: %v", c.Name, err))
		r.cfg.Progress(fmt.Sprintf("could not archive #%s: %v", c.Name, err))
		return nil
	}
	r.rep.Archived++
	r.cfg.Progress(fmt.Sprintf("archived #%s", c.Name))
	return nil
}

// timestamps lists every message ts in a channel, thread replies before their parents and
// newest first within each group, so no deleted parent leaves a tombstone behind.
func (r *resetter) timestamps(ctx context.Context, c slack.Channel) ([]string, error) {
	var parents, replies []string
	cursor := ""
	for {
		var resp *slack.GetConversationHistoryResponse
		err := r.retry(ctx, func() error {
			var err error
			resp, err = r.cfg.User.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: c.ID, Limit: 200, Cursor: cursor})
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("slack conversations.history #%s (user token needs channels:history): %w", c.Name, err)
		}
		for _, m := range resp.Messages {
			parents = append(parents, m.Timestamp)
			if m.ReplyCount == 0 {
				continue
			}
			rcur := ""
			for {
				var msgs []slack.Message
				var more bool
				var next string
				err := r.retry(ctx, func() error {
					var err error
					msgs, more, next, err = r.cfg.User.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: c.ID, Timestamp: m.Timestamp, Limit: 200, Cursor: rcur})
					return err
				})
				if err != nil {
					return nil, fmt.Errorf("slack conversations.replies #%s: %w", c.Name, err)
				}
				for _, rm := range msgs {
					if rm.Timestamp != m.Timestamp {
						replies = append(replies, rm.Timestamp)
					}
				}
				if !more || next == "" {
					break
				}
				rcur = next
			}
		}
		if !resp.HasMore || resp.ResponseMetaData.NextCursor == "" {
			break
		}
		cursor = resp.ResponseMetaData.NextCursor
	}
	newestFirst := func(a, b string) int { return strings.Compare(b, a) }
	slices.SortFunc(replies, newestFirst)
	slices.SortFunc(parents, newestFirst)
	return append(replies, parents...), nil
}

func (r *resetter) wipe(ctx context.Context, c slack.Channel) error {
	tss, err := r.timestamps(ctx, c)
	if err != nil {
		return err
	}
	r.rep.Wiped++
	if r.cfg.DryRun {
		r.cfg.Progress(fmt.Sprintf("would clear #%s (%d messages)", c.Name, len(tss)))
		r.rep.Deleted += len(tss)
		return nil
	}
	deleted, failed := 0, 0
	for _, ts := range tss {
		err := r.retry(ctx, func() error { _, _, err := r.cfg.User.DeleteMessageContext(ctx, c.ID, ts); return err })
		if slackErr(err) == "cant_delete_message" {
			// Not deletable as the user: our own bot can still delete what it posted.
			err = r.retry(ctx, func() error { _, _, err := r.cfg.Bot.DeleteMessageContext(ctx, c.ID, ts); return err })
		}
		switch {
		case err == nil:
			deleted++
		case slackErr(err) == "message_not_found":
		case ctx.Err() != nil:
			return ctx.Err()
		default:
			failed++
			r.cfg.Logf("slack: could not delete %s in #%s: %v", ts, c.Name, err)
		}
	}
	r.rep.Deleted += deleted
	r.rep.Undeletable += failed
	line := fmt.Sprintf("cleared #%s (%d messages)", c.Name, deleted)
	if failed > 0 {
		line += fmt.Sprintf(", %d could not be deleted", failed)
		r.rep.Problems = append(r.rep.Problems, fmt.Sprintf("#%s: %d messages could not be deleted", c.Name, failed))
	}
	r.cfg.Progress(line)
	return nil
}

// seedBlocks decodes each line's Slack blocks, so a malformed file stops the reset before
// anything is deleted.
func seedBlocks(seed *script.Seed) ([][]slack.Block, error) {
	out := make([][]slack.Block, len(seed.Messages))
	for i, m := range seed.Messages {
		if len(m.Blocks) == 0 {
			continue
		}
		var shapes []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(m.Blocks, &shapes); err != nil {
			return nil, fmt.Errorf("seed line %d blocks (%s): %w", i, m.BlocksFile, err)
		}
		for j, b := range shapes {
			if b.Type == "" {
				return nil, fmt.Errorf("seed line %d blocks (%s): block %d has no type", i, m.BlocksFile, j)
			}
		}
		var bs slack.Blocks
		if err := json.Unmarshal(m.Blocks, &bs); err != nil {
			return nil, fmt.Errorf("seed line %d blocks (%s): %w", i, m.BlocksFile, err)
		}
		out[i] = bs.BlockSet
	}
	return out, nil
}

// pause is the deterministic wait before seed line i.
func pause(p script.Range, i int) time.Duration {
	if p.Max <= p.Min {
		return p.Min
	}
	frac := float64((i*7919+13)%101) / 100
	return p.Min + time.Duration(frac*float64(p.Max-p.Min))
}

func (r *resetter) seed(ctx context.Context, seed *script.Seed, targets []string, names map[string]string, blocks [][]slack.Block) error {
	joined := map[string]bool{}
	for _, id := range targets {
		if joined[id] || r.cfg.DryRun {
			continue
		}
		joined[id] = true
		if _, _, _, err := r.cfg.Bot.JoinConversationContext(ctx, id); err != nil && slackErr(err) != "method_not_supported_for_channel_type" {
			return fmt.Errorf("slack conversations.join #%s for the bot (needs channels:join): %w", names[id], err)
		}
	}
	keys := map[string]string{}
	for i, m := range seed.Messages {
		wait := pause(seed.Pace, i)
		if m.After != nil {
			wait = m.After.Duration
		}
		if r.cfg.DryRun {
			r.rep.Posted++
			continue
		}
		if i > 0 {
			if err := r.cfg.Sleep(ctx, wait); err != nil {
				return err
			}
		}
		opts := []slack.MsgOption{slack.MsgOptionText(m.Text, false)}
		if len(blocks[i]) > 0 {
			opts = append(opts, slack.MsgOptionBlocks(blocks[i]...))
		}
		if m.ReplyTo != "" {
			opts = append(opts, slack.MsgOptionTS(keys[m.ReplyTo]))
		}
		api := r.cfg.Bot
		switch m.From {
		case script.SeedFromOperator:
			api = r.cfg.User
		case script.SeedFromApp:
			if m.As != nil {
				opts = append(opts, slack.MsgOptionUsername(m.As.Name))
				if m.As.IconURL != "" {
					opts = append(opts, slack.MsgOptionIconURL(m.As.IconURL))
				}
				break
			}
			if r.cfg.AppName != "" {
				opts = append(opts, slack.MsgOptionUsername(r.cfg.AppName))
			}
			if r.cfg.AppEmoji != "" {
				opts = append(opts, slack.MsgOptionIconEmoji(r.cfg.AppEmoji))
			}
		default:
			id := r.cfg.Personas[m.From]
			opts = append(opts, slack.MsgOptionUsername(id.Name), personaIcon(id),
				slack.MsgOptionMetadata(slack.SlackMetadata{EventType: SeedEventType, EventPayload: map[string]any{"seed": seed.Name, "persona": m.From}}))
		}
		var ts string
		err := r.retry(ctx, func() error {
			var err error
			_, ts, err = api.PostMessageContext(ctx, targets[i], opts...)
			return err
		})
		if err != nil {
			return fmt.Errorf("slack chat.postMessage seed line %d in #%s: %w", i, names[targets[i]], err)
		}
		if m.Key != "" {
			keys[m.Key] = ts
		}
		r.rep.Posted++
	}
	if r.cfg.DryRun {
		r.cfg.Progress(fmt.Sprintf("would seed %d lines", len(seed.Messages)))
	} else {
		r.cfg.Progress(fmt.Sprintf("seeded %d lines", r.rep.Posted))
	}
	return nil
}

// WebClient returns a plain Web API client for token (no Socket Mode), for a reset run outside
// a live session: the operator's xoxp- user token or the bot's xoxb- token.
func WebClient(token string) ResetAPI { return slack.New(token) }

// ResetAPI returns the transport's bot client for a reset during a live run.
func (t *Transport) ResetAPI() ResetAPI { return t.api }
