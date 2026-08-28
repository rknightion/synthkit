// SPDX-License-Identifier: AGPL-3.0-only

package host

// native_otlp.go is the hostmetricsreceiver-shaped metric lane for a standalone
// host. It deliberately does not reuse nodeexp names: hostmetricsreceiver is a
// second, semantic-name catalogue whose OTLP resource and datapoint shape is
// sourced from the captured receiver permutation (signals/host.md,
// reality-corpus/host/k3d-lab-otel-receivers.json).

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/nodeexp"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const (
	nativeCollectorVersion = "0.171.0"
	nativeScopeName        = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver"
)

// These are the state values emitted by the hostmetrics CPU scraper. Linux has
// eight values; the receiver's non-Linux implementation has only four. Keep the
// lists separate: the platform distinction is part of the observed contract.
var (
	nativeLinuxCPUStates = []string{
		"user", "system", "idle", "interrupt", "nice", "softirq", "steal", "wait",
	}
	nativeNonLinuxCPUStates = []string{"user", "system", "idle", "interrupt"}

	// The captured Linux host exposed these six memory states. `inactive` is present
	// in newer receiver metadata but was not present in the captured contract, so it
	// is intentionally not synthesized here.
	nativeMemoryStates = []string{
		"buffered", "cached", "free", "slab_reclaimable", "slab_unreclaimable", "used",
	}

	nativeDiskDirections    = []string{"read", "write"}
	nativeNetworkDirections = []string{"receive", "transmit"}
	nativeTCPStates         = []string{
		"CLOSE", "CLOSE_WAIT", "CLOSING", "DELETE", "ESTABLISHED", "FIN_WAIT_1",
		"FIN_WAIT_2", "LAST_ACK", "LISTEN", "SYN_RECV", "SYN_SENT", "TIME_WAIT",
	}
)

// nativeOTLPState is separate from the Prometheus state store. Enabling native
// emission must not perturb the established node_exporter/windows_exporter
// state or its draw order. Construct ticks are serialized by the runner.
type nativeOTLPState struct {
	start    time.Time
	counters map[string]float64
}

func newNativeOTLPState() *nativeOTLPState {
	return &nativeOTLPState{counters: map[string]float64{}}
}

func (s *nativeOTLPState) begin(now time.Time) time.Time {
	if s.start.IsZero() {
		s.start = now
	}
	return s.start
}

func (s *nativeOTLPState) add(key string, delta float64) float64 {
	s.counters[key] += delta
	return s.counters[key]
}

func (c *Construct) tickOTLPMetrics(
	ctx context.Context,
	now time.Time,
	top nodeexp.HostTopology,
	factor, tickSec float64,
	w *core.World,
) error {
	if !c.otelMetrics || c.otlpState == nil || w == nil || w.OTLPMetrics == nil {
		return nil
	}
	if tickSec <= 0 {
		tickSec = tickCadence.Seconds()
	}
	start := c.otlpState.begin(now)
	metrics := c.nativeMetrics(now, start, top, factor, tickSec)
	resource := otlp.MetricResource{
		// A top-level host has no resolved Kubernetes cluster/node identity. The
		// standalone hostmetrics contract therefore carries only host.name and
		// os.type; k8s.cluster.name/k8s.node.name belong to the captured k3d
		// processor enrichment, not to this construct's fixture.
		Attrs: map[string]any{
			"host.name": top.Hostname,
			"os.type":   nativeOSType(c.h.OS),
		},
		Scope:   nativeScope(),
		Metrics: metrics,
	}
	return w.OTLPMetrics.Write(ctx, []otlp.MetricResource{resource})
}

