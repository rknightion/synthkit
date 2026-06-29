// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// ── registration meta ─────────────────────────────────────────────────────────

func TestRegistration_DeclaresFailureModes(t *testing.T) {
	reg := Registration()
	if len(reg.FailureModes) != 5 {
		t.Fatalf("want 5 failure modes, got %d", len(reg.FailureModes))
	}
	for _, m := range reg.FailureModes {
		if m.Axis != failuremode.AxisNetwork {
			t.Errorf("%s: axis %q want network", m.Name, m.Axis)
		}
	}
}

// makeTestConstruct builds a Construct directly from hand-crafted fixtures.
// Same-package access because Construct fields are unexported.
func makeTestConstruct() *Construct {
	return &Construct{
		rc: resolvedConfig{
			job:       "j",
			instance:  "i",
			protocols: []string{ProtoLLDP, ProtoBGP},
			protoSet:  map[string]bool{ProtoLLDP: true, ProtoBGP: true},
			oosCount:  2,
		},
		graph: Graph{
			Devices: []Device{
				{ID: "spine-01", Vendor: "arista", OSVersion: "4.36.0F", Site: "dc1",
					UptimeBaseSecs: UptimeBaseSeconds("spine-01", "")},
			},
			Edges: []Edge{
				{
					SrcDevice: "spine-01",
					SrcPort:   "Ethernet1",
					DstDevice: "leaf-01",
					DstPort:   "Ethernet1",
					Proto:     ProtoLLDP,
					LinkKind:  LinkEthernet,
					Direction: DirBidirectional,
				},
			},
		},
		st: state.NewState(),
	}
}

// findSeries returns all series with the given metric name.
func findSeries(series []promrw.Series, name string) []promrw.Series {
	var out []promrw.Series
	for _, s := range series {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// findSeriesLabeled returns the first series matching name AND label key=val.
func findSeriesLabeled(series []promrw.Series, name, key, val string) (promrw.Series, bool) {
	for _, s := range series {
		if s.Name == name && s.Labels[key] == val {
			return s, true
		}
	}
	return promrw.Series{}, false
}

// sumByName returns the sum of all series values with the given name.
func sumByName(series []promrw.Series, name string) float64 {
	var total float64
	for _, s := range series {
		if s.Name == name {
			total += s.Value
		}
	}
	return total
}

// firstValueByName returns the first value for the named metric and whether it was found.
func firstValueByName(series []promrw.Series, name string) (float64, bool) {
	for _, s := range series {
		if s.Name == name {
			return s.Value, true
		}
	}
	return 0, false
}

func noon() time.Time {
	return time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
}

// ── device_info model omission ────────────────────────────────────────────────

func TestDeviceInfo_OmitsModelLabel(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(time.Unix(1_750_000_000, 0), shape.New("", nil))
	series := c.st.Collect(time.Unix(1_750_000_000, 0))

	di, ok := findSeriesLabeled(series, "network_topology_device_info", "device_id", "spine-01")
	if !ok {
		t.Fatal("no device_info series for spine-01")
	}
	if _, present := di.Labels["model"]; present {
		t.Errorf("device_info must omit model label, got labels %v", di.Labels)
	}
	for _, want := range []string{"job", "instance", "device_id", "vendor", "os_version", "site"} {
		if _, ok := di.Labels[want]; !ok {
			t.Errorf("device_info missing required label %q", want)
		}
	}
}

// ── device_info ───────────────────────────────────────────────────────────────

func TestBuildData_DeviceInfo_Present(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	matches := findSeries(series, "network_topology_device_info")
	if len(matches) == 0 {
		t.Fatal("no network_topology_device_info series found")
	}
	s := matches[0]
	if s.Value != 1.0 {
		t.Errorf("device_info value = %v, want 1.0", s.Value)
	}
}

func TestBuildData_DeviceInfo_Labels(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	s, ok := findSeriesLabeled(series, "network_topology_device_info", "device_id", "spine-01")
	if !ok {
		t.Fatal("no network_topology_device_info{device_id=spine-01}")
	}
	want := map[string]string{
		"job":        "j",
		"instance":   "i",
		"device_id":  "spine-01",
		"vendor":     "arista",
		"os_version": "4.36.0F",
		"site":       "dc1",
	}
	for k, v := range want {
		if got := s.Labels[k]; got != v {
			t.Errorf("device_info label %q = %q, want %q", k, got, v)
		}
	}
}

// ── edge_info ─────────────────────────────────────────────────────────────────

func TestBuildData_EdgeInfo_Present(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	matches := findSeries(series, "network_topology_edge_info")
	if len(matches) == 0 {
		t.Fatal("no network_topology_edge_info series found")
	}
	s := matches[0]
	if s.Value != 1.0 {
		t.Errorf("edge_info value = %v, want 1.0", s.Value)
	}
}

func TestBuildData_EdgeInfo_LabelSchemaStability(t *testing.T) {
	// Guards against accidental cardinality creep: asserts the label key-set is EXACTLY
	// the 9 keys specified in the contract (job + instance + 7 edge keys).
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	wantSet := map[string]bool{
		"job": true, "instance": true,
		"src_device": true, "src_port": true,
		"dst_device": true, "dst_port": true,
		"discovery_proto": true, "link_kind": true, "direction": true,
	}

	for _, s := range findSeries(series, "network_topology_edge_info") {
		got := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			got = append(got, k)
		}
		sort.Strings(got)

		for _, k := range got {
			if !wantSet[k] {
				t.Errorf("edge_info has unexpected label %q (cardinality creep)", k)
			}
		}
		for k := range wantSet {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("edge_info missing required label %q", k)
			}
		}
		if len(s.Labels) != len(wantSet) {
			t.Errorf("edge_info label count = %d, want %d; keys: %v", len(s.Labels), len(wantSet), got)
		}
	}
}

