// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rknightion/synthkit/internal/fixture"
)

// ————————————————————————————————————————————————————————————————————————————
// Vendor catalogue
// ————————————————————————————————————————————————————————————————————————————

// osVersionDefaultIdx is the index into a vendor's osVersions slice that represents
// the "never-upgraded" default. The baseline before any upgrade reboots. idx=1 means
// devices start at the middle of a 3-element list, able to advance one step to newest.
const osVersionDefaultIdx = 1

type vendorEntry struct {
	// osVersions is the ordered (oldest→newest) realistic OS/firmware version list for this
	// vendor. osVersions[0] is the oldest; osVersions[len-1] is the newest. The default
	// emitted by makeDevice is osVersions[osVersionDefaultIdx]. Used for upgrade-reboot
	// progression; the models field was removed (no ENTITY-MIB walk in the exporter).
	osVersions []string
	// ifName returns a realistic interface name for the given role ("spine","leaf","host","rtr","edge","core")
	// and 1-based index.
	ifName func(role string, idx int) string
	mgmtIf string
}

var vendorCatalogue = map[string]vendorEntry{
	"cisco": {
		osVersions: []string{"17.9.4", "17.12.1", "17.15.1"}, // default 17.12.1 at idx 1
		ifName: func(role string, idx int) string {
			return fmt.Sprintf("GigabitEthernet0/%d", idx)
		},
		mgmtIf: "Loopback0",
	},
	"arista": {
		osVersions: []string{"4.33.2F", "4.36.0F", "4.38.1F"}, // default 4.36.0F at idx 1
		ifName: func(role string, idx int) string {
			return fmt.Sprintf("Ethernet%d", idx)
		},
		mgmtIf: "Management0",
	},
	"juniper": {
		osVersions: []string{"24.4R1", "25.4R1", "25.8R1"}, // default 25.4R1 at idx 1
		ifName: func(role string, idx int) string {
			return fmt.Sprintf("ge-0/0/%d", idx)
		},
		mgmtIf: "lo0",
	},
	"nokia": {
		osVersions: []string{"24.10.R1", "25.7.R2", "25.10.R1"}, // default 25.7.R2 at idx 1
		ifName: func(role string, idx int) string {
			return fmt.Sprintf("1/1/%d", idx)
		},
		mgmtIf: "lag-1",
	},
}

// defaultVendorMix is the default vendor list when none is declared.
var defaultVendorMix = []string{"arista"}

// ————————————————————————————————————————————————————————————————————————————
// Proto edge attribute table (LD-10 ladder)
// ————————————————————————————————————————————————————————————————————————————

type protoAttrs struct {
	rank      int
	conf      string
	linkKind  string
	adjacency string
	// isL2 indicates L2 fabric links (lldp/cdp/fdb) where direction depends on endpoint types.
	isL2 bool
}

var protoAttrTable = map[string]protoAttrs{
	ProtoConfigured: {rank: 1, conf: ConfHigh, linkKind: LinkEthernet, adjacency: AdjDirect, isL2: true},
	ProtoLLDP:       {rank: 2, conf: ConfHigh, linkKind: LinkEthernet, adjacency: AdjDirect, isL2: true},
	ProtoCDP:        {rank: 3, conf: ConfHigh, linkKind: LinkEthernet, adjacency: AdjDirect, isL2: true},
	ProtoFDB:        {rank: 4, conf: ConfMedium, linkKind: LinkEthernet, adjacency: AdjDirect, isL2: true},
	ProtoISIS:       {rank: 5, conf: ConfMedium, linkKind: LinkIP, adjacency: AdjDirect, isL2: false},
	ProtoOSPF:       {rank: 6, conf: ConfMedium, linkKind: LinkIP, adjacency: AdjDirect, isL2: false},
	ProtoBGP:        {rank: 7, conf: ConfLow, linkKind: LinkIP, adjacency: AdjUnknown, isL2: false},
	ProtoMPLSTE:     {rank: 8, conf: ConfMedium, linkKind: LinkMPLSTE, adjacency: AdjUnknown, isL2: false},
}

