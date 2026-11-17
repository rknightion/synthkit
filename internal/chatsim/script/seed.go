// SPDX-License-Identifier: AGPL-3.0-only

package script

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SeedKind is the header kind of a seed file: the known state a workspace reset returns to.
const SeedKind = "ChatSeed"

// Seed is one seed file. A reset wipes every message from the channels the resetting user is
// in, archives leftover dynamic channels, then posts Messages in order. It is written once and
// replayed verbatim on every reset; no model is involved.
type Seed struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Name       string        `yaml:"name"`
	Wipe       SeedWipe      `yaml:"wipe"`
	Pace       Range         `yaml:"pace"`
	Messages   []SeedMessage `yaml:"messages"`
}

// SeedWipe says what a reset clears.
type SeedWipe struct {
	// ArchiveMatch, a regexp on the channel name, archives matching channels (an earlier
	// run's incident rooms) instead of wiping them. Bound channels are never archived.
	ArchiveMatch string `yaml:"archive_match"`
	// Keep lists channel names ("#name") or logical channels left untouched.
	Keep []string `yaml:"keep"`
	// IRM also resolves every open OnCall alert group and incident on the bindings' Grafana
	// stack, before the Slack wipe.
	IRM bool `yaml:"irm"`
}

// SeedMessage is one posted line.
type SeedMessage struct {
	// Channel is a logical channel from the bindings or a channel name "#name".
	Channel string `yaml:"channel"`
	// From is a persona id, "operator" (posted as the resetting user) or "app".
	From string `yaml:"from"`
	Text string `yaml:"text"`
	// After overrides the pause before this line (default: pace).
	After *Duration `yaml:"after"`
	// Key names this line so a later line can reply in its thread.
	Key string `yaml:"key"`
	// ReplyTo is the Key of an earlier line; this line goes in that line's thread.
	ReplyTo string `yaml:"reply_to"`
	// As gives an "app" line its own name and picture, to replay another integration's post.
	As *SeedAs `yaml:"as"`
	// BlocksFile is a JSON array of Slack blocks, relative to the seed file, posted with Text
	// as the notification fallback.
	BlocksFile string `yaml:"blocks_file"`
	// Blocks is BlocksFile's content, read by LoadSeed.
	Blocks json.RawMessage `yaml:"-"`
}

// SeedAs is the identity an "app" line posts under.
type SeedAs struct {
	Name    string `yaml:"name"`
	IconURL string `yaml:"icon_url"`
}

// Seed message authors besides personas.
const (
	SeedFromOperator = "operator"
	SeedFromApp      = "app"
)

// LoadSeed reads and validates a seed file against the scenario whose cast speaks in it.
func LoadSeed(path string, sc *Scenario) (*Seed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := ParseSeed(data, sc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range s.Messages {
		m := &s.Messages[i]
		if m.BlocksFile == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), m.BlocksFile))
		if err != nil {
			return nil, fmt.Errorf("%s: messages[%d]: %w", path, i, err)
		}
		raw = bytes.TrimSpace(raw)
		if !json.Valid(raw) || len(raw) == 0 || raw[0] != '[' {
			return nil, fmt.Errorf("%s: messages[%d]: blocks_file %s is not a JSON array", path, i, m.BlocksFile)
		}
		m.Blocks = raw
	}
	return s, nil
}

// ParseSeed strictly decodes a seed file, applies defaults and validates it.
func ParseSeed(data []byte, sc *Scenario) (*Seed, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s Seed
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode: exactly one YAML document is allowed")
	}
	if s.Pace.Max == 0 {
		s.Pace = Range{Min: 4 * time.Second, Max: 10 * time.Second}
	}
	if err := s.validate(sc); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Seed) validate(sc *Scenario) error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if s.APIVersion != APIVersion {
		add("apiVersion must be %q, got %q", APIVersion, s.APIVersion)
	}
	if s.Kind != SeedKind {
		add("kind must be %q, got %q", SeedKind, s.Kind)
	}
	if strings.TrimSpace(s.Name) == "" {
		add("name is required")
	}
	if _, err := regexp.Compile(s.Wipe.ArchiveMatch); s.Wipe.ArchiveMatch != "" && err != nil {
		add("wipe.archive_match: %v", err)
	}
	personas := map[string]bool{}
	if sc != nil {
		for _, p := range sc.Personas {
			personas[p.ID] = true
		}
	}
	keys := map[string]bool{}
	for i, m := range s.Messages {
		at := fmt.Sprintf("messages[%d]", i)
		if strings.TrimSpace(m.Channel) == "" {
			add("%s: channel is required", at)
		}
		if strings.TrimSpace(m.Text) == "" {
			add("%s: text is required", at)
		}
		switch {
		case m.From == SeedFromOperator || m.From == SeedFromApp:
		case personas[m.From]:
		default:
			add("%s: from %q is not a persona id, %q or %q", at, m.From, SeedFromOperator, SeedFromApp)
		}
		if m.As != nil {
			if m.From != SeedFromApp {
				add("%s: as is only for from %q", at, SeedFromApp)
			}
			if strings.TrimSpace(m.As.Name) == "" {
				add("%s: as.name is required", at)
			}
			if m.As.IconURL != "" && !strings.HasPrefix(m.As.IconURL, "https://") {
				add("%s: as.icon_url must be an https URL", at)
			}
		}
		if f := m.BlocksFile; f != "" && (filepath.IsAbs(f) || strings.HasPrefix(filepath.Clean(f), "..")) {
			add("%s: blocks_file must be a path inside the seed's directory", at)
		}
		if m.ReplyTo != "" && !keys[m.ReplyTo] {
			add("%s: reply_to %q names no earlier key", at, m.ReplyTo)
		}
		if m.Key != "" {
			if keys[m.Key] {
				add("%s: key %q is used twice", at, m.Key)
			}
			keys[m.Key] = true
		}
	}
	if len(errs) > 0 {
		return errors.New("invalid seed:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}
