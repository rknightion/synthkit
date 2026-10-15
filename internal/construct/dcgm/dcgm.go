// SPDX-License-Identifier: AGPL-3.0-only

// Package dcgm renders the pinned H100 SXM HGX exporter contract in signals/dcgm.md.
package dcgm

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

type Config struct {
	Profile                    string   `yaml:"profile"`                       // stock (default), dcp, or fault-detection; H100 HGX only.
	Mode                       string   `yaml:"mode"`                          // host (default) or kubernetes; allocation labels are conditional.
	MemoryTemperatureOffsetC   *float64 `yaml:"memory_temperature_offset_c"`   // Explicit synthetic HBM/core offset, not a vendor temperature limit.
	TensorActivityFraction     float64  `yaml:"tensor_activity_fraction"`      // Workload fraction using tensor pipes, 0..1.
	MemoryCopyActivityFraction float64  `yaml:"memory_copy_activity_fraction"` // Workload fraction exercising device-memory copies, 0..1.
	EncoderActivityFraction    float64  `yaml:"encoder_activity_fraction"`     // Workload fraction exercising encoder engines, 0..1.
	DecoderActivityFraction    float64  `yaml:"decoder_activity_fraction"`     // Workload fraction exercising decoder engines, 0..1.
	PCIERxBytesPerSecond       float64  `yaml:"pcie_rx_bytes_per_second"`      // Declared traffic at full workload utilization; not a hardware bandwidth claim.
	PCIETxBytesPerSecond       float64  `yaml:"pcie_tx_bytes_per_second"`      // Declared traffic at full workload utilization.
	NVLinkMBPerSecond          float64  `yaml:"nvlink_mb_per_second"`          // Declared aggregate MB/s at full workload utilization.
	WindowSeconds              int      `yaml:"window_seconds"`                // Exporter event window; default 300 seconds from the pinned capture.
	CollectionIntervalMS       int      `yaml:"collection_interval_ms"`        // Modeled field sampling interval; default 5000 ms from the pinned capture.
	XidCode                    int      `yaml:"xid_code"`                      // Explicit sourced nonfatal event code used by gpu_xid_burst; default 48.
	XidEventsPerObservation    int      `yaml:"xid_events_per_observation"`    // Explicit burst size per active emitter observation; default 1.
	ECCErrorsPerObservation    int      `yaml:"ecc_errors_per_observation"`    // Synthetic DBE events during dcgm_ecc_error_growth; default 1.
	RemappedRowsPerObservation int      `yaml:"remapped_rows_per_observation"` // Synthetic uncorrectable remapped rows during that event; default 1, not a vendor conversion ratio.
}

type gpuState struct {
	clockSeen                                        bool
	last                                             time.Time
	available                                        bool
	power, nvErrors                                  float64
	thermal, powerLimited                            bool
	energy, thermalNS, powerNS, nvCRC, ecc, remapped float64
	clockTotals                                      map[string]float64
	xidTotal                                         float64
	lastXid                                          int
	clocks                                           []clockSample
	xids                                             []xidSample
}
type clockSample struct {
	at             time.Time
	thermal, power bool
	valid          bool
}
type xidSample struct {
	at    time.Time
	count float64
}

type Construct struct {
	cfg                Config
	selection          *fixture.GPUSelection
	states             map[string]*gpuState
	window, collection time.Duration
}

