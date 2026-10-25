// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"container/list"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// Identities maps raw Slack identities onto engine sources.
type Identities struct {
	OwnBotID   string   // from auth.test: our echoes are dropped
	OwnUserID  string   // our bot user id
	OperatorID string   // the only human
	IRMBotIDs  []string // Grafana IRM's bot_id(s)
	IRMAppIDs  []string // Grafana IRM's app_id(s)
}

// source classifies a message author; ok is false for our own echoes.
func (id Identities) source(user, botID, appID string) (engine.Source, bool) {
	switch {
	case botID != "" && botID == id.OwnBotID, user != "" && user == id.OwnUserID:
		return "", false
	case user != "" && user == id.OperatorID:
		return engine.FromOperator, true
	case botID != "" && slices.Contains(id.IRMBotIDs, botID), appID != "" && slices.Contains(id.IRMAppIDs, appID):
		return engine.FromIRM, true
	}
	return engine.FromOther, true
}

type rawMessage struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	Channel     json.RawMessage `json:"channel"`
	User        string          `json:"user"`
	BotID       string          `json:"bot_id"`
	AppID       string          `json:"app_id"`
	TS          string          `json:"ts"`
	Message     json.RawMessage `json:"message"`
	Hidden      bool            `json:"hidden"`
	ChannelType string          `json:"channel_type"`
	ThreadTS    string          `json:"thread_ts"`
}

type rawChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Translate turns one Events API inner event into engine events (zero or one). It is pure so it
// can be tested against recorded payloads.
func (id Identities) Translate(raw json.RawMessage) []engine.Event {
	var m rawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	switch m.Type {
	case "channel_created":
		var c rawChannel
		if json.Unmarshal(m.Channel, &c) != nil || c.ID == "" {
			return nil
		}
		return []engine.Event{{Kind: engine.EvChannelCreated, ChannelID: c.ID, ChannelName: c.Name}}
	case "member_joined_channel":
		src, ok := id.source(m.User, "", "")
		if !ok {
			return nil
		}
		return []engine.Event{{Kind: engine.EvMemberJoined, ChannelID: channelString(m.Channel), From: src, User: m.User}}
	case "message":
	default:
		return nil
	}
	ch := channelString(m.Channel)
	if m.ChannelType == "im" {
		// The control DM: only the operator's own new messages count, and each is a command
		// or a stage direction, never story chat.
		if m.Subtype != "" || id.OperatorID == "" || m.User != id.OperatorID || m.BotID != "" {
			return nil
		}
		verb, args := engine.ParseDM(Flatten(raw))
		if args == "" && verb == "direct" {
			return nil
		}
		return []engine.Event{{Kind: engine.EvControl, ChannelID: ch, From: engine.FromOperator, User: m.User, Command: verb, Args: args, TS: m.TS}}
	}
	switch m.Subtype {
	case "", "bot_message", "thread_broadcast", "me_message", "file_share":
		src, ok := id.source(m.User, m.BotID, m.AppID)
		if !ok {
			return nil
		}
		return []engine.Event{{Kind: engine.EvMessage, ChannelID: ch, From: src, User: m.User, Text: Flatten(raw), TS: m.TS}}
	case "message_changed":
		var inner rawMessage
		if json.Unmarshal(m.Message, &inner) != nil {
			return nil
		}
		src, ok := id.source(inner.User, inner.BotID, inner.AppID)
		if !ok {
			return nil
		}
		return []engine.Event{{Kind: engine.EvMessageChanged, ChannelID: ch, From: src, User: inner.User, Text: Flatten(m.Message), TS: inner.TS}}
	}
	return nil // joins, deletes, topic changes and other subtypes are not story events
}

func channelString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var c rawChannel
	_ = json.Unmarshal(raw, &c)
	return c.ID
}

var textKeys = map[string]bool{"text": true, "fallback": true, "pretext": true, "title": true, "value": true, "footer": true, "author_name": true}

// Flatten collects a message's human-readable text: the top-level text plus every text-like
// string inside blocks and attachments, deduplicated, one per line. Alert apps often put the
// real content in blocks or attachments and only a fallback in text.
func Flatten(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var parts []string
	seen := map[string]bool{}
	addText := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			parts = append(parts, s)
		}
	}
	if s, ok := m["text"].(string); ok {
		addText(s)
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys { // an object's own text first, then what it nests
				if s, ok := x[k].(string); ok && textKeys[k] {
					addText(s)
				}
			}
			for _, k := range keys {
				if _, ok := x[k].(string); !ok {
					walk(x[k])
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(m["blocks"])
	walk(m["attachments"])
	return strings.Join(parts, "\n")
}

// lru is a small set of recently seen keys (event ids, channel/ts pairs).
type lru struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

func newLRU(max int) *lru {
	return &lru{max: max, order: list.New(), items: map[string]*list.Element{}}
}

// Seen reports whether key was seen, and records it.
func (l *lru) Seen(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.items[key]; ok {
		l.order.MoveToFront(e)
		return true
	}
	l.items[key] = l.order.PushFront(key)
	if l.order.Len() > l.max {
		old := l.order.Back()
		l.order.Remove(old)
		delete(l.items, old.Value.(string))
	}
	return false
}
