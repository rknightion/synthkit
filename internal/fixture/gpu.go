// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"fmt"
	"github.com/rknightion/synthkit/internal/failuremode"
	"slices"
	"sort"
)

type GPUModelSpec struct {
	Key                string
	Product            string
	AdvertisedMemoryGB float64
	MaxPowerW          *float64
	NVLinkCount        *int
	MIGSupported       bool
	MIGMaxInstances    int
	SlowdownTempC      *float64
	ShutdownTempC      *float64
	MaxOperatingTempC  *float64
	Sources            map[string]GPUFieldSource
}

type GPU struct {
	Key, UUID, Model                 string
	Minor, Slot                      int
	BoardSerial, PCIBusID            string
	NodeKey, DomainKey, PartitionKey string
	UsableMemory                     *GPUUsableMemorySpec
	HGXModuleID                      *int `json:",omitempty" yaml:"hgx_module_id,omitempty"`
}

type GPUCPU struct {
	Key, Product string
	Ordinal      int
}

type GPUNode struct {
	Key, Pool, Site, RackKey, TrayKey, TrayID string
	Slot                                      int
	Serial                                    string
	Node                                      *Node
	Host                                      *Host
	KubernetesCluster                         string
	BMC                                       GPUDevice
	GPUs                                      []GPU
	NICs                                      []GPUNIC
	StorageClient                             *GPUStorageClient
	GraceCPUs                                 []GPUCPU
	Operating                                 GPUOperatingSpec
}

type GPUNIC struct {
	Key, Name, NodeKey, IP, FabricKey, ScalableUnit string
	Rail                                            *int
	SpeedGbps                                       float64
	SwitchKey, PortKey                              string
}

type GPUDevice struct {
	Key, Name, Kind, Site, RackKey, TrayKey        string
	Vendor, Model, OSVersion, Serial, ManagementIP string
	Rail                                           *int
	ScalableUnit                                   string
	StaticPowerW                                   *float64
	Ports                                          []GPUDevicePortSpec
	NICs                                           []GPUNIC
	Collection                                     *GPUCollectionSpec
	NodeKey                                        string `json:",omitempty" yaml:"node_key,omitempty"`
}

type GPUPool struct {
	Name, Shape, Model, KubernetesCluster, ScalableUnit string
	NodeKeys                                            []string
}

type GPURack struct {
	Key, Name, Site, Shape, Cooling, DomainKey          string
	ComputeTrayKeys, SwitchTrayKeys, FacilityDeviceKeys []string
	Physics                                             GPURackPhysicsSpec
}

type GPUTray struct {
	Key, ID, RackKey, Kind, NodeKey string
	Ordinal                         int
	DeviceKeys                      []string
}

type GPUFabricPort struct {
	Key, DeviceKey, Name string
	Rail                 *int
	SpeedGbps            float64
}

type GPUFabricLink struct {
	APortKey, BPortKey string
}

type GPUFabric struct {
	Key, Name, Site, Role string
	Devices               []GPUDevice
	Ports                 []GPUFabricPort
	Links                 []GPUFabricLink
}

type GPUStorageCluster struct {
	Key, Name, Site, FabricKey string
	Devices                    []GPUDevice
}

type GPUStorageClient struct {
	Key, NodeKey, Hostname, IP, NICKey, StorageClusterKey, Role string
}

type NVLinkPartition struct {
	Key, Name, DomainKey string
	GPUKeys              []string
}

type NVLinkPort struct {
	Key, DeviceKey string
	LogicalOrdinal int
	VendorID       string // empty means absent; never emitted as an empty dimension
	Source         *GPUFieldSource
}

type NVLinkLink struct {
	Key, GPUKey, SwitchPortKey string
	GPULinkIndex               int
	Source                     GPUFieldSource
}

type GPUEntityMapping struct {
	Kind, Key, VendorID string
	Source              GPUFieldSource
}

type NVLinkDomain struct {
	Key, RackKey        string
	GPUKeys, SwitchKeys []string
	Partitions          []NVLinkPartition
	Ports               []NVLinkPort
	Links               []NVLinkLink
	EntityMappings      []GPUEntityMapping
	LinksPerGPU         *int
	LinkCountSource     *GPUFieldSource
	NodeKey             string `json:",omitempty" yaml:"node_key,omitempty"`
}

