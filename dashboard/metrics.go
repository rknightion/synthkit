// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// MetricsOptions customizes the generated metrics dashboard. Zero value = default: overall
// aggregation, p50/p95/p99, units via unitFor. All maps keyed by metric family base name.
type MetricsOptions struct {
	GroupBy      map[string][]string // family → breakdown dims; absent = aggregate overall
	Quantiles    []float64           // histogram quantiles; nil = {0.5, 0.95, 0.99}
	UnitByFamily map[string]string   // family → Grafana unit override; absent = derived
	Exclude      []string            // family names to skip entirely
}

// PanelSpec is one built panel + its layout reference id. Returned by SignalPanels so a bespoke
// template can cherry-pick / reorder / re-tab panels instead of taking MetricsDashboard.
type PanelSpec struct {
	ID    string
	Panel *dashboardv2.PanelBuilder
}

func (o MetricsOptions) quantiles() []float64 {
	if len(o.Quantiles) == 0 {
		return []float64{0.5, 0.95, 0.99}
	}
	return o.Quantiles
}

func (o MetricsOptions) excluded(name string) bool {
	for _, e := range o.Exclude {
		if e == name {
			return true
		}
	}
	return false
}

func (o MetricsOptions) unit(name string, inst InstrumentKind) string {
	if u, ok := o.UnitByFamily[name]; ok {
		return u
	}
	return unitFor(name, inst)
}

// groupFor returns the requested breakdown dims intersected with the signal's actual label
// keys; unknown dims are dropped + logged so a typo can't yield an empty-result panel. The
// returned slice preserves the caller's GroupBy order, not LabelKeys order.
func (o MetricsOptions) groupFor(sig MetricSignal) []string {
	req := o.GroupBy[sig.Name]
	if len(req) == 0 {
		return nil
	}
	valid := map[string]struct{}{}
	for _, k := range sig.LabelKeys {
		valid[k] = struct{}{}
	}
	var out []string
	for _, d := range req {
		if _, ok := valid[d]; ok {
			out = append(out, d)
		} else {
			log.Printf("[dashboard] metrics: dropping unknown groupBy dim %q for %q (not in its label set)", d, sig.Name)
		}
	}
	return out
}

// groupLegend joins the breakdown dims into Grafana legend tokens (`{{dim}} {{dim2}}`),
// or "" when there is no grouping.
func groupLegend(group []string) string {
	if len(group) == 0 {
		return ""
	}
	parts := make([]string, len(group))
	for i, g := range group {
		parts[i] = "{{" + g + "}}"
	}
	return strings.Join(parts, " ")
}

// seriesLegend is the per-series legend for non-histogram panels: the metric name when
// aggregating overall, or the breakdown dims when grouped.
func seriesLegend(name string, group []string) string {
	if len(group) == 0 {
		return name
	}
	return groupLegend(group)
}

// SignalPanels builds the panel(s) for one signal under opts.
func SignalPanels(sig MetricSignal, label string, opts MetricsOptions) []PanelSpec {
	sel := Selector(sig, label, nil)
	group := opts.groupFor(sig)
	unit := opts.unit(sig.Name, sig.Instrument)
	switch sig.Instrument {
	case HistogramNative:
		out := []PanelSpec{{"m-" + sig.Name + "-native", TimeseriesPanel(sig.Name+" (native)", unit, quantileTargets(true, sig.Name, sel, opts.quantiles(), group)...)}}
		if sig.Dual {
			out = append(out, PanelSpec{"m-" + sig.Name + "-classic", TimeseriesPanel(sig.Name+" (classic)", unit, quantileTargets(false, sig.Name, sel, opts.quantiles(), group)...)})
		}
		return out
	case HistogramClassic:
		return []PanelSpec{{"m-" + sig.Name, TimeseriesPanel(sig.Name+" latency", unit, quantileTargets(false, sig.Name, sel, opts.quantiles(), group)...)}}
	case Counter:
		return []PanelSpec{{"m-" + sig.Name, TimeseriesPanel(sig.Name+" rate", unit, PromTarget(RateExpr(sig.Name, sel, group), seriesLegend(sig.Name, group)))}}
	default: // Gauge
		return []PanelSpec{{"m-" + sig.Name, TimeseriesPanel(sig.Name, unit, PromTarget(GaugeExpr(sig.Name, sel, group), seriesLegend(sig.Name, group)))}}
	}
}

func quantileTargets(native bool, name, sel string, quantiles []float64, group []string) []*dashboardv2.TargetBuilder {
	ts := make([]*dashboardv2.TargetBuilder, 0, len(quantiles))
	for i, q := range quantiles {
		var expr string
		if native {
			expr = NativeHistogramQuantile(q, name, sel, group)
		} else {
			expr = ClassicHistogramQuantile(q, name, sel, group)
		}
		legend := fmt.Sprintf("p%g", q*100)
		if len(group) > 0 {
			legend = groupLegend(group) + " " + legend
		}
		refID := fmt.Sprintf("%c", 'A'+i)
		if i >= 26 {
			refID = fmt.Sprintf("Q%d", i)
		}
		ts = append(ts, PromTarget(expr, legend).RefId(refID))
	}
	return ts
}

// MetricsDashboard builds the per-blueprint metrics dashboard: query panels per classified
// MetricSignal, grouped into tabs by instrument kind. Mirrors IndexDashboard's assembly.
func MetricsDashboard(m *Manifest, opts ...MetricsOptions) (Dashboard, error) {
	var o MetricsOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	uid := m.Blueprint + "-metrics"
	title := m.Blueprint + " — metrics"
	d, err := NewDashboard(uid, title)
	if err != nil {
		return Dashboard{}, err
	}
	AddPanel(&d, "metrics-header", TextPanel(title, fmt.Sprintf("# %s — generated metrics\n\nOne panel per emitted metric family, classified from the dry-run inventory. Histograms show the configured quantiles; dual families render both a native and a classic panel.", m.Blueprint)))

	sigs := append([]MetricSignal(nil), m.Metrics...)
	sort.Slice(sigs, func(i, j int) bool { return sigs[i].Name < sigs[j].Name })

	var histo, counters, gauges []string
	for _, sig := range sigs {
		if o.excluded(sig.Name) {
			continue
		}
		for _, ps := range SignalPanels(sig, m.Label, o) {
			AddPanel(&d, ps.ID, ps.Panel)
			switch sig.Instrument {
			case HistogramClassic, HistogramNative:
				histo = append(histo, ps.ID)
			case Counter:
				counters = append(counters, ps.ID)
			default:
				gauges = append(gauges, ps.ID)
			}
		}
	}
	WithTabs(&d, buildTabs(histo, counters, gauges)...)
	if len(m.Environments) > 0 {
		d.Builder.CustomVariable(EnvVar(m))
	}
	return d, nil
}

func buildTabs(histo, counters, gauges []string) []TabSpec {
	var tabs []TabSpec
	first := []string{"metrics-header"}
	addTab := func(title string, ids []string) {
		if len(ids) == 0 {
			return
		}
		if first != nil {
			ids = append(append([]string{}, first...), ids...)
			first = nil
		}
		tabs = append(tabs, Tab(title, ids...))
	}
	addTab("Histograms", histo)
	addTab("Counters", counters)
	addTab("Gauges", gauges)
	if first != nil {
		// No panels were added — emit a lone header tab
		tabs = append(tabs, Tab("Metrics", "metrics-header"))
	}
	return tabs
}
