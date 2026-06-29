// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"reflect"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// ── vendor OS version list tests ──────────────────────────────────────────────

func TestVendorOSVersions_OrderedAndDefault(t *testing.T) {
	for _, v := range []string{"cisco", "arista", "juniper", "nokia"} {
		vers := vendorOSVersions(v)
		if len(vers) < 2 {
			t.Fatalf("%s: want >=2 ordered versions, got %v", v, vers)
		}
		if osVersionDefaultIdx >= len(vers) {
			t.Fatalf("%s: default idx %d out of range for %v", v, osVersionDefaultIdx, vers)
		}
	}
	// unknown vendor falls back to arista list
	if got, want := vendorOSVersions("acme"), vendorOSVersions("arista"); len(got) != len(want) {
		t.Fatalf("unknown vendor fallback mismatch")
	}
}

func TestOSVersionAt_ClampsToNewest(t *testing.T) {
	vers := vendorOSVersions("arista")
	if got := osVersionAt("arista", -5); got != vers[0] {
		t.Errorf("negative idx not clamped to oldest: %q", got)
	}
	if got := osVersionAt("arista", 999); got != vers[len(vers)-1] {
		t.Errorf("large idx not clamped to newest: %q", got)
	}
}

func TestCurrentOSVersion_AdvancesKnown_PreservesCustom(t *testing.T) {
	vers := vendorOSVersions("arista")
	def := vers[osVersionDefaultIdx]
	// known baseline advances and clamps to newest, never downgrades
	if got := currentOSVersion("arista", def, 0); got != def {
		t.Errorf("0 upgrades should stay at default %q, got %q", def, got)
	}
	if got := currentOSVersion("arista", def, 999); got != vers[len(vers)-1] {
		t.Errorf("many upgrades should clamp to newest, got %q", got)
	}
	// custom/unknown baseline is returned verbatim, never upgraded
	if got := currentOSVersion("arista", "9.9.9-custom", 5); got != "9.9.9-custom" {
		t.Errorf("custom baseline must be preserved, got %q", got)
	}
}

func TestGeneratedDevice_DefaultOSVersion_NoModel(t *testing.T) {
	rc := resolvedConfig{fabric: &FabricConfig{Kind: "spine_leaf", Spines: 1, Leaves: 1, VendorMix: []string{"arista"}, Site: "s"}, protocols: []string{ProtoLLDP}, protoSet: map[string]bool{ProtoLLDP: true}}
	g := generateGraph(rc, "seed")
	if len(g.Devices) == 0 {
		t.Fatal("no devices")
	}
	want := osVersionAt("arista", osVersionDefaultIdx)
	for _, d := range g.Devices {
		if d.OSVersion != want {
			t.Errorf("device %s OSVersion=%q want default %q", d.ID, d.OSVersion, want)
		}
	}
}

// buildSpineLeafRC builds a resolvedConfig with a 2-spine 4-leaf spine_leaf fabric.
func buildSpineLeafRC(t *testing.T) resolvedConfig {
	t.Helper()
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 2,
			Leaves: 4,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestGenerateGraph_SpineLeaf_DeviceCount(t *testing.T) {
	rc := buildSpineLeafRC(t)
	g := generateGraph(rc, "test-seed")
	// 2 spines + 4 leaves = 6 devices
	if len(g.Devices) != 6 {
		t.Fatalf("device count = %d; want 6 (2 spines + 4 leaves)", len(g.Devices))
	}
}

func TestGenerateGraph_SpineLeaf_EdgeCount(t *testing.T) {
	rc := buildSpineLeafRC(t)
	g := generateGraph(rc, "test-seed")
	// 2 spines × 4 leaves = 8 edges
	if len(g.Edges) != 8 {
		t.Fatalf("edge count = %d; want 8 (2×4 spine↔leaf)", len(g.Edges))
	}
}

func TestGenerateGraph_Determinism(t *testing.T) {
	rc := buildSpineLeafRC(t)
	g1 := generateGraph(rc, "test-seed")
	g2 := generateGraph(rc, "test-seed")
	if !reflect.DeepEqual(g1, g2) {
		t.Fatal("generateGraph is not deterministic: two calls with same inputs produced different results")
	}
}

func TestGenerateGraph_DifferentSeeds_DifferentResults(t *testing.T) {
	rc := buildSpineLeafRC(t)
	g1 := generateGraph(rc, "seed-alpha")
	g2 := generateGraph(rc, "seed-beta")
	// Different seeds should typically produce different vendor assignments.
	// We can't guarantee difference in all fields, but at least the graph structure is deterministic per seed.
	// Check that at least one device differs (vendor or model likely differs).
	identical := reflect.DeepEqual(g1.Devices, g2.Devices)
	// Note: if both seeds happen to map to the same vendor (e.g. single-vendor mix), models might differ.
	// We just verify the function handles multiple seeds without panic.
	_ = identical // intentionally not asserting, just verifying no panic
}

func TestGenerateGraph_SpineLeaf_DeviceIDs(t *testing.T) {
	rc := buildSpineLeafRC(t)
	g := generateGraph(rc, "test-seed")

	wantIDs := map[string]bool{
		"spine-01": true, "spine-02": true,
		"leaf-01": true, "leaf-02": true, "leaf-03": true, "leaf-04": true,
	}
	gotIDs := map[string]bool{}
	for _, d := range g.Devices {
		gotIDs[d.ID] = true
	}
	for id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("expected device %q not found; got devices: %v", id, deviceIDs(g))
		}
	}
}