// ————————————————————————————————————————————————————————————————————————————
// generateGraph
// ————————————————————————————————————————————————————————————————————————————
//
// NOTE: UptimeBaseSeconds(deviceID, seed string) float64 (defined in nettopo.go) returns a
// stable per-device uptime base in [uptimeBaseMinSecs, uptimeBaseMaxSecs] via FNV hash of
// deviceID+seed. It is stored on Device.UptimeBaseSecs here (or overridden by an explicit
// `uptime:`); buildData calls deviceUptimeSecs which starts at the base on cold-start and
// grows, clamped below the ~497-day sysUpTime wrap ceiling.

// generateGraph builds the deterministic topology snapshot from the resolved config + seed.
// Two sources are merged: fabric generator output (first) then explicit overrides.
// See types.go / CLAUDE.md for the full contract.
func generateGraph(rc resolvedConfig, seed string) Graph {
	// Build vendor mix list (default to arista if not set or fabric is nil).
	vendorMix := defaultVendorMix
	if rc.fabric != nil && len(rc.fabric.VendorMix) > 0 {
		vendorMix = rc.fabric.VendorMix
	}

	// fabricDevices and fabricEdges accumulate generated topology.
	var fabricDevices []Device
	var fabricEdges []Edge

	// Map from device ID to ordinal (for round-robin vendor assignment).
	deviceOrdinal := map[string]int{}

	if rc.fabric != nil {
		switch rc.fabric.Kind {
		case "spine_leaf", "clos":
			fabricDevices, fabricEdges = genSpineLeaf(rc, seed, vendorMix, deviceOrdinal)
		case "linear":
			fabricDevices, fabricEdges = genLinear(rc, seed, vendorMix, deviceOrdinal)
		case "star":
			fabricDevices, fabricEdges = genStar(rc, seed, vendorMix, deviceOrdinal)
		}
	}

	// Build a device map from fabricDevices (for override merging).
	deviceMap := make(map[string]Device, len(fabricDevices))
	for _, d := range fabricDevices {
		deviceMap[d.ID] = d
	}

	// Apply explicit device overrides: if ID matches a generated device, override fields.
	for _, ec := range rc.devices {
		if d, ok := deviceMap[ec.ID]; ok {
			// Override fields that are explicitly set (non-zero/non-empty).
			if ec.Vendor != "" {
				d.Vendor = ec.Vendor
			}
			if ec.OSVersion != "" {
				d.OSVersion = ec.OSVersion
			}
			if ec.Site != "" {
				d.Site = ec.Site
			}
			// Honor an explicitly-declared uptime as the device's uptime base; otherwise the
			// generated (hash-derived) base is kept. Emitted uptime stays monotonic.
			if ec.Uptime > 0 {
				d.UptimeBaseSecs = ec.Uptime.Seconds()
			}
			deviceMap[d.ID] = d
		} else {
			// New explicit-only device (not in fabric).
			vendor := ec.Vendor
			if vendor == "" {
				vendor = "arista"
			}
			os := ec.OSVersion
			if os == "" {
				os = osVersionAt(vendor, osVersionDefaultIdx)
			}
			uptimeBase := UptimeBaseSeconds(ec.ID, seed)
			if ec.Uptime > 0 {
				uptimeBase = ec.Uptime.Seconds()
			}
			deviceMap[ec.ID] = Device{
				ID:             ec.ID,
				Vendor:         vendor,
				OSVersion:      os,
				Site:           ec.Site,
				UptimeBaseSecs: uptimeBase,
			}
		}
	}

	// Collect and sort devices deterministically.
	allDevices := make([]Device, 0, len(deviceMap))
	for _, d := range deviceMap {
		allDevices = append(allDevices, d)
	}
	sort.Slice(allDevices, func(i, j int) bool { return allDevices[i].ID < allDevices[j].ID })

	// Merge explicit links: append after fabric edges; dedupe exact (src/dst/port/proto) dupes.
	allEdges := make([]Edge, len(fabricEdges))
	copy(allEdges, fabricEdges)

	edgeKeys := make(map[string]bool, len(fabricEdges))
	for _, e := range fabricEdges {
		edgeKeys[edgeKey(e)] = true
	}
	for _, lc := range rc.links {
		proto := lc.Proto
		if proto == "" {
			proto = ProtoLLDP
		}
		e := buildExplicitEdge(lc, proto, rc, seed)
		k := edgeKey(e)
		if !edgeKeys[k] {
			allEdges = append(allEdges, e)
			edgeKeys[k] = true
		}
	}

	// Sort edges: (src_device, src_port, dst_device, dst_port, proto).
	sort.Slice(allEdges, func(i, j int) bool {
		ki := edgeKey(allEdges[i])
		kj := edgeKey(allEdges[j])
		return ki < kj
	})

	return Graph{
		Devices: allDevices,
		Edges:   allEdges,
	}
}

