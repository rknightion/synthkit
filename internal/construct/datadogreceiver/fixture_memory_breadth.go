// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// memoryBreadthFixtureResources emits the independently admissible swap subset
// of the host receiver surface. Swap is disabled in the host fixture: no
// swap-device or paging state is selected, and there is no admissible
// non-degenerate basis for these values. Keep the seven observed envelopes as
// honest degenerate gauges at zero without inventing arithmetic, state, or
// capacity.
func (c *Construct) memoryBreadthFixtureResources(now time.Time, w *core.World) []otlp.MetricResource {
	return []otlp.MetricResource{
		c.gaugeResource("system.swap.cached", c.baseResourceAttrs(), nil, 0, now),
		c.gaugeResource("system.swap.free", c.baseResourceAttrs(), nil, 0, now),
		c.gaugeResource("system.swap.pct_free", c.baseResourceAttrs(), nil, 0, now),
		c.gaugeResource("system.swap.swap_in", c.baseResourceAttrs(), nil, 0, now),
		c.gaugeResource("system.swap.swap_out", c.baseResourceAttrs(), nil, 0, now),
		c.gaugeResource("system.swap.total", c.baseResourceAttrs(), nil, 0, now),
		c.gaugeResource("system.swap.used", c.baseResourceAttrs(), nil, 0, now),
	}
}
