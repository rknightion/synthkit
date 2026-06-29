// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"sort"
	"sync"
	"time"
)

// Diagnostic is one load-time problem surfaced to operators via the control UI: a blueprint that
// failed to load (and was skipped under degrade-and-continue), or a config entry that was dropped
// (a malformed/over-long incident schedule entry, a zero-construct declaration, etc.). It exists so
// these no longer vanish into stderr — the admin dashboard can show them.
type Diagnostic struct {
	Level    string `json:"level"`    // "error" (blueprint skipped) | "warning" (entry skipped/degraded)
	Source   string `json:"source"`   // blueprint name or source file path
	Category string `json:"category"` // "load" | "resolve" | "incident"
	Message  string `json:"message"`
	Time     string `json:"time"` // RFC3339 (UTC) when recorded
}

// Diagnostics is a concurrency-safe collector of load-time Diagnostics. It is populated at startup
// (and on any future live reload) and read by GET /control/diagnostics.
type Diagnostics struct {
	mu    sync.Mutex
	items []Diagnostic
}

// NewDiagnostics returns an empty collector.
func NewDiagnostics() *Diagnostics { return &Diagnostics{} }

// Add records one diagnostic. level is "error" or "warning"; category is "load"/"resolve"/"incident".
func (d *Diagnostics) Add(level, source, category, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items = append(d.items, Diagnostic{
		Level:    level,
		Source:   source,
		Category: category,
		Message:  message,
		Time:     time.Now().UTC().Format(time.RFC3339),
	})
}

// Count returns the number of recorded diagnostics.
func (d *Diagnostics) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items)
}

// Snapshot returns a copy of the collected diagnostics, errors first then warnings, each level
// group preserving insertion order (stable). Safe to call concurrently.
func (d *Diagnostics) Snapshot() []Diagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Diagnostic, len(d.items))
	copy(out, d.items)
	sort.SliceStable(out, func(i, j int) bool { return levelRank(out[i].Level) < levelRank(out[j].Level) })
	return out
}

func levelRank(level string) int {
	if level == "error" {
		return 0
	}
	return 1
}
