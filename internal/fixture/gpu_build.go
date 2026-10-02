// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var gpuNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

func gpuName(s string) bool {
	return len(s) > 0 && len(s) <= 63 && gpuNamePattern.MatchString(s) && !strings.HasSuffix(s, ".") && !strings.Contains(s, "..")
}
func gpuSourceValid(s GPUFieldSource) bool {
	_, err := strconv.ParseUint(s.SHA256[:min(len(s.SHA256), 16)], 16, 64)
	return s.URL != "" && s.Revision != "" && s.Section != "" && len(s.SHA256) == 64 && err == nil && regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(s.SHA256)
}
func gpuFinite(xs ...float64) bool {
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
func gpuFraction(xs ...float64) bool {
	if !gpuFinite(xs...) {
		return false
	}
	for _, x := range xs {
		if x < 0 || x > 1 {
			return false
		}
	}
	return true
}
func gpuAddress(block *GPUAddressBlockSpec, ordinal int, override string) (string, error) {
	if override != "" {
		a, e := netip.ParseAddr(override)
		if e != nil || !a.Is4() {
			return "", fmt.Errorf("invalid IP %q", override)
		}
		return a.String(), nil
	}
	if block == nil {
		return "", nil
	}
	p, e := netip.ParsePrefix(block.CIDR)
	if e != nil || !p.Addr().Is4() || p != p.Masked() {
		return "", fmt.Errorf("invalid IPv4 address block")
	}
	off := int64(block.StartOffset) + int64(ordinal)
	size := int64(1) << uint(32-p.Bits())
	if off <= 0 || off >= size-1 {
		return "", fmt.Errorf("network/broadcast/out-of-range IP assignment")
	}
	a := p.Addr().As4()
	n := uint64(a[0])<<24 | uint64(a[1])<<16 | uint64(a[2])<<8 | uint64(a[3])
	n += uint64(off)
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}).String(), nil
}
func gpuOperatingValid(o GPUOperatingSpec, m GPUModelSpec) error {
	if !gpuFraction(o.IdlePowerFraction, o.CoolingPerfLoss, o.CongestionPerfLoss, o.StoragePerfLoss) || !gpuFinite(o.AmbientTempC, o.FullPowerRiseC, o.CoolingFaultRiseC, o.NodeBasePowerW, o.NodeDynamicPerCoreW, o.NodeDynamicPerNICW, o.NVLinkErrorsPerSecond) || o.FullPowerRiseC < 0 || o.CoolingFaultRiseC < 0 || o.NodeBasePowerW <= 0 || o.NodeDynamicPerCoreW < 0 || o.NodeDynamicPerNICW < 0 || o.NVLinkErrorsPerSecond < 0 {
		return fmt.Errorf("nonfinite or invalid operating parameter")
	}
	if m.MaxPowerW == nil {
		return fmt.Errorf("sourced power maximum absent")
	}
	if o.PowerLimitW != nil && (!gpuFinite(*o.PowerLimitW) || *o.PowerLimitW <= 0 || *o.PowerLimitW > *m.MaxPowerW) {
		return fmt.Errorf("power limit exceeds sourced maximum or invalid")
	}
	return nil
}

