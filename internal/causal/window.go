// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"time"
)

// Window is the sum of one resource's steps in a time window (from, to].
//
// Arrivals, Admitted, Rejected, Served and Failed are counts over the window from effective
// demand (declared or upstream demand plus expected retry attempts).
// MeanInUse, MaxInUse and MeanWaitMS summarise the steps present. BacklogEnd and
// OldestAgeSecEnd are the queue state at the last step present. Truncated reports that part of
// the window is older than the retained history (15 minutes), predates the model's start, or
// asks about a model that has not been advanced yet (no history at all).
type Window struct {
	From, To                             time.Time
	Steps                                int
	Arrivals, Admitted, Rejected, Served float64
	Failed                               float64 // served work that failed after admission (State.FailedShare)
	MeanInUse, MaxInUse, MeanWaitMS      float64
	BacklogEnd, OldestAgeSecEnd          float64
	Truncated                            bool
}

// Window sums the steps in (from, to]; Truncated reports history older than the ring, and a
// model never advanced has none, so its non-empty window is empty and Truncated. Steps newer
// than the last Advance are absent, not truncated. An empty or inverted interval is a clean
// empty window.
func (m *Model) Window(name string, from, to time.Time) (Window, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := Window{From: from, To: to}
	c, ok := m.byName[name]
	if !ok {
		return w, false
	}
	if !to.After(from) {
		return w, true
	}
	if !m.started {
		w.Truncated = true
		return w, true
	}
	kStart := gridIndex(from) + 1 // first grid point strictly after from
	kEnd := gridIndex(to)         // last grid point at or before to
	if kEnd > m.last {
		kEnd = m.last
	}
	if kEnd-kStart+1 > ringSteps {
		kStart = kEnd - ringSteps + 1
		w.Truncated = true
	}
	var sumInUse, sumWait float64
	for k := kStart; k <= kEnd; k++ {
		r := c.ring[mod(k, ringSteps)]
		if !r.valid || r.k != k {
			w.Truncated = true
			continue
		}
		w.Steps++
		w.Arrivals += r.arrivals
		w.Admitted += r.admitted
		w.Rejected += r.rejected
		w.Served += r.served
		w.Failed += r.failed
		sumInUse += r.st.InUse
		sumWait += r.st.WaitMS
		w.MaxInUse = math.Max(w.MaxInUse, r.st.InUse)
		w.BacklogEnd = r.st.Backlog
		w.OldestAgeSecEnd = r.st.OldestAgeSec
	}
	if w.Steps > 0 {
		w.MeanInUse = sumInUse / float64(w.Steps)
		w.MeanWaitMS = sumWait / float64(w.Steps)
	}
	return w, true
}
