// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
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
	sinks := runner.Sinks{Metrics: metrics, Logs: logs, Traces: traces}
	r := runner.New(sinks, reg, runner.Options{MasterTick: time.Second})

	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.DrainQueues(dctx)
	}()

	if err := r.AddBlueprint(res); err != nil {
		return -1, true
	}
	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		return -1, true
	}
	return int(r.Inventory().Totals.DistinctSeries), false
}
