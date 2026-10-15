// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

func dcgmRuntime(t *testing.T, profile, mode string) (*Runner, *coretest.MetricCapture, *blueprint.Resolved) {
	t.Helper()
	b, err := os.ReadFile("../../e2e/fixtures/dcgm-hgx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(b), "profile: stock", "profile: "+profile, 1)
	s = strings.Replace(s, "mode: kubernetes", "mode: "+mode, 1)
	reg := Catalog()
	resolved, err := blueprint.Load([]byte(s), reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := blueprint.ValidateSet([]*blueprint.Resolved{resolved}); err != nil {
		t.Fatal(err)
	}
	capture := &coretest.MetricCapture{}
	r := New(Sinks{Metrics: capture, Logs: &coretest.LogCapture{}}, reg, Options{SendShards: 1, SendCapacity: 20000, SendBatchMax: 1000})
	if err := r.AddBlueprint(resolved); err != nil {
		t.Fatal(err)
	}
	r.ApplyControl(control.State{VolumeMultiplier: 1})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.DrainQueues(ctx)
	})
	return r, capture, resolved
}

func dcgmHeldTime(t *testing.T, top *fixture.GPUTopology) time.Time {
	t.Helper()
	start := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 360; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		a := fixture.GPUAllocationPlan(top, now, nil)
		later := fixture.GPUAllocationPlan(top, now.Add(10*time.Minute), nil)
		if len(a.Allocations) > 0 && a.Allocations[0].State == "held" && later.Allocations[0].State == "held" {
			return now
		}
	}
	t.Fatal("fixture has no stable held interval")
	return time.Time{}
}

func dcgmFrame(c *coretest.MetricCapture, at time.Time) []promrw.Series {
	var out []promrw.Series
	for _, s := range c.All() {
		if s.T.Equal(at) && strings.HasPrefix(s.Name, "DCGM_") {
			out = append(out, s)
		}
	}
	return out
}

func dcgmValue(t *testing.T, c *coretest.MetricCapture, at time.Time, name, uuid string, labels map[string]string) float64 {
	t.Helper()
	for _, s := range dcgmFrame(c, at) {
		if s.Name != name || s.Labels["UUID"] != uuid {
			continue
		}
		match := true
		for k, v := range labels {
			if s.Labels[k] != v {
				match = false
			}
		}
		if match {
			return s.Value
		}
	}
	t.Fatalf("missing %s UUID=%s labels=%v", name, uuid, labels)
	return 0
}