// ————————————————————————————————————————————————————————————————————————————
// Fabric generators
// ————————————————————————————————————————————————————————————————————————————

func genSpineLeaf(rc resolvedConfig, seed string, vendorMix []string, ordinalMap map[string]int) ([]Device, []Edge) {
	nSpines := rc.fabric.Spines
	nLeaves := rc.fabric.Leaves
	hostsPerLeaf := rc.fabric.HostsPerLeaf
	site := rc.fabric.Site

	ordinal := 0

	// Create spines.
	spines := make([]Device, nSpines)
	for i := 0; i < nSpines; i++ {
		id := fmt.Sprintf("spine-%02d", i+1)
		ordinalMap[id] = ordinal
		spines[i] = makeDevice(id, "spine", ordinal, vendorMix, seed, site)
		ordinal++
	}

	// Create leaves.
	leaves := make([]Device, nLeaves)
	for i := 0; i < nLeaves; i++ {
		id := fmt.Sprintf("leaf-%02d", i+1)
		ordinalMap[id] = ordinal
		leaves[i] = makeDevice(id, "leaf", ordinal, vendorMix, seed, site)
		ordinal++
	}

	// Create hosts (if any).
	var hosts []Device
	if hostsPerLeaf > 0 {
		for li := 0; li < nLeaves; li++ {
			for hi := 0; hi < hostsPerLeaf; hi++ {
				id := fmt.Sprintf("host-leaf%02d-%02d", li+1, hi+1)
				ordinalMap[id] = ordinal
				hosts = append(hosts, makeDevice(id, "host", ordinal, vendorMix, seed, site))
				ordinal++
			}
		}
	}

	// Spine→leaf edges.
	var edges []Edge
	for si, spine := range spines {
		for li, leaf := range leaves {
			spinePort := vendorIfName(spine.Vendor, "spine", li+1)
			leafPort := vendorIfName(leaf.Vendor, "leaf", si+1)
			proto := chooseFabricProto(rc, false /*isHostLink*/)
			e := buildEdge(spine.ID, spinePort, leaf.ID, leafPort, proto, false /*isHostLink*/, rc, seed)
			edges = append(edges, e)
		}
	}

	// Host→leaf edges (FDB-style access links).
	if hostsPerLeaf > 0 {
		hostIdx := 0
		for li, leaf := range leaves {
			for hi := 0; hi < hostsPerLeaf; hi++ {
				hostID := fmt.Sprintf("host-leaf%02d-%02d", li+1, hi+1)
				hostDev := hosts[hostIdx]
				hostPort := vendorIfName(hostDev.Vendor, "host", 1)
				leafPort := vendorIfName(leaf.Vendor, "leaf", nSpines+hi+1)
				proto := chooseFabricProto(rc, true /*isHostLink*/)
				e := buildEdge(hostID, hostPort, leaf.ID, leafPort, proto, true /*isHostLink*/, rc, seed)
				edges = append(edges, e)
				hostIdx++
			}
		}
	}

	devices := make([]Device, 0, len(spines)+len(leaves)+len(hosts))
	devices = append(devices, spines...)
	devices = append(devices, leaves...)
	devices = append(devices, hosts...)
	return devices, edges
}