func (c *Construct) nativeMetrics(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) []otlp.Metric {
	metrics := make([]otlp.Metric, 0, 19)

	// Keep the output order aligned with the captured inventory. Ordering is not
	// part of OTLP semantics, but stable order makes dry-run captures and tests
	// easier to inspect.
	metrics = append(metrics,
		nativeGauge("system.cpu.load_average.15m", "{thread}", "Average CPU Load over 15 minutes.", now,
			nativeLoad(top, factor, "15m")),
		nativeGauge("system.cpu.load_average.1m", "{thread}", "Average CPU Load over 1 minute.", now,
			nativeLoad(top, factor, "1m")),
		nativeGauge("system.cpu.load_average.5m", "{thread}", "Average CPU Load over 5 minutes.", now,
			nativeLoad(top, factor, "5m")),
	)

	metrics = append(metrics,
		c.nativeCPUTime(now, start, top, factor, tickSec),
		c.nativeLogicalCPUCount(now, start, top),
	)

	metrics = append(metrics,
		c.nativeDiskIO(now, start, top, factor, tickSec),
		c.nativeDiskIOTime(now, start, top, factor, tickSec),
		c.nativeDiskMerged(now, start, top, factor, tickSec),
		c.nativeDiskOperationTime(now, start, top, factor, tickSec),
		c.nativeDiskOperations(now, start, top, factor, tickSec),
		c.nativeDiskPending(now, start, top, factor),
		c.nativeDiskWeightedIOTime(now, start, top, factor, tickSec),
	)

	metrics = append(metrics,
		c.nativeMemoryLimit(now, start, top),
		c.nativeMemoryUsage(now, start, top, factor),
	)

	metrics = append(metrics,
		c.nativeNetworkConnections(now, start),
		c.nativeNetworkMetric(now, start, top, factor, tickSec,
			"system.network.dropped", "{packets}", "The number of packets dropped.",
			func(bytes float64) float64 { return bytes / 1500 * 0.0001 }),
		c.nativeNetworkMetric(now, start, top, factor, tickSec,
			"system.network.errors", "{errors}", "The number of errors encountered.",
			func(bytes float64) float64 { return bytes / 1500 * 0.00001 }),
		c.nativeNetworkMetric(now, start, top, factor, tickSec,
			"system.network.io", "By", "The number of bytes transmitted and received.",
			func(bytes float64) float64 { return bytes }),
		c.nativeNetworkMetric(now, start, top, factor, tickSec,
			"system.network.packets", "{packets}", "The number of packets transferred.",
			func(bytes float64) float64 { return bytes / 1500 }),
	)
	return metrics
}

func nativeScope() otlp.Scope {
	return otlp.Scope{Name: nativeScopeName, Version: nativeCollectorVersion}
}

func nativeOSType(os string) string {
	switch os {
	case "windows":
		return "windows"
	case "darwin", "macos":
		return "darwin"
	default:
		return "linux"
	}
}

func nativeCPUStates(os string) []string {
	if nativeOSType(os) == "linux" {
		return nativeLinuxCPUStates
	}
	return nativeNonLinuxCPUStates
}

func nativeGauge(name, unit, description string, now time.Time, value float64) otlp.Metric {
	return otlp.Metric{
		Name: name, Description: description, Unit: unit, Kind: otlp.MetricGauge,
		Numbers: []otlp.NumberPoint{{Time: now, Value: value}},
	}
}

func nativeSum(name, unit, description string, monotonic bool, points []otlp.NumberPoint) otlp.Metric {
	return otlp.Metric{
		Name: name, Description: description, Unit: unit, Kind: otlp.MetricSum,
		Monotonic: monotonic, Temporality: otlp.TemporalityCumulative, Numbers: points,
	}
}

func nativeSumPoint(attrs map[string]any, start, now time.Time, value float64) otlp.NumberPoint {
	return otlp.NumberPoint{Attrs: attrs, Start: start, Time: now, Value: value}
}

func hostCPUs(top nodeexp.HostTopology) int {
	if top.NumCPU > 0 {
		return top.NumCPU
	}
	return defaultNumCPU
}

func hostMemory(top nodeexp.HostTopology) float64 {
	if top.MemTotal > 0 {
		return top.MemTotal
	}
	return float64(defaultMemTotal)
}

func hostDisks(top nodeexp.HostTopology) []string {
	if len(top.Disks) > 0 {
		return top.Disks
	}
	if nativeOSType(top.OS.ID) == "darwin" {
		return []string{"disk0"}
	}
	return []string{"nvme0n1"}
}

func hostNICs(top nodeexp.HostTopology) []nodeexp.NIC {
	if len(top.NICs) > 0 {
		return top.NICs
	}
	return []nodeexp.NIC{{Name: "eth0", SpeedBytes: 1e9}}
}

func nativeLoad(top nodeexp.HostTopology, factor float64, window string) float64 {
	n := float64(hostCPUs(top))
	base := n * (0.18 + 0.55*factor)
	// Use a host-stable offset rather than a new random draw. This keeps the
	// native lane deterministic and independent from the Prometheus lane's RNG.
	offset := hashUnit(top.Hostname+":otel-load:"+window) * n * 0.08
	switch window {
	case "5m":
		base *= 0.88
	case "15m":
		base *= 0.76
	}
	if base+offset < 0.05 {
		return 0.05
	}
	return base + offset
}