func New(raw any, fx *fixture.Set) (core.Construct, error) {
	cfg, ok := raw.(*Config)
	if !ok || cfg == nil || fx == nil || fx.GPU == nil {
		return nil, fmt.Errorf("dcgm: GPU selection and config required")
	}
	c := &Construct{cfg: *cfg, selection: fx.GPU, states: map[string]*gpuState{}}
	if len(fx.GPU.Nodes()) == 0 {
		return nil, fmt.Errorf("dcgm: nonempty compute pool selection required")
	}
	if c.cfg.Profile == "" {
		c.cfg.Profile = "stock"
	}
	if c.cfg.Mode == "" {
		c.cfg.Mode = "host"
	}
	if c.cfg.Profile != "stock" && c.cfg.Profile != "dcp" && c.cfg.Profile != "fault-detection" {
		return nil, fmt.Errorf("dcgm: unsupported profile")
	}
	if c.cfg.Mode != "host" && c.cfg.Mode != "kubernetes" {
		return nil, fmt.Errorf("dcgm: unsupported mode")
	}
	if c.cfg.MemoryTemperatureOffsetC == nil || !finite(*c.cfg.MemoryTemperatureOffsetC) {
		return nil, fmt.Errorf("dcgm: explicit finite memory-temperature offset required")
	}
	for _, f := range []float64{cfg.TensorActivityFraction, cfg.MemoryCopyActivityFraction, cfg.EncoderActivityFraction, cfg.DecoderActivityFraction} {
		if !finite(f) || f < 0 || f > 1 {
			return nil, fmt.Errorf("dcgm: activity fractions must be 0..1")
		}
	}
	for _, rate := range []float64{cfg.PCIERxBytesPerSecond, cfg.PCIETxBytesPerSecond, cfg.NVLinkMBPerSecond} {
		if !finite(rate) || rate < 0 {
			return nil, fmt.Errorf("dcgm: traffic rates must be finite and nonnegative")
		}
	}
	if c.cfg.WindowSeconds == 0 {
		c.cfg.WindowSeconds = 300
	}
	if c.cfg.CollectionIntervalMS == 0 {
		c.cfg.CollectionIntervalMS = 5000
	}
	if c.cfg.WindowSeconds < 1 || c.cfg.WindowSeconds > 3600 || c.cfg.CollectionIntervalMS < 1000 || c.cfg.CollectionIntervalMS > 60000 || c.cfg.CollectionIntervalMS > c.cfg.WindowSeconds*1000 {
		return nil, fmt.Errorf("dcgm: invalid bounded collection/window interval")
	}
	c.window = time.Duration(c.cfg.WindowSeconds) * time.Second
	c.collection = time.Duration(c.cfg.CollectionIntervalMS) * time.Millisecond
	if c.cfg.XidCode == 0 {
		c.cfg.XidCode = 48
	}
	if _, ok := xidMessages[c.cfg.XidCode]; !ok {
		return nil, fmt.Errorf("dcgm: supported modeled Xid codes are 48, 63, 64 and 74")
	}
	if c.cfg.XidEventsPerObservation == 0 {
		c.cfg.XidEventsPerObservation = 1
	}
	if c.cfg.ECCErrorsPerObservation == 0 {
		c.cfg.ECCErrorsPerObservation = 1
	}
	if c.cfg.RemappedRowsPerObservation == 0 {
		c.cfg.RemappedRowsPerObservation = 1
	}
	for _, n := range []int{c.cfg.XidEventsPerObservation, c.cfg.ECCErrorsPerObservation, c.cfg.RemappedRowsPerObservation} {
		if n < 1 || n > 10000 {
			return nil, fmt.Errorf("dcgm: event count outside synthetic safety bound")
		}
	}
	if err := fixture.RequireGPUCapabilities(fx.GPU, fixture.GPUCapabilities{UsableFramebuffer: true, NVLinkCorrelation: true}); err != nil {
		return nil, fmt.Errorf("dcgm: %w", err)
	}
	for _, node := range fx.GPU.Nodes() {
		if node.Operating.Constraints == nil {
			return nil, fmt.Errorf("dcgm: shared operating.constraints declaration required")
		}
		if err := fixture.RequireGPUFaultProfile(fx.GPU, node.Operating.Constraints.DriverVersion); err != nil {
			return nil, fmt.Errorf("dcgm: %w", err)
		}
		if node.Operating.AmbientTempC+node.Operating.FullPowerRiseC*node.Operating.IdlePowerFraction+*c.cfg.MemoryTemperatureOffsetC < -273.15 {
			return nil, fmt.Errorf("dcgm: declared memory temperature is below absolute zero")
		}
		if c.cfg.Mode == "kubernetes" && node.KubernetesCluster == "" {
			return nil, fmt.Errorf("dcgm: kubernetes mode requires bound compute nodes")
		}
		for _, gpu := range node.GPUs {
			if gpu.UsableMemory.Bytes != 81081*1024*1024 {
				return nil, fmt.Errorf("dcgm: framebuffer differs from captured H100 profile")
			}
			c.states[gpu.Key] = &gpuState{clockTotals: map[string]float64{}}
		}
	}
	return c, nil
}

func (c *Construct) Kind() string                { return "dcgm" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return time.Minute }

