// SPDX-License-Identifier: AGPL-3.0-only

// Package pyroscope is the Pyroscope continuous-profiling mechanic lib (peer to internal/cw and
// internal/genai): it builds pprof Profile protos + per-runtime flamegraph vocab and holds the
// profiling config block. It is imported by the construct + both workloads. It must NOT import the
// sink (internal/sink/pyroscope) or any profiling SDK — pure proto construction only.
package pyroscope

import "fmt"

// ProfilingCfg is the `pyroscope:` block on a web_service Config or an app ServiceNode. It is NOT a
// telemetryspec Profile (those are metrics/logs/spans bundles). Owned by the profiling workstream.
type ProfilingCfg struct {
	Enabled      bool     `yaml:"enabled"`
	Mode         string   `yaml:"mode"`          // "sdk" (self-push, default) | "scraped" (Alloy collects)
	Runtime      string   `yaml:"runtime"`       // go|jvm|node|python; on an app node defaults from node.Runtime
	Types        []string `yaml:"types"`         // optional profile-type subset; empty => the runtime's full set
	SpanProfiles bool     `yaml:"span_profiles"` // tag a sample subset with real span_ids (request correlation)
}

// Validate checks the mode enum (loud load error). Empty mode is allowed (defaults to sdk).
func (c ProfilingCfg) Validate() error {
	if c.Mode != "" && c.Mode != "sdk" && c.Mode != "scraped" {
		return fmt.Errorf("pyroscope: unknown mode %q (want sdk|scraped)", c.Mode)
	}
	return nil
}

// ModeOrDefault returns the mode, defaulting to "sdk".
func (c ProfilingCfg) ModeOrDefault() string {
	if c.Mode == "" {
		return "sdk"
	}
	return c.Mode
}