func TestBuildData_EdgeInfo_Values(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	s, ok := findSeriesLabeled(series, "network_topology_edge_info", "src_device", "spine-01")
	if !ok {
		t.Fatal("no network_topology_edge_info{src_device=spine-01}")
	}
	checks := map[string]string{
		"dst_device":      "leaf-01",
		"src_port":        "Ethernet1",
		"dst_port":        "Ethernet1",
		"discovery_proto": ProtoLLDP,
		"link_kind":       LinkEthernet,
		"direction":       DirBidirectional,
	}
	for k, v := range checks {
		if got := s.Labels[k]; got != v {
			t.Errorf("edge_info label %q = %q, want %q", k, got, v)
		}
	}
}

// ── graph totals ──────────────────────────────────────────────────────────────

func TestBuildData_GraphTotals(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	tests := []struct {
		name string
		want float64
	}{
		{"network_topology_graph_devices_total", float64(len(c.graph.Devices))},
		{"network_topology_graph_edges_total", float64(len(c.graph.Edges))},
		{"network_topology_out_of_scope_neighbours_total", float64(c.rc.oosCount)},
	}
	for _, tt := range tests {
		v, ok := firstValueByName(series, tt.name)
		if !ok {
			t.Errorf("missing %s", tt.name)
			continue
		}
		if v != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, v, tt.want)
		}
	}
}

// ── device uptime ─────────────────────────────────────────────────────────────

func TestBuildData_DeviceUptime_PositiveAndGrows(t *testing.T) {
	eng := shape.New("", nil)
	now1 := noon()
	now2 := now1.Add(1 * time.Hour)

	// Use a single construct ticked twice: uptime must be > 0 after the first tick
	// and must grow after the second tick.
	c := makeTestConstruct()
	c.buildData(now1, eng)
	s1 := c.st.Collect(now1)
	u1, ok := findSeriesLabeled(s1, "network_topology_device_uptime_seconds", "device_id", "spine-01")
	if !ok {
		t.Fatal("missing device_uptime_seconds at t1")
	}
	if u1.Value <= 0 {
		t.Errorf("uptime at t1 = %v, want > 0", u1.Value)
	}

	c.buildData(now2, eng)
	s2 := c.st.Collect(now2)
	u2, ok := findSeriesLabeled(s2, "network_topology_device_uptime_seconds", "device_id", "spine-01")
	if !ok {
		t.Fatal("missing device_uptime_seconds at t2")
	}
	if u2.Value <= u1.Value {
		t.Errorf("uptime should grow: t1=%v t2=%v", u1.Value, u2.Value)
	}
}

