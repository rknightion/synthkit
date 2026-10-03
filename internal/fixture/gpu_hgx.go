// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/csv"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const hgxProfile = "dgxh100_hgxh100-595.91.07"
const hgxProjectionSHA = "3ec71bc45a3689d0e6144801b1fbb6aee9c4b09265e60613e4dbf06edd33a302"

//go:embed gpu_hgx_links.csv
var hgxLinksCSV string

var hgxSource = GPUFieldSource{
	URL:      "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/nvidia-fabricmanager_595.91.07-1ubuntu1_amd64.deb",
	Revision: "nvidia-fabricmanager 595.91.07-1ubuntu1 amd64; dgxh100_hgxh100",
	SHA256:   "b185b23035d928ad82ad372b72a707d7e1560a9e653417d47110334b1eacd7f8",
	Section:  "usr/share/nvidia/nvswitch/dgxh100_hgxh100_topology; ACCESS_GPU_CONNECT endpoint projection",
}

type hgxEndpoint struct{ module, link, sw, port int }

// Structural validation alone cannot detect a same-switch port swap. Pin the
// complete canonical factual projection as well; provenance comments are free to change.
func parseHGXLinks(data string) ([]hgxEndpoint, error) {
	r := csv.NewReader(strings.NewReader(data))
	r.Comment = '#'
	r.FieldsPerRecord = 7
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("HGX topology profile: %w", err)
	}
	if len(rows) != 145 || strings.Join(rows[0], ",") != "node,gpu_physical_id,gpu_link_index,switch_physical_id,switch_port,far_node,connect_type" {
		return nil, fmt.Errorf("HGX topology profile: incomplete endpoint map")
	}
	rows = rows[1:]
	out := make([]hgxEndpoint, 0, 144)
	gpuLinks, ports := map[string]bool{}, map[string]bool{}
	counts := [4]int{}
	for _, row := range rows {
		var v [7]int
		for i, s := range row {
			n, e := strconv.Atoi(s)
			if e != nil {
				return nil, fmt.Errorf("HGX topology profile: invalid endpoint integer")
			}
			v[i] = n
		}
		if v[0] != 0 || v[5] != 0 || v[6] != 2 || v[1] < 0 || v[1] > 7 || v[2] < 0 || v[2] > 17 || v[3] < 0 || v[3] > 3 || v[4] < 0 || v[4] > 63 {
			return nil, fmt.Errorf("HGX topology profile: nonlocal/invalid endpoint map")
		}
		gk, pk := fmt.Sprintf("%d/%d", v[1], v[2]), fmt.Sprintf("%d/%d", v[3], v[4])
		if gpuLinks[gk] || ports[pk] {
			return nil, fmt.Errorf("HGX topology profile: duplicate endpoint map")
		}
		gpuLinks[gk], ports[pk] = true, true
		counts[v[3]]++
		out = append(out, hgxEndpoint{v[1], v[2], v[3], v[4]})
	}
	if counts != [4]int{32, 40, 40, 32} {
		return nil, fmt.Errorf("HGX topology profile: incomplete switch ports")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].module != out[j].module {
			return out[i].module < out[j].module
		}
		return out[i].link < out[j].link
	})
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"node", "gpu_physical_id", "gpu_link_index", "switch_physical_id", "switch_port", "far_node", "connect_type"})
	for _, e := range out {
		_ = w.Write([]string{"0", strconv.Itoa(e.module), strconv.Itoa(e.link), strconv.Itoa(e.sw), strconv.Itoa(e.port), "0", "2"})
	}
	w.Flush()
	if fmt.Sprintf("%x", sha256.Sum256(b.Bytes())) != hgxProjectionSHA {
		return nil, fmt.Errorf("HGX topology profile: endpoint map differs from audited source projection")
	}
	return out, nil
}

