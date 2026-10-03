// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
)

func hgxMinimalDecl() Decl {
	d := gpuMinimalDecl()
	d.Name = "hgx-minimal"
	d.GPUCompute.Racks[0].Shape = "hgx"
	p := &d.GPUCompute.Pools[0]
	p.Shape, p.GPUModel, p.GPUsPerNode = "hgx", "h100_sxm_80gb", 8
	p.Operating.FullPowerRiseC, p.Operating.CoolingFaultRiseC = 35, 10
	p.Operating.NVLinkErrorsPerSecond = 1 // Diagnostic test assumption, no healthy errors.
	return d
}

// Catches rejection of the eight-GPU declaration at the public loader, not just a constructor.
func TestHGXFixtureLoad(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-hgx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Load(data, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSet([]*Resolved{r}); err != nil {
		t.Fatal(err)
	}
	if r.GPU == nil || len(r.GPU.Nodes) != 1 || len(r.GPU.Nodes[0].GPUs) != 8 {
		t.Fatal("eight-GPU HGX node absent")
	}
	n := r.GPU.Nodes[0]
	if len(n.GraceCPUs) != 0 || n.TrayKey != "" || len(r.GPU.Trays) != 0 || r.GPU.Racks[0].DomainKey != "" {
		t.Fatal("HGX inherited NVL72 inventory")
	}
	for _, ci := range r.Constructs {
		if ci.Fixtures != nil && ci.Fixtures.Cluster != nil {
			for i := range ci.Fixtures.Cluster.Nodes {
				if ci.Fixtures.Cluster.Nodes[i].Hostname == n.Node.Hostname && n.Node != &ci.Fixtures.Cluster.Nodes[i] {
					t.Fatal("canonical cluster pointer lost")
				}
			}
		}
	}
	sel, _ := fixture.SelectGPUTopology(r.GPU, []string{"hgx-pool-a"}, nil, nil, nil)
	if err := fixture.RequireGPUCapabilities(sel, fixture.GPUCapabilities{ThermalEnvelope: true, UsableFramebuffer: true, NVLinkCorrelation: true}); err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("HGX_INVENTORY_OUTPUT"); path != "" {
		inventory := map[string]any{"topology": r.GPU, "devices": sel.Devices(), "targets": sel.Targets("dcgm")}
		data, err := json.MarshalIndent(inventory, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("HGX inventory: nodes=%d GPUs/node=%d domains=%d switches/node=%d edges/node=%d source_profile=dgxh100_hgxh100-595.91.07", len(r.GPU.Nodes), len(n.GPUs), len(sel.Domains()), len(sel.Domains()[0].SwitchKeys), len(sel.Domains()[0].Links))
	for _, c := range []fixture.GPUCapabilities{{NVSwitchEntities: true}, {GraceEntities: true}} {
		if fixture.RequireGPUCapabilities(sel, c) == nil {
			t.Fatal("unsourced entity admission")
		}
	}
}

// Catches shape-specific field leakage, guessed profiles and incomplete logical memberships.
func TestHGXLoaderRejections(t *testing.T) {
	tests := []struct {
		name, reason string
		change       func(*Decl)
	}{
		{"model", "incompatible", func(d *Decl) { d.GPUCompute.Pools[0].GPUModel = "h100_pcie_80gb" }},
		{"count", "must be 8", func(d *Decl) { d.GPUCompute.Pools[0].GPUsPerNode = 7 }},
		{"omitted-count", "must be 8", func(d *Decl) { d.GPUCompute.Pools[0].GPUsPerNode = 0 }},
		{"rack", "placement/rack", func(d *Decl) { d.GPUCompute.Racks[0].Shape = "pcie" }},
		{"rack-height", "rack slot out of range", func(d *Decl) { d.GPUCompute.Racks[0].HeightU = 3 }},
		{"placement", "must be positive", func(d *Decl) { d.GPUCompute.Pools[0].Placements[0].NodeHeightU = 0 }},
		{"profile", "unsupported HGX NVLink profile", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Profile: "guessed"}}}
		}},
		{"module-duplicate", "permutation", func(d *Decl) { id := 1; d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{HGXModuleID: &id}} }},
		{"module-range", "permutation", func(d *Decl) { id := 8; d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{HGXModuleID: &id}} }},
		{"switch-range", "ordinal out of range", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Switches: []fixture.NVSwitchSpec{{Ordinal: 4}}}}}
		}},
		{"switch-duplicate", "duplicate HGX switch ordinal", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Switches: []fixture.NVSwitchSpec{{Ordinal: 0}, {Ordinal: 0}}}}}
		}},
		{"switch-name", "invalid device name", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Switches: []fixture.NVSwitchSpec{{Name: "bad/name"}}}}}
		}},
		{"switch-host-collision", "duplicate hostname", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Switches: []fixture.NVSwitchSpec{{Name: "node-a-0000"}}}}}
		}},
		{"switch-serial-collision", "duplicate serial", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Switches: []fixture.NVSwitchSpec{{Serial: "same"}, {Ordinal: 1, Serial: "same"}}}}}
		}},
		{"partition-coverage", "do not cover", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Partitions: []fixture.NVLinkPartitionSpec{{Name: "one", GPUs: []string{"gpu:node-a-0000/0"}}}}}}
		}},
		{"partition-overlap", "overlap", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Partitions: []fixture.NVLinkPartitionSpec{{Name: "one", GPUs: []string{"gpu:node-a-0000/0"}}, {Name: "two", GPUs: []string{"gpu:node-a-0000/0"}}}}}}
		}},
		{"partition-nonmember", "nonexistent", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Partitions: []fixture.NVLinkPartitionSpec{{Name: "one", GPUs: []string{"gpu:other/0"}}}}}}
		}},
		{"entity-cross-node", "outside HGX node domain", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{EntityMappings: []fixture.GPUEntityMappingSpec{{Key: "node:other", Kind: "nvswitch", VendorID: "0", Source: gpuTestSource()}}}}}
		}},
		{"entity-unsourced", "invalid entity mapping", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{EntityMappings: []fixture.GPUEntityMappingSpec{{Key: "node:node-a-0000", Kind: "node", VendorID: "0"}}}}}
		}},
		{"rack-nvlink", "NVLink requires NVL72", func(d *Decl) { d.GPUCompute.Racks[0].NVLink = &fixture.NVLinkSpec{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := hgxMinimalDecl()
			tc.change(&d)
			_, err := gpuDeclLoad(t, d, testRegistry(t))
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected %s, got %v", tc.reason, err)
			}
		})
	}
	for _, count := range []int{3, 4, 5, 6, 7, 8, 9} {
		d := gpuMinimalDecl()
		d.GPUCompute.Pools[0].GPUsPerNode = count
		_, err := gpuDeclLoad(t, d, testRegistry(t))
		if (err == nil) != (count >= 4 && count <= 8) {
			t.Fatalf("PCIe count %d: %v", count, err)
		}
	}
	for _, shape := range []string{"pcie", "nvl72"} {
		for _, field := range []string{"nvlink", "hgx_module_id"} {
			d := gpuMinimalDecl()
			pi := 0
			if shape == "nvl72" {
				data, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
				if err != nil {
					t.Fatal(err)
				}
				if err = yaml.Unmarshal(data, &d); err != nil {
					t.Fatal(err)
				}
				pi = 1
			}
			d.GPUCompute.Pools[pi].Nodes = []fixture.GPUNodeOverrideSpec{{Ordinal: 0}}
			d.GPUCompute.Pools[pi].GPUs = []fixture.GPUOverrideSpec{{Slot: 0}}
			raw, _ := yaml.Marshal(d)
			anchor := "ordinal: 0"
			if field == "hgx_module_id" {
				anchor = "slot: 0"
			}
			// Mutate the parsed mapping so malformed indentation cannot become the rejection.
			var doc yaml.Node
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			var add func(*yaml.Node)
			add = func(n *yaml.Node) {
				if n.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(n.Content); i += 2 {
						if n.Content[i].Value == anchor[:strings.Index(anchor, ":")] {
							n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"})
							return
						}
					}
				}
				for _, c := range n.Content {
					add(c)
				}
			}
			// Rebuild cleanly; explicit null is present even though typed pointer is nil.
			raw, _ = yaml.Marshal(d)
			_ = yaml.Unmarshal(raw, &doc)
			add(&doc)
			raw, _ = yaml.Marshal(&doc)
			if _, err := Load(raw, testRegistry(t)); err == nil || !strings.Contains(err.Error(), "supported only for hgx") {
				t.Fatalf("old shape %s accepted %s:null or wrong rejection: %v", shape, field, err)
			}
		}
	}
}