// ── change counter ────────────────────────────────────────────────────────────

func TestBuildData_ChangeCounter_AccumulatesAcrossTicks(t *testing.T) {
	c := makeTestConstruct()
	eng := shape.New("", nil)

	// Tick 1
	c.buildData(noon(), eng)
	after1 := sumByName(c.st.Collect(noon()), "network_topology_change_total")

	// Tick 2
	now2 := noon().Add(60 * time.Second)
	c.buildData(now2, eng)
	after2 := sumByName(c.st.Collect(now2), "network_topology_change_total")

	if after2 < after1 {
		t.Errorf("change_total should never decrease: tick1=%v tick2=%v", after1, after2)
	}
	// After 2 ticks the cumulative counter must be > 0
	if after2 <= 0 {
		t.Errorf("change_total after 2 ticks = %v, want > 0", after2)
	}
}

func TestBuildData_ChangeCounter_HasChangekindLabel(t *testing.T) {
	c := makeTestConstruct()
	c.buildData(noon(), shape.New("", nil))
	series := c.st.Collect(noon())

	validKinds := map[string]bool{"added": true, "removed": true, "updated": true}
	for _, s := range findSeries(series, "network_topology_change_total") {
		ck := s.Labels["change_kind"]
		if !validKinds[ck] {
			t.Errorf("change_total change_kind = %q, want one of added|removed|updated", ck)
		}
		if s.Labels["discovery_proto"] == "" {
			t.Errorf("change_total missing discovery_proto label")
		}
	}
}

// ── conflict counter ──────────────────────────────────────────────────────────

func TestBuildData_ConflictCounter_Present(t *testing.T) {
	c := makeTestConstruct()
	eng := shape.New("", nil)

	// Run many ticks to ensure the conflict counter gets registered at least once.
	for i := range 50 {
		c.buildData(noon().Add(time.Duration(i)*60*time.Second), eng)
	}
	series := c.st.Collect(noon().Add(50 * 60 * time.Second))

	matches := findSeries(series, "network_topology_conflict_total")
	if len(matches) == 0 {
		t.Fatal("missing network_topology_conflict_total series")
	}
	for _, s := range matches {
		if s.Labels["conflict_type"] != "neighbour_disagreement" {
			t.Errorf("conflict_total conflict_type = %q, want neighbour_disagreement", s.Labels["conflict_type"])
		}
	}
}

// ── log streams ───────────────────────────────────────────────────────────────

func TestBuildData_Streams_NoHighCardLabels(t *testing.T) {
	c := makeTestConstruct()
	streams := c.buildData(noon(), shape.New("", nil))

	highCard := map[string]bool{
		"device_id": true, "src_device": true, "dst_device": true,
		"src_port": true, "dst_port": true,
	}
	for _, st := range streams {
		for k := range st.Labels {
			if highCard[k] {
				t.Errorf("stream label contains high-cardinality key %q; must be in Body/Meta", k)
			}
		}
	}
}

func TestBuildData_Streams_ChangeStreamPresent(t *testing.T) {
	c := makeTestConstruct()
	streams := c.buildData(noon(), shape.New("", nil))

	var found bool
	for _, st := range streams {
		if st.Labels["source"] != "network-topology-exporter" {
			continue
		}
		found = true
		if st.Labels["job"] != "j" {
			t.Errorf("change stream job = %q, want j", st.Labels["job"])
		}
		if st.Labels["instance"] != "i" {
			t.Errorf("change stream instance = %q, want i", st.Labels["instance"])
		}
		for _, line := range st.Lines {
			if strings.TrimSpace(line.Body) == "" {
				t.Error("change stream line has empty body")
			}
			if !strings.Contains(line.Body, "topology_change") {
				t.Errorf("change stream body missing topology_change: %q", line.Body)
			}
		}
	}
	if !found {
		t.Error("no topology change stream found (source=network-topology-exporter)")
	}
}