func gpuBuildHGX(t *GPUTopology, n *GPUNode, _ GPUPoolSpec, nv *HGXNVLinkSpec, makeDevice func(GPUDeviceSpec, string, string, string, string, string) (GPUDevice, error)) error {
	if nv == nil {
		nv = &HGXNVLinkSpec{}
	}
	if nv.Profile != "" && nv.Profile != hgxProfile {
		return fmt.Errorf("unsupported HGX NVLink profile %q", nv.Profile)
	}
	domain := nv.Domain
	if domain == "" {
		domain = "default"
	}
	if !gpuName(domain) {
		return fmt.Errorf("invalid domain name")
	}
	endpoints, err := parseHGXLinks(hgxLinksCSV)
	if err != nil {
		return err
	}
	var rack GPURack
	for _, r := range t.Racks {
		if r.Key == n.RackKey {
			rack = r
		}
	}
	stem := rack.Site + "/" + rack.Name + "/hgx/" + n.Node.Hostname + "/" + domain
	linksPerGPU := 18
	src := hgxSource
	d := NVLinkDomain{Key: "domain:" + stem, RackKey: n.RackKey, NodeKey: n.Key, LinksPerGPU: &linksPerGPU, LinkCountSource: &src}
	modules := map[int]int{}
	for gi := range n.GPUs {
		g := &n.GPUs[gi]
		if g.HGXModuleID == nil || *g.HGXModuleID < 0 || *g.HGXModuleID > 7 {
			return fmt.Errorf("HGX module IDs must be a permutation of [0,7]")
		}
		if _, dup := modules[*g.HGXModuleID]; dup {
			return fmt.Errorf("HGX module IDs must be a permutation of [0,7]")
		}
		// Own the resolved pointer, independent of the declaration's storage.
		id := *g.HGXModuleID
		g.HGXModuleID = &id
		modules[id] = gi
		g.DomainKey = d.Key
		d.GPUKeys = append(d.GPUKeys, g.Key)
	}
	if len(modules) != 8 {
		return fmt.Errorf("HGX module IDs must be a permutation of [0,7]")
	}
	d.GPUKeys = gpuUnique(d.GPUKeys)
	overrides := map[int]NVSwitchSpec{}
	for _, sw := range nv.Switches {
		if sw.Ordinal < 0 || sw.Ordinal > 3 {
			return fmt.Errorf("HGX switch ordinal out of range")
		}
		if _, dup := overrides[sw.Ordinal]; dup {
			return fmt.Errorf("duplicate HGX switch ordinal")
		}
		overrides[sw.Ordinal] = sw
	}
	for sw := 0; sw < 4; sw++ {
		o := overrides[sw]
		key := "nvswitch:" + stem + "/" + strconv.Itoa(sw)
		name := o.Name
		if name == "" {
			name = fmt.Sprintf("%s-nvsw-%d", n.Node.Hostname, sw)
		}
		serial := o.Serial
		if serial == "" {
			serial = GPUSerial(t.Seed, "device_serial", key)
		}
		dev, e := makeDevice(GPUDeviceSpec{Name: name, Kind: "nvswitch", Serial: serial}, rack.Site, rack.Key, "", key, "hgx.nvlink")
		if e != nil {
			return e
		}
		dev.NodeKey = n.Key
		t.devices = append(t.devices, dev)
		d.SwitchKeys = append(d.SwitchKeys, key)
	}
	partitions := nv.Partitions
	if len(partitions) == 0 {
		partitions = []NVLinkPartitionSpec{{Name: "default", GPUs: d.GPUKeys}}
	}
	held, names := map[string]bool{}, map[string]bool{}
	for _, p := range partitions {
		if !gpuName(p.Name) || names[p.Name] || len(p.GPUs) == 0 {
			return fmt.Errorf("invalid/duplicate partition")
		}
		names[p.Name] = true
		pk := "partition:" + stem + "/" + p.Name
		for _, key := range p.GPUs {
			if held[key] || !slices.Contains(d.GPUKeys, key) {
				return fmt.Errorf("custom partitions overlap or reference nonexistent GPU")
			}
			held[key] = true
			for gi := range n.GPUs {
				if n.GPUs[gi].Key == key {
					n.GPUs[gi].PartitionKey = pk
				}
			}
		}
		d.Partitions = append(d.Partitions, NVLinkPartition{Key: pk, Name: p.Name, DomainKey: d.Key, GPUKeys: gpuUnique(p.GPUs)})
	}
	if len(held) != 8 {
		return fmt.Errorf("custom partitions do not cover domain GPUs")
	}
	sort.Slice(d.Partitions, func(i, j int) bool { return d.Partitions[i].Key < d.Partitions[j].Key })
	for _, e := range endpoints {
		g := n.GPUs[modules[e.module]]
		sk := "nvswitch:" + stem + "/" + strconv.Itoa(e.sw)
		pk := "nvport:" + stem + "/" + strconv.Itoa(e.sw) + "/" + strconv.Itoa(e.port)
		s := hgxSource
		d.Ports = append(d.Ports, NVLinkPort{Key: pk, DeviceKey: sk, LogicalOrdinal: e.port, VendorID: strconv.Itoa(e.port), Source: &s})
		d.Links = append(d.Links, NVLinkLink{Key: "nvlink:" + strings.TrimPrefix(g.Key, "gpu:") + "/" + strconv.Itoa(e.link), GPUKey: g.Key, SwitchPortKey: pk, GPULinkIndex: e.link, Source: hgxSource})
	}
	sort.Slice(d.Ports, func(i, j int) bool { return d.Ports[i].Key < d.Ports[j].Key })
	valid := map[string]bool{d.Key: true, n.Key: true}
	for _, k := range d.GPUKeys {
		valid[k] = true
	}
	for _, k := range d.SwitchKeys {
		valid[k] = true
	}
	for _, p := range d.Ports {
		valid[p.Key] = true
	}
	for _, p := range d.Partitions {
		valid[p.Key] = true
	}
	seen, vendor := map[string]bool{}, map[string]bool{}
	for _, e := range nv.EntityMappings {
		if !valid[e.Key] {
			return fmt.Errorf("entity outside HGX node domain membership")
		}
		vk := e.Kind + "/" + e.VendorID
		if e.Kind == "" || e.VendorID == "" || !gpuSourceValid(e.Source) || seen[e.Key] || vendor[vk] {
			return fmt.Errorf("invalid entity mapping")
		}
		seen[e.Key], vendor[vk] = true, true
		d.EntityMappings = append(d.EntityMappings, GPUEntityMapping{Kind: e.Kind, Key: e.Key, VendorID: e.VendorID, Source: e.Source})
	}
	if nv.Correlated {
		if err := gpuValidateCorrelation(d); err != nil {
			return err
		}
	}
	t.Domains = append(t.Domains, d)
	return nil
}
