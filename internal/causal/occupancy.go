// SPDX-License-Identifier: AGPL-3.0-only

package causal

import "time"

// Occupancy is one grid step's split of the work a pool, concurrency or queue resource served:
// the work that completes and the work that fails at the held-across dependency (State
// FailedShare), each with its rate and the time one unit occupies a connection, slot or consumer.
//
// CompletedRPS is State.ServedRPS x (1 - FailedShare), held for HoldMS (State.HoldMS).
// FailedRPS is State.ServedRPS x FailedShare, held for FailedHoldMS: for pool and concurrency
// the failed unit's elapsed time on the evaluator's failure timeline (the failed term of the
// model's occupancy, PreWorkFraction of the hold plus the failing call's attempts), and for a
// queue the full HoldMS (failed work returns its message, see occupancy). With nothing failing,
// CompletedRPS is ServedRPS exactly and FailedHoldMS is HoldMS. For pool and concurrency, Little's
// law ties the split to State.InUse: (CompletedRPS x HoldMS + FailedRPS x FailedHoldMS) / 1000 is
// the step's InUse.
type Occupancy struct {
	At                      time.Time
	CompletedRPS, HoldMS    float64
	FailedRPS, FailedHoldMS float64
}

// Occupancy is the named resource's occupancy split at the last grid point at or before at. ok is
// false for an unknown name, a step outside the retained history, or a resource that holds no
// work (quota, dependency, credential, rule).
func (m *Model) Occupancy(name string, at time.Time) (Occupancy, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byName[name]
	if !ok {
		return Occupancy{}, false
	}
	switch c.r.Kind {
	case KindPool, KindConcurrency, KindQueue:
	default:
		return Occupancy{}, false
	}
	r, ok := m.recAt(name, at)
	if !ok {
		return Occupancy{}, false
	}
	st := r.st
	return Occupancy{
		At:           st.At,
		CompletedRPS: st.ServedRPS * (1 - st.FailedShare),
		HoldMS:       st.HoldMS,
		FailedRPS:    st.ServedRPS * st.FailedShare,
		FailedHoldMS: r.failedHold,
	}, true
}