func TestBuildData_Streams_ChangeStreamDeviceInfoInBodyMeta(t *testing.T) {
	c := makeTestConstruct()
	streams := c.buildData(noon(), shape.New("", nil))

	for _, st := range streams {
		if st.Labels["source"] != "network-topology-exporter" {
			continue
		}
		for _, line := range st.Lines {
			// src_device should be in body or meta
			inBody := strings.Contains(line.Body, "spine-01") || strings.Contains(line.Body, "leaf-01")
			inMeta := line.Meta["src_device"] != "" || line.Meta["dst_device"] != ""
			if !inBody && !inMeta {
				t.Errorf("change stream line missing device info in body/meta; body=%q", line.Body)
			}
		}
	}
}

// addedTotal sums change_total across all change_kind="added" series.
func addedTotal(series []promrw.Series) float64 {
	var t float64
	for _, s := range series {
		if s.Name == "network_topology_change_total" && s.Labels["change_kind"] == "added" {
			t += s.Value
		}
	}
	return t
}

// buildTestConstructWithDevice returns a Construct with a single device whose uptime base
// is set to the given duration. The construct has no seed so determinism falls to the
// empty-seed path, which is stable (FNV of deviceID + "").
func buildTestConstructWithDevice(t *testing.T, deviceID string, uptimeBase time.Duration) *Construct {
	t.Helper()
	return &Construct{
		rc: resolvedConfig{
			job:       "test-job",
			instance:  "test-instance",
			protocols: []string{ProtoLLDP},
			protoSet:  map[string]bool{ProtoLLDP: true},
		},
		graph: Graph{
			Devices: []Device{
				{
					ID:             deviceID,
					Vendor:         "arista",
					OSVersion:      "4.36.0F",
					Site:           "dc1",
					UptimeBaseSecs: uptimeBase.Seconds(),
				},
			},
			Edges: []Edge{},
		},
		st: state.NewState(),
	}
}

// tickOnce calls buildData once at the given time and returns the collected series.
// It is equivalent to the first tick of the construct (cold-start).
func tickOnce(t *testing.T, c *Construct, now time.Time) []promrw.Series {
	t.Helper()
	c.buildData(now, shape.New("", nil))
	return c.st.Collect(now)
}

// ── real-transition tests (Task 5) ────────────────────────────────────────────

// findRebootDay scans day buckets forward from the day after coldStart, looking for the first
// day where rebootInstant fires for the given device/seed. Returns (rebootTime, day, true) or
// zero values if not found within maxDays. O(maxDays) — fast because it only calls hashUnit.
func findRebootDay(id, seed string, coldStart time.Time, maxDays int) (time.Time, int64, bool) {
	coldDay := dayBucket(coldStart)
	for delta := int64(1); delta <= int64(maxDays); delta++ {
		day := coldDay + delta
		tR, fired := rebootInstant(id, seed, day, rebootPerDayProb)
		if !fired {
			continue
		}
		// Must be after coldStart.
		if !tR.After(coldStart) {
			continue
		}
		return tR, day, true
	}
	return time.Time{}, 0, false
}

// findFlapDay scans day buckets forward from the day after coldStart, looking for the first
// day where rebootInstant fires for the given edgeKey at edgeFlapPerDayProb.
// Returns (flapTime, day, true) or zero values if not found within maxDays.
func findFlapDay(ek, seed string, coldStart time.Time, maxDays int) (time.Time, int64, bool) {
	coldDay := dayBucket(coldStart)
	for delta := int64(1); delta <= int64(maxDays); delta++ {
		day := coldDay + delta
		tR, fired := rebootInstant(ek, seed, day, edgeFlapPerDayProb)
		if !fired {
			continue
		}
		if !tR.After(coldStart) {
			continue
		}
		return tR, day, true
	}
	return time.Time{}, 0, false
}

// removedTotal sums change_total across all change_kind="removed" series.
func removedTotal(series []promrw.Series) float64 {
	var t float64
	for _, s := range series {
		if s.Name == "network_topology_change_total" && s.Labels["change_kind"] == "removed" {
			t += s.Value
		}
	}
	return t
}

