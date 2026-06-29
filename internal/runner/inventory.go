// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"hash/maphash"
	"sort"
	"sync"

	"github.com/rknightion/synthkit/internal/control"
)

// maxSigsPerConstruct caps the distinct-series signature set per construct so a pathological emitter
// can't grow it unbounded. Names/label-keys are structurally tiny and uncapped. The wire is already
// backstopped by SERIES_CAP; this just bounds the X-ray bookkeeping.
const maxSigsPerConstruct = 200_000

// constructInv is one instance's private, internal-only emission inventory. It is held by that
// instance's writers (one writer set per construct, ticked on one goroutine), so its mutex sees
// effectively no cross-construct contention. NOTHING here is ever stamped on the wire.
type constructInv struct {
	bp, kind, name string

	mu           sync.Mutex
	seed         maphash.Seed
	sigs         map[uint64]struct{} // distinct metric-series signatures (hashed)
	capped       bool
	metricNames  map[string]struct{}
	metricLabels map[string]struct{}
	logSources   map[string]struct{}
	logLabelKeys map[string]struct{}
	spanServices map[string]struct{}
	spanNames    map[string]struct{}
	spanAttrKeys map[string]struct{}
}

func newConstructInv(bp, kind, name string) *constructInv {
	return &constructInv{
		bp: bp, kind: kind, name: name, seed: maphash.MakeSeed(),
		sigs: map[uint64]struct{}{}, metricNames: map[string]struct{}{}, metricLabels: map[string]struct{}{},
		logSources: map[string]struct{}{}, logLabelKeys: map[string]struct{}{},
		spanServices: map[string]struct{}{}, spanNames: map[string]struct{}{}, spanAttrKeys: map[string]struct{}{},
	}
}

// recordMetric folds one emitted series (PRE-stamp; label values hashed but never stored) into the inventory.
// labelPairs must be sorted by key for a stable signature; each entry is [key, value].
func (ci *constructInv) recordMetric(name string, labelPairs [][2]string) {
	if ci == nil {
		return
	}
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.metricNames[name] = struct{}{}
	var h maphash.Hash
	h.SetSeed(ci.seed)
	_, _ = h.WriteString(name)
	for _, kv := range labelPairs { // sorted by key for a stable signature
		ci.metricLabels[kv[0]] = struct{}{}
		_, _ = h.WriteString("\x00")
		_, _ = h.WriteString(kv[0])
		_, _ = h.WriteString("\x00")
		_, _ = h.WriteString(kv[1]) // hash values to count distinct series (values not stored/exposed)
	}
	if !ci.capped {
		if len(ci.sigs) >= maxSigsPerConstruct {
			ci.capped = true
		} else {
			ci.sigs[h.Sum64()] = struct{}{}
		}
	}
}

func (ci *constructInv) recordLog(source string, labelKeys []string) {
	if ci == nil {
		return
	}
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.logSources[source] = struct{}{}
	for _, k := range labelKeys {
		ci.logLabelKeys[k] = struct{}{}
	}
}

func (ci *constructInv) recordTrace(service string, spanNames, attrKeys []string) {
	if ci == nil {
		return
	}
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.spanServices[service] = struct{}{}
	for _, n := range spanNames {
		ci.spanNames[n] = struct{}{}
	}
	for _, k := range attrKeys {
		ci.spanAttrKeys[k] = struct{}{}
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (ci *constructInv) snapshot() control.ConstructInventory {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return control.ConstructInventory{
		Kind: ci.kind, Name: ci.name, DistinctSeries: int64(len(ci.sigs)), Capped: ci.capped,
		MetricNames: sortedKeys(ci.metricNames), MetricLabels: sortedKeys(ci.metricLabels),
		LogSources: sortedKeys(ci.logSources), LogLabelKeys: sortedKeys(ci.logLabelKeys),
		SpanServices: sortedKeys(ci.spanServices), SpanNames: sortedKeys(ci.spanNames),
		SpanAttrKeys: sortedKeys(ci.spanAttrKeys),
	}
}

// zeroConstructEmission blanks the emission-derived fields of a construct snapshot (used when its
// blueprint is disabled), keeping only the structural Kind/Name so the construct stays listed.
func zeroConstructEmission(cs *control.ConstructInventory) {
	cs.DistinctSeries = 0
	cs.Capped = false
	cs.MetricNames = nil
	cs.MetricLabels = nil
	cs.LogSources = nil
	cs.LogLabelKeys = nil
	cs.SpanServices = nil
	cs.SpanNames = nil
	cs.SpanAttrKeys = nil
}

// Inventory implements control.InventorySource: aggregate every construct/workload writer inventory.
func (r *Runner) Inventory() control.InventoryReport {
	rep := control.InventoryReport{}
	totalSeries := int64(0)
	totalConstructs := 0
	for _, bp := range r.bps {
		bi := control.BlueprintInventory{Blueprint: bp.name}
		names := map[string]struct{}{}
		labels := map[string]struct{}{}
		add := func(ci *constructInv) {
			if ci == nil {
				return
			}
			cs := ci.snapshot()
			bi.Constructs = append(bi.Constructs, cs)
			bi.DistinctSeries += cs.DistinctSeries
			for _, n := range cs.MetricNames {
				names[n] = struct{}{}
			}
			for _, l := range cs.MetricLabels {
				labels[l] = struct{}{}
			}
			totalConstructs++
		}
		for _, bc := range bp.constructs {
			add(bc.inv)
		}
		for _, bw := range bp.workloads {
			add(bw.inv)
		}
		bi.MetricNames = len(names)
		bi.LabelKeys = len(labels)
		// A disabled blueprint emits nothing right now: zero its emission-derived counters so the
		// Overview drops to zero (the construct signature maps are cumulative-ever and would otherwise
		// freeze at their last value). Constructs stay listed — they are structural, not emission.
		if !r.enabled(bp.name) {
			bi.DistinctSeries = 0
			bi.MetricNames = 0
			bi.LabelKeys = 0
			for i := range bi.Constructs {
				zeroConstructEmission(&bi.Constructs[i])
			}
		}
		totalSeries += bi.DistinctSeries
		sort.Slice(bi.Constructs, func(i, j int) bool { return bi.Constructs[i].Name < bi.Constructs[j].Name })
		rep.Blueprints = append(rep.Blueprints, bi)
	}
	sort.Slice(rep.Blueprints, func(i, j int) bool { return rep.Blueprints[i].Blueprint < rep.Blueprints[j].Blueprint })
	rep.Totals = control.InventoryTotals{DistinctSeries: totalSeries, Constructs: totalConstructs, Blueprints: len(r.bps)}
	return rep
}