func genLinear(rc resolvedConfig, seed string, vendorMix []string, ordinalMap map[string]int) ([]Device, []Edge) {
	count := rc.fabric.Leaves
	if count < 2 {
		count = 2
	}
	site := rc.fabric.Site

	devices := make([]Device, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("rtr-%02d", i+1)
		ordinalMap[id] = i
		devices[i] = makeDevice(id, "rtr", i, vendorMix, seed, site)
	}

	var edges []Edge
	for i := 0; i < count-1; i++ {
		a := devices[i]
		b := devices[i+1]
		aPort := vendorIfName(a.Vendor, "rtr", i+1)
		bPort := vendorIfName(b.Vendor, "rtr", i)
		proto := chooseFabricProto(rc, false)
		e := buildEdge(a.ID, aPort, b.ID, bPort, proto, false, rc, seed)
		edges = append(edges, e)
	}
	return devices, edges
}

func genStar(rc resolvedConfig, seed string, vendorMix []string, ordinalMap map[string]int) ([]Device, []Edge) {
	nEdges := rc.fabric.Leaves
	if nEdges < 1 {
		nEdges = 1
	}
	site := rc.fabric.Site

	coreID := "core-01"
	ordinalMap[coreID] = 0
	core := makeDevice(coreID, "core", 0, vendorMix, seed, site)

	edgeDevices := make([]Device, nEdges)
	for i := 0; i < nEdges; i++ {
		id := fmt.Sprintf("edge-%02d", i+1)
		ordinalMap[id] = i + 1
		edgeDevices[i] = makeDevice(id, "edge", i+1, vendorMix, seed, site)
	}

	var edges []Edge
	for i, ed := range edgeDevices {
		corePort := vendorIfName(core.Vendor, "core", i+1)
		edgePort := vendorIfName(ed.Vendor, "edge", 1)
		proto := chooseFabricProto(rc, false)
		e := buildEdge(core.ID, corePort, ed.ID, edgePort, proto, false, rc, seed)
		edges = append(edges, e)
	}

	devices := make([]Device, 0, 1+nEdges)
	devices = append(devices, core)
	devices = append(devices, edgeDevices...)
	return devices, edges
}

// ————————————————————————————————————————————————————————————————————————————
// Device / edge construction helpers
// ————————————————————————————————————————————————————————————————————————————

// makeDevice creates a generated Device with vendor catalogue identity.
func makeDevice(id, role string, ordinal int, vendorMix []string, seed, site string) Device {
	vendor := vendorMix[ordinal%len(vendorMix)]
	return Device{
		ID:             id,
		Vendor:         vendor,
		OSVersion:      osVersionAt(vendor, osVersionDefaultIdx),
		Site:           site,
		UptimeBaseSecs: UptimeBaseSeconds(id, seed),
	}
}

// lookupVendor returns the vendorEntry for the given vendor name; falls back to arista.
func lookupVendor(vendor string) vendorEntry {
	if entry, ok := vendorCatalogue[vendor]; ok {
		return entry
	}
	return vendorCatalogue["arista"]
}

// vendorOSVersions returns the ordered (oldest→newest) OS version list for a vendor.
// Falls back to arista for unknown vendors.
func vendorOSVersions(vendor string) []string {
	return lookupVendor(vendor).osVersions
}

