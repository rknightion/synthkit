// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
	"math"
	"net/netip"
	"reflect"
	"slices"
	"strings"
)

// yaml.v3 otherwise truncates fractional scalars when decoding into integer
// fields. Inspect the raw GPU subtree against the frozen typed declaration first
// and require explicit operating assumptions; outer decoding owns unknown fields.
func validateGPUIntegerInput(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 {
		return nil
	}
	doc, err := gpuEffectiveYAML(root.Content[0], map[*yaml.Node]bool{})
	if err != nil {
		return err
	}
	var walk func(*yaml.Node, reflect.Type, string) error
	walk = func(n *yaml.Node, t reflect.Type, path string) error {
		for n.Kind == yaml.AliasNode && n.Alias != nil {
			n = n.Alias
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Struct:
			if t == reflect.TypeOf(fixture.GPUOperatingSpec{}) {
				present := map[string]bool{}
				for i := 0; i+1 < len(n.Content); i += 2 {
					c := n.Content[i+1]
					for c.Kind == yaml.AliasNode && c.Alias != nil {
						c = c.Alias
					}
					present[n.Content[i].Value] = c.Tag != "!!null"
				}
				for i := 0; i < t.NumField(); i++ {
					key := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
					if key != "power_limit_w" && !present[key] {
						return fmt.Errorf("%s.%s: explicit operating assumption is required", path, key)
					}
				}
			}
			fields := map[string]reflect.Type{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				key := strings.Split(f.Tag.Get("yaml"), ",")[0]
				if key != "" && key != "-" {
					fields[key] = f.Type
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i].Value
				if ft, ok := fields[key]; ok {
					if err := walk(n.Content[i+1], ft, path+"."+key); err != nil {
						return err
					}
				}
			}
		case reflect.Slice, reflect.Array:
			for i, c := range n.Content {
				if err := walk(c, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if n.Tag == "!!float" {
				var v float64
				if err := n.Decode(&v); err != nil {
					return err
				}
				if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) {
					return fmt.Errorf("%s: must be a finite whole integer, not fractional ownership/capacity", path)
				}
			}
		}
		return nil
	}
	child := func(n *yaml.Node, key string) *yaml.Node {
		for n.Kind == yaml.AliasNode && n.Alias != nil {
			n = n.Alias
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		switch doc.Content[i].Value {
		case "gpu_compute":
			if pools := child(doc.Content[i+1], "pools"); pools != nil {
				for pi, pool := range pools.Content {
					shape := child(pool, "shape")
					if shape != nil && shape.Value == "hgx" {
						continue
					}
					for _, group := range []struct{ list, field string }{{"nodes", "nvlink"}, {"gpus", "hgx_module_id"}} {
						if entries := child(pool, group.list); entries != nil {
							for _, entry := range entries.Content {
								if child(entry, group.field) != nil {
									return fmt.Errorf("gpu_compute.pools[%d]: field %s is supported only for hgx shape", pi, group.field)
								}
							}
						}
					}
				}
			}
			if err := walk(doc.Content[i+1], reflect.TypeOf(fixture.GPUTopologySpec{}), "gpu_compute"); err != nil {
				return err
			}
		case "environments":
			for ei, e := range doc.Content[i+1].Content {
				if c := child(e, "cluster"); c != nil {
					if typ := child(c, "type"); typ != nil && typ.Value == "baremetal" {
						if nodes := child(c, "nodes"); nodes != nil {
							if err := walk(nodes, reflect.TypeOf([]BareMetalNodeDecl{}), fmt.Sprintf("environments[%d].cluster.nodes", ei)); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}
	return nil
}

// These private limits bound the extra raw-validation pass, including disabled
// raw configs: 64K normalization steps and logically expanded nodes, 128 levels.
// They are safety ceilings, not topology sizing/defaults. Count logical expansion
// as well as actual work so downstream typed traversal cannot re-expand a DAG.
const gpuYAMLMaxWork = 1 << 16
const gpuYAMLMaxDepth = 128

type gpuYAMLValue struct {
	node        *yaml.Node
	size, depth int
}

type gpuYAMLNormalizer struct {
	active map[*yaml.Node]bool
	memo   map[*yaml.Node]gpuYAMLValue
	work   int
}

func (s *gpuYAMLNormalizer) step() error {
	s.work++
	if s.work > gpuYAMLMaxWork {
		return fmt.Errorf("YAML resource budget: normalization work exceeds %d steps", gpuYAMLMaxWork)
	}
	return nil
}

// gpuEffectiveYAML normalizes aliases/merges for raw validation only. Completed
// subgraphs are immutable and shared, never copied once per alias. The original
// document still goes through KnownFields decoding, so strictness is unchanged.
// Explicit keys win; earlier maps in a merge sequence win over later.
func gpuEffectiveYAML(n *yaml.Node, active map[*yaml.Node]bool) (*yaml.Node, error) {
	s := gpuYAMLNormalizer{active: active, memo: map[*yaml.Node]gpuYAMLValue{}}
	v, err := s.normalize(n, 0)
	return v.node, err
}

func (s *gpuYAMLNormalizer) normalize(n *yaml.Node, depth int) (gpuYAMLValue, error) {
	if err := s.step(); err != nil {
		return gpuYAMLValue{}, err
	}
	if depth >= gpuYAMLMaxDepth {
		return gpuYAMLValue{}, fmt.Errorf("YAML resource budget: depth exceeds %d levels", gpuYAMLMaxDepth)
	}
	if n == nil {
		return gpuYAMLValue{}, fmt.Errorf("invalid YAML alias")
	}
	if s.active[n] {
		return gpuYAMLValue{}, fmt.Errorf("recursive YAML alias/merge")
	}
	if v, ok := s.memo[n]; ok {
		if depth+v.depth > gpuYAMLMaxDepth {
			return gpuYAMLValue{}, fmt.Errorf("YAML resource budget: depth exceeds %d levels", gpuYAMLMaxDepth)
		}
		return v, nil
	}
	s.active[n] = true
	defer delete(s.active, n)
	if n.Kind == yaml.AliasNode {
		v, err := s.normalize(n.Alias, depth+1)
		if err == nil {
			s.memo[n] = v
		}
		return v, err
	}
	out := *n
	out.Content = nil
	result := gpuYAMLValue{node: &out, size: 1, depth: 1}
	appendValue := func(v gpuYAMLValue) error {
		if result.size > gpuYAMLMaxWork-v.size {
			return fmt.Errorf("YAML resource budget: logical expansion exceeds %d nodes", gpuYAMLMaxWork)
		}
		result.size += v.size
		result.depth = max(result.depth, 1+v.depth)
		if depth+result.depth > gpuYAMLMaxDepth {
			return fmt.Errorf("YAML resource budget: depth exceeds %d levels", gpuYAMLMaxDepth)
		}
		out.Content = append(out.Content, v.node)
		return nil
	}
	if n.Kind != yaml.MappingNode {
		for _, child := range n.Content {
			v, err := s.normalize(child, depth+1)
			if err != nil {
				return gpuYAMLValue{}, err
			}
			if err := appendValue(v); err != nil {
				return gpuYAMLValue{}, err
			}
		}
	} else {
		keys := map[string]bool{}
		var merges []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			v, err := s.normalize(n.Content[i+1], depth+1)
			if err != nil {
				return gpuYAMLValue{}, err
			}
			if key.Tag == "!!merge" {
				if v.node.Kind == yaml.SequenceNode {
					merges = append(merges, v.node.Content...)
				} else {
					merges = append(merges, v.node)
				}
				continue
			}
			k, err := s.normalize(key, depth+1)
			if err != nil {
				return gpuYAMLValue{}, err
			}
			if err := appendValue(k); err != nil {
				return gpuYAMLValue{}, err
			}
			if err := appendValue(v); err != nil {
				return gpuYAMLValue{}, err
			}
			keys[key.Value] = true
		}
		for _, inherited := range merges {
			if inherited.Kind != yaml.MappingNode {
				return gpuYAMLValue{}, fmt.Errorf("YAML merge requires mapping")
			}
			for i := 0; i+1 < len(inherited.Content); i += 2 {
				// Shadowed keys still cost work: repeated merge lists must not
				// evade the budget just because their effective output is small.
				if err := s.step(); err != nil {
					return gpuYAMLValue{}, err
				}
				key := inherited.Content[i]
				if !keys[key.Value] {
					for _, child := range inherited.Content[i : i+2] {
						if err := appendValue(s.memo[child]); err != nil {
							return gpuYAMLValue{}, err
						}
					}
					keys[key.Value] = true
				}
			}
		}
	}
	// Index both parsed and effective nodes: merges refer to effective children.
	s.memo[n] = result
	s.memo[&out] = result
	return result, nil
}

// The registry instance owns one Host binding, even when its pool selection also
// contains other collected hosts. Use canonical parent identities from fixture,
// not a second shared-mode reachability implementation in the loader.
func gpuInstanceOwnsTarget(ci ConstructInstance, key string) bool {
	if ci.Kind != KindHost {
		return true
	}
	if ci.Fixtures == nil || ci.Fixtures.Host == nil || ci.Fixtures.Host.GPU == nil {
		return false
	}
	node := ci.Fixtures.Host.GPU
	if node.Host != ci.Fixtures.Host {
		return false
	}
	for _, gpu := range node.GPUs {
		if key == gpu.Key || slices.Contains(ci.Fixtures.GPU.Topology.GPUParentTargets(gpu.Key), key) {
			return true
		}
	}
	return false
}

func validateBareMetal(c *ClusterDecl) error {
	if len(c.NodeGroups) > 0 || c.Observability != nil && c.Observability.CloudWatch != nil && *c.Observability.CloudWatch {
		return fmt.Errorf("baremetal cluster cannot enable CloudWatch/use EKS node groups")
	}
	p := c.Platform
	if p == nil || p.OS != "" || p.OSImage == "" || p.OSID == "" || p.ContainerRuntime == "" || p.KubeletVersion == "" || p.KubernetesVersion == "" || p.KernelVersion == "" {
		return fmt.Errorf("baremetal cluster requires explicit exact platform block")
	}
	names := map[string]bool{}
	ips := map[string]bool{}
	for _, n := range c.Nodes {
		a, err := netip.ParseAddr(n.IP)
		if n.Hostname == "" || names[n.Hostname] || n.CPUs <= 0 || math.IsNaN(n.MemoryGiB) || math.IsInf(n.MemoryGiB, 0) || n.MemoryGiB <= 0 || (n.Arch != "arm64" && n.Arch != "x86_64") || err != nil || !a.Is4() || ips[n.IP] {
			return fmt.Errorf("invalid or duplicate baremetal node capacity/IP")
		}
		names[n.Hostname] = true
		ips[n.IP] = true
	}
	return nil
}
func clusterGPUSelection(t *fixture.GPUTopology, cluster string) *fixture.GPUSelection {
	if t == nil {
		return nil
	}
	var pools []string
	for _, p := range t.Pools {
		if p.KubernetesCluster == cluster {
			pools = append(pools, p.Name)
		}
	}
	if len(pools) == 0 {
		return nil
	}
	s, _ := fixture.SelectGPUTopology(t, pools, nil, nil, nil)
	return s
}

var gpuConsumerKinds = []string{"dcgm", "snmp_exporter", "redfish", "spectrumx", "nvlink", "bcm", "mission_control", "runai", "vast", "rack_facility", "slurm", "inference_serving"}

func splitGPUSelectors(t *fixture.GPUTopology, kind string, node *yaml.Node) (*fixture.GPUSelection, *yaml.Node, error) {
	rest := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var p, f, st, sc []string
	present := false
	if node != nil && node.Kind != 0 {
		if node.Kind != yaml.MappingNode {
			return nil, node, nil
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			var dest *[]string
			switch key.Value {
			case "gpu_pools":
				dest = &p
			case "gpu_fabrics":
				dest = &f
			case "gpu_storage_clusters":
				dest = &st
			case "gpu_schedulers":
				dest = &sc
			default:
				if strings.HasPrefix(key.Value, "gpu_") {
					return nil, nil, fmt.Errorf("unknown GPU selector %q", key.Value)
				}
				rest.Content = append(rest.Content, key, value)
				continue
			}
			present = true
			if err := value.Decode(dest); err != nil {
				return nil, nil, err
			}
		}
	}
	if !present {
		if slices.Contains(gpuConsumerKinds, kind) {
			return nil, nil, fmt.Errorf("GPU-consuming integration requires at least one selector")
		}
		return nil, rest, nil
	}
	if len(p)+len(f)+len(st)+len(sc) == 0 {
		return nil, nil, fmt.Errorf("GPU selectors must select at least one entity")
	}
	s, err := fixture.SelectGPUTopology(t, p, f, st, sc)
	return s, rest, err
}
func validateResolvedEffect(r *Resolved, reg *core.Registry, e EffectDecl, axes map[string]failuremode.Axis, vocab []failuremode.Mode, multi map[string]bool) error {
	if err := validateEffect(e, axes, vocab, multi); err != nil {
		return err
	}
	if r.GPU == nil {
		return nil
	}
	shared := false
	for _, c := range fixture.GPUFailureContracts() {
		if c.Name == e.Mode {
			shared = true
		}
	}
	scopes := expandScopes(e.Target, r.Targets)
	if e.Target == "" {
		axis, _ := soleAxis(vocab, e.Mode)
		scopes = nil
		for _, x := range r.Targets {
			if x.Axis == axis {
				scopes = append(scopes, x.Name)
			}
		}
	}
	for _, key := range scopes {
		x, isGPU := r.GPU.Target(key)
		if !isGPU {
			if shared {
				return fmt.Errorf("shared GPU mode cannot target non-GPU identity %q", key)
			}
			continue
		}
		covered := false
		for _, ci := range r.Constructs {
			cr, ok := reg.Construct(ci.Kind)
			if !ok || ci.Fixtures.GPU == nil {
				continue
			}
			registered := false
			for _, m := range cr.FailureModes {
				if m.Name == e.Mode && m.Axis == x.Axis {
					registered = true
				}
			}
			if !registered {
				continue
			}
			if shared {
				if ci.Fixtures.GPU.CoversTarget(ci.Kind, e.Mode, key) && gpuInstanceOwnsTarget(ci, key) {
					covered = true
				}
			} else if gpuLocalMembership(ci.Fixtures.GPU, key) {
				covered = true
			}
		}
		if !covered {
			return fmt.Errorf("mode %q: no enabled compatible consumer covers %q (axis/subkind/selection mismatch)", e.Mode, key)
		}
	}
	return nil
}

// Local collection membership is disjoint from shared physical-effect reachability.
// Source-specific module/subkind compatibility belongs to the consumer builder.
func gpuLocalMembership(s *fixture.GPUSelection, key string) bool {
	if s == nil || s.Topology == nil {
		return false
	}
	for _, d := range s.Devices() {
		if d.Key == key {
			return d.Collection != nil && d.Collection.Source.URL != "" && d.Collection.Source.Revision != "" && len(d.Collection.Source.SHA256) == 64 && d.Collection.Source.Section != "" && d.Collection.Protocol != "" && (d.Collection.Module != "" || (d.Kind != "pdu" && d.Collection.Protocol != "snmp"))
		}
	}
	for _, n := range s.Nodes() {
		if n.Key == key {
			return true
		}
		for _, g := range n.GPUs {
			if g.Key == key {
				return true
			}
		}
	}
	for _, r := range s.Racks() {
		if r.Key == key {
			return true
		}
	}
	for _, f := range s.Fabrics() {
		if f.Key == key {
			return true
		}
		for _, d := range f.Devices {
			if d.Key == key {
				return d.Collection != nil
			}
		}
		for _, p := range f.Ports {
			if p.Key == key {
				for _, d := range f.Devices {
					if d.Key == p.DeviceKey {
						return d.Collection != nil
					}
				}
			}
		}
	}
	for _, d := range s.Domains() {
		if d.Key == key {
			return true
		}
		for _, p := range d.Partitions {
			if p.Key == key {
				return true
			}
		}
		for _, p := range d.Ports {
			if p.Key == key {
				return true
			}
		}
	}
	for _, st := range s.Topology.StorageClusters {
		if slices.Contains(s.StorageClusterNames, st.Name) && st.Key == key {
			return true
		}
	}
	for _, sc := range s.Topology.Schedulers {
		if slices.Contains(s.SchedulerNames, sc.Name) {
			if sc.Key == key {
				return true
			}
			for _, p := range sc.Projects {
				if p.Key == key {
					return true
				}
			}
			for _, w := range sc.Workloads {
				if w.Key == key {
					return true
				}
			}
		}
	}
	return false
}

type gpuIdentityClaim struct{ class, value string }

// Retain physical claim records only after the fixture has validated and generated
// their canonical targets. Unselected/orphan racks must still claim endpoint identity;
// collection selection must NOT be broadened merely to enumerate these claims.
func captureRackDeviceClaims(seed string, t *fixture.GPUTopology, racks []fixture.GPURackSpec) []gpuIdentityClaim {
	var out []gpuIdentityClaim
	for _, r := range racks {
		for _, d := range r.FacilityDevices {
			key := "device:" + d.Name
			if _, ok := t.Target(key); !ok {
				continue
			}
			serial := d.Serial
			if serial == "" {
				serial = fixture.GPUSerial(seed, "device_serial", key)
			}
			out = append(out, gpuIdentityClaim{"hostname", d.Name}, gpuIdentityClaim{"serial", serial})
			if addr, err := netip.ParseAddr(d.ManagementIP); err == nil {
				out = append(out, gpuIdentityClaim{"IP", addr.String()})
			}
			for _, nic := range d.NICs {
				if addr, err := netip.ParseAddr(nic.IP); err == nil {
					out = append(out, gpuIdentityClaim{"IP", addr.String()})
				}
			}
		}
	}
	return out
}

func claimGPUIdentities(r *Resolved, claim func(string, string) error) error {
	if r.GPU == nil {
		return nil
	}
	t := r.GPU
	for _, c := range r.gpuDeviceClaims {
		if err := claim(c.class, c.value); err != nil {
			return err
		}
	}
	add := func(class, key string) error {
		if key == "" {
			return nil
		}
		return claim(class, key)
	}
	for _, n := range t.Nodes {
		for _, entry := range [][2]string{{"hostname", n.Node.Hostname}, {"IP", n.Node.PrivateIP}, {"serial", n.Serial}, {"tray ID", n.TrayID}} {
			if err := add(entry[0], entry[1]); err != nil {
				return err
			}
		}
		for _, nic := range n.NICs {
			if err := add("IP", nic.IP); err != nil {
				return err
			}
		}
		for _, g := range n.GPUs {
			if err := add("GPU UUID", g.UUID); err != nil {
				return err
			}
			if err := add("serial", g.BoardSerial); err != nil {
				return err
			}
		}
	}
	allPools, allFabrics, allStorage, allSchedulers := []string{}, []string{}, []string{}, []string{}
	for _, p := range t.Pools {
		allPools = append(allPools, p.Name)
	}
	for _, f := range t.Fabrics {
		allFabrics = append(allFabrics, f.Name)
	}
	for _, s := range t.StorageClusters {
		allStorage = append(allStorage, s.Name)
	}
	for _, s := range t.Schedulers {
		allSchedulers = append(allSchedulers, s.Name)
	}
	sel, _ := fixture.SelectGPUTopology(t, allPools, allFabrics, allStorage, allSchedulers)
	for _, d := range sel.Devices() {
		for _, entry := range [][2]string{{"hostname", d.Name}, {"IP", d.ManagementIP}, {"serial", d.Serial}} {
			if err := add(entry[0], entry[1]); err != nil {
				return err
			}
		}
		for _, nic := range d.NICs {
			if err := add("IP", nic.IP); err != nil {
				return err
			}
		}
	}
	for _, tr := range t.Trays {
		if err := add("tray ID", tr.ID); err != nil {
			return err
		}
	}
	for _, x := range t.Targets {
		if err := add("GPU target", x.Key); err != nil {
			return err
		}
	}
	return nil
}
