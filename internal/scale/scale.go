// SPDX-License-Identifier: AGPL-3.0-only

// Package scale holds per-blueprint live scaling overrides (target → count) for the control
// plane. It is a leaf (no synthkit-tier imports) so constructs/workloads may import it. Reads are
// lock-free on the tick hot path via an atomic snapshot pointer; Set swaps the whole map.
package scale

import "sync/atomic"

// Source holds the live override map only — never the declared defaults (the caller, which knows
// its own declared count, passes that to Count).
type Source struct {
	m atomic.Pointer[map[string]int]
}

// New returns an empty Source (no overrides).
func New() *Source {
	s := &Source{}
	empty := map[string]int{}
	s.m.Store(&empty)
	return s
}

// Set replaces the override map atomically. The caller must not mutate m after passing it.
func (s *Source) Set(m map[string]int) {
	cp := make(map[string]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	s.m.Store(&cp)
}

// Count returns the override for target when present, else declaredDefault.
func (s *Source) Count(target string, declaredDefault int) int {
	m := s.m.Load()
	if m == nil {
		return declaredDefault
	}
	if v, ok := (*m)[target]; ok {
		return v
	}
	return declaredDefault
}
