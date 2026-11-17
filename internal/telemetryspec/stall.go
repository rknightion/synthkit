// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"fmt"
	"strings"
)

// BaseStallMode is the incident mode that stalls the metric's value (a now gauge or a
// source_read counter) for every series without its own enum stall_mode; "" when it declares none.
func (v ValueModel) BaseStallMode() string {
	switch {
	case v.Now != nil:
		return v.Now.StallMode
	case v.SourceRead != nil:
		return v.SourceRead.StallMode
	}
	return ""
}

// SeriesStallMode returns the stall mode for the series carrying labels: the stall_mode of the
// enum value the series carries on the one label that declares them, else the metric's base mode.
func (m MetricSpec) SeriesStallMode(labels map[string]string) string {
	for _, k := range sortedKeys(m.Labels) {
		for _, e := range m.Labels[k].Enum {
			if e.StallMode != "" && labels[k] == e.Value {
				return e.StallMode
			}
		}
	}
	return m.Value.BaseStallMode()
}

// HasSeriesStall reports whether any enum label value declares its own stall_mode.
func (m MetricSpec) HasSeriesStall() bool {
	for _, vm := range m.Labels {
		for _, e := range vm.Enum {
			if e.StallMode != "" {
				return true
			}
		}
	}
	return false
}

// validateStall enforces the stall rules that need the owning metric: source_read is a counter,
// read_failures has a stall mode to fail in, and an enum value's own stall_mode sits on one label
// of a metric whose value can stall.
func (m MetricSpec) validateStall() error {
	if m.Value.SourceRead != nil {
		if m.Instrument != InstrumentCounter {
			return fmt.Errorf("metric %q: source_read is valid on a counter value only", m.Name)
		}
		if m.Value.SourceRead.Measure == ReadFailures && m.Value.BaseStallMode() == "" && !m.HasSeriesStall() {
			return fmt.Errorf("metric %q: source_read read_failures needs a stall_mode (on the value or on an enum label value)", m.Name)
		}
	}
	stalling := ""
	for _, k := range sortedKeys(m.Labels) {
		for _, e := range m.Labels[k].Enum {
			if e.StallMode == "" {
				continue
			}
			if m.Value.Now == nil && m.Value.SourceRead == nil {
				return fmt.Errorf("metric %q label %q value %q: stall_mode is valid only with a now or source_read value", m.Name, k, e.Value)
			}
			if strings.TrimSpace(e.StallMode) != e.StallMode {
				return fmt.Errorf("metric %q label %q value %q: stall_mode %q must be a nonblank incident mode name without surrounding whitespace", m.Name, k, e.Value, e.StallMode)
			}
			if stalling != "" && stalling != k {
				return fmt.Errorf("metric %q: enum stall_mode may be declared on one label only (found %q and %q)", m.Name, stalling, k)
			}
			stalling = k
		}
	}
	return nil
}
