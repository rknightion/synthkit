// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// projectCardinality builds a throwaway dry runner, executes one tick against the
// resolved blueprint, and returns the projected distinct-series count.
//
// The runner is created with all three mandatory sinks set to dry-run mode (no
// network I/O). RunOnce→MasterTick→startQueues starts sender goroutines for each
// queue shard; those goroutines only exit when their queue is Drained. The deferred
// DrainQueues call ensures every sender goroutine exits before this function returns,
// preventing goroutine accumulation when called repeatedly (e.g. on every Validate
// request in a long-lived server).
//
// Returns (-1, true) if AddBlueprint or RunOnce fails — callers treat that as
// "estimate unavailable".
func projectCardinality(reg *core.Registry, res *blueprint.Resolved) (count int, estimated bool) {
	// Quiet dry sinks: record inventory but suppress the per-push "[dry-run …]" log lines, so a
	// Validate/save click doesn't spew this throwaway runner's inventory into the live process log.
	metrics := promrw.New("", "", "", true, func() int { return 0 })
	metrics.Quiet = true
	logs := loki.New("", "", "", true)
	logs.Quiet = true
	traces := otlp.New("", "", "", true)
	traces.Quiet = true
	sinks := runner.Sinks{Metrics: metrics, Logs: logs, Traces: traces, OTLPMetrics: otlp.NewMetrics("", "", "", true)}
	r := runner.New(sinks, reg, runner.Options{MasterTick: time.Second})

	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.DrainQueues(dctx)
	}()

	if err := r.AddBlueprint(res); err != nil {
		return -1, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.RunOnce(ctx, time.Now()); err != nil {
		return -1, true
	}
	return int(r.Inventory().Totals.DistinctSeries), false
}

// ProjectCardinality projects one full dry cycle without credentials or persisted state.
// Counts cover the metric series observed in that cycle, not a long-term upper bound.
func ProjectCardinality(reg *core.Registry, res *blueprint.Resolved) (int, bool) {
	return projectCardinality(reg, res)
}

// ResolveOffline reads the same selected built-in, upload and fetched git snapshots as
// Resolve, but never secures paths, writes a manifest or updates source status. It never
// fetches remote sources. Use a fresh Manager for each offline validation.
func (m *Manager) ResolveOffline() ([]Loaded, []Diag) {
	m.available = map[string]struct{}{}
	load := func(dir string, prov Provenance, source string, optional bool, nsFor func(string) (string, bool)) ([]Loaded, []Diag) {
		if _, err := os.ReadDir(dir); err != nil {
			if optional && os.IsNotExist(err) {
				return nil, nil
			}
			return nil, []Diag{{"error", source, "read", err.Error()}}
		}
		return m.loadDir(dir, prov, source, true, nsFor)
	}
	loaded, diags := load(m.bakedDir, ProvBuiltin, "builtin", false, func(fn string) (string, bool) {
		return "", filepath.Ext(fn) == ".yaml"
	})
	custom, cd := load(filepath.Join(m.dataDir, customDir), ProvUpload, "custom", true, func(fn string) (string, bool) {
		ns, _, ok := parseUploadFilename(fn)
		return ns, ok
	})
	loaded = append(loaded, custom...)
	diags = append(diags, cd...)
	for _, source := range m.sourceSnapshot() {
		if source.FetchedSHA == "" {
			continue
		}
		if err := ValidateSource(source); err != nil {
			diags = append(diags, Diag{"error", source.ID, "source", err.Error()})
			continue
		}
		git, gd := load(filepath.Join(m.dataDir, gitDir, source.ID), ProvGit, source.ID, false, func(fn string) (string, bool) {
			return source.Namespace, filepath.Ext(fn) == ".yaml"
		})
		loaded = append(loaded, git...)
		diags = append(diags, gd...)
	}
	return loaded, diags
}
