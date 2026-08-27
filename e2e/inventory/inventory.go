// SPDX-License-Identifier: AGPL-3.0-only

// Package inventory adapts the legacy human-readable -dump output to the canonical
// internal/inventory schema for the Docker e2e correlation tests.
package inventory

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	canonical "github.com/rknightion/synthkit/internal/inventory"
)

type Schema = canonical.Schema

func ReceiptCount(schema Schema, protocol string) int {
	for _, receipt := range schema.Receipts {
		if receipt.Protocol == protocol {
			return receipt.Count
		}
	}
	return 0
}

func bracketList(s string) []string {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s), "["), "]"))
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	sort.Strings(parts)
	return parts
}

func keyMap(keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = ""
	}
	return out
}

// ParseDump parses the stable text presentation into the canonical schema. It is structural:
// the text format carries keys, not values, and cannot faithfully split span names with spaces.
func ParseDump(r io.Reader) (Schema, error) {
	out := canonical.New()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	section := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "== metrics: series"):
			section = "metrics"
			continue
		case strings.HasPrefix(line, "== logs:"):
			section = "logs"
			continue
		case strings.HasPrefix(line, "== traces:"):
			section = "traces"
			continue
		case strings.HasPrefix(line, "== sigil:"):
			section = "sigil"
			continue
		case strings.HasPrefix(line, "=== PYROSCOPE ==="):
			section = "profiles"
			continue
		case strings.HasPrefix(line, "== metrics:") || strings.HasPrefix(line, "=== PYROSCOPE:"):
			section = ""
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch section {
		case "metrics":
			i := strings.Index(line, "  {[")
			if i < 0 {
				continue
			}
			name := strings.TrimSpace(line[:i])
			labels := keyMap(bracketList(strings.TrimSuffix(strings.TrimPrefix(line[i:], "  {"), "}")))
			instrument := canonical.InstrumentUnknown
			var histogram *canonical.Histogram
			// The text dump carries no instrument kind, so a component suffix is the only
			// classic-histogram signal it has. The fold rule itself is the shared one, so this
			// adapter cannot drift from the other inventory producers.
			if family, folded := canonical.ClassicHistogramFamily(name); folded {
				name = family
				instrument = canonical.InstrumentHistogram
				histogram = &canonical.Histogram{Classic: true}
			}
			out.AddMetric(name, canonical.TransportPrometheusRW2, instrument, labels, histogram)
		case "logs":
			i := strings.Index(line, "  stream=[")
			if i < 0 {
				continue
			}
			source := strings.TrimSpace(line[:i])
			rest := line[i+len("  stream="):]
			end := strings.Index(rest, "] meta=")
			if end < 0 {
				continue
			}
			streamKeys := bracketList(rest[:end+1])
			metaKeys := bracketList(rest[end+len("] meta="):])
			out.AddLog(source, canonical.TransportLoki, keyMap(streamKeys), metaKeys)
		case "traces":
			if !strings.HasPrefix(line, "  ") {
				out.AddTrace(strings.TrimSpace(line), nil, "", nil)
			}
		case "sigil":
			i := strings.Index(line, "  ops=")
			if i < 0 {
				continue
			}
			out.AddSigil(strings.TrimSpace(line[:i]), bracketList(line[i+len("  ops="):])...)
		case "profiles":
			i := strings.Index(line, "  {[")
			if i < 0 {
				continue
			}
			out.AddProfile(strings.TrimSpace(line[:i]), keyMap(bracketList(strings.TrimSuffix(strings.TrimPrefix(line[i:], "  {"), "}"))))
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("scan dump: %w", err)
	}
	out.Normalize()
	return out, nil
}
