// SPDX-License-Identifier: AGPL-3.0-only

// Package inventory parses the synthkit `-once -dump` inventory text and the e2e receiver's
// captured schema into a comparable Schema (names + label KEYS only — values are non-deterministic).
package inventory

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

type Schema struct {
	Metrics    map[string][]string // series name → sorted label keys
	LogSources map[string][]string // source → sorted stream label keys
	// Traces: service → span names (informational only; NOT used in correlation — the -dump
	// format cannot faithfully encode space-containing span names, so Subset compares trace
	// SERVICES, not spans).
	Traces map[string][]string
}

func newSchema() Schema {
	return Schema{
		Metrics:    map[string][]string{},
		LogSources: map[string][]string{},
		Traces:     map[string][]string{},
	}
}

// bracketList parses "[a b c]" → []string{"a","b","c"} (the fmt "%v" rendering of a []string).
func bracketList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	sort.Strings(parts)
	return parts
}

func ParseDump(r io.Reader) (Schema, error) {
	out := newSchema()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	section := ""
	var curService string
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
		case strings.HasPrefix(line, "== metrics:") || strings.HasPrefix(line, "=== PYROSCOPE"):
			section = "" // count/footer lines
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch section {
		case "metrics":
			// "<name>  {[k1 k2]}"
			i := strings.Index(line, "  {[")
			if i < 0 {
				continue
			}
			name := strings.TrimSpace(line[:i])
			keys := bracketList(strings.TrimSuffix(strings.TrimPrefix(line[i:], "  {"), "}"))
			out.Metrics[name] = keys
		case "logs":
			// "<source>  stream=[...] meta=[...]"
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
			out.LogSources[source] = bracketList(rest[:end+1])
		case "traces":
			if !strings.HasPrefix(line, "  ") { // service header (no indent)
				curService = strings.TrimSpace(line)
				if _, ok := out.Traces[curService]; !ok {
					out.Traces[curService] = nil
				}
				continue
			}
			t := strings.TrimSpace(line)
			if spans, ok := strings.CutPrefix(t, "spans="); ok {
				out.Traces[curService] = bracketList(spans)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("scan dump: %w", err)
	}
	return out, nil
}

// Subset returns a diff message for every entry in s that is absent from of (s ⊄ of).
func (s Schema) Subset(of Schema) []string {
	var missing []string
	for name := range s.Metrics {
		if _, ok := of.Metrics[name]; !ok {
			missing = append(missing, "metric: "+name)
		}
	}
	for src := range s.LogSources {
		if _, ok := of.LogSources[src]; !ok {
			missing = append(missing, "log source: "+src)
		}
	}
	for svc := range s.Traces {
		// Service-level correlation only: the -dump format cannot faithfully encode
		// space-containing span names, and the e2e receiver builds its Schema from real
		// OTLP protos (not via ParseDump), so span sets are not comparable across sides.
		if _, ok := of.Traces[svc]; !ok {
			missing = append(missing, "trace service: "+svc)
		}
	}
	sort.Strings(missing)
	return missing
}