// osVersionAt returns the OS version at the given index, clamped to [0, len-1].
// idx 0 is the oldest (baseline); idx len-1 is the newest. Never returns empty string.
func osVersionAt(vendor string, idx int) string {
	vers := vendorOSVersions(vendor)
	if idx < 0 {
		return vers[0]
	}
	if idx >= len(vers) {
		return vers[len(vers)-1]
	}
	return vers[idx]
}

// currentOSVersion returns the device's current OS version after `upgrades` upgrade reboots.
// If baseline is a known version in the vendor list at position p, returns osVersionAt(vendor, p+upgrades).
// If baseline is a custom/unknown string not in the vendor list, returns baseline verbatim (never upgraded).
// This ensures explicitly-declared os_version values are honoured and never silently overwritten.
func currentOSVersion(vendor, baseline string, upgrades int) string {
	vers := vendorOSVersions(vendor)
	for i, v := range vers {
		if v == baseline {
			return osVersionAt(vendor, i+upgrades)
		}
	}
	// Custom or unknown baseline — return verbatim, never upgrade.
	return baseline
}

// vendorIfName returns the interface name for a given vendor, role, and 1-based index.
func vendorIfName(vendor, role string, idx int) string {
	entry := lookupVendor(vendor)
	return entry.ifName(role, idx)
}

// chooseFabricProto selects the protocol to use for a generated fabric edge.
// For host links, FDB is preferred (L2 access); for fabric links, lldp/cdp/fdb then routing protos.
// Falls back to the first available protocol in ladder order.
func chooseFabricProto(rc resolvedConfig, isHostLink bool) string {
	if isHostLink {
		// Host access links: prefer fdb, then lldp, then cdp, then whatever is available.
		for _, p := range []string{ProtoFDB, ProtoLLDP, ProtoCDP} {
			if rc.protoSet[p] {
				return p
			}
		}
	} else {
		// Fabric core links: prefer lldp, cdp, fdb (L2), then routing protos in ladder order.
		for _, p := range []string{ProtoLLDP, ProtoCDP, ProtoFDB, ProtoISIS, ProtoOSPF, ProtoBGP, ProtoMPLSTE} {
			if rc.protoSet[p] {
				return p
			}
		}
	}
	// Fallback: first protocol in the set.
	if len(rc.protocols) > 0 {
		return rc.protocols[0]
	}
	return ProtoLLDP
}

// buildEdge constructs an Edge with correct per-protocol LD-10 attributes.
// isHostLink controls direction (host links are unidirectional).
func buildEdge(srcDev, srcPort, dstDev, dstPort, proto string, isHostLink bool, rc resolvedConfig, seed string) Edge {
	attrs, ok := protoAttrTable[proto]
	if !ok {
		attrs = protoAttrTable[ProtoLLDP]
	}

	// Direction: bidirectional for L2 core links (lldp/cdp between two switches),
	// unidirectional for host links and routing-proto edges.
	direction := DirBidirectional
	if isHostLink || !attrs.isL2 {
		direction = DirUnidirectional
	}

	// Adjacency overrides for routing protocols.
	adjacency := attrs.adjacency
	// ospf/bgp/isis edges have DstPort empty (no interface in those MIBs).
	srcPortFinal := srcPort
	dstPortFinal := dstPort
	switch proto {
	case ProtoOSPF, ProtoBGP, ProtoISIS:
		dstPortFinal = ""
	case ProtoMPLSTE:
		// mpls_te SrcPort = "te-tunnel<idx>"
		// Use a deterministic tunnel number from the edge endpoints.
		h := fixture.Sum(seed, "te_tunnel", srcDev, dstDev)
		b, _ := strconv.ParseUint(h[0:2], 16, 8)
		srcPortFinal = fmt.Sprintf("te-tunnel%d", int(b)%100+1)
		dstPortFinal = ""
	}

	meta := buildEdgeMeta(proto, srcDev, dstDev, seed)

	return Edge{
		SrcDevice:      srcDev,
		SrcPort:        srcPortFinal,
		DstDevice:      dstDev,
		DstPort:        dstPortFinal,
		Proto:          proto,
		LinkKind:       attrs.linkKind,
		Direction:      direction,
		Confidence:     attrs.conf,
		Adjacency:      adjacency,
		PrecedenceRank: attrs.rank,
		Metadata:       meta,
	}
}