// makeConstructWithEdges builds a Construct with the given edges (and derived device list), seed set.
func makeConstructWithEdges(seed string, edges []Edge) *Construct {
	devSet := map[string]bool{}
	for _, e := range edges {
		devSet[e.SrcDevice] = true
		devSet[e.DstDevice] = true
	}
	var devices []Device
	for id := range devSet {
		devices = append(devices, Device{
			ID:             id,
			Vendor:         "arista",
			OSVersion:      "4.36.0F",
			Site:           "dc1",
			UptimeBaseSecs: UptimeBaseSeconds(id, seed),
		})
	}
	return &Construct{
		seed: seed,
		rc: resolvedConfig{
			job:       "j",
			instance:  "i",
			protocols: []string{ProtoLLDP},
			protoSet:  map[string]bool{ProtoLLDP: true},
		},
		graph: Graph{Devices: devices, Edges: edges},
		st:    state.NewState(),
	}
}

// tickAtBucket aligns t to its 60s bucket start.
func tickAtBucket(t time.Time) time.Time {
	return time.Unix(t.Unix()/60*60, 0)
}

// TestEdgeInfo_OmittedWhileDeviceRebooting verifies that edge_info is absent for edges
// whose endpoint device is currently rebooting, and that a removed transition is counted.
// Strategy: find the exact reboot instant by scanning day buckets (O(maxDays)) then
// snap to the correct 60s bucket — no minute-by-minute full-range scan needed.
//
// Verified seeds (probe4 output): seed="s71" device="r1" reboots at cold+77 days (2024-03-18).
func TestEdgeInfo_OmittedWhileDeviceRebooting(t *testing.T) {
	const seed = "s71"
	const deviceID = "r1"
	edge := Edge{
		SrcDevice: deviceID, SrcPort: "Ethernet1",
		DstDevice: "r2", DstPort: "Ethernet1",
		Proto: ProtoLLDP, LinkKind: LinkEthernet, Direction: DirBidirectional,
	}

	// Use a cold-start well in the past so we have room to scan forward.
	coldStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tR, _, found := findRebootDay(deviceID, seed, coldStart, 500)
	if !found {
		t.Skip("no reboot day found for r1/s71 in 500 days")
	}

	// The reboot instant tR is within the day. The device is rebooting for rebootDownDur (2 min).
	// The FIRST 60s bucket covering tR is tickAtBucket(tR) if tR is already bucket-aligned,
	// else the bucket containing tR. Since edgeFlapTransition and our device reboot detection
	// compare bucket(now) vs bucket(now-60s), we need the bucket AFTER tR where deviceRebooting
	// is true AND the previous bucket was false.
	rebootBucket := tickAtBucket(tR)
	// Advance by one bucket if tR is exactly at the boundary (bucket start == tR).
	if rebootBucket.Equal(tR) {
		// Already at bucket boundary; rebootBucket is the first bucket where tR ≤ t.
	} else {
		// tR is partway through the bucket; rebootBucket starts before tR, so the
		// first FULL bucket starting after tR starts at rebootBucket+60.
		rebootBucket = rebootBucket.Add(60 * time.Second)
	}

	// Verify: at rebootBucket the device is rebooting and at rebootBucket-60s it is not.
	prevBucket := rebootBucket.Add(-60 * time.Second)
	if !deviceRebooting(deviceID, seed, coldStart, rebootBucket) {
		t.Skipf("device not rebooting at computed bucket %v (reboot at %v) — boundary edge case, skip", rebootBucket, tR)
	}
	if deviceRebooting(deviceID, seed, coldStart, prevBucket) {
		t.Skipf("device already rebooting one bucket before %v — already mid-reboot, skip", rebootBucket)
	}

	c := makeConstructWithEdges(seed, []Edge{edge})

	// Cold-start tick.
	c.buildData(coldStart, shape.New("", nil))
	coldSeries := c.st.Collect(coldStart)
	// Edge must be present at cold-start (device not rebooting at coldStart).
	if _, ok := findSeriesLabeled(coldSeries, "network_topology_edge_info", "src_device", deviceID); !ok {
		t.Error("edge_info absent at cold-start, expected present")
	}

	// One tick at rebootBucket: device just entered reboot window.
	c.buildData(rebootBucket, shape.New("", nil))
	rebootSeries := c.st.Collect(rebootBucket)

	// edge_info must be absent (device is rebooting).
	_, present := findSeriesLabeled(rebootSeries, "network_topology_edge_info", "src_device", deviceID)
	if present {
		t.Errorf("edge_info present during device reboot at %v — expected suppressed", rebootBucket)
	}

	// change_total{removed} must have incremented (transition counted).
	removed := removedTotal(rebootSeries)
	if removed <= 0 {
		t.Errorf("change_total{removed} = %v at reboot transition tick %v, want > 0", removed, rebootBucket)
	}
}