func (c *Construct) nativeCPUTime(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	states := nativeCPUStates(c.h.OS)
	points := make([]otlp.NumberPoint, 0, hostCPUs(top)*len(states))
	for cpu := 0; cpu < hostCPUs(top); cpu++ {
		busy := 0.20 + factor*0.55 + hashUnit(c.seed+":otel-cpu:"+strconv.Itoa(cpu))*0.12
		if busy < 0.05 {
			busy = 0.05
		}
		if busy > 0.95 {
			busy = 0.95
		}

		for _, state := range states {
			fraction := nativeCPUFraction(state, busy, nativeOSType(c.h.OS) == "linux")
			delta := tickSec * fraction
			key := fmt.Sprintf("system.cpu.time\x00%d\x00%s", cpu, state)
			value := c.otlpState.add(key, delta)
			points = append(points, nativeSumPoint(map[string]any{
				"cpu": strconv.Itoa(cpu), "state": state,
			}, start, now, value))
		}
	}
	return nativeSum("system.cpu.time", "s", "Total seconds each logical CPU spent on each mode.", true, points)
}

func nativeCPUFraction(state string, busy float64, linux bool) float64 {
	if state == "idle" {
		return 1 - busy
	}
	// The receiver's Linux names map to the gopsutil CPU fields as follows. The
	// non-Linux implementation exposes only user/system/idle/interrupt.
	switch state {
	case "user":
		if linux {
			return busy * 0.45
		}
		return busy * 0.55
	case "system":
		if linux {
			return busy * 0.25
		}
		return busy * 0.35
	case "interrupt":
		if linux {
			return busy * 0.03
		}
		return busy * 0.10
	case "nice":
		return busy * 0.01
	case "softirq":
		return busy * 0.05
	case "steal":
		return busy * 0.01
	case "wait":
		return busy * 0.20
	default:
		return busy * 0.01
	}
}

func (c *Construct) nativeLogicalCPUCount(now, start time.Time, top nodeexp.HostTopology) otlp.Metric {
	return nativeSum("system.cpu.logical.count", "{cpu}", "Number of available logical CPUs.", false,
		[]otlp.NumberPoint{nativeSumPoint(nil, start, now, float64(hostCPUs(top)))})
}

func (c *Construct) nativeMemoryLimit(now, start time.Time, top nodeexp.HostTopology) otlp.Metric {
	return nativeSum("system.memory.limit", "By", "Total bytes of memory available.", false,
		[]otlp.NumberPoint{nativeSumPoint(nil, start, now, hostMemory(top))})
}

func (c *Construct) nativeMemoryUsage(now, start time.Time, top nodeexp.HostTopology, factor float64) otlp.Metric {
	mem := hostMemory(top)
	// Keep the captured six-value state vocabulary. The values are a stable,
	// plausible breakdown of the host's memory pressure, not a Linux-only
	// node_exporter metric translated into an OTLP name.
	fractions := map[string]float64{
		"buffered":           0.035,
		"cached":             0.18 + factor*0.04,
		"free":               0.25 - factor*0.10,
		"slab_reclaimable":   0.035,
		"slab_unreclaimable": 0.025,
	}
	points := make([]otlp.NumberPoint, 0, len(nativeMemoryStates))
	for _, state := range nativeMemoryStates {
		fraction := fractions[state]
		if state == "used" {
			fraction = 1
			for _, known := range nativeMemoryStates {
				if known != "used" {
					fraction -= fractions[known]
				}
			}
		}
		if fraction < 0.01 {
			fraction = 0.01
		}
		points = append(points, nativeSumPoint(map[string]any{"state": state}, start, now, mem*fraction))
	}
	return nativeSum("system.memory.usage", "By", "Bytes of memory in use.", false, points)
}

func (c *Construct) nativeDiskIO(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(hostDisks(top))*len(nativeDiskDirections))
	for _, device := range hostDisks(top) {
		for _, direction := range nativeDiskDirections {
			multiplier := 1.0
			if direction == "write" {
				multiplier = 0.65
			}
			delta := tickSec * (256*1024 + factor*4*1024*1024) * multiplier
			delta *= 0.8 + hashUnit(c.seed+":otel-disk-io:"+device+":"+direction)*0.4
			key := "system.disk.io\x00" + device + "\x00" + direction
			value := c.otlpState.add(key, delta)
			points = append(points, nativeSumPoint(map[string]any{
				"device": device, "direction": direction,
			}, start, now, value))
		}
	}
	return nativeSum("system.disk.io", "By", "Disk bytes transferred.", true, points)
}

func (c *Construct) nativeDiskIOTime(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(hostDisks(top)))
	for _, device := range hostDisks(top) {
		delta := tickSec * (0.04 + factor*0.14) * (0.8 + hashUnit(c.seed+":otel-disk-time:"+device)*0.4)
		value := c.otlpState.add("system.disk.io_time\x00"+device, delta)
		points = append(points, nativeSumPoint(map[string]any{"device": device}, start, now, value))
	}
	return nativeSum("system.disk.io_time", "s", "Time disk spent activated.", true, points)
}

