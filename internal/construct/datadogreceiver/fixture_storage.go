// SPDX-License-Identifier: AGPL-3.0-only

package datadogreceiver

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const (
	// The standalone host topology's existing Linux fallback names this one root
	// device. Datadog receiver host mode carries no HostTopology, so this is a
	// representative fixture identity, not a claim about a selected physical host.
	storageFixtureDevice     = "nvme0n1"
	storageFixtureFilesystem = "/dev/nvme0n1p1"
	storageFixtureGiB        = float64(1024 * 1024 * 1024)
	storageFixtureDiskBytes  = float64(100 * 1024 * 1024 * 1024)

	storageFixtureSectorBytes = 512.0
)

// storageFixtureModel holds the per-tick values derived from the Linux nodeexp
// mechanics. It is kept local to this construct because the Datadog receiver's
// frozen Construct has no HostTopology or storage state seam.
type storageFixtureModel struct {
	// Filesystem and inode gauges.
	diskTotal, diskFree, diskUsed, diskInUse, diskUtilized      float64
	inodeTotal, inodeFree, inodeUsed, inodeInUse, inodeUtilized float64

	// File-handle gauges with direct nodeexp mechanics.
	fileAllocated, fileMax float64

	// Disk time families.
	readTime, writeTime, readTimePct, writeTimePct float64

	// I/O rates and derived iostat gauges.
	readOps, writeOps             float64
	readBytes, writeBytes         float64
	readKB, writeKB               float64
	readAwait, writeAwait         float64
	await, avgRequestSize         float64
	averageQueueSize, serviceTime float64
	utilized                      float64
	blockIn, blockOut             float64
}

// storageFixtureResources emits the source-backed storage families classified as
// observed_unimplemented_fixture_mechanics_candidate in the immutable host
// verdict. The envelope is assembled only through the shared fixture helpers:
// resource placement remains host.name/source, device dimensions remain native
// resource and datapoint attributes, and the translator scope is unchanged.
//
// The values mirror internal/nodeexp/linux.go rather than the Kubernetes
// Datadog fixture. The filesystem uses nodeexp's 100 GiB absent-size fallback,
// the standalone host topology's existing Linux fallback supplies nvme0n1 and
// /dev/nvme0n1p1, and the disk loop reuses nodeexp's read/write, operation-time,
// and vmstat coefficients. Datadog's families without a direct nodeexp name
// are derived from those quantities only where that mapping is source-backed.
// No selected host capacity is implied.
func (c *Construct) storageFixtureResources(now time.Time, w *core.World) []otlp.MetricResource {
	model := c.storageFixtureModel(now, w)
	base := c.baseResourceAttrs()
	ioAttrs := map[string]any{
		"device":    storageFixtureDevice,
		"host.name": c.hostName,
		"source":    c.source,
	}
	ioPointAttrs := map[string]any{"device_name": storageFixtureDevice}
	fsAttrs := map[string]any{
		"device":    storageFixtureFilesystem,
		"host.name": c.hostName,
		"source":    c.source,
	}
	fsPointAttrs := map[string]any{"device_name": "nvme0n1p1"}

	resources := make([]otlp.MetricResource, 0, 29)
	// system.io's operation rates are represented by the observed receiver gauge
	// envelope. nodeexp does not expose merged-operation counters, so rrqm_s and
	// wrqm_s remain explicitly unimplemented until an admissible source is found.
	for _, metric := range []struct {
		name  string
		value float64
	}{
		{"system.io.avg_q_sz", model.averageQueueSize},
		{"system.io.avg_rq_sz", model.avgRequestSize},
		{"system.io.await", model.await},
		{"system.io.r_await", model.readAwait},
		{"system.io.r_s", model.readOps},
		{"system.io.rkb_s", model.readKB},
		{"system.io.svctm", model.serviceTime},
		{"system.io.util", model.utilized},
		{"system.io.w_await", model.writeAwait},
		{"system.io.w_s", model.writeOps},
		{"system.io.wkb_s", model.writeKB},
	} {
		resources = append(resources, c.gaugeResource(metric.name, ioAttrs, ioPointAttrs, metric.value, now))
	}
	// Datadog's block families come from Linux /proc/vmstat. The nodeexp
	// pgpgin/pgpgout rates are the available source mechanics; the immutable
	// verdict requires the receiver's observed gauge envelope here.
	resources = append(resources,
		c.gaugeResource("system.io.block_in", base, nil, model.blockIn, now),
		c.gaugeResource("system.io.block_out", base, nil, model.blockOut, now),
	)

	// system.disk partition values use the filesystem device and basename, as the
	// Datadog disk check does for its usage and inode families. Disk time values
	// use the underlying nodeexp device identity because they come from its disk
	// I/O counter loop.
	// system.disk.utilized has no nodeexp-named series; it is the documented
	// percentage form of the nodeexp filesystem used/total relationship.
	for _, metric := range []struct {
		name  string
		value float64
	}{
		{"system.disk.free", model.diskFree},
		{"system.disk.in_use", model.diskInUse},
		{"system.disk.read_time_pct", model.readTimePct},
		{"system.disk.total", model.diskTotal},
		{"system.disk.used", model.diskUsed},
		{"system.disk.utilized", model.diskUtilized},
		{"system.disk.write_time_pct", model.writeTimePct},
	} {
		attrs, pointAttrs := fsAttrs, fsPointAttrs
		if metric.name == "system.disk.read_time_pct" || metric.name == "system.disk.write_time_pct" {
			attrs, pointAttrs = ioAttrs, ioPointAttrs
		}
		resources = append(resources, c.gaugeResource(metric.name, attrs, pointAttrs, metric.value, now))
	}
	resources = append(resources,
		c.sumResource("system.disk.read_time", ioAttrs, ioPointAttrs, model.readTime, now),
		c.sumResource("system.disk.write_time", ioAttrs, ioPointAttrs, model.writeTime, now),
	)

	// file-nr has no direct nodeexp emitter. Only allocated/max have a source
	// backed counterpart here; allocated_unused, used and in_use depend on the
	// unavailable file-nr unused field and remain unimplemented.
	for _, metric := range []struct {
		name  string
		value float64
	}{
		{"system.fs.file_handles.allocated", model.fileAllocated},
		{"system.fs.file_handles.max", model.fileMax},
	} {
		resources = append(resources, c.gaugeResource(metric.name, base, nil, metric.value, now))
	}
	// system.fs.inodes.utilized likewise has no nodeexp-named series. Both inode
	// percentages are derived from nodeexp's files/files_free gauges, while the
	// in_use family retains the Agent fraction form.
	for _, metric := range []struct {
		name  string
		value float64
	}{
		{"system.fs.inodes.free", model.inodeFree},
		{"system.fs.inodes.in_use", model.inodeInUse},
		{"system.fs.inodes.total", model.inodeTotal},
		{"system.fs.inodes.used", model.inodeUsed},
		{"system.fs.inodes.utilized", model.inodeUtilized},
	} {
		resources = append(resources, c.gaugeResource(metric.name, fsAttrs, fsPointAttrs, metric.value, now))
	}
	return resources
}