func TestDCGMPinnedInventoriesAndActualKSMJoin(t *testing.T) {
	var expected map[string]struct {
		Types     map[string]string `json:"types"`
		Samples   int               `json:"samples"`
		LabelKeys []string          `json:"label_keys"`
	}
	b, err := os.ReadFile("testdata/dcgm-h100-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &expected); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"stock", "dcp", "fault-detection"} {
		t.Run(profile, func(t *testing.T) {
			r, cap, res := dcgmRuntime(t, profile, "kubernetes")
			at := dcgmHeldTime(t, res.GPU)
			if err := r.RunOnce(context.Background(), at); err != nil {
				t.Fatal(err)
			}
			want := expected[profile]
			if profile == "dcp" {
				want = expected["stock"]
			}
			frame := dcgmFrame(cap, at)
			if len(frame) != want.Samples {
				t.Fatalf("samples %d, captured profile %d", len(frame), want.Samples)
			}
			types := map[string]string{}
			pods := map[string]bool{}
			for _, s := range cap.Find("kube_pod_info") {
				pods[s.Labels["namespace"]+"/"+s.Labels["pod"]] = true
			}
			for _, s := range frame {
				kind := "gauge"
				if s.Kind == promrw.KindCounter {
					kind = "counter"
				} else if s.Kind != promrw.KindGauge {
					t.Fatalf("unexpected kind %v", s.Kind)
				}
				types[s.Name] = kind
				wantKeys := append([]string(nil), expected["stock"].LabelKeys...)
				wantKeys = append(wantKeys, "pod", "namespace", "container")
				if s.Name == "DCGM_EXP_GPU_HEALTH_STATUS" {
					wantKeys = append(wantKeys, "health_watch", "health_error_code", "health_error_severity", "health_error_category")
				}
				if s.Name == "DCGM_EXP_CLOCK_EVENTS_COUNT" || s.Name == "DCGM_EXP_XID_ERRORS_COUNT" {
					wantKeys = append(wantKeys, "window_size_in_ms")
				}
				var gotKeys []string
				for k := range s.Labels {
					gotKeys = append(gotKeys, k)
				}
				sort.Strings(wantKeys)
				sort.Strings(gotKeys)
				if !reflect.DeepEqual(gotKeys, wantKeys) {
					t.Fatalf("%s label keys: got %v want sourced %v", s.Name, gotKeys, wantKeys)
				}
				if !pods[s.Labels["namespace"]+"/"+s.Labels["pod"]] || s.Labels["container"] != "trainer" {
					t.Fatalf("GPU allocation has no real KSM pod join: %+v", s.Labels)
				}
				if _, ok := s.Labels["blueprint"]; ok {
					t.Fatal("substrate DCGM has blueprint label")
				}
			}
			if !reflect.DeepEqual(types, want.Types) {
				t.Fatalf("field types differ from pinned capture: got %v want %v", types, want.Types)
			}
			// Release removes allocation dimensions instead of retaining a stale labelled series.
			released := at
			for i := 1; i <= 360; i++ {
				candidate := at.Add(time.Duration(i) * time.Minute)
				if fixture.GPUAllocationPlan(res.GPU, candidate, nil).Allocations[0].State != "held" {
					released = candidate
					break
				}
			}
			if released.Equal(at) {
				t.Fatal("fixture never releases allocation")
			}
			if err := r.RunOnce(context.Background(), released); err != nil {
				t.Fatal(err)
			}
			for _, s := range dcgmFrame(cap, released) {
				for _, k := range []string{"pod", "namespace", "container"} {
					if _, ok := s.Labels[k]; ok {
						t.Fatalf("released GPU retains %s", k)
					}
				}
			}
		})
	}
	r, cap, res := dcgmRuntime(t, "stock", "host")
	at := dcgmHeldTime(t, res.GPU)
	if err := r.RunOnce(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	for _, s := range dcgmFrame(cap, at) {
		if _, ok := s.Labels["pod"]; ok {
			t.Fatal("host mode added pod attribution")
		}
	}
}

func TestDCGMSharedFaultsAndTemporalCounters(t *testing.T) {
	r, cap, res := dcgmRuntime(t, "fault-detection", "kubernetes")
	at := dcgmHeldTime(t, res.GPU)
	gpu := res.GPU.Nodes[0].GPUs[0]
	if err := r.RunOnce(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	fire := func(f map[string]control.FailureSetting) {
		r.ApplyControl(control.State{VolumeMultiplier: 1, Failures: f})
	}
	fire(map[string]control.FailureSetting{
		"gpu_cooling_fault":            {Enabled: true, Intensity: 1, Scope: res.GPU.Nodes[0].RackKey},
		fixture.GPUPowerConstraintMode: {Enabled: true, Intensity: 1, Scope: gpu.Key},
	})
	t1, t2 := at.Add(time.Minute), at.Add(2*time.Minute)
	if err := r.RunOnce(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	if got := dcgmValue(t, cap, t1, "DCGM_FI_DEV_SM_CLOCK", gpu.UUID, nil); got != 1200 {
		t.Fatalf("simultaneous caps achieved %.0f MHz", got)
	}
	if got := dcgmValue(t, cap, t1, "DCGM_FI_DEV_POWER_VIOLATION", gpu.UUID, nil); got != 0 {
		t.Fatalf("new activation invented prior duration: %.0f", got)
	}
	if err := r.RunOnce(context.Background(), t2); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"DCGM_FI_DEV_POWER_VIOLATION", "DCGM_FI_DEV_THERMAL_VIOLATION"} {
		if got := dcgmValue(t, cap, t2, name, gpu.UUID, nil); got != float64(time.Minute.Nanoseconds()) {
			t.Fatalf("%s=%.0f, want one minute ns", name, got)
		}
	}
	for _, reason := range []string{"sw_thermal", "power_cap"} {
		if got := dcgmValue(t, cap, t2, "DCGM_EXP_CLOCK_EVENTS_TOTAL", gpu.UUID, map[string]string{"clock_event": reason}); got != 1 {
			t.Fatalf("sustained reason counted as edges: %g", got)
		}
		if got := dcgmValue(t, cap, t2, "DCGM_EXP_CLOCK_EVENTS_COUNT", gpu.UUID, map[string]string{"clock_event": reason}); got <= 1 {
			t.Fatal("window observations collapsed into edge count")
		}
	}
	if got := dcgmValue(t, cap, t2, "DCGM_EXP_GPU_HEALTH_STATUS", gpu.UUID, map[string]string{"health_watch": "POWER", "health_error_code": "DCGM_FR_CLOCK_THROTTLE_POWER", "health_error_severity": "MONITOR", "health_error_category": "HARDWARE_POWER"}); got != 10 {
		t.Fatalf("power warning encoding %g", got)
	}
	fire(map[string]control.FailureSetting{
		"gpu_xid_burst":          {Enabled: true, Intensity: 1, Scope: gpu.Key},
		"dcgm_ecc_error_growth":  {Enabled: true, Intensity: 1, Scope: gpu.Key},
		"dcgm_health_failure":    {Enabled: true, Intensity: 1, Scope: gpu.Key},
		"gpu_nvlink_error_burst": {Enabled: true, Intensity: 1, Scope: gpu.Key},
	})
	t3, t4 := at.Add(3*time.Minute), at.Add(4*time.Minute)
	if err := r.RunOnce(context.Background(), t3); err != nil {
		t.Fatal(err)
	}
	if got := dcgmValue(t, cap, t3, "DCGM_FI_DEV_XID_ERRORS", gpu.UUID, map[string]string{"err_code": "48", "err_msg": "Double Bit ECC Error"}); got != 48 {
		t.Fatal("Xid source mismatch")
	}
	if got := dcgmValue(t, cap, t3, "DCGM_EXP_XID_ERRORS_TOTAL", gpu.UUID, map[string]string{"xid": "48"}); got != 3 {
		t.Fatalf("burst total %g", got)
	}
	if got := dcgmValue(t, cap, t3, "DCGM_FI_DEV_UNCORRECTABLE_REMAPPED_ROWS", gpu.UUID, nil); got != 1 {
		t.Fatal("remap collection event absent")
	}
	if err := r.RunOnce(context.Background(), t4); err != nil {
		t.Fatal(err)
	}
	if got := dcgmValue(t, cap, t4, "DCGM_FI_DEV_NVLINK_CRC_ERROR_TOTAL", gpu.UUID, nil); got <= 0 {
		t.Fatal("shared physical link errors absent")
	}
	if got := dcgmValue(t, cap, t4, "DCGM_FI_DEV_ECC_DBE_VOL_TOTAL", gpu.UUID, nil); got != 2 {
		t.Fatalf("ECC accumulation %g", got)
	}
	fire(map[string]control.FailureSetting{"gpu_fallen_off_bus": {Enabled: true, Intensity: 1, Scope: gpu.Key}})
	t5 := at.Add(5 * time.Minute)
	if err := r.RunOnce(context.Background(), t5); err != nil {
		t.Fatal(err)
	}
	for _, s := range dcgmFrame(cap, t5) {
		if s.Labels["UUID"] == gpu.UUID {
			t.Fatal("unavailable GPU still emits series")
		}
	}
	fire(nil)
	t11 := at.Add(11 * time.Minute)
	if err := r.RunOnce(context.Background(), t11); err != nil {
		t.Fatal(err)
	}
	if got := dcgmValue(t, cap, t11, "DCGM_EXP_XID_ERRORS_COUNT", gpu.UUID, nil); got != 0 {
		t.Fatal("expired Xid window retained events")
	}
	if got := dcgmValue(t, cap, t11, "DCGM_EXP_XID_ERRORS_TOTAL", gpu.UUID, map[string]string{"xid": "48"}); got != 6 {
		t.Fatalf("window expiry reset cumulative Xids: %g", got)
	}
	for _, s := range dcgmFrame(cap, t11) {
		if s.Labels["UUID"] == gpu.UUID && s.Name == "DCGM_EXP_CLOCK_EVENTS_COUNT" {
			t.Fatal("valid zero-mask source values must not become a fabricated zero count row")
		}
		if s.Name == "DCGM_EXP_XID_ERRORS_COUNT" {
			if _, ok := s.Labels["xid"]; ok {
				t.Fatal("empty Xid window retains xid label")
			}
		}
	}
}

func TestDCGMFirstObservationPrimesClockTotals(t *testing.T) {
	r, cap, res := dcgmRuntime(t, "fault-detection", "host")
	at := dcgmHeldTime(t, res.GPU)
	gpu := res.GPU.Nodes[0].GPUs[0]
	r.ApplyControl(control.State{VolumeMultiplier: 1, Failures: map[string]control.FailureSetting{
		fixture.GPUPowerConstraintMode: {Enabled: true, Intensity: 1, Scope: gpu.Key},
	}})
	for _, now := range []time.Time{at, at.Add(time.Minute)} {
		if err := r.RunOnce(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		for _, s := range dcgmFrame(cap, now) {
			if s.Name == "DCGM_EXP_CLOCK_EVENTS_TOTAL" {
				t.Fatal("startup-active reason manufactured a transition")
			}
		}
	}
	if got := dcgmValue(t, cap, at, "DCGM_FI_DEV_POWER_VIOLATION", gpu.UUID, nil); got != 0 {
		t.Fatal("first sample invented previous violation time")
	}
	if got := dcgmValue(t, cap, at.Add(time.Minute), "DCGM_FI_DEV_POWER_VIOLATION", gpu.UUID, nil); got != float64(time.Minute.Nanoseconds()) {
		t.Fatal("sustained startup-active reason lost elapsed duration")
	}
}

func TestDCGMRejectsUnsourcedProfilesAtPublicLoader(t *testing.T) {
	b, err := os.ReadFile("../../e2e/fixtures/dcgm-hgx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range [][2]string{{"driver_version: 595.91.07", "driver_version: unknown"}, {"xid_code: 48", "xid_code: 95"}, {"memory_temperature_offset_c: 7", "memory_temperature_offset_c: -1000"}, {"requested_sm_clock_mhz: 1980", "requested_sm_clock_mhz: 1981"}, {"cooling_fault_rise_c: 9", "cooling_fault_rise_c: 10"}, {"bytes: 85019590656", "bytes: 80000000000"}} {
		candidate := strings.Replace(string(b), change[0], change[1], 1)
		res, err := blueprint.Load([]byte(candidate), Catalog())
		if err == nil {
			r := New(Sinks{}, Catalog(), Options{})
			err = r.AddBlueprint(res)
		}
		if err == nil {
			t.Fatalf("unsourced/unsafe change accepted: %s", change[1])
		}
	}
}