// TestEdgeInfo_OmittedWhileEdgeFlapping verifies that edge_info is absent during a flap-down
// window. Strategy: find the flap instant directly via day-bucket scan.
//
// Verified seeds (probe output): seed="seed1" edge key "spine-01|Eth1|leaf-01|Eth1|lldp" flaps
// at cold+277 days (2024-10-04). Note: edgeKey uses exact port strings from the Edge struct.
func TestEdgeInfo_OmittedWhileEdgeFlapping(t *testing.T) {
	const seed = "seed1"
	edge := Edge{
		SrcDevice: "spine-01", SrcPort: "Eth1",
		DstDevice: "leaf-01", DstPort: "Eth1",
		Proto: ProtoLLDP, LinkKind: LinkEthernet, Direction: DirBidirectional,
	}
	ek := edgeKey(edge)

	coldStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tR, _, found := findFlapDay(ek, seed, coldStart, 1000)
	if !found {
		t.Skip("no flap day found for edge/seed1 within 1000 days")
	}

	// edgeFlapTransition uses 60s bucket comparison. Find the bucket where edgeDown
	// transitions from false→true: that's the tick immediately after tR starts.
	flapBucket := tickAtBucket(tR)
	if flapBucket.Before(tR) {
		flapBucket = flapBucket.Add(60 * time.Second)
	}
	prevFlapBucket := flapBucket.Add(-60 * time.Second)

	// Verify transition: was up before, is down now.
	if !edgeDown(ek, seed, coldStart, flapBucket) {
		t.Skipf("edge not down at computed flap bucket %v — skip", flapBucket)
	}
	if edgeDown(ek, seed, coldStart, prevFlapBucket) {
		t.Skipf("edge was already down before flap bucket %v — skip", flapBucket)
	}

	c := makeConstructWithEdges(seed, []Edge{edge})

	// Cold-start.
	c.buildData(coldStart, shape.New("", nil))

	// Jump to the flap transition tick.
	c.buildData(flapBucket, shape.New("", nil))
	flapSeries := c.st.Collect(flapBucket)

	// edge_info must be absent during flap-down.
	_, presentDown := findSeriesLabeled(flapSeries, "network_topology_edge_info", "src_device", edge.SrcDevice)
	if presentDown {
		t.Errorf("edge_info present during flap-down at %v — expected suppressed", flapBucket)
	}

	// change_total{removed} must be positive (flap-down transition counted).
	removed := removedTotal(flapSeries)
	if removed <= 0 {
		t.Errorf("change_total{removed} = %v at flap-down tick %v, want > 0", removed, flapBucket)
	}
}