func (c *Construct) storageFixtureModel(now time.Time, w *core.World) storageFixtureModel {
	factor := storageFixtureFactor(now, w)
	basis := now
	if !c.start.IsZero() {
		basis = c.start
	}
	elapsed := storageFixtureElapsed(now, basis)

	// linux.go uses linuxCPUBusyFrac to scale IO time. Its hostHash offset is
	// replaced by the required scoped fixtureSeriesVar so this isolated construct
	// remains deterministic without importing nodeexp or adding another RNG.
	busy := storageClamp((0.22+0.50*factor)*c.storageSeriesVar(w, now, "node_cpu_busy_fraction", 0.12, 0.04), 0.01, 0.99)

	// The nodeexp disk loop computes rw=factor*tickSec*(0.5+hostHash(host,dev)).
	// Divide by tickSec for rates, retaining its 0.5 base and host/device spread.
	rwRate := factor * (0.5 + 0.5*c.storageSeriesVar(w, now, "node_disk."+storageFixtureDevice, 0.45, 0.03))
	if rwRate < 0 {
		rwRate = 0
	}
	readBytesRate := rwRate * 4 * 1024 * 1024 * c.storageSeriesVar(w, now, "node_disk_read_bytes_total", 0.20, 0.04)
	writeBytesRate := rwRate * 8 * 1024 * 1024 * c.storageSeriesVar(w, now, "node_disk_written_bytes_total", 0.20, 0.04)
	readOpsRate := rwRate * 50 * c.storageSeriesVar(w, now, "node_disk_reads_completed_total", 0.20, 0.04)
	writeOpsRate := rwRate * 120 * c.storageSeriesVar(w, now, "node_disk_writes_completed_total", 0.20, 0.04)
	if readBytesRate < 0 {
		readBytesRate = 0
	}
	if writeBytesRate < 0 {
		writeBytesRate = 0
	}
	if readOpsRate < 0 {
		readOpsRate = 0
	}
	if writeOpsRate < 0 {
		writeOpsRate = 0
	}

	// nodeexp stores these two counters as seconds per disk. Datadog's disk
	// check reports the cumulative values in milliseconds, so convert only at
	// this receiver boundary. The frozen Construct has no storage accumulator;
	// using the declared-start rate keeps the cumulative point stable across ticks.
	readTimeRate := 0.05 * c.storageSeriesVar(w, basis, "node_disk_read_time_seconds_total", 0.20, 0.03)
	writeTimeRate := 0.10 * c.storageSeriesVar(w, basis, "node_disk_write_time_seconds_total", 0.20, 0.03)
	readTime := elapsed * readTimeRate * 1000
	writeTime := elapsed * writeTimeRate * 1000

	totalOps := readOpsRate + writeOpsRate
	readAwait := storageSafeRatio(readTimeRate*1000, readOpsRate)
	writeAwait := storageSafeRatio(writeTimeRate*1000, writeOpsRate)
	await := storageSafeRatio((readTimeRate+writeTimeRate)*1000, totalOps)
	avgRequestSize := storageSafeRatio(readBytesRate+writeBytesRate, totalOps*storageFixtureSectorBytes)
	ioTimeRate := 0.20 * busy * c.storageSeriesVar(w, now, "node_disk_io_time_seconds_total", 0.20, 0.03)
	weightedIOTimeRate := 0.40 * busy * c.storageSeriesVar(w, now, "node_disk_io_time_weighted_seconds_total", 0.20, 0.03)
	if ioTimeRate < 0 {
		ioTimeRate = 0
	}
	if weightedIOTimeRate < 0 {
		weightedIOTimeRate = 0
	}
	utilized := ioTimeRate * 100
	// Datadog's iostat svctm is milliseconds per operation: nodeexp's IO-time
	// fraction is seconds per second, so convert the numerator to milliseconds
	// before dividing by the operation rate.
	serviceTime := storageSafeRatio(ioTimeRate*1000, totalOps)

	// linux.go's filesystem branch uses its 100 GiB fallback and samples free
	// space uniformly in the [30,80] GiB range. A scoped variation supplies a
	// deterministic equivalent while retaining those source bounds.
	freeGiB := storageClamp(55*c.storageSeriesVar(w, now, "node_filesystem_avail_bytes", 0.40, 0.03), 30, 80)
	// The Agent disk check retains its legacy KiB values for total/used/free.
	// Convert nodeexp's byte quantities at this boundary; ratios remain unitless.
	diskFree := freeGiB * storageFixtureGiB / 1024
	diskTotal := storageFixtureDiskBytes / 1024
	diskUsed := diskTotal - diskFree
	diskInUse := storageSafeRatio(diskUsed, diskTotal)
	diskUtilized := diskInUse * 100

	// linux.go's node_filesystem_files/files_free are its inode-capacity basis.
	inodeTotal := 6_553_600.0
	inodeFree := 6_000_000.0
	inodeUsed := inodeTotal - inodeFree
	inodeInUse := storageSafeRatio(inodeUsed, inodeTotal)
	inodeUtilized := inodeInUse * 100

	// linux.go exposes node_filefd_allocated as 1000+int(hostHash*3000) and
	// node_filefd_maximum as MaxInt64. Scoped variation substitutes the unavailable
	// host hash while keeping the allocated value within that source range.
	fileAllocated := float64(1000 + int(1500*c.storageSeriesVar(w, now, "node_filefd_allocated", 0.20, 0.03)))
	fileMax := float64(1<<63 - 1)

	return storageFixtureModel{
		diskTotal: diskTotal, diskFree: diskFree, diskUsed: diskUsed, diskInUse: diskInUse, diskUtilized: diskUtilized,
		inodeTotal: inodeTotal, inodeFree: inodeFree, inodeUsed: inodeUsed, inodeInUse: inodeInUse, inodeUtilized: inodeUtilized,
		fileAllocated: fileAllocated, fileMax: fileMax,
		readTime: readTime, writeTime: writeTime, readTimePct: readTimeRate * 100, writeTimePct: writeTimeRate * 100,
		readOps: readOpsRate, writeOps: writeOpsRate, readBytes: readBytesRate, writeBytes: writeBytesRate,
		readKB: readBytesRate / 1024, writeKB: writeBytesRate / 1024,
		readAwait: readAwait, writeAwait: writeAwait, await: await, avgRequestSize: avgRequestSize,
		averageQueueSize: weightedIOTimeRate, serviceTime: serviceTime, utilized: utilized,
		blockIn:  800 * factor * c.storageSeriesVar(w, now, "node_vmstat_pgpgin", 0.20, 0.03),
		blockOut: 1200 * factor * c.storageSeriesVar(w, now, "node_vmstat_pgpgout", 0.20, 0.03),
	}
}

func (c *Construct) storageSeriesVar(w *core.World, now time.Time, key string, spreadAmp, wanderAmp float64) float64 {
	value := c.fixtureSeriesVar(w, now, "storage."+key, spreadAmp, wanderAmp)
	if value < 0 {
		return 0
	}
	return value
}

func storageFixtureFactor(now time.Time, w *core.World) float64 {
	if w == nil || w.Shape == nil {
		return 1
	}
	factor := w.Shape.Factor(now, 1, false)
	if factor < 0 {
		return 0
	}
	return factor
}

func storageFixtureElapsed(now, start time.Time) float64 {
	if start.IsZero() || !now.After(start) {
		return 0
	}
	return now.Sub(start).Seconds()
}

func storageSafeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func storageClamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
