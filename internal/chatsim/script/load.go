// SPDX-License-Identifier: AGPL-3.0-only

package script

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults applied by Parse when a field is zero.
const (
	DefaultModel           = "deepseek/deepseek-v4.1-flash-1017"
	DefaultMaxLLMCalls     = 250
	DefaultMaxTokens       = 400
	DefaultMaxTurns        = 2
	DefaultTypingWPM       = 70
	DefaultResponsiveness  = 0.8
	DefaultMaxPerMinute    = 6
	DefaultPersonaPerMin   = 2
	DefaultMaxLineChars    = 400
	DefaultMaxLinesPerTurn = 2
	DefaultReasoning       = "none"
	DefaultEnd             = 30 * time.Minute
)

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	emojiRe = regexp.MustCompile(`^:[a-z0-9_+'-]+:$`)
)

// Load reads and validates a scenario file.
func Load(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sc, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sc, nil
}

// Parse strictly decodes (unknown fields are errors), applies defaults and validates.
func Parse(data []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var sc Scenario
	if err := dec.Decode(&sc); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode: exactly one YAML document is allowed")
	}
	sc.applyDefaults()
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}

func (sc *Scenario) applyDefaults() {
	if sc.LLM.Model == "" {
		sc.LLM.Model = DefaultModel
	}
	if sc.LLM.MaxLLMCalls == 0 {
		sc.LLM.MaxLLMCalls = DefaultMaxLLMCalls
	}
	if sc.LLM.MaxTokens == 0 {
		sc.LLM.MaxTokens = DefaultMaxTokens
	}
	if sc.LLM.ReasoningEffort == "" {
		sc.LLM.ReasoningEffort = DefaultReasoning
	}
	for i := range sc.Personas {
		p := &sc.Personas[i]
		if p.TypingWPM == 0 {
			p.TypingWPM = DefaultTypingWPM
		}
		if p.Responsiveness == nil {
			v := DefaultResponsiveness
			p.Responsiveness = &v
		}
	}
	for i := range sc.Beats {
		if sc.Beats[i].MaxTurns == 0 {
			sc.Beats[i].MaxTurns = DefaultMaxTurns
		}
	}
	g := &sc.Guardrails
	if g.MaxMessagesPerMinute == 0 {
		g.MaxMessagesPerMinute = DefaultMaxPerMinute
	}
	if g.MaxPerPersonaPerMinute == 0 {
		g.MaxPerPersonaPerMinute = DefaultPersonaPerMin
	}
	if g.MaxLineChars == 0 {
		g.MaxLineChars = DefaultMaxLineChars
	}
	if g.MaxLinesPerTurn == 0 {
		g.MaxLinesPerTurn = DefaultMaxLinesPerTurn
	}
	if sc.End.After.Duration == 0 {
		sc.End.After.Duration = DefaultEnd
	}
}