// Catches selection leakage between nodes sharing a rack, module/slot conflation,
// domain-less GPU capability laundering and duplicate cross-blueprint identity.
func TestHGXMembershipPermutationAndCapabilities(t *testing.T) {
	d := hgxMinimalDecl()
	p := d.GPUCompute.Pools[0]
	p.Name = "pool-b"
	p.HostnamePrefix = "node-b"
	p.Placements = append([]fixture.GPUPoolPlacementSpec(nil), p.Placements...)
	p.Placements[0].SlotStart = 5
	d.GPUCompute.Pools = append(d.GPUCompute.Pools, p)
	r, err := gpuDeclLoad(t, d, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	again, err := gpuDeclLoad(t, d, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(r.GPU)
	b, _ := json.Marshal(again.GPU)
	if !bytes.Equal(a, b) {
		t.Fatal("same-seed HGX topology differs")
	}
	d.Name = "different-seed"
	other, err := gpuDeclLoad(t, d, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.GPU.Nodes[0].GPUs[0].UUID == other.GPU.Nodes[0].GPUs[0].UUID {
		t.Fatal("seed ignored")
	}
	d.Name = "hgx-minimal"
	sel, _ := fixture.SelectGPUTopology(r.GPU, []string{"pool-a"}, nil, nil, nil)
	if len(sel.Domains()) != 1 {
		t.Fatal("domain selection leaked")
	}
	switches := 0
	for _, dev := range sel.Devices() {
		if dev.Kind == "nvswitch" {
			switches++
			if dev.NodeKey != "node:node-a-0000" {
				t.Fatal("switch selection leaked")
			}
		}
	}
	if switches != 4 {
		t.Fatal("four switches absent")
	}
	for _, target := range r.GPU.Targets {
		if strings.Contains(target.Key, "node-b-0000") && target.Kind == "nvswitch" && sel.CoversTarget("dcgm", "gpu_nvlink_error_burst", target.Key) {
			t.Fatal("unselected switch reachable")
		}
	}
	base := r.GPU.NVLinkForGPU("gpu:node-a-0000/0")
	if len(base) != 18 {
		t.Fatal("eighteen links absent")
	}
	id0, id1 := 1, 0
	d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{Slot: 0, HGXModuleID: &id0}, {Slot: 1, HGXModuleID: &id1}}
	swapped, err := gpuDeclLoad(t, d, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, l := range swapped.GPU.NVLinkForGPU("gpu:node-a-0000/0") {
		if l.GPULinkIndex == 2 {
			got = l.SwitchPortKey
		}
	}
	if got != "nvport:site-a/rack-a/hgx/node-a-0000/default/1/2" {
		t.Fatalf("slot/module conflation: %s", got)
	}
	if err := fixture.RequireGPUCapabilities(sel, fixture.GPUCapabilities{UsableFramebuffer: true}); err == nil {
		t.Fatal("missing framebuffer admitted")
	}
	d.GPUCompute.Pools[0].Operating.FullPowerRiseC = 80
	hot, err := gpuDeclLoad(t, d, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	hs, _ := fixture.SelectGPUTopology(hot.GPU, []string{"pool-a"}, nil, nil, nil)
	if fixture.RequireGPUCapabilities(hs, fixture.GPUCapabilities{ThermalEnvelope: true}) == nil {
		t.Fatal("excessive thermal envelope admitted")
	}
	if ValidateSet([]*Resolved{r, other}) == nil {
		t.Fatal("set identity collision accepted")
	}
	oldData, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var mixed Decl
	if err := yaml.Unmarshal(oldData, &mixed); err != nil {
		t.Fatal(err)
	}
	hgx := hgxMinimalDecl()
	mixed.GPUCompute.Racks = append(mixed.GPUCompute.Racks, hgx.GPUCompute.Racks...)
	mixed.GPUCompute.Pools = append(mixed.GPUCompute.Pools, hgx.GPUCompute.Pools...)
	mr, err := gpuDeclLoad(t, mixed, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(mr.GPU.Domains) != 2 {
		t.Fatal("mixed shape domains missing")
	}
	ms, _ := fixture.SelectGPUTopology(mr.GPU, []string{"pool-a", "pcie-pool"}, nil, nil, nil)
	if err := fixture.RequireGPUCapabilities(ms, fixture.GPUCapabilities{NVLinkCorrelation: true}); err == nil || !strings.Contains(err.Error(), "selected GPU has no sourced") {
		t.Fatalf("HGX laundered unmapped PCIe: %v", err)
	}
}

// Catches rack-wide HGX bursts, partition membership loss and additive double counting.
func TestHGXSharedEdgePhysics(t *testing.T) {
	d := hgxMinimalDecl()
	p := &d.GPUCompute.Pools[0]
	p.NodeCount = 2
	p.Placements[0].NodeCount = 2
	var left, right []string
	for i := 0; i < 8; i++ {
		k := fmt.Sprintf("gpu:node-a-0000/%d", i)
		if i < 4 {
			left = append(left, k)
		} else {
			right = append(right, k)
		}
	}
	p.Nodes = []fixture.GPUNodeOverrideSpec{{NVLink: &fixture.HGXNVLinkSpec{Correlated: true, Partitions: []fixture.NVLinkPartitionSpec{{Name: "left", GPUs: left}, {Name: "right", GPUs: right}}}}}
	r, err := gpuDeclLoad(t, d, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	top := r.GPU
	sel, _ := fixture.SelectGPUTopology(top, []string{"pool-a"}, nil, nil, nil)
	stem := "site-a/rack-a/hgx/node-a-0000/default"
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		target string
		edges  int
	}{{"gpu:node-a-0000/0", 18}, {"domain:" + stem, 144}, {"partition:" + stem + "/left", 72}, {"nvswitch:" + stem + "/0", 32}, {"nvswitch:" + stem + "/1", 40}, {"nvport:" + stem + "/1/36", 1}} {
		if !sel.CoversTarget("dcgm", "gpu_nvlink_error_burst", tc.target) {
			t.Fatalf("sourced target not reachable: %s", tc.target)
		}
		snap := fixture.GPUAllocationPlan(top, at, func(_ time.Time, m, k string) (bool, float64) {
			return m == "gpu_nvlink_error_burst" && k == tc.target, 0.5
		})
		phys, err := fixture.GPUOperatingPoints(top, snap)
		if err != nil {
			t.Fatal(err)
		}
		affected := 0
		integral := 0.0
		for _, e := range phys.NVLinkErrors {
			if e.ErrorsPerSecond != 0 {
				affected++
				integral += e.ErrorsPerSecond * 10
				if !strings.HasPrefix(e.GPUKey, "gpu:node-a-0000/") {
					t.Fatal("cross-node fault propagation")
				}
				if e.ErrorsPerSecond != 0.5 {
					t.Fatal("edge rate not exact")
				}
			}
		}
		if affected != tc.edges || integral != float64(tc.edges)*5 {
			t.Fatalf("%s edges/integral %d/%g", tc.target, affected, integral)
		}
	}
	overlap := fixture.GPUAllocationPlan(top, at, func(_ time.Time, m, k string) (bool, float64) {
		return m == "gpu_nvlink_error_burst" && (k == "domain:"+stem || k == "gpu:node-a-0000/0"), 0.5
	})
	phys, err := fixture.GPUOperatingPoints(top, overlap)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, e := range phys.NVLinkErrors {
		sum += e.ErrorsPerSecond
	}
	if sum != 72 {
		t.Fatalf("overlap double counted: %g", sum)
	}
	copyTop := *top
	if _, err := fixture.GPUOperatingPoints(&copyTop, overlap); err == nil {
		t.Fatal("snapshot origin accepted reconstructed topology")
	}
}

// BEGIN LEGACY PROOF
// This identical function is executed against immutable old Go blobs and the
// candidate. It freezes actual allocation time/fault inputs, not a randomized CLI.
func TestHGXLegacyFixedInputProof(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := testRegistry(t)
	load := func(raw []byte) *Resolved {
		t.Helper()
		r, e := Load(raw, reg)
		if e != nil {
			t.Fatal(e)
		}
		if e = ValidateSet([]*Resolved{r}); e != nil {
			t.Fatal(e)
		}
		return r
	}
	baseline := load(data)
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	held, pending := start, start
	if os.Getenv("HGX_COMPAT_DISCOVER") == "1" {
		gotHeld, gotPending := false, false
		for i := 0; i < 24*60; i++ {
			at := start.Add(time.Duration(i) * time.Minute)
			s := fixture.GPUAllocationPlan(baseline.GPU, at, nil)
			running := 0
			for _, w := range s.Workloads {
				if w.Phase == "running" {
					running++
				}
				if w.WorkloadKey == "gpuworkload:scheduler-a/training-a" && w.Phase == "pending" && !gotPending {
					pending = at
					gotPending = true
				}
			}
			if running == 2 && !gotHeld {
				held = at
				gotHeld = true
			}
			if gotHeld && gotPending {
				break
			}
		}
		if !gotHeld || !gotPending {
			t.Fatal("fixed clock discovery failed")
		}
		times, _ := json.Marshal([]time.Time{held, pending})
		if err := os.WriteFile(os.Getenv("HGX_COMPAT_TIMES"), times, 0600); err != nil {
			t.Fatal(err)
		}
	} else {
		// Discovered once against immutable 76c3ea3, never chosen from candidate phases.
		held, pending = start, start.Add(145*time.Minute)
		if path := os.Getenv("HGX_COMPAT_TIMES"); path != "" {
			times, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var clock []time.Time
			if err = json.Unmarshal(times, &clock); err != nil || len(clock) != 2 {
				t.Fatalf("fixed clock input: %v", err)
			}
			held, pending = clock[0], clock[1]
		}
	}
	output := map[string]any{"fixture_sha_input": string(data), "held": held, "pending": pending}
	for _, profile := range []string{"unchanged", "mapped-nvl72"} {
		r := baseline
		if profile == "mapped-nvl72" {
			var d Decl
			if err := yaml.Unmarshal(data, &d); err != nil {
				t.Fatal(err)
			}
			nv := d.GPUCompute.Racks[1].NVLink
			one := 1
			source := gpuTestSource()
			nv.LinksPerGPU = &one
			nv.LinkCountSource = &source
			nv.Correlated = true
			// Entirely synthetic test-only source mapping: never a vendor profile.
			for tray := 0; tray < 9; tray++ {
				for sw := 0; sw < 2; sw++ {
					for port := 0; port < 72; port++ {
						nv.PortMappings = append(nv.PortMappings, fixture.NVLinkPortMappingSpec{Tray: tray, Switch: sw, Port: port, VendorID: fmt.Sprint(port), Source: source})
					}
				}
			}
			for node := 0; node < 18; node++ {
				for slot := 0; slot < 4; slot++ {
					nv.Links = append(nv.Links, fixture.NVLinkLinkSpec{GPU: fmt.Sprintf("gpu:nvl-node-%04d/%d", node, slot), GPULinkIndex: 0, Tray: node / 2, Switch: 0, Port: (node%2)*4 + slot, Source: source})
				}
			}
			raw, _ := yaml.Marshal(d)
			r = load(raw)
		}
		top := r.GPU
		record := map[string]any{"topology": top}
		clusters := map[string]*fixture.Cluster{}
		for _, ci := range r.Constructs {
			if ci.Fixtures != nil && ci.Fixtures.Cluster != nil {
				cl := ci.Fixtures.Cluster
				clusters[cl.Name] = cl
			}
		}
		record["clusters"] = clusters
		for _, names := range [][]string{{"pcie-pool"}, {"nvl-pool"}, {"pcie-pool", "nvl-pool"}} {
			sel, e := fixture.SelectGPUTopology(top, names, nil, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			sr := map[string]any{"nodes": sel.Nodes(), "racks": sel.Racks(), "devices": sel.Devices(), "domains": sel.Domains(), "fabrics": sel.Fabrics()}
			covers := map[string]bool{}
			targets := map[string][]fixture.GPUTarget{}
			for _, kind := range []string{"dcgm", "nvlink", "runai", "k8s_cluster", "host", "rack_facility"} {
				targets[kind] = sel.Targets(kind)
				for _, mode := range fixture.GPUFailureModes(kind) {
					for _, x := range top.Targets {
						covers[kind+"/"+mode.Name+"/"+x.Key] = sel.CoversTarget(kind, mode.Name, x.Key)
					}
				}
			}
			sr["covers"], sr["targets"] = covers, targets
			caps := map[string]string{}
			for name, c := range map[string]fixture.GPUCapabilities{"thermal": {ThermalEnvelope: true}, "framebuffer": {UsableFramebuffer: true}, "correlation": {NVLinkCorrelation: true}, "switch": {NVSwitchEntities: true}, "grace": {GraceEntities: true}} {
				if e := fixture.RequireGPUCapabilities(sel, c); e != nil {
					caps[name] = e.Error()
				} else {
					caps[name] = "accepted"
				}
			}
			sr["capabilities"] = caps
			record["selection/"+strings.Join(names, ",")] = sr
		}
		cases := []struct {
			name, mode, target string
			at                 time.Time
		}{{"normal", "", "", start}, {"held", "", "", held}, {"pending", "", "", pending}, {"fatal", "gpu_fallen_off_bus", "gpu:nvl-node-0000/2", held}, {"pending-fatal", "gpu_fallen_off_bus", "gpu:nvl-node-0000/2", pending}, {"preemption", "gpu_preemption_storm", "gpuworkload:scheduler-a/training-a", held}, {"quota", "gpu_quota_exhaustion", "project:scheduler-a/research", held}, {"cooling", "gpu_cooling_fault", "rack:site-a/nvl-a", held}, {"idle-cooling", "gpu_cooling_fault", "rack:site-a/nvl-a", pending}, {"unavailable-cooling", "gpu_fallen_off_bus", "gpu:nvl-node-0000/2", held}, {"backend", "gpu_backend_congestion", "fabric:backend-a", held}, {"storage", "gpu_storage_latency", "storage:storage-a", held}, {"mapped-error", "gpu_nvlink_error_burst", "gpu:nvl-node-0000/0", held}, {"mapped-domain", "gpu_nvlink_degraded", "domain:site-a/nvl-a/domain-a", held}}
		for _, tc := range cases {
			snap := fixture.GPUAllocationPlan(top, tc.at, func(_ time.Time, m, k string) (bool, float64) {
				return (m == tc.mode && k == tc.target) || (tc.name == "unavailable-cooling" && m == "gpu_cooling_fault" && k == "rack:site-a/nvl-a"), 0.5
			})
			if tc.mode != "" && !(tc.name == "mapped-error" && profile == "unchanged") && len(snap.Faults) == 0 {
				t.Fatalf("%s did not activate physical fault", tc.name)
			}
			for _, w := range snap.Workloads {
				if w.WorkloadKey == "gpuworkload:scheduler-a/training-a" {
					expect := map[string]string{"held": "running", "pending": "pending", "fatal": "failed", "preemption": "preempted", "quota": "pending"}[tc.name]
					if expect != "" && w.Phase != expect {
						t.Fatalf("%s: phase %s, expected %s", tc.name, w.Phase, expect)
					}
				}
			}
			phys, e := fixture.GPUOperatingPoints(top, snap)
			if e != nil {
				t.Fatal(e)
			}
			views := map[string]fixture.Cluster{}
			for name, cl := range clusters {
				views[name] = fixture.GPUClusterView(cl, snap)
			}
			record[tc.name] = map[string]any{"allocation": snap, "physical": phys, "cluster_views": views}
			other := *top
			if _, e := fixture.GPUOperatingPoints(&other, snap); e == nil {
				t.Fatal("snapshot origin guard absent")
			}
		}
		output[profile] = record
	}
	rejects := map[string]string{}
	for _, count := range []int{3, 4, 5, 6, 7, 8, 9} {
		d := gpuMinimalDecl()
		d.GPUCompute.Pools[0].GPUsPerNode = count
		r, e := gpuDeclLoad(t, d, reg)
		if e != nil {
			rejects[fmt.Sprint(count)] = e.Error()
		} else {
			if e = ValidateSet([]*Resolved{r}); e != nil {
				t.Fatal(e)
			}
			rejects[fmt.Sprint(count)] = "accepted"
		}
		if (e == nil) != (count >= 4 && count <= 8) {
			t.Fatalf("legacy PCIe admission %d: %v", count, e)
		}
	}
	output["rejections"] = rejects
	raw, e := json.Marshal(output)
	if e != nil {
		t.Fatal(e)
	}
	if path := os.Getenv("HGX_COMPAT_OUTPUT"); path != "" {
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("fixed real-loader compatibility: held=%s pending=%s output_bytes=%d", held.Format(time.RFC3339), pending.Format(time.RFC3339), len(raw))
}

// END LEGACY PROOF
