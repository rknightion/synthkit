// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/selfobs"
)

// deliveryManifestSource adapts the runner's running-set manifest to the self-observability
// export. A standby process has no producers and a dry run pushes nothing live, so their expected
// lanes are relabelled rather than dropped; standby takes precedence.
func deliveryManifestSource(r *runner.Runner, standby, dryRun bool) func() selfobs.DeliveryManifest {
	return func() selfobs.DeliveryManifest {
		m := r.DeliveryManifest()
		switch {
		case standby:
			m = m.Suppress(runner.LaneStandby)
		case dryRun:
			m = m.Suppress(runner.LaneDryRun)
		}
		out := selfobs.DeliveryManifest{Fingerprint: m.Fingerprint, Lanes: make([]selfobs.ManifestLane, 0, len(m.Lanes))}
		for _, lane := range m.Lanes {
			out.Lanes = append(out.Lanes, selfobs.ManifestLane{Sink: lane.Sink, State: string(lane.State), Freshness: string(lane.Freshness), FreshAfter: lane.FreshAfter})
		}
		return out
	}
}
