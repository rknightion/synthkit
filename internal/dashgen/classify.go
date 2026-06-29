// SPDX-License-Identifier: AGPL-3.0-only

// Package dashgen is the dashboard generator engine: it resolves a blueprint, runs ONE
// dry cycle through the runner, and turns the sink inventories into a dashboard.Manifest.
// Private — only cmd/synthkit-dash uses it. It imports the synthetic stack (blueprint,
// runner, sinks, cw) but the synthetic stack never imports it (archtest-guarded).
package dashgen

import (
	"sort"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// distinctiveCWSuffixes are the CloudWatch stat suffixes that UNAMBIGUOUSLY mark a CW
// stat-set — NOT _sum/_count, which classic histograms also use. cw.EmitStats always
// emits all five stats, so a CW base always carries at least one distinctive suffix.
var distinctiveCWSuffixes = []string{"_average", "_maximum", "_minimum", "_sample_count"}

// classicComponentSuffixes are the three series a hand-mangled classic histogram emits.
var classicComponentSuffixes = []string{"_bucket", "_sum", "_count"}

// ClassifyMetrics turns the raw promrw inventory (name → label keys) + the authoritative
// per-series instrument kinds + the native-histogram name set into classified, family-grouped
// MetricSignals. Two passes:
// (1) identify family bases by NAME — a base with a _bucket series is a CLASSIC histogram;
// a base with a distinctive CloudWatch stat suffix is a per-period GAUGE stat-set (the five
// stats fold to one base). (2) fold each series into its base; standalone series take their
// instrument from the AUTHORITATIVE kind (state's Add/Set/Observe origin) — so summaries
// (_sum/_count, no _bucket) and _count-suffixed counters are correctly COUNTER, not guessed
// gauges. A base that appears in natives AND has classic _bucket components is Dual — the
// family emits both native and classic forms; the signal is classified HistogramNative.
// Scope is ScopeBlueprint iff the series carries the "blueprint" selector label
// (added by the runner's scoped writer BEFORE record).
func ClassifyMetrics(inv map[string][]string, kinds map[string]promrw.Kind, natives map[string]bool) []dashboard.MetricSignal {
	// Pass 1 — identify family bases.
	classicBase := map[string]bool{}
	cwBase := map[string]bool{}
	for name := range inv {
		if b, ok := strings.CutSuffix(name, "_bucket"); ok {
			classicBase[b] = true
		}
		for _, suf := range distinctiveCWSuffixes {
			if b, ok := strings.CutSuffix(name, suf); ok {
				cwBase[b] = true
			}
		}
	}

	// histoKind returns HistogramNative for bases that appear in the native inventory,
	// HistogramClassic otherwise. It is a PURE function of base, so the classic-component
	// arm and the default/native arm of Pass 2 always agree on the kind for a given base —
	// no fold-order conflict in get's set-on-first-create (whichever series lands first sets
	// the same kind the other would have).
	histoKind := func(base string) dashboard.InstrumentKind {
		if natives[base] {
			return dashboard.HistogramNative
		}
		return dashboard.HistogramClassic
	}

	type acc struct {
		kind   dashboard.InstrumentKind
		keys   map[string]struct{}
		scoped bool
	}
	bases := map[string]*acc{}
	get := func(base string, kind dashboard.InstrumentKind) *acc {
		a := bases[base]
		if a == nil {
			a = &acc{kind: kind, keys: map[string]struct{}{}}
			bases[base] = a
		}
		return a
	}
	add := func(a *acc, keys []string, dropLE bool) {
		for _, k := range keys {
			if dropLE && k == "le" {
				continue
			}
			if k == runner.BlueprintLabel {
				a.scoped = true
				continue // the selector is consumed into Scope, not a query dimension
			}
			a.keys[k] = struct{}{}
		}
	}

	// Pass 2 — fold each series into its base. Histogram + CW families group by NAME;
	// every other (standalone) series takes its instrument from the authoritative kind.
	// Precedence edge: a bare series whose name also matches a CloudWatch stat suffix
	// (e.g. foo_average) hits the cwComponent arm before the natives[name] check in default,
	// folding as Gauge. Accepted — no native+CW-named family exists today.
	for name, keys := range inv {
		switch {
		case classicComponent(name, classicBase):
			base := classicBaseOf(name)
			add(get(base, histoKind(base)), keys, true)
		case cwComponent(name, cwBase):
			add(get(cwBaseOf(name, cwBase), dashboard.Gauge), keys, false)
		default:
			if natives[name] {
				add(get(name, histoKind(name)), keys, false)
			} else {
				add(get(name, instrumentFor(kinds[name])), keys, false)
			}
		}
	}

	out := make([]dashboard.MetricSignal, 0, len(bases))
	for base, a := range bases {
		keys := make([]string, 0, len(a.keys))
		for k := range a.keys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		scope := dashboard.ScopeSubstrate
		if a.scoped {
			scope = dashboard.ScopeBlueprint
		}
		out = append(out, dashboard.MetricSignal{
			Name: base, Instrument: a.kind, LabelKeys: keys, Scope: scope,
			Dual: natives[base] && classicBase[base],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// classicComponent reports whether name is the _bucket/_sum/_count of a known classic base.
func classicComponent(name string, classicBase map[string]bool) bool {
	for _, suf := range classicComponentSuffixes {
		if b, ok := strings.CutSuffix(name, suf); ok && classicBase[b] {
			return true
		}
	}
	return false
}

func classicBaseOf(name string) string {
	for _, suf := range classicComponentSuffixes {
		if b, ok := strings.CutSuffix(name, suf); ok {
			return b
		}
	}
	return name
}

// cwComponent reports whether name ends with a CW stat suffix of a known CW base.
func cwComponent(name string, cwBase map[string]bool) bool {
	for _, suf := range cw.StatSuffixes {
		if b, ok := strings.CutSuffix(name, suf); ok && cwBase[b] {
			return true
		}
	}
	return false
}

func cwBaseOf(name string, cwBase map[string]bool) string {
	for _, suf := range cw.StatSuffixes {
		if b, ok := strings.CutSuffix(name, suf); ok && cwBase[b] {
			return b
		}
	}
	return name
}

// instrumentFor maps the authoritative promrw kind to the dashboard instrument kind. The
// zero value (KindGauge) is the conservative default for any series the inventory didn't
// kind-tag (none on the real emit path — every series flows through state.Collect).
func instrumentFor(k promrw.Kind) dashboard.InstrumentKind {
	switch k {
	case promrw.KindCounter:
		return dashboard.Counter
	case promrw.KindHistogram:
		return dashboard.HistogramClassic
	default:
		return dashboard.Gauge
	}
}
