// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/pyroscope"
	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"
	psink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// maxSpanProfileIDs is the maximum number of span IDs folded into a BuildWithSpans call
// per tick. Mirrors sampleBudget's spirit but kept smaller: span labels bloat the pprof
// string table so we cap tightly.
const maxSpanProfileIDs = 20

// tickProfiles is the Pyroscope SDK-push profiling lane. It is driven by the same 60-second
// metric cadence (NOT ProjectBatch). It is a no-op when:
//   - world.Pyroscope is nil (runner did not wire a Pyroscope sink — e.g. PyroscopeProfiles
//     not in Signals() or scraped mode)
//   - cfg.Pyroscope is nil or not enabled
func (w *Workload) tickProfiles(ctx context.Context, now time.Time, world *core.World) {
	if world.Pyroscope == nil {
		return
	}
	if w.cfg.Pyroscope == nil || !w.cfg.Pyroscope.Enabled {
		return
	}

	cfg := w.cfg.Pyroscope

	// Resolve runtime; default "go" when absent.
	runtime := cfg.Runtime
	if runtime == "" {
		runtime = "go"
	}

	// Determine which profile types to emit: SDK-push view (distinct from the Alloy collector
	// view returned by RuntimeTypes — SDKRuntimeTypes omits async-profiler types that are only
	// produced by the Alloy pyroscope.java collector, not by any SDK push client).
	types := pyroscope.SDKRuntimeTypes(runtime)
	if len(cfg.Types) > 0 {
		wanted := make(map[string]bool, len(cfg.Types))
		for _, t := range cfg.Types {
			wanted[t] = true
		}
		filtered := types[:0]
		for _, pt := range types {
			if wanted[pt.Name] {
				filtered = append(filtered, pt)
			}
		}
		types = filtered
	}
	if len(types) == 0 {
		return
	}

	// Load.Factor: use the same diurnal×weekly shape factor the metric lane uses for this
	// workload (world.Shape.Factor(now, weight, nonProd)). A factor of 1.0 at peak, lower
	// during troughs — so profile sample weights scale with traffic intensity.
	factor := world.Shape.Factor(now, w.weight, w.nonProd)

	// Build failure-mode intensities for Load.Modes.
	// AxisWorkload scope = workload name (mirrors minter.go's shape.Eval(now, mode, m.name)).
	modes := map[string]float64{}
	for _, modeName := range []string{"cpu_hotspot", "memory_leak", "lock_contention", "goroutine_leak"} {
		if _, inten := world.Shape.Eval(now, modeName, w.name); inten > 0 {
			modes[modeName] = inten
		}
	}

	load := pyroscope.Load{
		Factor: factor,
		Now:    now,
		Modes:  modes,
	}

	// Optionally collect span IDs from the active ledger window for span profiles.
	var spanIDs []string
	if cfg.SpanProfiles && world.Ledger != nil {
		spanIDs = collectSpanIDs(w, now, world)
	}

	// ONE builder per tick (cache is per-builder, reused across types in this tick).
	builder := pyroscope.NewBuilder(runtime, w.name)

	series := make([]psink.Series, 0, len(types))
	for _, pt := range types {
		// Span profiles ride ONLY on the CPU profile: the Go SDK propagates span context via
		// runtime/pprof.SetGoroutineLabels, which is captured solely by process_cpu ("Only CPU
		// profiling is supported", Pyroscope Go span-profiles docs). Labelling memory/block/mutex/
		// goroutines would fabricate a span-profile shape no real Go SDK produces.
		var prof *pprofpb.Profile
		if len(spanIDs) > 0 && pt.Name == "process_cpu" {
			prof = builder.BuildWithSpans(pt, load, spanIDs)
		} else {
			prof = builder.Build(pt, load)
		}

		labels := profileLabels(pt, w.name, w.env, runtime)
		series = append(series, psink.Series{
			Labels:  labels,
			Profile: prof,
		})
	}

	// Best-effort: ignore write errors (mirrors the metric lane's error propagation model
	// where world.Metrics.Write is the terminal step — here the caller (Tick) already
	// returns after the metric write; profile errors are non-fatal).
	_ = world.Pyroscope.Write(ctx, series)
}

// collectSpanIDs collects non-empty SpanIDs from the active ledger window for this workload,
// newest-first, capped at maxSpanProfileIDs. Mirrors sampleRequests but takes SpanID not TraceID.
func collectSpanIDs(w *Workload, now time.Time, world *core.World) []string {
	type spanEntry struct {
		spanID string
		start  time.Time
	}
	reqs := world.Ledger.ActiveFor(w.name, now, interval)
	entries := make([]spanEntry, 0, len(reqs))
	for _, r := range reqs {
		if r.SpanID == "" {
			continue
		}
		entries = append(entries, spanEntry{spanID: r.SpanID, start: r.Start})
	}
	// Newest-first (ActiveFor returns ring order, oldest-first).
	sort.Slice(entries, func(i, j int) bool { return entries[i].start.After(entries[j].start) })
	if len(entries) > maxSpanProfileIDs {
		entries = entries[:maxSpanProfileIDs]
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.spanID
	}
	return out
}

// profileLabels builds the SDK-push Pyroscope label set for one profile type emission.
// Label shape is branched by runtime to match real SDK-push shapes:
//
//   - go:     service_name, env (omit if ""), version, pyroscope_spy=gospy
//     (signals/profiles.md [slug: profiles-sdk-go])
//   - python: service_name, language=python, env (omit if "")
//     NO version, NO pyroscope_spy (pyroscope-io 1.0.11 reality)
//     (signals/profiles.md [slug: profiles-sdk-python])
//   - jvm/node/dotnet/other: service_name, env (omit if "")
//     NO version, NO spy, NO language (UNCAPTURED — conservative)
//
// Always includes __name__ and __profile_type__.
// Do NOT add source label here — its absence is the SDK-push discriminator.
// Do NOT add the blueprint label here — stampedProfiles adds it via the scoped writer.
func profileLabels(pt pyroscope.ProfileType, serviceName, env, runtime string) []psink.LabelPair {
	labels := []psink.LabelPair{
		{Name: "__name__", Value: pt.Name},
		{Name: "__profile_type__", Value: pt.Selector()},
		{Name: "service_name", Value: serviceName},
	}
	switch runtime {
	case "go":
		if env != "" {
			labels = append(labels, psink.LabelPair{Name: "env", Value: env})
		}
		labels = append(labels, psink.LabelPair{Name: "version", Value: serviceVersion})
		labels = append(labels, psink.LabelPair{Name: "pyroscope_spy", Value: "gospy"})
	case "python":
		labels = append(labels, psink.LabelPair{Name: "language", Value: "python"})
		if env != "" {
			labels = append(labels, psink.LabelPair{Name: "env", Value: env})
		}
		// NO version, NO pyroscope_spy — real pyroscope-io 1.0.11 Python SDK omits both.
	default:
		// jvm, node, dotnet, unknown — UNCAPTURED: conservative minimal set.
		if env != "" {
			labels = append(labels, psink.LabelPair{Name: "env", Value: env})
		}
		// NO version, NO pyroscope_spy, NO language.
	}
	return labels
}