// Validate checks ids, references, regexes and budgets. It reports every problem it finds.
func (sc *Scenario) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if sc.APIVersion != APIVersion {
		add("apiVersion must be %q, got %q", APIVersion, sc.APIVersion)
	}
	if sc.Kind != Kind {
		add("kind must be %q, got %q", Kind, sc.Kind)
	}
	if strings.TrimSpace(sc.Name) == "" {
		add("name is required")
	}
	if strings.TrimSpace(sc.Operator.Name) == "" {
		add("operator.name is required")
	}
	if t := sc.Trigger.SynthkitScenario; t != "" && strings.Count(t, "/") != 1 {
		add("trigger.synthkit_scenario %q must be \"<blueprint>/<name>\"", t)
	}
	for _, p := range [][2]string{{"trigger.start_phrase", sc.Trigger.StartPhrase}, {"trigger.fire_phrase", sc.Trigger.FirePhrase}} {
		if _, err := regexp.Compile(p[1]); p[1] != "" && err != nil {
			add("%s: %v", p[0], err)
		}
	}
	if sc.Trigger.FirePhrase != "" && sc.Trigger.SynthkitScenario == "" {
		add("trigger.fire_phrase needs trigger.synthkit_scenario")
	}
	if e := sc.LLM.ReasoningEffort; e != "none" && e != "low" && e != "medium" && e != "high" && e != "omit" {
		add("llm.reasoning_effort %q must be none|low|medium|high|omit", e)
	}
	if t := sc.LLM.Temperature; t != nil && (*t < 0 || *t > 2) {
		add("llm.temperature %v must be within 0..2", *t)
	}
	if sc.LLM.MaxLLMCalls < 0 || sc.LLM.MaxTokens < 0 {
		add("llm budgets must be positive")
	}

	if len(sc.Channels) == 0 {
		add("channels: at least one channel is required")
	}
	static := 0
	for _, id := range sc.ChannelIDs() {
		ch := sc.Channels[id]
		if !idRe.MatchString(id) {
			add("channel id %q must match %s", id, idRe)
		}
		if ch.Bind == nil {
			static++
			if ch.Fallback != "" {
				add("channel %s: fallback is only meaningful with bind", id)
			}
			continue
		}
		if ch.Bind.On != "channel_created" {
			add("channel %s: bind.on must be channel_created", id)
		}
		if _, err := regexp.Compile(ch.Bind.NameMatch); err != nil || ch.Bind.NameMatch == "" {
			add("channel %s: bind.name_match must be a valid, non-empty regex", id)
		}
		if fb, ok := sc.Channels[ch.Fallback]; !ok || fb.Bind != nil {
			add("channel %s: fallback %q must name a static channel", id, ch.Fallback)
		}
	}
	if len(sc.Channels) > 0 && static == 0 {
		add("channels: at least one static (bindings-bound) channel is required")
	}

	personas := map[string]bool{}
	names := map[string]bool{}
	if len(sc.Personas) == 0 {
		add("personas: at least one persona is required")
	}
	for _, p := range sc.Personas {
		if !idRe.MatchString(p.ID) {
			add("persona id %q must match %s", p.ID, idRe)
		}
		if personas[p.ID] {
			add("persona id %q is duplicated", p.ID)
		}
		personas[p.ID] = true
		if strings.TrimSpace(p.Name) == "" {
			add("persona %s: name is required", p.ID)
		}
		if key := strings.ToLower(p.FirstName()); names[key] {
			add("persona %s: first name %q is shared with another persona (addressing would be ambiguous)", p.ID, p.FirstName())
		} else {
			names[key] = true
		}
		if u := p.Avatar.ImageURL; u != "" && !strings.HasPrefix(u, "https://") {
			add("persona %s: avatar.image_url %q must be an https URL", p.ID, u)
		}
		if !emojiRe.MatchString(p.Avatar.Emoji) {
			add("persona %s: avatar.emoji %q must look like :name:", p.ID, p.Avatar.Emoji)
		}
		if p.TypingWPM < 10 || p.TypingWPM > 300 {
			add("persona %s: typing_wpm %d must be within 10..300", p.ID, p.TypingWPM)
		}
		if r := *p.Responsiveness; r < 0 || r > 1 {
			add("persona %s: responsiveness %v must be within 0..1", p.ID, r)
		}
		for _, c := range p.Present {
			if _, ok := sc.Channels[c]; !ok {
				add("persona %s: present channel %q is not declared", p.ID, c)
			}
		}
	}
	if f := strings.Fields(sc.Operator.Name); len(f) > 0 && names[strings.ToLower(f[0])] {
		add("operator.name %q shares a first name with a persona", sc.Operator.Name)
	}

	facts := map[string]bool{}
	for _, f := range sc.Facts {
		if !idRe.MatchString(f.ID) || facts[f.ID] {
			add("fact id %q must be unique and match %s", f.ID, idRe)
		}
		facts[f.ID] = true
		if strings.TrimSpace(f.Text) == "" {
			add("fact %s: text is required", f.ID)
		}
	}

	beats := map[string]bool{}
	for _, b := range sc.Beats {
		if !idRe.MatchString(b.ID) || beats[b.ID] {
			add("beat id %q must be unique and match %s", b.ID, idRe)
		}
		beats[b.ID] = true
	}
	for _, b := range sc.Beats {
		sc.validateBeat(b, personas, beats, facts, add)
	}
	if od := sc.Trigger.OnDeactivate; od != "" && !beats[od] {
		add("trigger.on_deactivate %q is not a beat", od)
	}

	g := sc.Guardrails
	if g.MaxMessagesPerMinute < 1 || g.MaxPerPersonaPerMinute < 1 || g.MaxLineChars < 20 || g.MaxLinesPerTurn < 1 || g.MaxLinesPerTurn > 4 {
		add("guardrails: per-minute caps must be >= 1, max_line_chars >= 20, max_lines_per_turn within 1..4")
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid chat scenario:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func (sc *Scenario) validateBeat(b Beat, personas, beats, facts map[string]bool, add func(string, ...any)) {
	n := 0
	if b.At != nil {
		n++
	}
	if b.After != "" {
		n++
		ref, _, err := AfterRef(b.After)
		switch {
		case err != nil:
			add("beat %s: %v", b.ID, err)
		case !beats[ref]:
			add("beat %s: after references unknown beat %q", b.ID, ref)
		case ref == b.ID:
			add("beat %s: after references itself", b.ID)
		}
	}
	if b.When != nil {
		n++
		sc.validateWhen(b, personas, add)
	}
	if n != 1 {
		add("beat %s: exactly one of at, after or when is required (got %d)", b.ID, n)
	}
	if b.Channel != "" {
		if _, ok := sc.Channels[b.Channel]; !ok {
			add("beat %s: channel %q is not declared", b.ID, b.Channel)
		}
	}
	if len(b.Speakers) == 0 {
		add("beat %s: at least one speaker is required", b.ID)
	}
	for _, s := range b.Speakers {
		if !personas[s] {
			add("beat %s: speaker %q is not a persona", b.ID, s)
		}
	}
	if strings.TrimSpace(b.Goal) == "" {
		add("beat %s: goal is required", b.ID)
	}
	if b.MaxTurns < 1 || b.MaxTurns > 20 {
		add("beat %s: max_turns %d must be within 1..20", b.ID, b.MaxTurns)
	}
	if r := b.Record; r != nil {
		if b.When == nil || r.From != "event" {
			add("beat %s: record needs an event trigger (when) and from: event", b.ID)
		}
		if !idRe.MatchString(r.Fact) || facts[r.Fact] {
			add("beat %s: record.fact %q must be a new id matching %s", b.ID, r.Fact, idRe)
		}
	}
	for _, a := range b.Then {
		if !personas[a.Summon] {
			add("beat %s: then.summon %q is not a persona", b.ID, a.Summon)
		}
		if _, ok := sc.Channels[a.Channel]; !ok {
			add("beat %s: then.channel %q is not declared", b.ID, a.Channel)
		}
	}
	for _, c := range b.MayClaim {
		if strings.TrimSpace(c) == "" {
			add("beat %s: may_claim entries must not be empty", b.ID)
		}
	}
}

func (sc *Scenario) validateWhen(b Beat, personas map[string]bool, add func(string, ...any)) {
	w := b.When
	n := 0
	if o := w.Operator; o != nil {
		n++
		if o.Intent != "" {
			add("beat %s: when.operator.intent is not supported in this release; use match", b.ID)
		}
		if _, err := regexp.Compile(o.Match); err != nil || o.Match == "" {
			add("beat %s: when.operator.match must be a valid, non-empty regex", b.ID)
		}
	}
	if s := w.Slack; s != nil {
		n++
		switch s.From {
		case "", "any", "irm", "operator", "other":
		default:
			add("beat %s: when.slack.from %q must be irm|operator|other|any", b.ID, s.From)
		}
		switch s.Kind {
		case "", "message", "message_changed", "channel_created", "member_joined_channel":
		default:
			add("beat %s: when.slack.kind %q is not supported", b.ID, s.Kind)
		}
		if s.Channel != "" {
			if _, ok := sc.Channels[s.Channel]; !ok {
				add("beat %s: when.slack.channel %q is not declared", b.ID, s.Channel)
			}
		}
		if _, err := regexp.Compile(s.TextMatch); err != nil {
			add("beat %s: when.slack.text_match does not compile: %v", b.ID, err)
		}
	}
	if w.Synthkit != "" {
		n++
		if w.Synthkit != "activated" && w.Synthkit != "deactivated" {
			add("beat %s: when.synthkit must be activated|deactivated", b.ID)
		}
	}
	if w.Summoned != "" {
		n++
		if !personas[w.Summoned] {
			add("beat %s: when.summoned %q is not a persona", b.ID, w.Summoned)
		}
	}
	if n != 1 {
		add("beat %s: when needs exactly one of operator, slack, synthkit, summoned", b.ID)
	}
}

// Persona returns the persona with id, or nil.
func (sc *Scenario) Persona(id string) *Persona {
	for i := range sc.Personas {
		if sc.Personas[i].ID == id {
			return &sc.Personas[i]
		}
	}
	return nil
}

// ChannelIDs returns the logical channel ids in sorted order.
func (sc *Scenario) ChannelIDs() []string {
	ids := make([]string, 0, len(sc.Channels))
	for id := range sc.Channels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