type GPUTarget struct {
	Key        string
	Kind       string
	Axis       failuremode.Axis
	ParentKeys []string // direct physical/logical parents; sorted and unique
}

type GPUTopology struct {
	devices         []GPUDevice // private canonical facility/switch-tray device storage; no local-mode metadata
	Seed            string
	Pools           []GPUPool
	Nodes           []*GPUNode
	Racks           []GPURack
	Trays           []GPUTray
	Fabrics         []GPUFabric
	Domains         []NVLinkDomain
	StorageClusters []GPUStorageCluster
	Schedulers      []GPUSchedulerPlan
	Targets         []GPUTarget
}

type GPUSelection struct {
	Topology                                                    *GPUTopology
	PoolNames, FabricNames, StorageClusterNames, SchedulerNames []string
}

type GPUCapabilities struct {
	NVLinkCorrelation bool
	ThermalEnvelope   bool
	UsableFramebuffer bool
	NVSwitchEntities  bool
	GraceEntities     bool
}

type GPUFailureContract struct {
	Name      string
	Axes      []failuremode.Axis
	Consumers []string
	Effect    string
}

func (t *GPUTopology) Node(key string) (*GPUNode, bool) {
	if t != nil {
		for _, n := range t.Nodes {
			if n.Key == key {
				return n, true
			}
		}
	}
	return nil, false
}
func (t *GPUTopology) GPU(key string) (GPU, bool) {
	if t != nil {
		for _, n := range t.Nodes {
			for _, g := range n.GPUs {
				if g.Key == key {
					return g, true
				}
			}
		}
	}
	return GPU{}, false
}
func (t *GPUTopology) Target(key string) (GPUTarget, bool) {
	if t != nil {
		for _, x := range t.Targets {
			if x.Key == key {
				return x, true
			}
		}
	}
	return GPUTarget{}, false
}
func gpuUnique(xs []string) []string {
	out := slices.Clone(xs)
	sort.Strings(out)
	return slices.Compact(out)
}
func (t *GPUTopology) GPUParentTargets(key string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(k string) {
		if seen[k] {
			return
		}
		seen[k] = true
		if x, ok := t.Target(k); ok {
			for _, p := range x.ParentKeys {
				visit(p)
			}
		}
	}
	visit(key)
	delete(seen, key)
	out := []string{}
	for k := range seen {
		out = append(out, k)
	}
	return gpuUnique(out)
}
func (t *GPUTopology) NVLinkForGPU(key string) []NVLinkLink {
	var out []NVLinkLink
	if t != nil {
		for _, d := range t.Domains {
			for _, l := range d.Links {
				if l.GPUKey == key {
					out = append(out, l)
				}
			}
		}
	}
	return out
}
func SelectGPUTopology(t *GPUTopology, pools, fabrics, storage, schedulers []string) (*GPUSelection, error) {
	if t == nil {
		return nil, fmt.Errorf("gpu_compute: no topology declared")
	}
	check := func(what string, chosen, all []string) error {
		seen := map[string]bool{}
		for _, k := range chosen {
			if seen[k] {
				return fmt.Errorf("duplicate %s selector %q", what, k)
			}
			seen[k] = true
			if !slices.Contains(all, k) {
				return fmt.Errorf("unknown %s selector %q", what, k)
			}
		}
		return nil
	}
	var p, f, st, sc []string
	for _, x := range t.Pools {
		p = append(p, x.Name)
	}
	for _, x := range t.Fabrics {
		f = append(f, x.Name)
	}
	for _, x := range t.StorageClusters {
		st = append(st, x.Name)
	}
	for _, x := range t.Schedulers {
		sc = append(sc, x.Name)
	}
	for _, x := range []struct {
		kind        string
		chosen, all []string
	}{{"pool", pools, p}, {"fabric", fabrics, f}, {"storage", storage, st}, {"scheduler", schedulers, sc}} {
		if err := check(x.kind, x.chosen, x.all); err != nil {
			return nil, err
		}
	}
	return &GPUSelection{Topology: t, PoolNames: gpuUnique(pools), FabricNames: gpuUnique(fabrics), StorageClusterNames: gpuUnique(storage), SchedulerNames: gpuUnique(schedulers)}, nil
}
func (s *GPUSelection) Nodes() []*GPUNode {
	var out []*GPUNode
	if s == nil || s.Topology == nil {
		return out
	}
	pools := slices.Clone(s.PoolNames)
	for _, sc := range s.Topology.Schedulers {
		if slices.Contains(s.SchedulerNames, sc.Name) {
			pools = append(pools, sc.PoolNames...)
		}
	}
	for _, n := range s.Topology.Nodes {
		if slices.Contains(pools, n.Pool) {
			out = append(out, n)
		}
	}
	return out
}
func (s *GPUSelection) Racks() []GPURack {
	var out []GPURack
	if s == nil || s.Topology == nil {
		return out
	}
	keys := map[string]bool{}
	for _, n := range s.Nodes() {
		keys[n.RackKey] = true
	}
	for _, r := range s.Topology.Racks {
		if keys[r.Key] {
			out = append(out, r)
		}
	}
	return out
}
func (s *GPUSelection) Fabrics() []GPUFabric {
	var out []GPUFabric
	if s == nil || s.Topology == nil {
		return out
	}
	keys := map[string]bool{}
	for _, name := range s.FabricNames {
		keys["fabric:"+name] = true
	}
	for _, n := range s.Nodes() {
		for _, nic := range n.NICs {
			keys[nic.FabricKey] = true
		}
	}
	for _, n := range s.Nodes() {
		for _, nic := range n.BMC.NICs {
			keys[nic.FabricKey] = true
		}
	}
	rackKeys := map[string]bool{}
	for _, r := range s.Racks() {
		rackKeys[r.Key] = true
	}
	nodeKeys := map[string]bool{}
	for _, n := range s.Nodes() {
		nodeKeys[n.Key] = true
	}
	for _, d := range s.Topology.devices {
		if (d.NodeKey != "" && nodeKeys[d.NodeKey]) || (d.NodeKey == "" && rackKeys[d.RackKey]) {
			for _, nic := range d.NICs {
				keys[nic.FabricKey] = true
			}
		}
	}
	for _, st := range s.Topology.StorageClusters {
		chosen := slices.Contains(s.StorageClusterNames, st.Name)
		for _, n := range s.Nodes() {
			if n.StorageClient != nil && n.StorageClient.StorageClusterKey == st.Key {
				chosen = true
			}
		}
		if chosen {
			for _, d := range st.Devices {
				for _, nic := range d.NICs {
					keys[nic.FabricKey] = true
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, f := range s.Topology.Fabrics {
			if keys[f.Key] {
				for _, d := range f.Devices {
					for _, nic := range d.NICs {
						if !keys[nic.FabricKey] {
							keys[nic.FabricKey] = true
							changed = true
						}
					}
				}
			}
		}
	}
	for _, f := range s.Topology.Fabrics {
		if keys[f.Key] {
			out = append(out, f)
		}
	}
	return out
}
func (s *GPUSelection) Domains() []NVLinkDomain {
	var out []NVLinkDomain
	if s == nil || s.Topology == nil {
		return out
	}
	keys := map[string]bool{}
	for _, n := range s.Nodes() {
		for _, g := range n.GPUs {
			keys[g.DomainKey] = true
		}
	}
	for _, d := range s.Topology.Domains {
		if keys[d.Key] {
			out = append(out, d)
		}
	}
	return out
}
func (s *GPUSelection) Devices() []GPUDevice {
	var out []GPUDevice
	if s == nil || s.Topology == nil {
		return out
	}
	seen := map[string]bool{}
	add := func(d GPUDevice) {
		if d.Key != "" && !seen[d.Key] {
			seen[d.Key] = true
			out = append(out, d)
		}
	}
	for _, n := range s.Nodes() {
		add(n.BMC)
	}
	for _, f := range s.Fabrics() {
		for _, d := range f.Devices {
			add(d)
		}
	}
	for _, st := range s.Topology.StorageClusters {
		selected := slices.Contains(s.StorageClusterNames, st.Name)
		for _, n := range s.Nodes() {
			if n.StorageClient != nil && n.StorageClient.StorageClusterKey == st.Key {
				selected = true
			}
		}
		if selected {
			for _, d := range st.Devices {
				add(d)
			}
		}
	}
	racks := map[string]bool{}
	for _, r := range s.Racks() {
		racks[r.Key] = true
	}
	nodeKeys := map[string]bool{}
	for _, n := range s.Nodes() {
		nodeKeys[n.Key] = true
	}
	for _, d := range s.Topology.devices {
		if (d.NodeKey != "" && nodeKeys[d.NodeKey]) || (d.NodeKey == "" && racks[d.RackKey]) {
			add(d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// gpuDevice reads canonical physical device inventory, independent of collection modes.
func gpuDevice(t *GPUTopology, key string) (GPUDevice, bool) {
	for _, d := range t.devices {
		if d.Key == key {
			return d, true
		}
	}
	for _, n := range t.Nodes {
		if n.BMC.Key == key {
			return n.BMC, true
		}
	}
	for _, f := range t.Fabrics {
		for _, d := range f.Devices {
			if d.Key == key {
				return d, true
			}
		}
	}
	for _, st := range t.StorageClusters {
		for _, d := range st.Devices {
			if d.Key == key {
				return d, true
			}
		}
	}
	return GPUDevice{}, false
}

func RequireGPUCapabilities(s *GPUSelection, c GPUCapabilities) error {
	if s == nil || s.Topology == nil {
		return fmt.Errorf("missing GPU selection")
	}
	for _, n := range s.Nodes() {
		for _, g := range n.GPUs {
			m, _ := LookupGPUModel(g.Model)
			if c.UsableFramebuffer && (g.UsableMemory == nil || !gpuSourceValid(g.UsableMemory.Source)) {
				return fmt.Errorf("usable framebuffer requires sourced bytes for %s", g.Key)
			}
			if c.ThermalEnvelope {
				if m.SlowdownTempC == nil || m.ShutdownTempC == nil || m.MaxOperatingTempC == nil {
					return fmt.Errorf("thermal envelope unknown for %s", g.Model)
				}
				maxTemp := n.Operating.AmbientTempC + n.Operating.FullPowerRiseC + n.Operating.CoolingFaultRiseC
				if maxTemp > *m.MaxOperatingTempC || maxTemp > *m.SlowdownTempC || maxTemp > *m.ShutdownTempC {
					return fmt.Errorf("declared thermal envelope exceeds sourced limit")
				}
			}
			if c.GraceEntities && len(n.GraceCPUs) == 0 {
				return fmt.Errorf("Grace entities absent")
			}
		}
	}
	// Close mixed-selection admission only for the additive HGX shape.
	hgx := false
	for _, n := range s.Nodes() {
		for _, g := range n.GPUs {
			hgx = hgx || g.HGXModuleID != nil
		}
	}
	if hgx && c.NVLinkCorrelation {
		for _, n := range s.Nodes() {
			for _, g := range n.GPUs {
				covered := false
				for _, d := range s.Domains() {
					if d.Key == g.DomainKey && slices.Contains(d.GPUKeys, g.Key) && gpuValidateCorrelation(d) == nil {
						covered = true
					}
				}
				if !covered {
					return fmt.Errorf("selected GPU has no sourced NVLink domain %s", g.Key)
				}
			}
		}
	}
	if (c.NVLinkCorrelation || c.NVSwitchEntities || c.GraceEntities) && len(s.Domains()) == 0 {
		return fmt.Errorf("selected NVLink domains absent")
	}
	if c.GraceEntities {
		for _, n := range s.Nodes() {
			for _, cpu := range n.GraceCPUs {
				found := false
				for _, d := range s.Domains() {
					if d.RackKey == n.RackKey {
						for _, m := range d.EntityMappings {
							if m.Key == cpu.Key && m.VendorID != "" && gpuSourceValid(m.Source) {
								found = true
							}
						}
					}
				}
				if !found {
					return fmt.Errorf("sourced Grace entity missing for %s", cpu.Key)
				}
			}
		}
	}
	for _, d := range s.Domains() {
		if c.NVLinkCorrelation {
			if err := gpuValidateCorrelation(d); err != nil {
				return err
			}
		}
		if c.NVSwitchEntities || c.GraceEntities {
			for _, key := range d.SwitchKeys {
				found := false
				for _, m := range d.EntityMappings {
					if m.Key == key && gpuSourceValid(m.Source) && m.VendorID != "" {
						found = true
					}
				}
				if c.NVSwitchEntities && !found {
					return fmt.Errorf("sourced NVSwitch entity missing for %s", key)
				}
			}
		}
	}
	return nil
}
