// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"

	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// isDerived reports whether a metric reads another metric on its node (stock_of / count_of)
// instead of evaluating only its own value model in declaration order.
func isDerived(spec telemetryspec.MetricSpec) bool {
	return spec.StockOf != nil || spec.CountOf != ""
}

// incKey keys a counter's per-tick increment by name and DSL label combination (node-local).
func incKey(name string, dsl map[string]string) string {
	return name + "\x00" + state.LabelSig(dsl)
}

// carryCount converts a fractional per-tick observation rate into a whole count, carrying the
// remainder per series so the long-run count equals the rate exactly. No RNG is consumed.
func (w *Workload) carryCount(name string, labels map[string]string, rate float64) int {
	if w.carry == nil {
		w.carry = make(map[string]float64)
	}
	key := name + "\x00" + state.LabelSig(labels)
	prev, seen := w.carry[key]
	if !seen {
		prev = 0.5 // round half up from the first tick: rate 0.5 observes on ticks 1, 3, 5, ...
	}
	c := prev + rate
	if c < 0 {
		c = 0
	}
	n := math.Floor(c)
	w.carry[key] = c - n
	return int(n)
}

// nodeHasDerived reports whether a node declares any stock_of / count_of spec, so the
// interpreter only records counter increments where something reads them.
func (n *node) hasDerived() bool {
	for _, spec := range n.metrics {
		if isDerived(spec) {
			return true
		}
	}
	return false
}

// observeDerived evaluates one stock_of gauge or count_of histogram after the node's ordinary
// specs, from the counter increments recorded this tick (incs). dt is the elapsed tick in
// seconds. paused steps stocks with zero inflow and observes no histogram values.
func (w *Workload) observeDerived(st, native *state.State, id nodeIdentity, n *node, spec telemetryspec.MetricSpec, ctx telemetryspec.EvalCtx, incs map[string]float64, dt float64, paused bool) {
	base := id.metricBaseLabels()
	switch {
	case spec.StockOf != nil:
		src := n.counterSpec(spec.StockOf.Metric)
		if src == nil {
			return // unreachable: buildGraph rejects a missing source counter
		}
		dwell := spec.StockOf.DwellSeconds
		if w.stock == nil {
			w.stock = make(map[string]float64)
		}
		for _, combo := range scaledLabelCombos(src.Labels) {
			var inc float64
			if !paused {
				inc = incs[incKey(src.Name, combo.labels)]
			}
			target := inc / dt * dwell
			labels := mergeLabels(base, combo.labels)
			// Key by the full series labels: the node identity keeps two nodes declaring the
			// same stock gauge name independent.
			key := spec.Name + "\x00" + state.LabelSig(labels)
			s, seen := w.stock[key]
			if !seen {
				s = target
			} else {
				s = target + (s-target)*math.Exp(-dt/dwell)
			}
			w.stock[key] = s
			if st != nil {
				st.Set(spec.Name, labels, s)
			}
			if native != nil {
				native.Set(spec.Name, combo.labels, s)
			}
		}
	case spec.CountOf != "":
		if paused {
			return
		}
		src := n.counterSpec(spec.CountOf)
		if src == nil {
			return // unreachable: buildGraph rejects a missing source counter
		}
		srcCombos := labelCombos(src.Labels)
		for _, combo := range scaledLabelCombos(spec.Labels) {
			labels := mergeLabels(base, combo.labels)
			// The histogram series observes the counter increments of every series that
			// projects onto it: one series when the labels are equal, a sum for a label subset.
			var rate float64
			for _, c := range srcCombos {
				if projects(combo.labels, c) {
					rate += incs[incKey(src.Name, c)]
				}
			}
			k := min(w.carryCount(spec.Name, labels, rate), telemetryspec.MaxObservationsPerTick)
			for range k {
				val, _ := spec.Value.Eval(ctx)
				w.observeHistogram(st, native, spec, labels, combo.labels, val*combo.value)
			}
		}
	}
}

// counterSpec returns the node's counter spec with the given name, or nil.
func (n *node) counterSpec(name string) *telemetryspec.MetricSpec {
	for i := range n.metrics {
		if n.metrics[i].Name == name && n.metrics[i].Instrument == telemetryspec.InstrumentCounter && !isDerived(n.metrics[i]) {
			return &n.metrics[i]
		}
	}
	return nil
}
