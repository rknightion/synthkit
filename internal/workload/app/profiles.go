// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/pyroscope"
	pprofpb "github.com/rknightion/synthkit/internal/pyroscope/pprofpb"
	psink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// maxSpanProfileIDs is the maximum number of span IDs folded into a BuildWithSpans call
// per tick. Mirrors webservice's limit; span labels bloat the pprof string table.
const maxSpanProfileIDs = 20

// spanIDForNode resolves the span ID that best represents nodeName in request r.
//
// Logic:
//   - If nodeName is the entry node, return r.SpanID (the root server span).
//   - Otherwise scan r.Calls for a hop whose Target == nodeName:
//   - If the node emits its own server span (hasServerSpan) and the call has a PeerSpanID,
//     return the PeerSpanID (the callee's SERVER span — the connected trace node).
//   - Otherwise return the CLIENT span (Call.SpanID).
//   - If no matching call is found, fall back to r.SpanID.
func spanIDForNode(r *ledger.Request, nodeName, entryName string, hasServerSpan bool) string {
	if nodeName == entryName {
		return r.SpanID
	}
	for _, c := range r.Calls {
		if c.Target == nodeName {
			if hasServerSpan && c.PeerSpanID != "" {
				return c.PeerSpanID
			}
			return c.SpanID
		}
	}
	return r.SpanID
}

// tickProfiles is the per-node Pyroscope SDK-push profiling lane. It is driven by the
// same 60-second metric cadence (NOT ProjectBatch). For each node that declares a
// pyroscope block in sdk mode, it builds pprof Profile protos and writes them to
// world.Pyroscope in one batched Write call.
//
// It is a no-op when world.Pyroscope is nil.
func (w *Workload) tickProfiles(ctx context.Context, now time.Time, world *core.World) {
	if world.Pyroscope == nil {
		return
	}

	entryName := ""
	if w.graph.entry != nil {
		entryName = w.graph.entry.decl.Name
	}

	// Pre-fetch active requests once if ANY node uses span profiles.
	var reqs []*ledger.Request
	for _, n := range w.graph.nodes {
		if n.decl.Pyroscope != nil && n.decl.Pyroscope.Enabled && n.decl.Pyroscope.SpanProfiles {
			if world.Ledger != nil {
				reqs = activeRequests(w, now, world)
			}
			break
		}
	}

	factor := world.Shape.Factor(now, w.weight, w.nonProd)

	var series []psink.Series

	for _, n := range w.graph.nodes {
		cfg := n.decl.Pyroscope
		if cfg == nil || !cfg.Enabled {
			continue
		}
		if cfg.ModeOrDefault() == "scraped" {
			continue
		}

		// Resolve runtime: node's own config > node's declared Runtime > default "go".
		runtime := cfg.Runtime
		if runtime == "" {
			runtime = n.decl.Runtime
		}
		if runtime == "" {
			runtime = "go"
		}

		// Determine which profile types to emit: SDK-push view (distinct from the Alloy
		// collector view returned by RuntimeTypes — SDKRuntimeTypes omits async-profiler
		// types that are only produced by the Alloy pyroscope.java collector).
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
			continue
		}

		// Build per-node failure-mode intensities for Load.Modes.
		// AxisService scope = node name (mirrors minter.go's shape.Eval(now, mode, n)).
		nodeModes := map[string]float64{}
		for _, modeName := range []string{"cpu_hotspot", "memory_leak", "lock_contention", "goroutine_leak"} {
			if _, inten := world.Shape.Eval(now, modeName, n.decl.Name); inten > 0 {
				nodeModes[modeName] = inten
			}
		}
		load := pyroscope.Load{
			Factor: factor,
			Now:    now,
			Modes:  nodeModes,
		}

		id := w.identity(n)
		builder := pyroscope.NewBuilder(runtime, id.service)

		// Collect per-node span IDs when span profiles are enabled.
		var spanIDs []string
		if cfg.SpanProfiles && len(reqs) > 0 {
			spanIDs = nodeSpanIDs(reqs, n.decl.Name, entryName, n.kind.serverSpan)
		}

		for _, pt := range types {
			// Span profiles ride ONLY on the CPU profile: the Go SDK propagates span context via
			// runtime/pprof.SetGoroutineLabels, which is captured solely by process_cpu ("Only CPU
			// profiling is supported", Pyroscope Go span-profiles docs). Labelling memory/block/
			// mutex/goroutines would fabricate a span-profile shape no real Go SDK produces.
			var prof *pprofpb.Profile
			if len(spanIDs) > 0 && pt.Name == "process_cpu" {
				prof = builder.BuildWithSpans(pt, load, spanIDs)
			} else {
				prof = builder.Build(pt, load)
			}

			labels := profileLabelsApp(pt, id.service, id.env, runtime)
			series = append(series, psink.Series{
				Labels:  labels,
				Profile: prof,
			})
		}
	}

	if len(series) > 0 {
		_ = world.Pyroscope.Write(ctx, series)
	}
}

// activeRequests fetches the active requests for this workload, newest-first, capped at
// maxSpanProfileIDs.
func activeRequests(w *Workload, now time.Time, world *core.World) []*ledger.Request {
	reqs := world.Ledger.ActiveFor(w.Name(), now, interval)
	// Sort newest-first (ActiveFor returns ring order, oldest-first).
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Start.After(reqs[j].Start) })
	if len(reqs) > maxSpanProfileIDs {
		reqs = reqs[:maxSpanProfileIDs]
	}
	return reqs
}

// nodeSpanIDs collects non-empty, deduped span IDs for nodeName from the active requests.
func nodeSpanIDs(reqs []*ledger.Request, nodeName, entryName string, hasServerSpan bool) []string {
	seen := make(map[string]bool, len(reqs))
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		sid := spanIDForNode(r, nodeName, entryName, hasServerSpan)
		if sid != "" && !seen[sid] {
			seen[sid] = true
			out = append(out, sid)
		}
	}
	return out
}

// profileLabelsApp builds the SDK-push Pyroscope label set for one profile type emission
// from an app service node. Label shape is branched by runtime — mirrors webservice.profileLabels.
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
func profileLabelsApp(pt pyroscope.ProfileType, serviceName, env, runtime string) []psink.LabelPair {
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