// TestChangeTotal_ReflectsRealTransitions verifies the invariant: over a window containing
// ≥1 flap or reboot transition, change_total increments driven by the construct exactly
// match the transitions independently counted using the dynamics helpers.
//
// Performance strategy: instead of scanning minute-by-minute across the full window, we
// find the relevant event instants via day-bucket scan (O(days)) and build a sparse tick
// set: {coldStart} ∪ {bucket-1, bucket, bucket+1, ..., bucket+4 around each event}.
// This makes the test O(events * rebootDownDur/60) rather than O(years * 60).
//
// Verified seeds: seed="s163" — r2 reboots day+77, edge r1|Ethernet1|r2|Ethernet1|lldp flaps day+477.
func TestChangeTotal_ReflectsRealTransitions(t *testing.T) {
	const seed = "s163"
	edges := []Edge{
		{SrcDevice: "r1", SrcPort: "Ethernet1", DstDevice: "r2", DstPort: "Ethernet1", Proto: ProtoLLDP, LinkKind: LinkEthernet, Direction: DirBidirectional},
	}

	coldStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	const maxDays = 500

	// ── Step 1: collect event instants via day-bucket scan ─────────────────────
	// Each event is a bucket-aligned instant; we test the construct around it.
	type event struct {
		kind string // "reboot" or "flap"
		id   string // device ID or edge key
		at   time.Time
	}
	var events []event

	devices := map[string]bool{}
	for _, e := range edges {
		devices[e.SrcDevice] = true
		devices[e.DstDevice] = true
	}
	for dev := range devices {
		tR, _, found := findRebootDay(dev, seed, coldStart, maxDays)
		if found {
			events = append(events, event{"reboot", dev, tR})
		}
	}
	for _, e := range edges {
		ek := edgeKey(e)
		tR, _, found := findFlapDay(ek, seed, coldStart, maxDays)
		if found {
			events = append(events, event{"flap", ek, tR})
		}
	}
	if len(events) == 0 {
		t.Skip("no transitions found in scan window — probabilities too low for this seed/range")
	}

	// ── Step 2: build sparse tick set covering transition points ───────────────
	// Include: coldStart + 5 buckets around each event's transition point.
	tickSet := map[int64]bool{coldStart.Unix() / 60 * 60: true}
	const windowBuckets = int64(6) // rebootDownDur=2m + flapDownDur=3m + buffer
	for _, ev := range events {
		base := ev.at.Unix() / 60 // bucket index of the event
		for delta := int64(-1); delta <= windowBuckets; delta++ {
			tickSet[(base+delta)*60] = true
		}
	}
	// Sort ticks.
	var ticks []int64
	for ts := range tickSet {
		ticks = append(ticks, ts)
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i] < ticks[j] })

	// ── Step 3: independently count transitions using the UNIFIED edgeVisible predicate ──
	// For each consecutive pair of ticks (60s apart) in the sparse set, count per-edge
	// visible true→false (removed) and false→true (added) transitions. This mirrors the
	// EXACT logic in buildData after the unified-visibility refactor — one predicate, no
	// separate flap/reboot branches, no attribution rule.
	var expectedAdded, expectedRemoved int

	for i := 1; i < len(ticks); i++ {
		cur := time.Unix(ticks[i], 0)
		prev := time.Unix(ticks[i-1], 0)
		// Only process ticks that are 60s apart (consecutive buckets); skip jumps.
		if ticks[i]-ticks[i-1] != 60 {
			continue
		}
		for _, e := range edges {
			curVis := edgeVisible(e, seed, coldStart, cur)
			prevVis := edgeVisible(e, seed, coldStart, prev)
			if prevVis && !curVis {
				expectedRemoved++
			} else if !prevVis && curVis {
				expectedAdded++
			}
		}
	}

	// ── Step 4: run the construct over the sparse tick set ─────────────────────
	c := makeConstructWithEdges(seed, edges)
	eng := shape.New("", nil)

	var emittedAdded, emittedRemoved float64
	var prevAdded, prevRemoved float64
	coldStarted := false
	edgeInfoAbsenceChecked := false // track that we verified edge_info absent on ≥1 not-visible tick

	for _, ts := range ticks {
		cur := time.Unix(ts, 0)
		c.buildData(cur, eng)
		series := c.st.Collect(cur)

		curAdded := addedTotal(series)
		curRemoved := removedTotal(series)

		if !coldStarted {
			// Cold-start burst: record baseline, don't count as transitions.
			prevAdded = curAdded
			prevRemoved = curRemoved
			coldStarted = true
			continue
		}

		emittedAdded += curAdded - prevAdded
		emittedRemoved += curRemoved - prevRemoved
		prevAdded = curAdded
		prevRemoved = curRemoved

		// Edge_info absence assertion: for any edge that is NOT visible at this tick,
		// assert the corresponding edge_info series is absent in Collect output.
		// Collect is map-ordered — sum over ALL matching series by src_device label.
		for _, e := range edges {
			if !edgeVisible(e, seed, coldStart, cur) && !edgeInfoAbsenceChecked {
				// Find whether edge_info for this edge is present in the series output.
				found := false
				for _, s := range series {
					if s.Name == "network_topology_edge_info" &&
						s.Labels["src_device"] == e.SrcDevice &&
						s.Labels["dst_device"] == e.DstDevice &&
						s.Labels["src_port"] == e.SrcPort &&
						s.Labels["dst_port"] == e.DstPort {
						found = true
						break
					}
				}
				if found {
					t.Errorf("edge_info present for edge %s→%s at tick %v where edgeVisible=false; must be absent",
						e.SrcDevice, e.DstDevice, cur)
				}
				edgeInfoAbsenceChecked = true
			}
		}
	}

	if expectedAdded == 0 && expectedRemoved == 0 {
		t.Skip("no transitions in sparse window — all events outside consecutive 60s buckets")
	}

	// Invariant: emitted transitions must match independently-counted transitions (EXACT).
	if int(emittedAdded) != expectedAdded {
		t.Errorf("change_total{added} post-cold-start = %d, independently counted = %d", int(emittedAdded), expectedAdded)
	}
	if int(emittedRemoved) != expectedRemoved {
		t.Errorf("change_total{removed} post-cold-start = %d, independently counted = %d", int(emittedRemoved), expectedRemoved)
	}
	if !edgeInfoAbsenceChecked {
		t.Log("note: no not-visible tick found in sparse window — edge_info absence not verified (transitions still checked)")
	}
}

