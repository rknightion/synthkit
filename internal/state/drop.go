// SPDX-License-Identifier: AGPL-3.0-only

package state

// DropWhere removes every tracked series (counter, gauge, or histogram) for which pred returns
// true. Use it to retire series for resources that no longer exist (e.g. pods/nodes removed by a
// live scale-down) so they stop being emitted and go stale in Prometheus, instead of lingering at
// their last value forever. pred receives the series name and its live label map (do not mutate).
func (s *State) DropWhere(pred func(name string, labels map[string]string) bool) {
	for sig, cs := range s.counters {
		if pred(cs.name, cs.labels) {
			delete(s.counters, sig)
		}
	}
	for sig, gs := range s.gauges {
		if pred(gs.name, gs.labels) {
			delete(s.gauges, sig)
		}
	}
	for sig, hs := range s.histos {
		if pred(hs.name, hs.labels) {
			delete(s.histos, sig)
		}
	}
}
