// SPDX-License-Identifier: AGPL-3.0-only

// Package host is the traditional (non-Kubernetes) machine construct. One instance per
// declared host emits the Grafana Alloy node_exporter (Linux), windows_exporter (Windows),
// or macos node_exporter (macOS) series — plus, when the host's `observability.docker`
// switch is set, the Docker cadvisor lane — by delegating the metric vocabulary + physics
// to the shared internal/nodeexp mechanic lib.
//
// Kind: "host"
// Scope: ScopeSubstrate — disambiguated by (job, instance); never stamped with a blueprint
// label. The hostname (`instance`) must be unique across blueprints (load-time gate).
// Group: omitted ("") — a topology kind emitted by a bespoke resolver pass over `hosts:`.
// Signals: Metrics + Logs.
// Interval: 60s.
//
// Config is EMPTY (fixture-driven): every per-host knob rides on the fixture.Host the
// resolver builds from the blueprint declaration (the ec2/rds precedent).
package host

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/nodeexp"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key for this construct.
const Kind = "host"

// tickCadence is the metric lane cadence (the DPM floor, I10).
const tickCadence = 60 * time.Second

// Construct is one host-instance emitter. The host topology is fixed at Build time
// (deterministic from seed + fixture.Host); per-tick dynamics come from the shape engine
// and `now`, so the only cross-tick mutable state is the cumulative counters in st.
type Construct struct {
	h    *fixture.Host
	seed string
	st   *state.State
}

// Build validates cfg (*Config), requires a non-nil fixture.Host, and captures the host +
// seed into the construct with a fresh cumulative state.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	if _, ok := cfg.(*Config); !ok {
		return nil, fmt.Errorf("host: Build called with %T, want *Config", cfg)
	}
	if fx == nil || fx.Host == nil {
		return nil, fmt.Errorf("host: Build requires fixture.Set.Host (the resolved host declaration)")
	}
	return &Construct{
		h:    fx.Host,
		seed: fx.Seed,
		st:   state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *Construct) Interval() time.Duration     { return tickCadence }

// Tick renders one 60s cycle: the OS exporter series (+ optional Docker cadvisor lane)
// accumulate into c.st via the nodeexp emitters, then the collected batch is written once.
// The emitters own their own `up` series (keyed by `job`) — the construct never emits up.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	// Hosts have no env binding → fixed prod-like diurnal weight.
	factor := 1.0
	if w.Shape != nil {
		factor = w.Shape.Factor(now, 1.0, false)
	}
	const tickSec = 60.0
	const scale = tickSec / 30.0

	top := toTopology(c.seed, c.h)
	base := baseLabels(c.h)
	prof := profileOf(c.h)

	switch c.h.OS {
	case "windows":
		nodeexp.EmitWindows(c.st, base, top, prof, factor, tickSec, scale, w.Shape)
	case "darwin", "macos":
		nodeexp.EmitMacOS(c.st, base, top, prof, factor, tickSec, scale, w.Shape)
	default: // linux
		nodeexp.EmitLinux(c.st, base, top, prof, factor, tickSec, scale, w.Shape)
	}

	if c.h.Docker {
		db := dockerBase(c.h)
		for _, ct := range dockerContainers(c.seed) {
			nodeexp.EmitContainer(c.st, db, ct, nodeexp.CadvisorDocker, factor, tickSec, scale, w.Shape)
		}
		nodeexp.EmitMachine(c.st, db, top.MemTotal, nodeexp.CadvisorDocker)
	}

	series := c.st.Collect(now)
	if len(series) > 0 && w.Metrics != nil {
		if err := w.Metrics.Write(ctx, series); err != nil {
			return fmt.Errorf("host: metrics write: %w", err)
		}
	}

	// Log streams (journal/winevent/macos/docker) — built by buildLogs in logs.go.
	if streams := buildLogs(c.h, c.seed, now, w.Shape); len(streams) > 0 && w.Logs != nil {
		if err := w.Logs.Write(ctx, streams); err != nil {
			return fmt.Errorf("host: logs write: %w", err)
		}
	}
	return nil
}