// TestBuildData_ColdStartBurstThenDecay locks the prod-realistic churn shape: the first
// discovery cycle "adds" the ENTIRE graph (one burst ≈ total edge count), then churn
// decays to a near-silent steady state — a day later the counter barely moves.
func TestBuildData_ColdStartBurstThenDecay(t *testing.T) {
	edges := []Edge{}
	for i := 0; i < 5; i++ {
		edges = append(edges, Edge{SrcDevice: "spine-01", SrcPort: "Ethernet" + string(rune('1'+i)), DstDevice: "leaf-0" + string(rune('1'+i)), DstPort: "Ethernet1", Proto: ProtoLLDP, LinkKind: LinkEthernet, Direction: DirBidirectional})
	}
	for i := 0; i < 3; i++ {
		edges = append(edges, Edge{SrcDevice: "spine-01", SrcPort: "Ethernet4" + string(rune('1'+i)), DstDevice: "core-0" + string(rune('1'+i)), Proto: ProtoBGP, LinkKind: LinkIP, Direction: DirUnidirectional})
	}
	c := &Construct{
		rc:    resolvedConfig{job: "j", instance: "i", protocols: []string{ProtoLLDP, ProtoBGP}, protoSet: map[string]bool{ProtoLLDP: true, ProtoBGP: true}},
		graph: Graph{Edges: edges},
		st:    state.NewState(),
	}
	eng := shape.New("", nil)

	// Cold-start tick: the whole 8-edge graph is discovered → big "added" burst.
	c.buildData(noon(), eng)
	burst := addedTotal(c.st.Collect(noon()))
	if burst < float64(len(edges)) {
		t.Fatalf("cold-start added burst = %v, want >= %d (full inventory)", burst, len(edges))
	}
	if burst > float64(len(edges))+4 {
		t.Fatalf("cold-start added burst = %v, unexpectedly large (warm-up tail should be tiny)", burst)
	}

	// A full day later: cumulative added should have barely moved (steady-state is quiet).
	later := noon().Add(24 * time.Hour)
	c.buildData(later, eng)
	after := addedTotal(c.st.Collect(later))
	if delta := after - burst; delta > 3 {
		t.Fatalf("steady-state added delta over 24h = %v, want small (<=3) — churn not toned down", delta)
	}
}

// ── bounded uptime + cold-start ───────────────────────────────────────────────

func TestDeviceUptime_BoundedAndStartsNearBase(t *testing.T) {
	// Construct with an explicit device uptime of 100h; first-tick uptime must be ≈ 100h
	// and must be below the sysUpTime wrap ceiling.
	c := buildTestConstructWithDevice(t, "edge-fw-01", 100*time.Hour)
	s := tickOnce(t, c, time.Unix(1_750_000_000, 0))
	u, ok := findSeriesLabeled(s, "network_topology_device_uptime_seconds", "device_id", "edge-fw-01")
	if !ok {
		t.Fatal("missing uptime series for edge-fw-01")
	}
	if u.Value >= wrapCeilingSecs {
		t.Errorf("uptime %v >= wrap ceiling %v", u.Value, wrapCeilingSecs)
	}
	if math.Abs(u.Value-(100*time.Hour).Seconds()) > 120 {
		t.Errorf("cold-start uptime %v not ≈ declared 100h (360000s); diff=%v",
			u.Value, math.Abs(u.Value-(100*time.Hour).Seconds()))
	}
}
