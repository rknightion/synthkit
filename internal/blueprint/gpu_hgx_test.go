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
			t.Run(shape+"-null-"+field, func(t *testing.T) {
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
			})
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
