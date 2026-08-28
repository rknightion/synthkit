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

// keyPresenceMap turns the key-only text representation into the non-empty raw attributes
// ClassifyLogStream expects. Values are placeholders used only for classification; the canonical
// inventory keeps the original key-only representation through keyMap.
func keyPresenceMap(keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = "present"
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
		case strings.HasPrefix(line, "== otlp metrics: series"):
			section = "otlp_metrics"
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
		case strings.HasPrefix(line, "== metrics:") || strings.HasPrefix(line, "== otlp metrics:") || strings.HasPrefix(line, "=== PYROSCOPE:"):
			section = ""
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch section {
		case "metrics", "otlp_metrics":
			i := strings.Index(line, "  {[")
			if i < 0 {
				continue
			}
			name := strings.TrimSpace(line[:i])
			labels := keyMap(bracketList(strings.TrimSuffix(strings.TrimPrefix(line[i:], "  {"), "}")))
			// The text dump carries label keys but no instrument kind. Record the raw names
			// first; once the complete dump has been read, `le` proves which component names
			// belong to real classic histograms. A suffix alone is not evidence because
			// CloudWatch five-stat gauges legitimately end in `_sum` and `_sample_count`.
			transport := canonical.TransportPrometheusRW2
			if section == "otlp_metrics" {
				transport = canonical.TransportOTLPMetrics
			}
			out.AddMetric(name, transport, canonical.InstrumentUnknown, labels, nil)
		case "logs":
			i := strings.Index(line, "  stream=[")
			if i < 0 {
				continue
			}
			rawSource := strings.TrimSpace(line[:i])
			rest := line[i+len("  stream="):]
			end := strings.Index(rest, "] meta=")
			if end < 0 {
				continue
			}
			streamKeys := bracketList(rest[:end+1])
			metaKeys := bracketList(rest[end+len("] meta="):])
			classificationLabels := keyPresenceMap(streamKeys)
			// Text dumps carry the source value outside the key-only label set. Include it in
			// the classifier input so source-declaring lanes keep their fallback identity
			// without changing the recorded stream-label keys. Set it even when empty: a
			// source-less stream may still list `source` as a key with an empty value.
			classificationLabels["source"] = rawSource
			source := canonical.ClassifyLogStream(classificationLabels, metaKeys)
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
	out = canonical.FoldClassicHistogramMetrics(out)
	return out, nil
}
