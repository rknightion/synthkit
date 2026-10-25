// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// holdEndResources is a pool held across the dependency its holder calls: the holder's hop ends
// exactly when its dependency call does, the shape that exposed independent rounding of a hop's
// offset and duration.
const holdEndResources = `
- {name: editor-pool, kind: pool, size: 20, hold_ms: 414, held_across: editor-store, acquire_timeout_ms: 10000,
   demand: {off_peak_rps: 1, peak_rps: 8}}
- {name: editor-store, kind: dependency, latency_ms: 411}
`

const holdEndApp = `
traffic: {off_peak_rps: 1, peak_rps: 8}
services:
  - {name: web, entry: true, calls: [editor]}
  - {name: editor, uses: [editor-pool], calls: [editor-db]}
  - {name: editor-db, type: db, uses: [editor-store]}
`

// TestCausalChildHopEndingWithItsCallerDoesNotOutliveIt mints healthy requests whose child hop
// ends exactly when its caller's does and requires every hop, and every projected span, to end
// at or before its parent: rounding a hop's start and duration separately could put the child's
// end one nanosecond past the caller's.
func TestCausalChildHopEndingWithItsCallerDoesNotOutliveIt(t *testing.T) {
	w := causalApps(t, holdEndResources, holdEndApp)[0]
	eng := shape.New("UTC", nil)
	for i := range 60 {
		now := causalNow.Add(time.Duration(i) * time.Minute)
		reqs := mintAt(w, eng, now)
		for _, r := range reqs {
			if r.Outcome != ledger.OutcomeSuccess {
				continue // check successful requests' ledger hops here; every outcome's projected spans are checked below
			}
			for _, c := range r.Calls {
				end := c.Offset + c.Duration
				if c.ParentHopIndex < 0 {
					if end > r.Duration {
						t.Fatalf("request %s: hop %s ends %v after the request", r.TraceID, c.Target, end-r.Duration)
					}
					continue
				}
				p := r.Calls[c.ParentHopIndex]
				if pend := p.Offset + p.Duration; end > pend {
					t.Fatalf("request %s: hop %s ends %v after its caller hop %s", r.TraceID, c.Target, end-pend, p.Target)
				}
			}
		}
		if v := nestingRequestViolations(project(t, w, eng, reqs).all, reqs, nil); len(v) > 0 {
			t.Fatalf("%d span(s) outside their parent, e.g. %s", len(v), v[0])
		}
	}
}
