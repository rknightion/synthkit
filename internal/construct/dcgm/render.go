// SPDX-License-Identifier: AGPL-3.0-only

package dcgm

import (
	"maps"
	"strconv"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// Exact exporter 4.8.4 xid_errors.go strings, restricted to sourced H100 events.
// No thermal/power cause is translated into a guessed Xid.
var xidMessages = map[int]string{
	48: "Double Bit ECC Error",
	63: "ECC page retirement or row remapping recording event",
	64: "ECC page retirement or row remapper recording failure",
	74: "NVLINK Error",
}

func sharedXid(top *fixture.GPUTopology, a fixture.GPUSnapshot, key string) bool {
	parents := top.GPUParentTargets(key)
	for _, f := range a.Faults {
		if f.Mode != "gpu_xid_burst" {
			continue
		}
		if f.Target == key {
			return true
		}
		for _, parent := range parents {
			if parent == f.Target {
				return true
			}
		}
	}
	return false
}

func (s *gpuState) advance(now time.Time, collection, window time.Duration) {
	if !s.last.IsZero() && s.available {
		elapsed := now.Sub(s.last)
		s.energy += s.power * elapsed.Seconds() * 1000 // DCGM mJ, not J.
		s.nvCRC += s.nvErrors * elapsed.Seconds()
		if s.thermal {
			s.thermalNS += float64(elapsed.Nanoseconds())
		}
		if s.powerLimited {
			s.powerNS += float64(elapsed.Nanoseconds())
		}
		// Piecewise-constant synthetic device state between observations. Sampling
		// is independent of emission; never count a sustained reason as new edges.
		start := s.last.Add(collection)
		cut := now.Add(-window)
		if start.Before(cut) {
			start = start.Add(((cut.Sub(start) + collection - 1) / collection) * collection)
		}
		for at := start; at.Before(now); at = at.Add(collection) {
			s.clocks = append(s.clocks, clockSample{at, s.thermal, s.powerLimited, s.clockSeen})
		}
	}
	cut := now.Add(-window)
	i := 0
	for i < len(s.clocks) && s.clocks[i].at.Before(cut) {
		i++
	}
	s.clocks = append([]clockSample(nil), s.clocks[i:]...)
	i = 0
	for i < len(s.xids) && s.xids[i].at.Before(cut) {
		i++
	}
	s.xids = append([]xidSample(nil), s.xids[i:]...)
}

func (c *Construct) render(out []promrw.Series, now time.Time, base map[string]string, p fixture.GPUOperatingPoint, cp fixture.GPUConstraintPoint, st *gpuState, healthFailure bool) []promrw.Series {
	add := func(name string, value float64, kind promrw.Kind, extra map[string]string) {
		labels := base
		if len(extra) > 0 {
			labels = maps.Clone(base)
			maps.Copy(labels, extra)
		}
		out = append(out, promrw.Series{Name: name, Labels: labels, Value: value, T: now, Kind: kind})
	}
	gauge := func(name string, value float64) { add(name, value, promrw.KindGauge, nil) }
	counter := func(name string, value float64) { add(name, value, promrw.KindCounter, nil) }
	const mib = 1024 * 1024
	gauge("DCGM_FI_DEV_FB_FREE", float64(p.MemoryAccountingLimitBytes-p.MemoryUsedBytes)/mib)
	gauge("DCGM_FI_DEV_FB_USED", float64(p.MemoryUsedBytes)/mib)
	gauge("DCGM_FI_DEV_FB_RESERVED", 478) // Captured exact product/driver profile.
	gauge("DCGM_FI_DEV_GPU_TEMP", p.TemperatureC)
	gauge("DCGM_FI_DEV_MEMORY_TEMP", p.TemperatureC+*c.cfg.MemoryTemperatureOffsetC)
	gauge("DCGM_FI_DEV_GPU_UTIL", 100*p.Utilization)
	gauge("DCGM_FI_DEV_MEM_COPY_UTIL", 100*p.Utilization*c.cfg.MemoryCopyActivityFraction)
	gauge("DCGM_FI_DEV_ENC_UTIL", 100*p.Utilization*c.cfg.EncoderActivityFraction)
	gauge("DCGM_FI_DEV_DEC_UTIL", 100*p.Utilization*c.cfg.DecoderActivityFraction)
	gauge("DCGM_FI_DEV_SM_CLOCK", cp.Achieved.SM)
	gauge("DCGM_FI_DEV_MEM_CLOCK", cp.Achieved.Memory)
	gauge("DCGM_FI_DEV_POWER_USAGE", p.PowerW)
	counter("DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION", st.energy)
	counter("DCGM_FI_DEV_PCIE_REPLAY_COUNTER", 0)       // No PCIe-error event declared.
	counter("DCGM_FI_DEV_CORRECTABLE_REMAPPED_ROWS", 0) // Selected collection events model DBE/uncorrectable rows only.
	counter("DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS", st.remapped)
	gauge("DCGM_FI_DEV_ROW_REMAP_FAILURE", 0)   // A recorded remap is not a remap failure.
	gauge("DCGM_FI_DEV_VGPU_LICENSE_STATUS", 0) // Bare HGX profile, captured vGPU license encoding.
	gauge("DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL", p.Utilization*c.cfg.NVLinkMBPerSecond)
	gauge("DCGM_FI_PROF_GR_ENGINE_ACTIVE", p.Utilization)
	gauge("DCGM_FI_PROF_PIPE_TENSOR_ACTIVE", p.Utilization*c.cfg.TensorActivityFraction)
	gauge("DCGM_FI_PROF_DRAM_ACTIVE", p.Utilization*c.cfg.MemoryCopyActivityFraction)
	gauge("DCGM_FI_PROF_PCIE_RX_BYTES", p.Utilization*c.cfg.PCIERxBytesPerSecond)
	gauge("DCGM_FI_PROF_PCIE_TX_BYTES", p.Utilization*c.cfg.PCIETxBytesPerSecond)
	if st.lastXid != 0 {
		add("DCGM_FI_DEV_XID_ERRORS", float64(st.lastXid), promrw.KindGauge, map[string]string{"err_code": strconv.Itoa(st.lastXid), "err_msg": xidMessages[st.lastXid]})
	}
	if c.cfg.Profile != "fault-detection" {
		return out
	}
	counter("DCGM_FI_DEV_POWER_VIOLATION", st.powerNS)
	counter("DCGM_FI_DEV_THERMAL_VIOLATION", st.thermalNS)
	counter("DCGM_FI_DEV_ECC_SBE_VOL_TOTAL", 0)
	counter("DCGM_FI_DEV_ECC_SBE_AGG_TOTAL", 0)
	counter("DCGM_FI_DEV_ECC_DBE_VOL_TOTAL", st.ecc)
	counter("DCGM_FI_DEV_ECC_DBE_AGG_TOTAL", st.ecc)
	counter("DCGM_FI_DEV_NVLINK_CRC_ERROR_TOTAL", st.nvCRC)
	counter("DCGM_FI_DEV_NVLINK_RECOVERY_TOTAL", 0)
	counter("DCGM_FI_DEV_NVLINK_REPLAY_TOTAL", 0)
	windowLabel := strconv.Itoa(c.cfg.WindowSeconds * 1000)
	counts := map[string]float64{}
	hasClockValues := false
	for _, sample := range st.clocks {
		hasClockValues = hasClockValues || sample.valid
		if sample.thermal {
			counts["sw_thermal"]++
		}
		if sample.power {
			counts["power_cap"]++
		}
	}
	if len(counts) == 0 && !hasClockValues {
		add("DCGM_EXP_CLOCK_EVENTS_COUNT", 0, promrw.KindGauge, map[string]string{"window_size_in_ms": windowLabel})
	}
	for _, reason := range []string{"sw_thermal", "power_cap"} {
		if n := counts[reason]; n > 0 {
			add("DCGM_EXP_CLOCK_EVENTS_COUNT", n, promrw.KindGauge, map[string]string{"window_size_in_ms": windowLabel, "clock_event": reason})
		}
		if n := st.clockTotals[reason]; n > 0 {
			add("DCGM_EXP_CLOCK_EVENTS_TOTAL", n, promrw.KindCounter, map[string]string{"clock_event": reason})
		}
	}
	xids := 0.0
	for _, event := range st.xids {
		xids += event.count
	}
	xidLabels := map[string]string{"window_size_in_ms": windowLabel}
	if xids > 0 {
		xidLabels["xid"] = strconv.Itoa(c.cfg.XidCode)
	}
	add("DCGM_EXP_XID_ERRORS_COUNT", xids, promrw.KindGauge, xidLabels)
	if st.xidTotal > 0 {
		add("DCGM_EXP_XID_ERRORS_TOTAL", st.xidTotal, promrw.KindCounter, map[string]string{"xid": strconv.Itoa(c.cfg.XidCode)})
	}
	for _, watch := range []string{"PCIE", "NVLINK", "PMU", "MCU", "MEM", "SM", "INFOROM", "THERMAL", "POWER", "DRIVER", "ALL"} {
		code, severity, category, value := "DCGM_FR_OK", "NONE", "NONE", 0.0
		switch {
		case watch == "POWER" && cp.PowerActive:
			code, severity, category, value = "DCGM_FR_CLOCK_THROTTLE_POWER", "MONITOR", "HARDWARE_POWER", 10
		case watch == "THERMAL" && cp.ThermalActive:
			code, severity, category, value = "DCGM_FR_CLOCK_THROTTLE_THERMAL", "MONITOR", "HARDWARE_THERMAL", 10
		case watch == "MEM" && (healthFailure || st.ecc > 0):
			code, severity, category, value = "DCGM_FR_DBE_VIOLATION", "ISOLATE", "HARDWARE_MEMORY", 20
		case watch == "NVLINK" && st.nvErrors > 0:
			code, severity, category, value = "DCGM_FR_NVLINK_ERROR_THRESHOLD", "ISOLATE", "HARDWARE_NVLINK", 20
		}
		add("DCGM_EXP_GPU_HEALTH_STATUS", value, promrw.KindGauge, map[string]string{"health_watch": watch, "health_error_code": code, "health_error_severity": severity, "health_error_category": category})
	}
	return out
}
