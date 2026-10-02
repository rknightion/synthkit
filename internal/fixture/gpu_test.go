// SPDX-License-Identifier: AGPL-3.0-only

package fixture_test

import (
	"encoding/json"
	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"
	"slices"
	"testing"
)

func gpuSpec(t *testing.T) fixture.GPUTopologySpec {
	t.Helper()
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		GPUCompute fixture.GPUTopologySpec `yaml:"gpu_compute"`
	}
	if err = yaml.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d.GPUCompute
}
func gpuBuild(t *testing.T, seed string, spec fixture.GPUTopologySpec) *fixture.GPUTopology {
	t.Helper()
	cl := &fixture.Cluster{Name: "compute-user", Type: "baremetal", Seed: seed}
	top, err := fixture.BuildGPUTopology(seed, spec, map[string]*fixture.Cluster{cl.Name: cl}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range top.Nodes {
		found := false
		for i := range cl.Nodes {
			if n.Node == &cl.Nodes[i] {
				found = true
			}
		}
		if !found {
			t.Fatalf("node %s not canonical", n.Key)
		}
	}
	return top
}
func gpuJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestGPUIdentityAndTopologyBoundary(t *testing.T) {
	s := gpuSpec(t)
	s.Fabrics[0].Devices[0].Ports = append(s.Fabrics[0].Devices[0].Ports, fixture.GPUDevicePortSpec{Name: "swp22", SpeedGbps: 100})
	s.Racks[0].FacilityDevices = []fixture.GPUDeviceSpec{{Name: "facility-pdu", Kind: "pdu", ManagementIP: "10.20.9.1", NICs: []fixture.GPUDeviceNICSpec{{Name: "eth0", IP: "10.40.0.1", Fabric: "frontend-a", Switch: "frontend-leaf-a", Port: "swp22", SpeedGbps: 100}}}}
	a := gpuBuild(t, "seed-a", s)
	b := gpuBuild(t, "seed-a", s)
	if !reflect.DeepEqual(gpuJSON(t, a), gpuJSON(t, b)) {
		t.Fatal("independent builds differ")
	}
	slices.Reverse(s.Racks)
	slices.Reverse(s.Pools)
	slices.Reverse(s.Fabrics)
	slices.Reverse(s.Schedulers[0].Projects)
	slices.Reverse(s.Schedulers[0].Workloads)
	c := gpuBuild(t, "seed-a", s)
	if !reflect.DeepEqual(gpuJSON(t, a), gpuJSON(t, c)) {
		t.Fatal("declaration reordering changed canonical topology")
	}
	d := gpuBuild(t, "seed-b", s)
	if a.Nodes[0].GPUs[0].UUID == d.Nodes[0].GPUs[0].UUID {
		t.Fatal("different seed kept GPU identity")
	}
	if len(a.Nodes) != 20 || len(a.Domains) != 1 || len(a.Domains[0].GPUKeys) != 72 || len(a.Domains[0].SwitchKeys) != 18 || len(a.Domains[0].Ports) != 1296 {
		t.Fatal("documented NVL72 inventory absent")
	}
	if len(a.Domains[0].Partitions) != 1 || a.Domains[0].Partitions[0].Name != "default" {
		t.Fatal("default owning partition absent")
	}
	for _, n := range a.Nodes {
		if n.Node.Hostname == "pcie-node-0000" {
			if n.GPUs[0].UUID != fixture.GPUUUID("seed-a", "pcie-pool", "0", "0") || n.GPUs[0].Minor != 0 || n.GPUs[0].PCIBusID != "00000000:20:00.0" {
				t.Fatal("physical identity default mismatch")
			}
			if n.StorageClient == nil || n.StorageClient.IP != n.NICs[0].IP || n.NICs[0].PortKey == "" {
				t.Fatal("storage/frontend attachment absent")
			}
		}
	}
	deviceSel, _ := fixture.SelectGPUTopology(a, []string{"pcie-pool"}, nil, nil, nil)
	foundDevice := false
	for _, d := range deviceSel.Devices() {
		if d.Name == "facility-pdu" {
			foundDevice = true
			if len(d.NICs) != 1 || d.NICs[0].PortKey != "port:frontend-a/frontend-leaf-a/swp22" || d.NICs[0].IP != "10.40.0.1" {
				t.Fatal("facility NIC lost canonical attachment/address")
			}
		}
	}
	if !foundDevice {
		t.Fatal("canonical facility device absent")
	}
	keys := fixture.GPUModelKeys()
	if len(keys) != 4 {
		t.Fatal("catalogue missing products")
	}
	for _, key := range keys {
		m, ok := fixture.LookupGPUModel(key)
		if !ok || m.MaxPowerW == nil || m.AdvertisedMemoryGB <= 0 || m.SlowdownTempC != nil || m.ShutdownTempC != nil || m.MaxOperatingTempC != nil {
			t.Fatal("strict sourced nullable catalogue mismatch")
		}
	}
	if _, ok := fixture.LookupGPUModel("unknown"); ok {
		t.Fatal("unknown model fallback")
	}
	sel, err := fixture.SelectGPUTopology(a, []string{"pcie-pool"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.RequireGPUCapabilities(sel, fixture.GPUCapabilities{ThermalEnvelope: true}); err == nil {
		t.Fatal("unknown thermal limits accepted")
	}
	if err = fixture.RequireGPUCapabilities(sel, fixture.GPUCapabilities{UsableFramebuffer: true}); err == nil {
		t.Fatal("advertised memory treated as usable framebuffer")
	}
	nvl, _ := fixture.SelectGPUTopology(a, []string{"nvl-pool"}, nil, nil, nil)
	if fixture.RequireGPUCapabilities(nvl, fixture.GPUCapabilities{NVLinkCorrelation: true}) == nil {
		t.Fatal("incomplete source map enabled correlation")
	}
}
func TestGPUSelectionModeSpecificCoverage(t *testing.T) {
	top := gpuBuild(t, "seed-a", gpuSpec(t))
	s, err := fixture.SelectGPUTopology(top, []string{"pcie-pool"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ mode, key string }{{"gpu_preemption_storm", "gpuworkload:scheduler-a/serving-a"}, {"gpu_backend_congestion", "switch:backend-a/backend-leaf-0"}, {"gpu_storage_latency", "storage:storage-a"}} {
		if !s.CoversTarget("dcgm", x.mode, x.key) {
			t.Fatalf("DCGM-only declaration cannot cover %s %s", x.mode, x.key)
		}
	}
	for _, x := range []struct{ mode, key string }{{"gpu_preemption_storm", "gpuworkload:scheduler-a/training-a"}, {"gpu_backend_congestion", "switch:backend-a/backend-leaf-1"}, {"gpu_backend_congestion", "port:frontend-a/frontend-leaf-a/swp1"}, {"gpu_fallen_off_bus", "gpu:nvl-node-0000/0"}, {"gpu_nvlink_error_burst", "gpu:pcie-node-0000/0"}} {
		if s.CoversTarget("dcgm", x.mode, x.key) {
			t.Fatalf("unrelated/source-unsupported target covered: %s", x.key)
		}
	}
	if _, err := fixture.SelectGPUTopology(top, []string{"pcie-pool", "pcie-pool"}, nil, nil, nil); err == nil {
		t.Fatal("duplicate selector accepted")
	}
	if _, err := fixture.SelectGPUTopology(top, []string{"missing"}, nil, nil, nil); err == nil {
		t.Fatal("unknown selector accepted")
	}
}