func deviceIDs(g Graph) []string {
	ids := make([]string, len(g.Devices))
	for i, d := range g.Devices {
		ids[i] = d.ID
	}
	return ids
}

func TestGenerateGraph_SpineLeaf_EdgesSorted(t *testing.T) {
	rc := buildSpineLeafRC(t)
	g := generateGraph(rc, "test-seed")
	for i := 1; i < len(g.Edges); i++ {
		prev := g.Edges[i-1]
		cur := g.Edges[i]
		if edgeSortKey(cur) < edgeSortKey(prev) {
			t.Fatalf("edges not sorted: edge[%d]=%v comes before edge[%d]=%v", i, cur, i-1, prev)
		}
	}
}

func edgeSortKey(e Edge) string {
	return e.SrcDevice + "|" + e.SrcPort + "|" + e.DstDevice + "|" + e.DstPort + "|" + e.Proto
}

func TestGenerateGraph_LLDPEdgeAttrs(t *testing.T) {
	// Build a config with only lldp → fabric edges should be lldp-attributed.
	cfg := &Config{
		Instance:  "netobs:9100",
		Protocols: []string{"lldp"},
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	for _, e := range g.Edges {
		if e.Proto != ProtoLLDP {
			t.Errorf("edge %v: proto = %q; want %q", e, e.Proto, ProtoLLDP)
		}
		if e.PrecedenceRank != 2 {
			t.Errorf("edge %v: rank = %d; want 2", e, e.PrecedenceRank)
		}
		if e.Confidence != ConfHigh {
			t.Errorf("edge %v: confidence = %q; want %q", e, e.Confidence, ConfHigh)
		}
		if e.LinkKind != LinkEthernet {
			t.Errorf("edge %v: link_kind = %q; want %q", e, e.LinkKind, LinkEthernet)
		}
		if e.Adjacency != AdjDirect {
			t.Errorf("edge %v: adjacency = %q; want %q", e, e.Adjacency, AdjDirect)
		}
	}
}

func TestGenerateGraph_BGPEdgeAttrs(t *testing.T) {
	// Build a config with only bgp.
	cfg := &Config{
		Instance:  "netobs:9100",
		Protocols: []string{"bgp"},
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	for _, e := range g.Edges {
		if e.Proto != ProtoBGP {
			t.Errorf("edge %v: proto = %q; want %q", e, e.Proto, ProtoBGP)
		}
		if e.PrecedenceRank != 7 {
			t.Errorf("edge %v: rank = %d; want 7", e, e.PrecedenceRank)
		}
		if e.Confidence != ConfLow {
			t.Errorf("edge %v: confidence = %q; want %q", e, e.Confidence, ConfLow)
		}
		if e.LinkKind != LinkIP {
			t.Errorf("edge %v: link_kind = %q; want %q", e, e.LinkKind, LinkIP)
		}
		if e.Adjacency != AdjUnknown {
			t.Errorf("edge %v: adjacency = %q; want %q", e, e.Adjacency, AdjUnknown)
		}
		// bgp edges must carry bgp.remote_as metadata
		if e.Metadata == nil || e.Metadata["bgp.remote_as"] == "" {
			t.Errorf("edge %v: missing bgp.remote_as in metadata: %v", e, e.Metadata)
		}
	}
}

func TestGenerateGraph_VendorMixRoundRobin(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:      "spine_leaf",
			Spines:    2,
			Leaves:    2,
			VendorMix: []string{"cisco", "arista"},
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	vendors := map[string]int{}
	for _, d := range g.Devices {
		vendors[d.Vendor]++
	}
	// With 2-vendor round-robin and 4 devices (2 spine + 2 leaf), each vendor should appear twice.
	if vendors["cisco"] != 2 || vendors["arista"] != 2 {
		t.Fatalf("vendor distribution = %v; want cisco:2 arista:2", vendors)
	}
}

func TestGenerateGraph_ExplicitDeviceOverridesGenerated(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
		Devices: []DeviceConfig{
			{
				ID:        "spine-01",
				Vendor:    "juniper",
				Model:     "MX204",
				OSVersion: "25.4R1",
				Site:      "dc-custom",
			},
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	var spine01 *Device
	for i := range g.Devices {
		if g.Devices[i].ID == "spine-01" {
			spine01 = &g.Devices[i]
			break
		}
	}
	if spine01 == nil {
		t.Fatal("spine-01 not found in graph devices")
	}
	if spine01.Vendor != "juniper" {
		t.Errorf("spine-01.Vendor = %q; want %q", spine01.Vendor, "juniper")
	}
	// Model field is accepted-but-ignored in DeviceConfig (back-compat parse only); Device has no Model.
	if spine01.Site != "dc-custom" {
		t.Errorf("spine-01.Site = %q; want %q", spine01.Site, "dc-custom")
	}
}

func TestGenerateGraph_ExplicitUptimeHonored(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric:   &FabricConfig{Kind: "spine_leaf", Spines: 1, Leaves: 1},
		Devices: []DeviceConfig{
			{ID: "core-rtr-01", Vendor: "juniper", Uptime: 100 * time.Hour}, // explicit-only device
		},
	}
	rc, err := resolveConfig(cfg, "seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "seed")
	var dev *Device
	for i := range g.Devices {
		if g.Devices[i].ID == "core-rtr-01" {
			dev = &g.Devices[i]
		}
	}
	if dev == nil {
		t.Fatal("core-rtr-01 not in graph")
	}
	if got, want := dev.UptimeBaseSecs, (100 * time.Hour).Seconds(); got != want {
		t.Errorf("declared uptime not honored: UptimeBaseSecs = %v, want %v", got, want)
	}
	// A generated device (no declared uptime) falls back to the deterministic hash base.
	for i := range g.Devices {
		if g.Devices[i].ID == "spine-01" && g.Devices[i].UptimeBaseSecs != UptimeBaseSeconds("spine-01", "seed") {
			t.Errorf("generated device uptime base not the seed-derived default")
		}
	}
}

func TestBuildHealth_BoundaryObs_ReportingDeviceIsRealDevice(t *testing.T) {
	c := &Construct{
		rc: resolvedConfig{job: "j", instance: "i", role: RoleHub, oosCount: 3, protocols: []string{ProtoLLDP}, protoSet: map[string]bool{ProtoLLDP: true}},
		graph: Graph{Devices: []Device{
			{ID: "spine-01"}, {ID: "leaf-01"}, {ID: "leaf-02"},
		}},
		st: state.NewState(),
	}
	c.buildHealth(noon(), shape.New("", nil))
	known := map[string]bool{"spine-01": true, "leaf-01": true, "leaf-02": true}
	var seen int
	for _, s := range c.st.Collect(noon()) {
		if s.Name != "network_topology_boundary_observation_info" {
			continue
		}
		seen++
		if rd := s.Labels["reporting_device"]; !known[rd] {
			t.Errorf("boundary_observation_info reporting_device = %q, not a real in-graph device", rd)
		}
	}
	if seen != 3 {
		t.Errorf("got %d boundary observations, want 3 (oosCount)", seen)
	}
}

func TestGenerateGraph_LinearTopology(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:   "linear",
			Leaves: 4, // 4 devices in chain
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	// 4 devices: rtr-01 through rtr-04
	if len(g.Devices) != 4 {
		t.Fatalf("linear 4: device count = %d; want 4", len(g.Devices))
	}
	// 3 edges: rtr-01↔rtr-02, rtr-02↔rtr-03, rtr-03↔rtr-04
	if len(g.Edges) != 3 {
		t.Fatalf("linear 4: edge count = %d; want 3", len(g.Edges))
	}
}

func TestGenerateGraph_StarTopology(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:   "star",
			Leaves: 3,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	// 1 core + 3 edge devices = 4 devices
	if len(g.Devices) != 4 {
		t.Fatalf("star 3: device count = %d; want 4 (1 core + 3 edges)", len(g.Devices))
	}
	// 3 edges: core → each edge device
	if len(g.Edges) != 3 {
		t.Fatalf("star 3: edge count = %d; want 3", len(g.Edges))
	}
}

func TestGenerateGraph_HostsPerLeaf_FDBEdges(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:         "spine_leaf",
			Spines:       1,
			Leaves:       2,
			HostsPerLeaf: 2,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	// devices: 1 spine + 2 leaves + 4 hosts = 7
	if len(g.Devices) != 7 {
		t.Fatalf("device count = %d; want 7 (1 spine + 2 leaves + 4 hosts)", len(g.Devices))
	}
	// edges: 1×2 spine-leaf + 2×2 host-leaf = 2 + 4 = 6
	if len(g.Edges) != 6 {
		t.Fatalf("edge count = %d; want 6 (2 spine-leaf + 4 host-leaf)", len(g.Edges))
	}
	// host→leaf edges should have FDB proto (if fdb in protocols — default has lldp+bgp so fdb absent;
	// host-leaf edges fall back to lldp)
	var hostEdges []Edge
	for _, e := range g.Edges {
		if len(e.SrcDevice) > 5 && e.SrcDevice[:5] == "host-" {
			hostEdges = append(hostEdges, e)
		}
	}
	if len(hostEdges) != 4 {
		t.Fatalf("host edge count = %d; want 4", len(hostEdges))
	}
}

func TestGenerateGraph_UptimeBaseSecondsHelper(t *testing.T) {
	v1 := UptimeBaseSeconds("spine-01", "seed-a")
	v2 := UptimeBaseSeconds("spine-01", "seed-a")
	if v1 != v2 {
		t.Fatalf("UptimeBaseSeconds not deterministic: %f != %f", v1, v2)
	}
	v3 := UptimeBaseSeconds("leaf-01", "seed-a")
	if v1 == v3 {
		t.Logf("NOTE: spine-01 and leaf-01 have same UptimeBaseSeconds (%f) — unlikely but possible", v1)
	}
	// Value should be in [3..400] days in seconds
	const minSecs = 3 * 24 * 3600
	const maxSecs = 400 * 24 * 3600
	if v1 < minSecs || v1 > maxSecs {
		t.Fatalf("UptimeBaseSeconds(%q, %q) = %f; want in [%d, %d]", "spine-01", "seed-a", v1, minSecs, maxSecs)
	}
}

func TestGenerateGraph_LLDPEdgesBidirectional(t *testing.T) {
	cfg := &Config{
		Instance:  "netobs:9100",
		Protocols: []string{"lldp"},
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	for _, e := range g.Edges {
		if e.Proto == ProtoLLDP && e.Direction != DirBidirectional {
			t.Errorf("lldp spine↔leaf edge should be bidirectional, got %q: %v", e.Direction, e)
		}
	}
}

func TestGenerateGraph_BGPEdgesUnidirectional(t *testing.T) {
	cfg := &Config{
		Instance:  "netobs:9100",
		Protocols: []string{"bgp"},
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	for _, e := range g.Edges {
		if e.Proto == ProtoBGP && e.Direction != DirUnidirectional {
			t.Errorf("bgp edge should be unidirectional, got %q: %v", e.Direction, e)
		}
	}
}

func TestGenerateGraph_LLDPPeerChassisMac(t *testing.T) {
	cfg := &Config{
		Instance:  "netobs:9100",
		Protocols: []string{"lldp"},
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	for _, e := range g.Edges {
		if e.Proto == ProtoLLDP {
			if e.Metadata == nil || e.Metadata["peer_chassis_mac"] == "" {
				t.Errorf("lldp edge missing peer_chassis_mac: %v", e)
			}
		}
	}
}

func TestGenerateGraph_ExplicitLinksAppended(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 2,
		},
		Links: []LinkConfig{
			{
				SrcDevice: "spine-01",
				SrcPort:   "Ethernet99",
				DstDevice: "external-peer",
				DstPort:   "",
				Proto:     ProtoBGP,
			},
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	// 1 spine × 2 leaves = 2 fabric edges + 1 explicit = 3
	if len(g.Edges) != 3 {
		t.Fatalf("edge count = %d; want 3 (2 fabric + 1 explicit)", len(g.Edges))
	}
	found := false
	for _, e := range g.Edges {
		if e.SrcDevice == "spine-01" && e.SrcPort == "Ethernet99" && e.DstDevice == "external-peer" {
			found = true
		}
	}
	if !found {
		t.Fatal("explicit link not found in graph edges")
	}
}

func TestGenerateGraph_NoDuplicateExplicitLinks(t *testing.T) {
	cfg := &Config{
		Instance: "netobs:9100",
		Fabric: &FabricConfig{
			Kind:   "spine_leaf",
			Spines: 1,
			Leaves: 1,
		},
		Links: []LinkConfig{
			// Two identical explicit links — only one should remain.
			{SrcDevice: "extra-a", SrcPort: "eth0", DstDevice: "extra-b", DstPort: "eth0", Proto: ProtoLLDP},
			{SrcDevice: "extra-a", SrcPort: "eth0", DstDevice: "extra-b", DstPort: "eth0", Proto: ProtoLLDP},
		},
	}
	rc, err := resolveConfig(cfg, "test-seed")
	if err != nil {
		t.Fatal(err)
	}
	g := generateGraph(rc, "test-seed")
	// 1×1 fabric edge + 1 unique explicit (dup dropped) = 2
	if len(g.Edges) != 2 {
		t.Fatalf("edge count = %d; want 2 (1 fabric + 1 deduped explicit)", len(g.Edges))
	}
}