func FailureModes() []failuremode.Mode {
	m := append(fixture.GPUFailureModes("dcgm"), fixture.GPUConstraintFailureModes()...)
	return append(m,
		failuremode.Mode{Name: "dcgm_ecc_error_growth", Axis: fixture.AxisGPU, Help: "Declared ECC and row-remap collection events"},
		failuremode.Mode{Name: "dcgm_health_failure", Axis: fixture.AxisGPU, Help: "Source-supported memory health failure"},
	)
}

func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	if w == nil || w.Metrics == nil {
		return nil
	}
	eval := func(at time.Time, mode, key string) (bool, float64) {
		if w.Shape == nil {
			return false, 0
		}
		return w.Shape.Eval(at, mode, key)
	}
	a := fixture.GPUAllocationPlan(c.selection.Topology, now, eval)
	physical, err := fixture.GPUOperatingPoints(c.selection.Topology, a)
	if err != nil {
		return err
	}
	demands, err := fixture.GPUConstraintDemands(c.selection.Topology, a, eval)
	if err != nil {
		return err
	}
	constraints, err := fixture.GPUConstraintPoints(c.selection.Topology, a, demands)
	if err != nil {
		return err
	}
	byPhysical := map[string]fixture.GPUOperatingPoint{}
	byConstraint := map[string]fixture.GPUConstraintPoint{}
	byAllocation := map[string]fixture.GPUAllocation{}
	nvRates := map[string]float64{}
	for _, p := range physical.GPUs {
		byPhysical[p.GPUKey] = p
	}
	for _, p := range constraints.GPUs {
		byConstraint[p.GPUKey] = p
	}
	for _, p := range a.Allocations {
		byAllocation[p.GPUKey] = p
	}
	for _, p := range physical.NVLinkErrors {
		nvRates[p.GPUKey] += p.ErrorsPerSecond
	}
	var out []promrw.Series
	for _, node := range c.selection.Nodes() {
		for _, gpu := range node.GPUs {
			st := c.states[gpu.Key]
			p := byPhysical[gpu.Key]
			cp := byConstraint[gpu.Key]
			if !st.last.IsZero() && now.Before(st.last) {
				return fmt.Errorf("dcgm: observation time moved backwards")
			}
			fresh := st.last.IsZero() || now.After(st.last)
			if fresh {
				st.advance(now, c.collection, c.window)
			}
			if !p.Available || cp.Achieved == nil {
				st.last, st.available = now, false
				continue
			}
			if fresh {
				if !st.last.IsZero() {
					if cp.ThermalActive && !st.thermal {
						st.clockTotals["sw_thermal"]++
					}
					if cp.PowerActive && !st.powerLimited {
						st.clockTotals["power_cap"]++
					}
				}
				st.clockSeen = st.clockSeen || cp.ThermalActive || cp.PowerActive
				st.clocks = append(st.clocks, clockSample{now, cp.ThermalActive, cp.PowerActive, st.clockSeen})
				if sharedXid(c.selection.Topology, a, gpu.Key) {
					count := float64(c.cfg.XidEventsPerObservation)
					st.lastXid = c.cfg.XidCode
					st.xidTotal += count
					st.xids = append(st.xids, xidSample{now, count})
				}
				if on, i := eval(now, "dcgm_ecc_error_growth", gpu.Key); on && i > 0 {
					st.ecc += float64(c.cfg.ECCErrorsPerObservation)
					st.remapped += float64(c.cfg.RemappedRowsPerObservation)
				}
			}
			st.last, st.available = now, true
			st.power, st.nvErrors = p.PowerW, nvRates[gpu.Key]
			st.thermal, st.powerLimited = cp.ThermalActive, cp.PowerActive
			labels := map[string]string{"gpu": strconv.Itoa(gpu.Minor), "UUID": gpu.UUID, "pci_bus_id": gpu.PCIBusID, "device": "nvidia" + strconv.Itoa(gpu.Minor), "modelName": "NVIDIA H100 80GB HBM3", "hostname": node.Node.Hostname, "DCGM_FI_DRIVER_VERSION": node.Operating.Constraints.DriverVersion}
			allocation := byAllocation[gpu.Key]
			if c.cfg.Mode == "kubernetes" && allocation.State == "held" && allocation.Pod != nil {
				labels["pod"], labels["namespace"], labels["container"] = allocation.Pod.Pod, allocation.Pod.Namespace, allocation.Pod.Container
			}
			healthFailure, _ := eval(now, "dcgm_health_failure", gpu.Key)
			out = c.render(out, now, labels, p, cp, st, healthFailure)
		}
	}
	return w.Metrics.Write(ctx, out)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
