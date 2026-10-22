// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// fileHandleFixtureResources emits the three file-nr families that the storage
// fixture leaves out. Datadog Agent file_handles.go reads /proc/sys/fs/file-nr as
// allocated, allocated-unused and max. It reports used = allocated - unused and
// in_use = (allocated - unused) / max.
//
// Physics basis: the Linux kernel's Documentation/admin-guide/sysctl/fs.rst says
// "Linux 2.6 and later always reports 0 as the number of free file handles", so
// unused is a kernel-documented zero, not a missing source. Allocated and max are
// the storage model's nodeexp values (node_filefd_allocated and
// node_filefd_maximum in internal/nodeexp/linux.go). The model is a pure function
// of declared start, time and shape keys, so recomputing it here gives the same
// allocated value that storageFixtureResources emits on this tick.
func (c *Construct) fileHandleFixtureResources(now time.Time, w *core.World) []otlp.MetricResource {
	model := c.storageFixtureModel(now, w)
	const allocatedUnused = 0.0
	used := model.fileAllocated - allocatedUnused
	base := c.baseResourceAttrs()
	return []otlp.MetricResource{
		c.gaugeResource("system.fs.file_handles.allocated_unused", base, nil, allocatedUnused, now),
		c.gaugeResource("system.fs.file_handles.used", base, nil, used, now),
		c.gaugeResource("system.fs.file_handles.in_use", base, nil, storageSafeRatio(used, model.fileMax), now),
	}
}
