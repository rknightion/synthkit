// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Reachability through switches must not hide a missing direct domain-to-node parent.
func TestHGXDomainNodeParent(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-hgx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var decl struct {
		GPUCompute GPUTopologySpec `yaml:"gpu_compute"`
	}
	if err := yaml.Unmarshal(data, &decl); err != nil {
		t.Fatal(err)
	}
	cluster := &Cluster{Name: "compute-cluster", Type: "baremetal", Seed: "hgx-parent"}
	topology, err := BuildGPUTopology("hgx-parent", decl.GPUCompute, map[string]*Cluster{cluster.Name: cluster}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.Domains) == 0 {
		t.Fatal("HGX domain absent")
	}
	for _, domain := range topology.Domains {
		if domain.NodeKey == "" {
			t.Fatalf("HGX domain %s has no owning node", domain.Key)
		}
		i := slices.IndexFunc(topology.Targets, func(target GPUTarget) bool { return target.Key == domain.Key })
		if i < 0 {
			t.Fatalf("HGX domain target %s absent", domain.Key)
		}
		if !slices.Contains(topology.Targets[i].ParentKeys, domain.NodeKey) {
			t.Fatalf("HGX domain %s direct node parent: got %v, want %s", domain.Key, topology.Targets[i].ParentKeys, domain.NodeKey)
		}
	}
}

// Catches an endpoint substitution that preserves every structural count.
func TestHGXProjectionFidelity(t *testing.T) {
	edges, err := parseHGXLinks(hgxLinksCSV)
	if err != nil || len(edges) != 144 {
		t.Fatalf("profile parse: %d %v", len(edges), err)
	}
	swapped := strings.Replace(hgxLinksCSV, "0,0,0,1,36,0,2", "0,0,0,1,37,0,2", 1)
	swapped = strings.Replace(swapped, "0,0,1,1,37,0,2", "0,0,1,1,36,0,2", 1)
	if _, err := parseHGXLinks(swapped); err == nil || !strings.Contains(err.Error(), "audited source projection") {
		t.Fatalf("same-switch port swap accepted or wrong rejection: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(hgxLinksCSV, "0,0,0,1,36,0,2\n", "", 1),
		strings.Replace(hgxLinksCSV, "0,0,0,1,36,0,2", "0,0,1,1,37,0,2", 1),
		strings.Replace(hgxLinksCSV, "0,0,0,1,36,0,2", "0,0,0,1,36,1,2", 1),
	} {
		if _, err := parseHGXLinks(bad); err == nil {
			t.Fatal("incomplete/duplicate/nonlocal map accepted")
		}
	}
}

// Catches thermal-envelope leakage, invalid catalogue admission and caller mutation.
func TestHGXScopedThermalCatalogue(t *testing.T) {
	m, _ := LookupGPUModel("h100_sxm_80gb")
	for _, field := range []string{"SlowdownTempC", "ShutdownTempC", "MaxOperatingTempC"} {
		if !gpuSourceValid(m.Sources[field]) || !strings.Contains(m.Sources[field].Revision, "595.91.07") {
			t.Fatalf("unscoped thermal source %s", field)
		}
	}
	*m.SlowdownTempC, *m.ShutdownTempC, *m.MaxOperatingTempC = 1, 2, 3
	fresh, _ := LookupGPUModel("h100_sxm_80gb")
	if *fresh.SlowdownTempC != 89 || *fresh.ShutdownTempC != 95 || *fresh.MaxOperatingTempC != 87 {
		t.Fatal("lookup thermal pointers shared")
	}
	original := gpuModelsCSV
	defer func() { gpuModelsCSV = original }()
	for _, row := range []string{"89,,87", "NaN,95,87", "+Inf,95,87", "0,95,87", "89,88,87", "89,95,90", "88,95,87"} {
		t.Run(row, func(t *testing.T) {
			gpuModelsCSV = strings.Replace(original, "89,95,87", row, 1)
			defer func() {
				if recover() == nil {
					t.Fatal("invalid thermal envelope accepted")
				}
			}()
			parseGPUModels()
		})
	}
	gpuModelsCSV = strings.Replace(original, "h100_pcie_80gb,H100 PCIe,80,350,,true,7,,,", "h100_pcie_80gb,H100 PCIe,80,350,,true,7,89,95,87", 1)
	defer func() {
		if recover() == nil {
			t.Fatal("unadmitted product thermal envelope accepted")
		}
	}()
	parseGPUModels()
}