// BuildGPUTopology resolves physical inventory before binding canonical node pointers.
// Private devices preserve full facility records without adding public declaration fields.
func BuildGPUTopology(seed string, spec GPUTopologySpec, clusters map[string]*Cluster, hosts map[string]*Host) (*GPUTopology, error) {
	t := &GPUTopology{Seed: seed}
	bad := func(path, reason string) error { return fmt.Errorf("gpu_compute.%s: %s", path, reason) }
	// Canonical sort removes every declaration-order dependency, including attachment order.
	racks := slices.Clone(spec.Racks)
	pools := slices.Clone(spec.Pools)
	fabrics := slices.Clone(spec.Fabrics)
	storage := slices.Clone(spec.StorageClusters)
	sort.Slice(racks, func(i, j int) bool { return racks[i].Site+"/"+racks[i].Name < racks[j].Site+"/"+racks[j].Name })
	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	sort.Slice(fabrics, func(i, j int) bool { return fabrics[i].Name < fabrics[j].Name })
	sort.Slice(storage, func(i, j int) bool { return storage[i].Name < storage[j].Name })
	seen := map[string]string{}
	claim := func(class, key, path string) error {
		if key == "" {
			return nil
		}
		k := class + ":" + key
		if prev, ok := seen[k]; ok {
			return bad(path, "duplicate "+class+" "+key+" (also "+prev+")")
		}
		seen[k] = path
		return nil
	}
	rackIndex := map[string]int{}
	rackSpecs := map[string]GPURackSpec{}
	for _, r := range racks {
		path := "racks." + r.Name
		if !gpuName(r.Name) || !gpuName(r.Site) || (r.Shape != "pcie" && r.Shape != "nvl72") || (r.Cooling != "air" && r.Cooling != "liquid") {
			return nil, bad(path, "invalid name or unsupported shape/cooling")
		}
		key := "rack:" + r.Site + "/" + r.Name
		if err := claim("rack", key, path); err != nil {
			return nil, err
		}
		if r.Shape == "pcie" && r.HeightU == 0 {
			r.HeightU = 42
		}
		if r.Shape == "pcie" && r.HeightU <= 0 {
			return nil, bad(path, "invalid rack height")
		}
		p := r.Physics
		if !gpuFinite(p.FixedOverheadW, p.LiquidHeatFraction, p.SupplyTempC, p.FlowLPM, p.FluidDensityKgPerL, p.FluidSpecificHeatJPerKgC) || p.FixedOverheadW < 0 || !gpuFraction(p.LiquidHeatFraction) {
			return nil, bad(path, "nonfinite or invalid physics parameter")
		}
		if r.Cooling == "liquid" && (p.FlowLPM <= 0 || p.FluidDensityKgPerL <= 0 || p.FluidSpecificHeatJPerKgC <= 0) {
			return nil, bad(path, "liquid fluid quantities must be positive")
		}
		if r.Cooling == "air" && p.LiquidHeatFraction != 0 {
			return nil, bad(path, "air rack has nonzero liquid heat fraction")
		}
		rackIndex[key] = len(t.Racks)
		rackSpecs[key] = r
		t.Racks = append(t.Racks, GPURack{Key: key, Name: r.Name, Site: r.Site, Shape: r.Shape, Cooling: r.Cooling, Physics: p})
	}
	makeDevice := func(d GPUDeviceSpec, site, rack, tray, key, path string) (GPUDevice, error) {
		if !gpuName(d.Name) {
			return GPUDevice{}, bad(path, "invalid device name")
		}
		kinds := []string{"leaf", "spine", "bmc", "nvswitch", "pdu", "cdu", "power_shelf", "cnode", "dnode", "vip", "storage_endpoint", "management_switch"}
		if !slices.Contains(kinds, d.Kind) {
			return GPUDevice{}, bad(path, "unsupported device kind")
		}
		if err := claim("hostname", d.Name, path); err != nil {
			return GPUDevice{}, err
		}
		ip, err := gpuAddress(nil, 0, d.ManagementIP)
		if err != nil {
			return GPUDevice{}, bad(path, err.Error())
		}
		if err = claim("IP", ip, path); err != nil {
			return GPUDevice{}, err
		}
		serial := d.Serial
		if serial == "" {
			serial = GPUSerial(seed, "device_serial", key)
		}
		if err = claim("serial", serial, path); err != nil {
			return GPUDevice{}, err
		}
		if d.StaticPowerW != nil && (!gpuFinite(*d.StaticPowerW) || *d.StaticPowerW < 0) {
			return GPUDevice{}, bad(path, "invalid static power")
		}
		if d.Rail != nil && *d.Rail < 0 {
			return GPUDevice{}, bad(path, "negative rail")
		}
		if d.Collection != nil {
			c := d.Collection
			if (c.Protocol != "snmp" && c.Protocol != "modbus") || c.Port < 1 || c.Port > 65535 || !gpuSourceValid(c.Source) || (c.Protocol == "snmp" && c.Module == "") || (c.ModbusUnit != nil && (*c.ModbusUnit < 0 || *c.ModbusUnit > 247)) {
				return GPUDevice{}, bad(path, "collection requires explicit sourced protocol/port/module")
			}
		}
		ps := map[string]bool{}
		for _, p := range d.Ports {
			if !gpuName(p.Name) || ps[p.Name] || !gpuFinite(p.SpeedGbps) || p.SpeedGbps <= 0 {
				return GPUDevice{}, bad(path, "invalid/duplicate device port")
			}
			ps[p.Name] = true
		}
		var nics []GPUNIC
		for _, nic := range d.NICs {
			if err := claim("NIC", d.Name+"/"+nic.Name, path); err != nil {
				return GPUDevice{}, err
			}
			if !gpuName(nic.Name) || !gpuName(nic.Fabric) || !gpuName(nic.Switch) || !gpuFinite(nic.SpeedGbps) || nic.SpeedGbps <= 0 {
				return GPUDevice{}, bad(path, "invalid device NIC")
			}
			addr, err := gpuAddress(nil, 0, nic.IP)
			if err != nil {
				return GPUDevice{}, bad(path, err.Error())
			}
			if err = claim("IP", addr, path); err != nil {
				return GPUDevice{}, err
			}
			port := ""
			if nic.Port != "" {
				port = "port:" + nic.Fabric + "/" + nic.Switch + "/" + nic.Port
			}
			nics = append(nics, GPUNIC{Key: "nic:" + d.Name + "/" + nic.Name, Name: nic.Name, IP: addr, FabricKey: "fabric:" + nic.Fabric, SwitchKey: "switch:" + nic.Fabric + "/" + nic.Switch, PortKey: port, SpeedGbps: nic.SpeedGbps, Rail: d.Rail, ScalableUnit: d.ScalableUnit})
		}
		sort.Slice(nics, func(i, j int) bool { return nics[i].Key < nics[j].Key })
		return GPUDevice{Key: key, Name: d.Name, Kind: d.Kind, Site: site, RackKey: rack, TrayKey: tray, Vendor: d.Vendor, Model: d.Model, OSVersion: d.OSVersion, Serial: serial, ManagementIP: ip, Rail: d.Rail, ScalableUnit: d.ScalableUnit, StaticPowerW: d.StaticPowerW, Ports: slices.Clone(d.Ports), NICs: nics, Collection: d.Collection}, nil
	}
	for ri := range t.Racks {
		r := &t.Racks[ri]
		rs := rackSpecs[r.Key]
		for _, d := range rs.FacilityDevices {
			x, err := makeDevice(d, r.Site, r.Key, "", "device:"+d.Name, "racks."+r.Name+".facility_devices")
			if err != nil {
				return nil, err
			}
			r.FacilityDeviceKeys = append(r.FacilityDeviceKeys, x.Key)
			t.devices = append(t.devices, x)
		}
	}
	// Explicit fabric links reserve ports before any access attachment is assigned.
	usedPorts := map[string]bool{}
	fabricIndex := map[string]int{}
	switchIndex := map[string]GPUDevice{}
	for _, f := range fabrics {
		path := "fabrics." + f.Name
		if !gpuName(f.Name) || !gpuName(f.Site) || (f.Role != "frontend" && f.Role != "backend") {
			return nil, bad(path, "invalid fabric name/site/role")
		}
		if err := claim("fabric", f.Name, path); err != nil {
			return nil, err
		}
		rf := GPUFabric{Key: "fabric:" + f.Name, Name: f.Name, Site: f.Site, Role: f.Role}
		ds := slices.Clone(f.Devices)
		sort.Slice(ds, func(i, j int) bool { return ds[i].Name < ds[j].Name })
		for _, d := range ds {
			x, err := makeDevice(d, f.Site, "", "", "switch:"+f.Name+"/"+d.Name, path)
			if err != nil {
				return nil, err
			}
			if x.Kind != "leaf" && x.Kind != "spine" && x.Kind != "management_switch" {
				return nil, bad(path, "fabric device must be a switch")
			}
			rf.Devices = append(rf.Devices, x)
			switchIndex[x.Key] = x
			for _, p := range x.Ports {
				rf.Ports = append(rf.Ports, GPUFabricPort{Key: "port:" + f.Name + "/" + x.Name + "/" + p.Name, DeviceKey: x.Key, Name: p.Name, Rail: x.Rail, SpeedGbps: p.SpeedGbps})
			}
		}
		for _, l := range f.Links {
			a := "port:" + f.Name + "/" + l.ADevice + "/" + l.APort
			b := "port:" + f.Name + "/" + l.BDevice + "/" + l.BPort
			valid := func(k string) bool {
				for _, p := range rf.Ports {
					if p.Key == k {
						return true
					}
				}
				return false
			}
			if !valid(a) || !valid(b) || a == b || usedPorts[a] || usedPorts[b] {
				return nil, bad(path, "invalid link or endpoint attached twice")
			}
			usedPorts[a] = true
			usedPorts[b] = true
			rf.Links = append(rf.Links, GPUFabricLink{APortKey: a, BPortKey: b})
		}
		fabricIndex[f.Name] = len(t.Fabrics)
		t.Fabrics = append(t.Fabrics, rf)
	}
	type request struct {
		node   *GPUNode
		device *GPUDevice
		nic    int
		port   string
	}
	var requests []request
	// Storage devices retain frontend NIC identities and explicit access attachments.
	for _, st := range storage {
		path := "storage_clusters." + st.Name
		if !gpuName(st.Name) || !gpuName(st.Site) {
			return nil, bad(path, "invalid storage name/site")
		}
		fi, ok := fabricIndex[st.Fabric]
		if !ok || t.Fabrics[fi].Role != "frontend" {
			return nil, bad(path, "storage requires frontend fabric")
		}
		if err := claim("storage", st.Name, path); err != nil {
			return nil, err
		}
		rs := GPUStorageCluster{Key: "storage:" + st.Name, Name: st.Name, Site: st.Site, FabricKey: "fabric:" + st.Fabric}
		for _, d := range st.Devices {
			x, err := makeDevice(d, st.Site, "", "", "device:"+d.Name, path)
			if err != nil {
				return nil, err
			}
			for _, nic := range x.NICs {
				if nic.FabricKey != rs.FabricKey || nic.IP == "" {
					return nil, bad(path, "storage NIC requires frontend IP and matching fabric")
				}
			}
			rs.Devices = append(rs.Devices, x)
		}
		t.StorageClusters = append(t.StorageClusters, rs)
	}
	// Device attachments are collected after every physical device slice is final.
	occupied := map[string]bool{}
	poolIndex := map[string]GPUPoolSpec{}
	for _, p := range pools {
		path := "pools." + p.Name
		m, ok := LookupGPUModel(p.GPUModel)
		if !ok {
			return nil, bad(path, "unknown gpu_model")
		}
		if !gpuName(p.Name) || !gpuName(p.HostnamePrefix) || p.NodeCount <= 0 || p.Hardware.CPUs <= 0 || !gpuFinite(p.Hardware.MemoryGiB) || p.Hardware.MemoryGiB <= 0 || (p.Hardware.Arch != "x86_64" && p.Hardware.Arch != "arm64") {
			return nil, bad(path, "invalid pool name/count/hardware")
		}
		if err := claim("pool", p.Name, path); err != nil {
			return nil, err
		}
		if (p.Shape == "pcie" && p.GPUModel != "h100_pcie_80gb") || (p.Shape == "nvl72" && p.GPUModel != "gb200_186gb") || (p.Shape != "pcie" && p.Shape != "nvl72") {
			return nil, bad(path, fmt.Sprintf("gpu_model %q incompatible with shape %q", p.GPUModel, p.Shape))
		}
		if p.Shape == "pcie" && (p.GPUsPerNode < 4 || p.GPUsPerNode > 8) {
			return nil, bad(path, "pcie gpus_per_node must be in [4,8]")
		}
		if p.Shape == "nvl72" && p.GPUsPerNode != 4 {
			return nil, bad(path, "NVL72 requires four GPUs per tray")
		}
		if err := gpuOperatingValid(p.Operating, m); err != nil {
			return nil, bad(path, err.Error())
		}
		if p.KubernetesCluster != "" {
			cl, ok := clusters[p.KubernetesCluster]
			if !ok || cl.Type != "baremetal" {
				return nil, bad(path, "GPU pool requires declared baremetal cluster")
			}
		}
		coverage := make([]*GPUPoolPlacementSpec, p.NodeCount)
		for pi := range p.Placements {
			pl := &p.Placements[pi]
			ri, ok := rackIndex[pl.RackKey]
			if !ok || t.Racks[ri].Shape != p.Shape || pl.NodeStart < 0 || pl.NodeCount <= 0 || pl.NodeStart+pl.NodeCount > p.NodeCount || pl.SlotStart < 0 {
				return nil, bad(path, "invalid placement/rack/range")
			}
			if p.Shape == "pcie" && (pl.NodeHeightU <= 0 || pl.SlotStart < 1) {
				return nil, bad(path, "PCIe node_height_u and slot_start must be positive")
			}
			if p.Shape == "nvl72" && pl.NodeHeightU != 0 {
				return nil, bad(path, "NVL72 node_height_u must be omitted")
			}
			for ord := pl.NodeStart; ord < pl.NodeStart+pl.NodeCount; ord++ {
				if coverage[ord] != nil {
					return nil, bad(path, "node ordinal assigned twice")
				}
				coverage[ord] = pl
				slot := pl.SlotStart + ord - pl.NodeStart
				if p.Shape == "pcie" {
					slot = pl.SlotStart + (ord-pl.NodeStart)*pl.NodeHeightU
				}
				width := 1
				if p.Shape == "pcie" {
					width = pl.NodeHeightU
					if slot+width-1 > rackSpecs[pl.RackKey].HeightU {
						return nil, bad(path, "rack slot out of range")
					}
				} else if slot > 17 {
					return nil, bad(path, "NVL72 tray ordinal out of range")
				}
				for s := slot; s < slot+width; s++ {
					k := pl.RackKey + "/" + strconv.Itoa(s)
					if occupied[k] {
						return nil, bad(path, "occupied rack slots overlap")
					}
					occupied[k] = true
				}
			}
		}
		overrides := map[int]GPUNodeOverrideSpec{}
		for _, o := range p.Nodes {
			if o.Ordinal < 0 || o.Ordinal >= p.NodeCount {
				return nil, bad(path, "node override out of range")
			}
			if _, dup := overrides[o.Ordinal]; dup {
				return nil, bad(path, "duplicate override ordinal")
			}
			overrides[o.Ordinal] = o
		}
		gs := map[string]GPUOverrideSpec{}
		for _, g := range p.GPUs {
			k := fmt.Sprintf("%d/%d", g.NodeOrdinal, g.Slot)
			if g.NodeOrdinal < 0 || g.NodeOrdinal >= p.NodeCount || g.Slot < 0 || g.Slot >= p.GPUsPerNode {
				return nil, bad(path, "GPU override out of range")
			}
			if _, dup := gs[k]; dup {
				return nil, bad(path, "duplicate GPU override")
			}
			gs[k] = g
		}
		rp := GPUPool{Name: p.Name, Shape: p.Shape, Model: p.GPUModel, KubernetesCluster: p.KubernetesCluster, ScalableUnit: p.ScalableUnit}
		for ord := 0; ord < p.NodeCount; ord++ {
			pl := coverage[ord]
			if pl == nil {
				return nil, bad(path, "node ordinal is missing")
			}
			o := overrides[ord]
			hostname := o.Hostname
			if hostname == "" {
				hostname = fmt.Sprintf("%s-%04d", p.HostnamePrefix, ord)
			}
			if !gpuName(hostname) {
				return nil, bad(path, "invalid hostname")
			}
			if err := claim("hostname", hostname, path); err != nil {
				return nil, err
			}
			ip, err := gpuAddress(p.NodeIPs, ord, o.IP)
			if err != nil {
				return nil, bad(path, err.Error())
			}
			if err = claim("IP", ip, path); err != nil {
				return nil, err
			}
			serial := o.Serial
			if serial == "" {
				serial = GPUSerial(seed, "node_serial", p.Name, strconv.Itoa(ord))
			}
			if err = claim("serial", serial, path); err != nil {
				return nil, err
			}
			slot := pl.SlotStart + ord - pl.NodeStart
			if p.Shape == "pcie" {
				slot = pl.SlotStart + (ord-pl.NodeStart)*pl.NodeHeightU
			}
			rack := &t.Racks[rackIndex[pl.RackKey]]
			node := &Node{Hostname: hostname, PrivateIP: ip, UID: NodeUID(seed, "gpu_node", p.Name, strconv.Itoa(ord)), OS: "linux", Capacity: &InstanceSpec{VCPU: p.Hardware.CPUs, MemBytes: p.Hardware.MemoryGiB * 1073741824, Arch: p.Hardware.Arch, Known: true}}
			n := &GPUNode{Key: "node:" + hostname, Pool: p.Name, Site: rack.Site, RackKey: rack.Key, Slot: slot, Serial: serial, Node: node, KubernetesCluster: p.KubernetesCluster, Operating: p.Operating}
			if h := hosts[hostname]; h != nil {
				if p.KubernetesCluster != "" {
					return nil, bad(path, "node cannot emit both standalone and cluster host lanes")
				}
				if h.NumCPU != p.Hardware.CPUs || h.MemTotal != node.Capacity.MemBytes || h.PrivateIP != ip {
					return nil, bad(path, "host capacity/address differs from GPU declaration")
				}
				n.Host = h
			}
			if p.Shape == "nvl72" {
				n.TrayKey = fmt.Sprintf("tray:%s/%s/compute/%d", rack.Site, rack.Name, slot)
				n.TrayID = o.TrayID
				if n.TrayID == "" {
					n.TrayID = GPUSerial(seed, "tray", n.TrayKey)
				}
				if err = claim("tray ID", n.TrayID, path); err != nil {
					return nil, err
				}
				rack.ComputeTrayKeys = append(rack.ComputeTrayKeys, n.TrayKey)
				t.Trays = append(t.Trays, GPUTray{Key: n.TrayKey, ID: n.TrayID, RackKey: rack.Key, Kind: "compute", Ordinal: slot, NodeKey: n.Key})
				for c := 0; c < 2; c++ {
					n.GraceCPUs = append(n.GraceCPUs, GPUCPU{Key: fmt.Sprintf("cpu:%s/%d", hostname, c), Product: "Grace", Ordinal: c})
				}
			}
			suffix := p.BMC.HostnameSuffix
			if suffix == "" {
				suffix = "-bmc"
			}
			bip, err := gpuAddress(p.BMC.ManagementIPs, ord, "")
			if err != nil {
				return nil, bad(path, err.Error())
			}
			bs := GPUDeviceSpec{Name: hostname + suffix, Kind: "bmc", ManagementIP: bip, Ports: p.BMC.Ports}
			if o.BMC != nil {
				bs = *o.BMC
			}
			n.BMC, err = makeDevice(bs, rack.Site, rack.Key, n.TrayKey, "device:"+bs.Name, path+".bmc")
			if err != nil {
				return nil, err
			}
			boards := map[int]string{}
			for slot := 0; slot < p.GPUsPerNode; slot++ {
				g := gs[fmt.Sprintf("%d/%d", ord, slot)]
				uuid := g.UUID
				if uuid == "" {
					uuid = GPUUUID(seed, p.Name, strconv.Itoa(ord), strconv.Itoa(slot))
				}
				if !regexp.MustCompile(`^GPU-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString(uuid) {
					return nil, bad(path, "invalid GPU UUID")
				}
				if err = claim("UUID", uuid, path); err != nil {
					return nil, err
				}
				pci, _ := GPUPCIBusID(0, 0x20+slot, 0, 0)
				if g.PCIBusID != "" {
					var dom, bus, dev, fun int
					if count, _ := fmt.Sscanf(g.PCIBusID, "%x:%x:%x.%d", &dom, &bus, &dev, &fun); count != 4 {
						return nil, bad(path, "invalid PCI address")
					}
					pci, err = GPUPCIBusID(dom, bus, dev, fun)
					if err != nil {
						return nil, bad(path, err.Error())
					}
				}
				if err = claim("PCI address", hostname+"/"+pci, path); err != nil {
					return nil, err
				}
				board := slot
				if p.Shape == "nvl72" {
					board = slot / 2
				}
				boardSerial := g.BoardSerial
				if boardSerial == "" {
					boardSerial = GPUSerial(seed, "board", p.Name, strconv.Itoa(ord), strconv.Itoa(board))
				}
				if prev, ok := boards[board]; ok && prev != boardSerial {
					return nil, bad(path, "board serial mismatch within board")
				}
				if _, ok := boards[board]; !ok {
					if err = claim("serial", boardSerial, path); err != nil {
						return nil, err
					}
					boards[board] = boardSerial
				}
				if g.UsableMemory != nil && (g.UsableMemory.Bytes == 0 || !gpuSourceValid(g.UsableMemory.Source)) {
					return nil, bad(path, "usable memory requires sourced positive bytes")
				}
				n.GPUs = append(n.GPUs, GPU{Key: fmt.Sprintf("gpu:%s/%d", hostname, slot), UUID: uuid, Model: p.GPUModel, Minor: slot, Slot: slot, BoardSerial: boardSerial, PCIBusID: pci, NodeKey: n.Key, UsableMemory: g.UsableMemory})
			}
			names := map[string]bool{}
			addresses := map[string]string{}
			att := map[string]GPUNICAttachmentSpec{}
			for _, a := range o.NICAddresses {
				if _, dup := addresses[a.NIC]; dup {
					return nil, bad(path, "duplicate NIC address override")
				}
				addresses[a.NIC] = a.IP
			}
			for _, a := range o.Attachments {
				if _, dup := att[a.NIC]; dup {
					return nil, bad(path, "duplicate NIC attachment")
				}
				att[a.NIC] = a
			}
			nics := slices.Clone(p.NICs)
			sort.Slice(nics, func(i, j int) bool { return nics[i].Name < nics[j].Name })
			for _, nic := range nics {
				fi, ok := fabricIndex[nic.Fabric]
				if !gpuName(nic.Name) || names[nic.Name] || !ok || !gpuFinite(nic.SpeedGbps) || nic.SpeedGbps <= 0 {
					return nil, bad(path, "invalid/duplicate NIC")
				}
				names[nic.Name] = true
				f := t.Fabrics[fi]
				sw := nic.Switch
				if a, ok := att[nic.Name]; ok && a.Switch != "" {
					sw = a.Switch
				}
				if f.Role == "backend" {
					if nic.Rail == nil || *nic.Rail < 0 || p.ScalableUnit == "" {
						return nil, bad(path, "backend NIC requires rail and scalable unit")
					}
					if sw == "" {
						for _, d := range f.Devices {
							if d.Kind == "leaf" && d.Rail != nil && *d.Rail == *nic.Rail && d.ScalableUnit == p.ScalableUnit {
								if sw != "" {
									return nil, bad(path, "ambiguous backend leaf")
								}
								sw = d.Name
							}
						}
					}
				} else if sw == "" {
					return nil, bad(path, "frontend NIC requires switch")
				}
				switchKey := "switch:" + nic.Fabric + "/" + sw
				device, ok := switchIndex[switchKey]
				if !ok || (f.Role == "backend" && device.Kind != "leaf") {
					return nil, bad(path, "unknown NIC leaf")
				}
				if f.Role == "backend" && (device.Rail == nil || *device.Rail != *nic.Rail || device.ScalableUnit != p.ScalableUnit) {
					return nil, bad(path, "rail mismatch")
				}
				nip, err := gpuAddress(nic.IPs, ord, addresses[nic.Name])
				if err != nil {
					return nil, bad(path, err.Error())
				}
				if err = claim("IP", nip, path); err != nil {
					return nil, err
				}
				n.NICs = append(n.NICs, GPUNIC{Key: "nic:" + hostname + "/" + nic.Name, Name: nic.Name, NodeKey: n.Key, IP: nip, FabricKey: f.Key, ScalableUnit: p.ScalableUnit, Rail: nic.Rail, SpeedGbps: nic.SpeedGbps, SwitchKey: switchKey})
				requests = append(requests, request{node: n, nic: len(n.NICs) - 1, port: att[nic.Name].Port})
			}
			for k := range addresses {
				if !names[k] {
					return nil, bad(path, "unknown NIC address override")
				}
			}
			for k := range att {
				if !names[k] {
					return nil, bad(path, "unknown NIC attachment override")
				}
			}
			if p.Storage != nil {
				var st *GPUStorageCluster
				for i := range t.StorageClusters {
					if t.StorageClusters[i].Name == p.Storage.Cluster {
						st = &t.StorageClusters[i]
					}
				}
				var nic *GPUNIC
				for i := range n.NICs {
					if n.NICs[i].Name == p.Storage.NIC {
						nic = &n.NICs[i]
					}
				}
				if st == nil || nic == nil || nic.IP == "" || nic.FabricKey != st.FabricKey || p.Storage.Role == "" {
					return nil, bad(path, "storage NIC must have a frontend IP and matching fabric")
				}
				n.StorageClient = &GPUStorageClient{Key: "client:" + st.Name + "/" + hostname, NodeKey: n.Key, Hostname: hostname, IP: nic.IP, NICKey: nic.Key, StorageClusterKey: st.Key, Role: p.Storage.Role}
			}
			t.Nodes = append(t.Nodes, n)
			rp.NodeKeys = append(rp.NodeKeys, n.Key)
		}
		poolIndex[p.Name] = p
		t.Pools = append(t.Pools, rp)
	}
	// Reserve explicit attachments first; auto requests use declared port-list order.
	assignAttachments := func() error {
		sort.Slice(requests, func(i, j int) bool {
			key := func(r request) string {
				if r.node != nil {
					return r.node.Key + "/" + r.node.NICs[r.nic].Name
				}
				return r.device.Key + "/" + r.device.NICs[r.nic].Name
			}
			return key(requests[i]) < key(requests[j])
		})
		for pass := 0; pass < 2; pass++ {
			for _, r := range requests {
				if (r.port == "") != (pass == 1) {
					continue
				}
				var nic *GPUNIC
				if r.node != nil {
					nic = &r.node.NICs[r.nic]
				} else {
					nic = &r.device.NICs[r.nic]
				}
				d := switchIndex[nic.SwitchKey]
				prefix := strings.Replace(nic.SwitchKey, "switch:", "port:", 1) + "/"
				port := r.port
				if port == "" {
					for _, p := range d.Ports {
						if !usedPorts[prefix+p.Name] && p.SpeedGbps >= nic.SpeedGbps {
							port = p.Name
							break
						}
					}
				}
				valid := false
				for _, p := range d.Ports {
					if p.Name == port && p.SpeedGbps >= nic.SpeedGbps {
						valid = true
					}
				}
				key := prefix + port
				if !valid || usedPorts[key] {
					return bad("attachments", "no free port or endpoint attached twice")
				}
				usedPorts[key] = true
				nic.PortKey = key
			}
		}
		return nil
	}
	// NVL72 domains are complete logical inventories; source maps remain optional/fail closed.
	for ri := range t.Racks {
		r := &t.Racks[ri]
		if r.Shape != "nvl72" {
			if rackSpecs[r.Key].NVLink != nil {
				return nil, bad("racks", "NVLink requires NVL72 shape")
			}
			continue
		}
		if len(r.ComputeTrayKeys) != 18 {
			return nil, bad("racks", "NVL72 requires exactly 18 compute trays × 4 GPUs")
		}
		nv := rackSpecs[r.Key].NVLink
		if nv == nil {
			nv = &NVLinkSpec{Domain: "default"}
		}
		domain := nv.Domain
		if domain == "" {
			domain = "default"
		}
		if !gpuName(domain) {
			return nil, bad("nvlink", "invalid domain name")
		}
		dk := "domain:" + r.Site + "/" + r.Name + "/" + domain
		r.DomainKey = dk
		if nv.LinksPerGPU != nil && (*nv.LinksPerGPU <= 0 || nv.LinkCountSource == nil || !gpuSourceValid(*nv.LinkCountSource)) {
			return nil, bad("nvlink", "links_per_gpu requires positive sourced count")
		}
		d := NVLinkDomain{Key: dk, RackKey: r.Key, LinksPerGPU: nv.LinksPerGPU, LinkCountSource: nv.LinkCountSource}
		for _, n := range t.Nodes {
			if n.RackKey == r.Key {
				for gi := range n.GPUs {
					n.GPUs[gi].DomainKey = dk
					d.GPUKeys = append(d.GPUKeys, n.GPUs[gi].Key)
				}
			}
		}
		d.GPUKeys = gpuUnique(d.GPUKeys)
		trayOverrides := map[int]NVSwitchTraySpec{}
		for _, tr := range nv.SwitchTrays {
			if tr.Ordinal < 0 || tr.Ordinal > 8 {
				return nil, bad("nvlink", "switch tray out of range")
			}
			if _, ok := trayOverrides[tr.Ordinal]; ok {
				return nil, bad("nvlink", "duplicate switch tray")
			}
			trayOverrides[tr.Ordinal] = tr
		}
		for tr := 0; tr < 9; tr++ {
			o := trayOverrides[tr]
			tk := fmt.Sprintf("tray:%s/%s/nvswitch/%d", r.Site, r.Name, tr)
			id := o.ID
			if id == "" {
				id = GPUSerial(seed, "tray", tk)
			}
			if err := claim("tray ID", id, "nvlink"); err != nil {
				return nil, err
			}
			rt := GPUTray{Key: tk, ID: id, RackKey: r.Key, Kind: "nvswitch", Ordinal: tr}
			switches := map[int]NVSwitchSpec{}
			for _, sw := range o.Switches {
				if sw.Ordinal < 0 || sw.Ordinal > 1 {
					return nil, bad("nvlink", "switch ordinal out of range")
				}
				if _, dup := switches[sw.Ordinal]; dup {
					return nil, bad("nvlink", "duplicate switch override")
				}
				switches[sw.Ordinal] = sw
			}
			for sw := 0; sw < 2; sw++ {
				o := switches[sw]
				key := fmt.Sprintf("nvswitch:%s/%s/%s/%d/%d", r.Site, r.Name, domain, tr, sw)
				name := o.Name
				if name == "" {
					name = fmt.Sprintf("%s-nvsw-%d-%d", r.Name, tr, sw)
				}
				x, err := makeDevice(GPUDeviceSpec{Name: name, Kind: "nvswitch", Serial: o.Serial}, r.Site, r.Key, tk, key, "nvlink")
				if err != nil {
					return nil, err
				}
				t.devices = append(t.devices, x)
				rt.DeviceKeys = append(rt.DeviceKeys, key)
				d.SwitchKeys = append(d.SwitchKeys, key)
				for port := 0; port < 72; port++ {
					d.Ports = append(d.Ports, NVLinkPort{Key: fmt.Sprintf("nvport:%s/%s/%s/%d/%d/%d", r.Site, r.Name, domain, tr, sw, port), DeviceKey: key, LogicalOrdinal: port})
				}
			}
			if o.BMC != nil {
				x, err := makeDevice(*o.BMC, r.Site, r.Key, tk, "device:"+o.BMC.Name, "nvlink.bmc")
				if err != nil {
					return nil, err
				}
				t.devices = append(t.devices, x)
				rt.DeviceKeys = append(rt.DeviceKeys, x.Key)
			}
			t.Trays = append(t.Trays, rt)
			r.SwitchTrayKeys = append(r.SwitchTrayKeys, tk)
		}
		partitions := nv.Partitions
		if len(partitions) == 0 {
			partitions = []NVLinkPartitionSpec{{Name: "default", GPUs: d.GPUKeys}}
		}
		held := map[string]bool{}
		pn := map[string]bool{}
		for _, p := range partitions {
			if !gpuName(p.Name) || pn[p.Name] || len(p.GPUs) == 0 {
				return nil, bad("nvlink.partitions", "invalid/duplicate partition")
			}
			pn[p.Name] = true
			pk := "partition:" + r.Site + "/" + r.Name + "/" + domain + "/" + p.Name
			for _, key := range p.GPUs {
				if held[key] || !slices.Contains(d.GPUKeys, key) {
					return nil, bad("nvlink.partitions", "custom partitions overlap or reference nonexistent GPU")
				}
				held[key] = true
				for _, n := range t.Nodes {
					for gi := range n.GPUs {
						if n.GPUs[gi].Key == key {
							n.GPUs[gi].PartitionKey = pk
						}
					}
				}
			}
			d.Partitions = append(d.Partitions, NVLinkPartition{Key: pk, Name: p.Name, DomainKey: dk, GPUKeys: gpuUnique(p.GPUs)})
		}
		if len(held) != len(d.GPUKeys) {
			return nil, bad("nvlink.partitions", "custom partitions do not cover domain GPUs")
		}
		mappedPorts := map[string]bool{}
		vendor := map[string]bool{}
		for _, p := range nv.PortMappings {
			key := fmt.Sprintf("nvport:%s/%s/%s/%d/%d/%d", r.Site, r.Name, domain, p.Tray, p.Switch, p.Port)
			found := false
			for i := range d.Ports {
				if d.Ports[i].Key == key {
					found = true
					if mappedPorts[key] || p.VendorID == "" || !gpuSourceValid(p.Source) {
						return nil, bad("nvlink.port_mappings", "invalid/duplicate sourced port mapping")
					}
					vk := d.Ports[i].DeviceKey + "/" + p.VendorID
					if vendor[vk] {
						return nil, bad("nvlink.port_mappings", "duplicate vendor ID")
					}
					vendor[vk] = true
					mappedPorts[key] = true
					d.Ports[i].VendorID = p.VendorID
					src := p.Source
					d.Ports[i].Source = &src
				}
			}
			if !found {
				return nil, bad("nvlink.port_mappings", "port outside domain")
			}
		}
		linkKeys := map[string]bool{}
		linkPorts := map[string]bool{}
		for _, l := range nv.Links {
			port := fmt.Sprintf("nvport:%s/%s/%s/%d/%d/%d", r.Site, r.Name, domain, l.Tray, l.Switch, l.Port)
			key := "nvlink:" + strings.TrimPrefix(l.GPU, "gpu:") + "/" + strconv.Itoa(l.GPULinkIndex)
			if !slices.Contains(d.GPUKeys, l.GPU) || l.GPULinkIndex < 0 || (d.LinksPerGPU != nil && l.GPULinkIndex >= *d.LinksPerGPU) || !mappedPorts[port] || !gpuSourceValid(l.Source) || linkKeys[key] || linkPorts[port] {
				return nil, bad("nvlink.links", "invalid/duplicate sourced endpoint")
			}
			linkKeys[key] = true
			linkPorts[port] = true
			d.Links = append(d.Links, NVLinkLink{Key: key, GPUKey: l.GPU, SwitchPortKey: port, GPULinkIndex: l.GPULinkIndex, Source: l.Source})
		}
		validEntityKeys := map[string]bool{}
		validEntityKeys[d.Key] = true
		validEntityKeys[r.Key] = true
		for _, key := range d.GPUKeys {
			validEntityKeys[key] = true
		}
		for _, key := range d.SwitchKeys {
			validEntityKeys[key] = true
		}
		for _, p := range d.Ports {
			validEntityKeys[p.Key] = true
		}
		for _, p := range d.Partitions {
			validEntityKeys[p.Key] = true
		}
		for _, n := range t.Nodes {
			if n.RackKey == r.Key {
				validEntityKeys[n.Key] = true
				validEntityKeys[n.TrayKey] = true
				for _, cpu := range n.GraceCPUs {
					validEntityKeys[cpu.Key] = true
				}
			}
		}
		entities := map[string]bool{}
		for _, e := range nv.EntityMappings {
			if !validEntityKeys[e.Key] {
				return nil, bad("nvlink.entity_mappings", "entity outside domain/rack membership")
			}
			if e.Key == "" || e.Kind == "" || e.VendorID == "" || !gpuSourceValid(e.Source) || entities[e.Key] {
				return nil, bad("nvlink.entity_mappings", "invalid entity mapping")
			}
			entities[e.Key] = true
			d.EntityMappings = append(d.EntityMappings, GPUEntityMapping{Kind: e.Kind, Key: e.Key, VendorID: e.VendorID, Source: e.Source})
		}
		if nv.Correlated {
			if err := gpuValidateCorrelation(d); err != nil {
				return nil, bad("nvlink", err.Error())
			}
		}
		t.Domains = append(t.Domains, d)
	}
	// All device slices are now final, including NVSwitch-tray BMC overrides.
	collectDevice := func(d *GPUDevice) error {
		for i := range d.NICs {
			nic := &d.NICs[i]
			fi, ok := fabricIndex[strings.TrimPrefix(nic.FabricKey, "fabric:")]
			sw, swOK := switchIndex[nic.SwitchKey]
			if !ok || !swOK {
				return bad("attachments", "unknown device NIC fabric/switch")
			}
			if t.Fabrics[fi].Role == "backend" && (sw.Kind != "leaf" || nic.Rail == nil || sw.Rail == nil || *nic.Rail != *sw.Rail || nic.ScalableUnit == "" || nic.ScalableUnit != sw.ScalableUnit) {
				return bad("attachments", "device NIC backend rail mismatch")
			}
			port := strings.TrimPrefix(nic.PortKey, strings.Replace(nic.SwitchKey, "switch:", "port:", 1)+"/")
			requests = append(requests, request{device: d, nic: i, port: port})
		}
		return nil
	}
	for _, n := range t.Nodes {
		if err := collectDevice(&n.BMC); err != nil {
			return nil, err
		}
	}
	for i := range t.devices {
		if err := collectDevice(&t.devices[i]); err != nil {
			return nil, err
		}
	}
	for si := range t.StorageClusters {
		for di := range t.StorageClusters[si].Devices {
			if err := collectDevice(&t.StorageClusters[si].Devices[di]); err != nil {
				return nil, err
			}
		}
	}
	for fi := range t.Fabrics {
		for di := range t.Fabrics[fi].Devices {
			if err := collectDevice(&t.Fabrics[fi].Devices[di]); err != nil {
				return nil, err
			}
		}
	}
	if err := assignAttachments(); err != nil {
		return nil, err
	}
	// Allocate final cluster slices once, then bind canonical pointers. Existing physical
	// nodes are separate declarations and must not duplicate a GPU declaration.
	for _, name := range gpuSortedMapKeys(clusters) {
		cl := clusters[name]
		if cl.Type == "baremetal" {
			cl.StaticNodes = true
		}
		nodes := slices.Clone(cl.Nodes)
		for _, n := range t.Nodes {
			if n.KubernetesCluster == name {
				for _, old := range nodes {
					if old.Hostname == n.Node.Hostname {
						return nil, bad("pools", "duplicate hostname in baremetal nodes and GPU pool")
					}
				}
				nodes = append(nodes, *n.Node)
			}
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Hostname < nodes[j].Hostname })
		cl.Nodes = nodes
		cl.GPU = t
		for _, n := range t.Nodes {
			if n.KubernetesCluster == name {
				for i := range cl.Nodes {
					if cl.Nodes[i].Hostname == n.Node.Hostname {
						n.Node = &cl.Nodes[i]
					}
				}
			}
		}
	}
	for _, n := range t.Nodes {
		if n.Host != nil {
			n.Host.GPU = n
		}
	}
	// Claim preexisting non-GPU physical identities against the new inventory.
	for _, cl := range clusters {
		for i := range cl.Nodes {
			n := &cl.Nodes[i]
			managed := false
			for _, gn := range t.Nodes {
				if gn.Node == n {
					managed = true
				}
			}
			if !managed {
				if err := claim("hostname", n.Hostname, "clusters"); err != nil {
					return nil, err
				}
				if err := claim("IP", n.PrivateIP, "clusters"); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, h := range hosts {
		if h.GPU == nil {
			if err := claim("hostname", h.Hostname, "hosts"); err != nil {
				return nil, err
			}
			if err := claim("IP", h.PrivateIP, "hosts"); err != nil {
				return nil, err
			}
		}
	}
	for i := range t.Racks {
		r := &t.Racks[i]
		r.ComputeTrayKeys = gpuUnique(r.ComputeTrayKeys)
		r.SwitchTrayKeys = gpuUnique(r.SwitchTrayKeys)
		r.FacilityDeviceKeys = gpuUnique(r.FacilityDeviceKeys)
	}
	sort.Slice(t.Nodes, func(i, j int) bool { return t.Nodes[i].Key < t.Nodes[j].Key })
	sort.Slice(t.Trays, func(i, j int) bool { return t.Trays[i].Key < t.Trays[j].Key })
	sort.Slice(t.devices, func(i, j int) bool { return t.devices[i].Key < t.devices[j].Key })
	for i := range t.Fabrics {
		sort.Slice(t.Fabrics[i].Links, func(a, b int) bool {
			x, y := t.Fabrics[i].Links[a], t.Fabrics[i].Links[b]
			return x.APortKey+"/"+x.BPortKey < y.APortKey+"/"+y.BPortKey
		})
	}
	for i := range t.StorageClusters {
		sort.Slice(t.StorageClusters[i].Devices, func(a, b int) bool { return t.StorageClusters[i].Devices[a].Key < t.StorageClusters[i].Devices[b].Key })
	}
	for i := range t.Domains {
		d := &t.Domains[i]
		sort.Slice(d.Partitions, func(a, b int) bool { return d.Partitions[a].Key < d.Partitions[b].Key })
		sort.Slice(d.Links, func(a, b int) bool { return d.Links[a].Key < d.Links[b].Key })
		sort.Slice(d.EntityMappings, func(a, b int) bool { return d.EntityMappings[a].Key < d.EntityMappings[b].Key })
	}
	if err := gpuBuildSchedulers(t, spec.Schedulers, clusters, poolIndex); err != nil {
		return nil, err
	}
	if err := gpuBuildTargets(t); err != nil {
		return nil, err
	}
	return t, nil
}
func gpuSortedMapKeys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func gpuValidateCorrelation(d NVLinkDomain) error {
	if d.LinksPerGPU == nil || *d.LinksPerGPU <= 0 || d.LinkCountSource == nil || !gpuSourceValid(*d.LinkCountSource) {
		return fmt.Errorf("NVLink correlation requires complete sourced endpoint/vendor map")
	}
	for _, p := range d.Ports {
		if p.VendorID == "" || p.Source == nil || !gpuSourceValid(*p.Source) {
			return fmt.Errorf("NVLink correlation requires complete sourced endpoint/vendor map")
		}
	}
	for _, g := range d.GPUKeys {
		for idx := 0; idx < *d.LinksPerGPU; idx++ {
			found := 0
			for _, l := range d.Links {
				if l.GPUKey == g && l.GPULinkIndex == idx && gpuSourceValid(l.Source) {
					for _, p := range d.Ports {
						if p.Key == l.SwitchPortKey && p.VendorID != "" && p.Source != nil && gpuSourceValid(*p.Source) {
							found++
						}
					}
				}
			}
			if found != 1 {
				return fmt.Errorf("NVLink correlation requires complete sourced endpoint/vendor map")
			}
		}
	}
	if len(d.Links) != len(d.GPUKeys)**d.LinksPerGPU {
		return fmt.Errorf("NVLink correlation map has extra edges")
	}
	return nil
}
func gpuBuildSchedulers(t *GPUTopology, specs []GPUSchedulerSpec, clusters map[string]*Cluster, pools map[string]GPUPoolSpec) error {
	specs = slices.Clone(specs)
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	owned := map[string]bool{}
	names := map[string]bool{}
	podNames := map[string]bool{}
	bad := func(reason string) error { return fmt.Errorf("gpu_compute.schedulers: %s", reason) }
	duration := func(v string) (time.Duration, error) {
		d, e := time.ParseDuration(v)
		if e != nil || d <= 0 || d%time.Minute != 0 {
			return 0, bad("durations must be positive whole multiples of 60 seconds")
		}
		return d, nil
	}
	for _, s := range specs {
		if !gpuName(s.Name) || names[s.Name] || (s.Kind != "runai" && s.Kind != "slurm") {
			return bad("invalid/duplicate scheduler name or kind")
		}
		names[s.Name] = true
		sc := GPUSchedulerPlan{Key: "scheduler:" + s.Name, Name: s.Name, Kind: s.Kind, PoolNames: gpuUnique(s.Pools)}
		if len(sc.PoolNames) != len(s.Pools) || len(s.Pools) == 0 {
			return bad("duplicate/empty scheduler pools")
		}
		for _, p := range sc.PoolNames {
			if _, ok := pools[p]; !ok {
				return bad("unknown scheduler pool")
			}
			if owned[p] {
				return bad("pool has multiple schedulers of record")
			}
			owned[p] = true
		}
		projects := map[string]GPUProject{}
		for _, p := range s.Projects {
			if !gpuName(p.Name) || p.GPUQuota < 0 || (s.Kind == "slurm" && p.Partition == "") {
				return bad("invalid project/quota/partition")
			}
			if _, ok := projects[p.Name]; ok {
				return bad("duplicate project key")
			}
			rp := GPUProject{Key: "project:" + s.Name + "/" + p.Name, Name: p.Name, Department: p.Department, Partition: p.Partition, GPUQuota: p.GPUQuota}
			projects[p.Name] = rp
			sc.Projects = append(sc.Projects, rp)
		}
		wn := map[string]bool{}
		for _, w := range s.Workloads {
			if !gpuName(w.Name) || wn[w.Name] || !slices.Contains([]string{"training", "batch", "inference"}, w.Role) {
				return bad("invalid/duplicate workload key/role")
			}
			wn[w.Name] = true
			if _, ok := projects[w.Project]; !ok {
				return bad("unknown project")
			}
			rp := GPUWorkloadPlan{Key: "gpuworkload:" + s.Name + "/" + w.Name, Name: w.Name, Scheduler: s.Name, Project: w.Project, Role: w.Role, Priority: w.Priority, Demand: w.Demand}
			var err error
			rp.Cycle, err = duration(w.Cycle)
			if err != nil {
				return err
			}
			rp.PendingFor, err = duration(w.PendingFor)
			if err != nil {
				return err
			}
			rp.RunningMin, err = duration(w.RunningMin)
			if err != nil {
				return err
			}
			rp.RunningMax, err = duration(w.RunningMax)
			if err != nil {
				return err
			}
			if rp.RunningMin > rp.RunningMax || rp.PendingFor+rp.RunningMax+time.Minute > rp.Cycle {
				return bad("durations must fit cycle")
			}
			d := w.Demand
			if !gpuFinite(d.UtilizationBase, d.UtilizationAmplitude, d.MemoryReservedFraction, d.MemoryDynamicFraction, d.RequestsPerSecondAtFullUtilization) || d.UtilizationAmplitude < 0 || d.UtilizationBase-d.UtilizationAmplitude < 0 || d.UtilizationBase+d.UtilizationAmplitude > 1 || !gpuFraction(d.MemoryReservedFraction, d.MemoryDynamicFraction) || d.MemoryReservedFraction+d.MemoryDynamicFraction > 1 || d.RequestsPerSecondAtFullUtilization < 0 || (w.Role != "inference" && d.RequestsPerSecondAtFullUtilization != 0) {
				return bad("demand envelope/memory accounting outside bounds")
			}
			if d.Period != "" || d.UtilizationAmplitude != 0 {
				period, e := time.ParseDuration(d.Period)
				if e != nil || period <= 0 {
					return bad("demand period must be positive when supplied or amplitude nonzero")
				}
			}
			workers := slices.Clone(w.Workers)
			sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
			workerNames := map[string]bool{}
			jobGPUs := map[string]bool{}
			clusterMode := -1
			if len(workers) == 0 {
				return bad("workload requires workers")
			}
			for _, worker := range workers {
				if !gpuName(worker.Name) || workerNames[worker.Name] || !slices.Contains(sc.PoolNames, worker.Pool) {
					return bad("duplicate worker key or worker outside scheduler pools")
				}
				workerNames[worker.Name] = true
				p := pools[worker.Pool]
				if worker.NodeOrdinal < 0 || worker.NodeOrdinal >= p.NodeCount || len(worker.GPUSlots) == 0 {
					return bad("worker requests nonexistent GPU")
				}
				var node *GPUNode
				for _, n := range t.Nodes {
					if n.Pool == worker.Pool { // Pool NodeKeys are in physical ordinal order.
						for _, pool := range t.Pools {
							if pool.Name == worker.Pool && pool.NodeKeys[worker.NodeOrdinal] == n.Key {
								node = n
							}
						}
					}
				}
				if node == nil {
					return bad("worker node not found")
				}
				rw := GPUWorkerPlacement{Key: "worker:" + s.Name + "/" + w.Name + "/" + worker.Name, Name: worker.Name, WorkloadKey: rp.Key, NodeKey: node.Key}
				for _, slot := range worker.GPUSlots {
					if slot < 0 || slot >= len(node.GPUs) {
						return bad("worker requests nonexistent GPU")
					}
					key := node.GPUs[slot].Key
					if jobGPUs[key] {
						return bad("conflicting within-job GPU lists")
					}
					jobGPUs[key] = true
					rw.GPUKeys = append(rw.GPUKeys, key)
				}
				rw.GPUKeys = gpuUnique(rw.GPUKeys)
				mode := 0
				if node.KubernetesCluster != "" {
					mode = 1
				}
				if clusterMode != -1 && clusterMode != mode {
					return bad("mixed Kubernetes/standalone workers")
				}
				clusterMode = mode
				if mode == 1 {
					if !gpuName(w.Namespace) || !gpuName(w.Container) {
						return bad("Kubernetes workers require namespace/container")
					}
					cl := clusters[node.KubernetesCluster]
					name := s.Name + "-" + w.Name + "-" + worker.Name
					pod := PodName(cl.Seed+":"+cl.Name, name, 0)
					if len(pod) > 63 || podNames[cl.Name+"/"+w.Namespace+"/"+pod] {
						return bad("generated pod name exceeds limit or duplicates")
					}
					podNames[cl.Name+"/"+w.Namespace+"/"+pod] = true
					index := -1
					for i := range cl.Nodes {
						if &cl.Nodes[i] == node.Node {
							index = i
						}
					}
					if index < 0 {
						return bad("canonical worker node pointer absent")
					}
					rw.Pod = &PodIdentity{Pod: pod, Namespace: w.Namespace, Container: w.Container, Node: node.Node.Hostname, PodIP: PodIP(index, w.Namespace, pod, 0)}
					for _, existing := range append(slices.Clone(cl.Workloads), cl.SubstrateWorkloads...) {
						if existing.Namespace == w.Namespace && existing.Name == name {
							return bad("duplicate cluster workload template")
						}
					}
					cl.SubstrateWorkloads = append(cl.SubstrateWorkloads, Workload{Name: name, Namespace: w.Namespace, Container: w.Container, Replicas: 1, PodNames: []string{pod}, NodeIdx: []int{index}, GPUWorkerKey: rw.Key})
				} else if w.Namespace != "" || w.Container != "" {
					return bad("standalone workers omit namespace/container")
				}
				rp.Workers = append(rp.Workers, rw)
			}
			sc.Workloads = append(sc.Workloads, rp)
		}
		sort.Slice(sc.Projects, func(i, j int) bool { return sc.Projects[i].Key < sc.Projects[j].Key })
		sort.Slice(sc.Workloads, func(i, j int) bool { return sc.Workloads[i].Key < sc.Workloads[j].Key })
		t.Schedulers = append(t.Schedulers, sc)
	}
	return nil
}