// buildExplicitEdge constructs an Edge from an explicit LinkConfig.
func buildExplicitEdge(lc LinkConfig, proto string, rc resolvedConfig, seed string) Edge {
	attrs, ok := protoAttrTable[proto]
	if !ok {
		attrs = protoAttrTable[ProtoLLDP]
	}

	linkKind := lc.LinkKind
	if linkKind == "" {
		linkKind = attrs.linkKind
	}

	direction := DirBidirectional
	if !attrs.isL2 {
		direction = DirUnidirectional
	}

	dstPort := lc.DstPort
	switch proto {
	case ProtoOSPF, ProtoBGP, ProtoISIS:
		dstPort = ""
	}

	meta := buildEdgeMeta(proto, lc.SrcDevice, lc.DstDevice, seed)

	return Edge{
		SrcDevice:      lc.SrcDevice,
		SrcPort:        lc.SrcPort,
		DstDevice:      lc.DstDevice,
		DstPort:        dstPort,
		Proto:          proto,
		LinkKind:       linkKind,
		Direction:      direction,
		Confidence:     attrs.conf,
		Adjacency:      attrs.adjacency,
		PrecedenceRank: attrs.rank,
		Metadata:       meta,
	}
}

// buildEdgeMeta assembles the per-proto metadata map for an edge.
func buildEdgeMeta(proto, srcDev, dstDev, seed string) map[string]string {
	meta := map[string]string{}
	switch proto {
	case ProtoLLDP, ProtoCDP:
		// peer_chassis_mac — deterministic lowercase MAC derived from both endpoints.
		h := fixture.Sum(seed, "mac", srcDev, dstDev)
		mac := fmt.Sprintf("%s:%s:%s:%s:%s:%s",
			h[0:2], h[2:4], h[4:6], h[6:8], h[8:10], h[10:12])
		meta["peer_chassis_mac"] = mac
	case ProtoBGP:
		// bgp.remote_as: same-fabric uses 65001; cross edges via fixture.Sum.
		// Determine iBGP vs eBGP by comparing first chars of device IDs.
		// Same fabric prefix (e.g. both "leaf-*" or "spine-*") → iBGP.
		remoteAS := remoteASN(srcDev, dstDev, seed)
		meta["bgp.remote_as"] = remoteAS
	case ProtoMPLSTE:
		meta["mpls_te.admin_status"] = "up"
	}
	return meta
}

// remoteASN returns a deterministic BGP remote AS number.
// Same-fabric prefix → iBGP (65001); otherwise hash between 65002/64512.
func remoteASN(srcDev, dstDev, seed string) string {
	srcPrefix := devicePrefix(srcDev)
	dstPrefix := devicePrefix(dstDev)
	if srcPrefix == dstPrefix {
		return "65001"
	}
	h := fixture.Sum(seed, "remote_as", srcDev, dstDev)
	b, _ := strconv.ParseUint(h[0:2], 16, 8)
	candidates := []string{"65001", "65002", "64512"}
	return candidates[int(b)%len(candidates)]
}

// devicePrefix returns the role prefix of a device ID (e.g. "spine-01" → "spine", "leaf-02" → "leaf").
func devicePrefix(id string) string {
	if idx := strings.LastIndex(id, "-"); idx >= 0 {
		return id[:idx]
	}
	return id
}

// edgeKey returns a canonical sort/dedup key for an edge.
func edgeKey(e Edge) string {
	return e.SrcDevice + "|" + e.SrcPort + "|" + e.DstDevice + "|" + e.DstPort + "|" + e.Proto
}