func (c *Construct) nativeDiskMerged(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	return c.nativeDiskDirectionalCounter(now, start, top, factor, tickSec,
		"system.disk.merged", "{operations}", "The number of disk reads/writes merged into single physical disk access operations.",
		func(base float64) float64 { return base * 0.03 })
}

func (c *Construct) nativeDiskOperationTime(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	return c.nativeDiskDirectionalCounter(now, start, top, factor, tickSec,
		"system.disk.operation_time", "s", "Time spent in disk operations.",
		func(base float64) float64 { return base * 0.002 })
}

func (c *Construct) nativeDiskOperations(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	return c.nativeDiskDirectionalCounter(now, start, top, factor, tickSec,
		"system.disk.operations", "{operations}", "Disk operations count.",
		func(base float64) float64 { return base * 0.12 })
}

func (c *Construct) nativeDiskDirectionalCounter(
	now, start time.Time,
	top nodeexp.HostTopology,
	factor, tickSec float64,
	name, unit, description string,
	valueFn func(float64) float64,
) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(hostDisks(top))*len(nativeDiskDirections))
	for _, device := range hostDisks(top) {
		for _, direction := range nativeDiskDirections {
			base := tickSec * (1 + factor*30) * (0.8 + hashUnit(c.seed+":otel-disk:"+name+":"+device+":"+direction)*0.4)
			delta := valueFn(base)
			if delta <= 0 {
				delta = 0.001
			}
			key := name + "\x00" + device + "\x00" + direction
			value := c.otlpState.add(key, delta)
			points = append(points, nativeSumPoint(map[string]any{
				"device": device, "direction": direction,
			}, start, now, value))
		}
	}
	return nativeSum(name, unit, description, true, points)
}

func (c *Construct) nativeDiskPending(now, start time.Time, top nodeexp.HostTopology, factor float64) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(hostDisks(top)))
	for _, device := range hostDisks(top) {
		value := 1 + hashUnit(c.seed+":otel-disk-pending:"+device)*3 + factor
		points = append(points, nativeSumPoint(map[string]any{"device": device}, start, now, value))
	}
	return nativeSum("system.disk.pending_operations", "{operations}", "The queue size of pending I/O operations.", false, points)
}

func (c *Construct) nativeDiskWeightedIOTime(now, start time.Time, top nodeexp.HostTopology, factor, tickSec float64) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(hostDisks(top)))
	for _, device := range hostDisks(top) {
		delta := tickSec * (0.05 + factor*0.20) * (0.8 + hashUnit(c.seed+":otel-disk-weighted:"+device)*0.4)
		value := c.otlpState.add("system.disk.weighted_io_time\x00"+device, delta)
		points = append(points, nativeSumPoint(map[string]any{"device": device}, start, now, value))
	}
	return nativeSum("system.disk.weighted_io_time", "s", "Time disk spent activated multiplied by the queue length.", true, points)
}

func (c *Construct) nativeNetworkConnections(now, start time.Time) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(nativeTCPStates))
	for _, state := range nativeTCPStates {
		value := 1 + hashUnit(c.seed+":otel-connection:"+state)*20
		points = append(points, nativeSumPoint(map[string]any{
			"protocol": "tcp", "state": state,
		}, start, now, value))
	}
	return nativeSum("system.network.connections", "{connections}", "The number of connections.", false, points)
}

func (c *Construct) nativeNetworkMetric(
	now, start time.Time,
	top nodeexp.HostTopology,
	factor, tickSec float64,
	name, unit, description string,
	deltaFn func(float64) float64,
) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, len(hostNICs(top))*len(nativeNetworkDirections))
	for _, nic := range hostNICs(top) {
		for _, direction := range nativeNetworkDirections {
			multiplier := 1.0
			if direction == "transmit" {
				multiplier = 0.72
			}
			base := tickSec * (1*1024*1024 + factor*50*1024*1024) * multiplier
			base *= 0.8 + hashUnit(c.seed+":otel-net:"+name+":"+nic.Name+":"+direction)*0.4
			delta := deltaFn(base)
			if delta <= 0 {
				delta = 0.001
			}
			key := name + "\x00" + nic.Name + "\x00" + direction
			value := c.otlpState.add(key, delta)
			points = append(points, nativeSumPoint(map[string]any{
				"device": nic.Name, "direction": direction,
			}, start, now, value))
		}
	}
	return nativeSum(name, unit, description, true, points)
}
