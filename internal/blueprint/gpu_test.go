// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func gpuTestSource() fixture.GPUFieldSource {
	return fixture.GPUFieldSource{URL: "https://example.invalid/test-only-profile", Revision: "test-only", SHA256: strings.Repeat("a", 64), Section: "test profile fixture; not a vendor contract"}
}
func gpuMinimalDecl() Decl {
	spec := &fixture.GPUTopologySpec{}
	spec.Racks = []fixture.GPURackSpec{{Name: "rack-a", Site: "site-a", Shape: "pcie", Cooling: "air", Physics: fixture.GPURackPhysicsSpec{FixedOverheadW: 100}}}
	spec.Pools = []fixture.GPUPoolSpec{{Name: "pool-a", Shape: "pcie", GPUModel: "h100_pcie_80gb", NodeCount: 1, GPUsPerNode: 4, HostnamePrefix: "node-a", Hardware: fixture.GPUNodeHardwareSpec{CPUs: 64, MemoryGiB: 512, Arch: "x86_64"}, Placements: []fixture.GPUPoolPlacementSpec{{RackKey: "rack:site-a/rack-a", NodeCount: 1, SlotStart: 1, NodeHeightU: 4}}, Operating: fixture.GPUOperatingSpec{IdlePowerFraction: 0.12, AmbientTempC: 25, FullPowerRiseC: 40, CoolingFaultRiseC: 15, CoolingPerfLoss: 0.3, CongestionPerfLoss: 0.6, StoragePerfLoss: 0.5, NodeBasePowerW: 250, NodeDynamicPerCoreW: 2, NodeDynamicPerNICW: 10}}}
	workload := fixture.GPUWorkloadSpec{Name: "workload-a", Project: "project-a", Role: "training", Cycle: "6h", PendingFor: "1m", RunningMin: "1h", RunningMax: "2h", Demand: fixture.GPUDemandSpec{UtilizationBase: 0.5, MemoryReservedFraction: 0.5}, Workers: []fixture.GPUWorkerSpec{{Name: "worker-a", Pool: "pool-a", GPUSlots: []int{0}}}}
	spec.Schedulers = []fixture.GPUSchedulerSpec{{Name: "scheduler-a", Kind: "runai", Pools: []string{"pool-a"}, Projects: []fixture.GPUProjectSpec{{Name: "project-a", GPUQuota: 4}}, Workloads: []fixture.GPUWorkloadSpec{workload}}}
	return Decl{Name: "gpu-minimal", GPUCompute: spec}
}
func gpuDeclLoad(t *testing.T, d Decl, reg *core.Registry) (*Resolved, error) {
	t.Helper()
	b, e := yaml.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	return Load(b, reg)
}
func TestGPULoadRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Decl)
	}{
		{"model-shape", func(d *Decl) { d.GPUCompute.Pools[0].GPUModel = "h100_sxm_80gb" }},
		{"pcie-count", func(d *Decl) { d.GPUCompute.Pools[0].GPUsPerNode = 9 }},
		{"placement-missing", func(d *Decl) { d.GPUCompute.Pools[0].Placements = nil }},
		{"placement-twice", func(d *Decl) { p := &d.GPUCompute.Pools[0]; p.Placements = append(p.Placements, p.Placements[0]) }},
		{"occupied-slots", func(d *Decl) {
			p := d.GPUCompute.Pools[0]
			p.Name = "pool-b"
			p.HostnamePrefix = "node-b"
			d.GPUCompute.Pools = append(d.GPUCompute.Pools, p)
		}},
		{"hardware", func(d *Decl) { d.GPUCompute.Pools[0].Hardware.CPUs = 0 }},
		{"address-network", func(d *Decl) { d.GPUCompute.Pools[0].NodeIPs = &fixture.GPUAddressBlockSpec{CIDR: "10.0.0.0/24"} }},
		{"duplicate-host", func(d *Decl) {
			p := &d.GPUCompute.Pools[0]
			p.NodeCount = 2
			p.Placements[0].NodeCount = 2
			p.Nodes = []fixture.GPUNodeOverrideSpec{{Ordinal: 1, Hostname: "node-a-0000"}}
		}},
		{"duplicate-uuid", func(d *Decl) {
			id := fixture.GPUUUID("any", "one")
			d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{Slot: 0, UUID: id}, {Slot: 1, UUID: id}}
		}},
		{"duplicate-serial", func(d *Decl) {
			d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{Slot: 0, BoardSerial: "duplicate"}, {Slot: 1, BoardSerial: "duplicate"}}
		}},
		{"duplicate-pci", func(d *Decl) {
			d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{Slot: 1, PCIBusID: "00000000:20:00.0"}}
		}},
		{"duplicate-node-override", func(d *Decl) { d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{Ordinal: 0}, {Ordinal: 0}} }},
		{"scheduler-name", func(d *Decl) { d.GPUCompute.Schedulers = append(d.GPUCompute.Schedulers, d.GPUCompute.Schedulers[0]) }},
		{"scheduler-ownership", func(d *Decl) {
			s := d.GPUCompute.Schedulers[0]
			s.Name = "scheduler-b"
			d.GPUCompute.Schedulers = append(d.GPUCompute.Schedulers, s)
		}},
		{"project-name", func(d *Decl) { s := &d.GPUCompute.Schedulers[0]; s.Projects = append(s.Projects, s.Projects[0]) }},
		{"workload-name", func(d *Decl) { s := &d.GPUCompute.Schedulers[0]; s.Workloads = append(s.Workloads, s.Workloads[0]) }},
		{"worker-name", func(d *Decl) {
			w := &d.GPUCompute.Schedulers[0].Workloads[0]
			w.Workers = append(w.Workers, w.Workers[0])
		}},
		{"impossible-worker", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Workers[0].GPUSlots = []int{4} }},
		{"within-job-conflict", func(d *Decl) {
			w := &d.GPUCompute.Schedulers[0].Workloads[0]
			x := w.Workers[0]
			x.Name = "worker-b"
			w.Workers = append(w.Workers, x)
		}},
		{"outside-scheduler-pool", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Workers[0].Pool = "absent" }},
		{"negative-quota", func(d *Decl) { d.GPUCompute.Schedulers[0].Projects[0].GPUQuota = -1 }},
		{"duration-fit", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Cycle = "2h" }},
		{"duration-minute", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].PendingFor = "1s" }},
		{"demand-envelope", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Demand.UtilizationAmplitude = 0.6 }},
		{"memory-envelope", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Demand.MemoryDynamicFraction = 0.6 }},
		{"period-malformed-constant", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Demand.Period = "garbage" }},
		{"period-zero-constant", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Demand.Period = "0s" }},
		{"period-negative-constant", func(d *Decl) { d.GPUCompute.Schedulers[0].Workloads[0].Demand.Period = "-1h" }},
		{"power-limit", func(d *Decl) { p := 351.0; d.GPUCompute.Pools[0].Operating.PowerLimitW = &p }},
		{"invalid-fraction", func(d *Decl) { d.GPUCompute.Pools[0].Operating.CoolingPerfLoss = 1.1 }},
		{"storage-client", func(d *Decl) {
			d.GPUCompute.Pools[0].Storage = &fixture.GPUStorageBindingSpec{Cluster: "absent", NIC: "eth0", Role: "client"}
		}},
		{"invalid-name", func(d *Decl) { d.GPUCompute.Pools[0].Name = "pool:*" }},
		{"source-memory", func(d *Decl) {
			d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{UsableMemory: &fixture.GPUUsableMemorySpec{Bytes: 1}}}
		}},
	}
	reg := testRegistry(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := gpuMinimalDecl()
			tc.mutate(&d)
			if _, err := gpuDeclLoad(t, d, reg); err == nil {
				t.Fatal("invalid blueprint accepted")
			} else if !strings.Contains(err.Error(), "gpu_compute") {
				t.Fatalf("wrong rejection surface: %v", err)
			}
		})
	}
	d := gpuMinimalDecl()
	if _, err := gpuDeclLoad(t, d, reg); err != nil {
		t.Fatal(err)
	}
	b, _ := yaml.Marshal(d)
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "ambient_temp_c:") {
			lines = append(lines, line)
		}
	}
	if _, err := Load([]byte(strings.Join(lines, "\n")), reg); err == nil || !strings.Contains(err.Error(), "explicit operating assumption") {
		t.Fatalf("missing operating assumption accepted or wrong rejection: %v", err)
	}
	fractional := []byte(strings.Replace(string(b), "gpus_per_node: 4", "gpus_per_node: 4.5", 1))
	if _, err := Load(fractional, reg); err == nil || !strings.Contains(err.Error(), "whole integer") {
		t.Fatalf("fractional GPU count accepted or wrong rejection: %v", err)
	}
	b = []byte(strings.Replace(string(b), "gpu_model:", "invented_field:", 1))
	if _, err := Load(b, reg); err == nil || !strings.Contains(err.Error(), "field") {
		t.Fatal("strict nested field rejection absent")
	}
}
func TestGPUFullFixtureLoadDomainAndBindingRejections(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := testRegistry(t)
	r, err := Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	if r.GPU == nil || len(r.GPU.Nodes) != 20 {
		t.Fatal("GPU topology not resolved")
	}
	for _, ci := range r.Constructs {
		if ci.Kind == KindEC2 {
			t.Fatal("baremetal emitted EC2 lane")
		}
	}
	for _, n := range r.GPU.Nodes {
		if n.Node.Capacity == nil {
			t.Fatal("baremetal capacity absent")
		}
	}
	tests := []struct {
		name   string
		mutate func(*Decl)
	}{
		{"nvl-count", func(d *Decl) {
			d.GPUCompute.Pools[1].NodeCount = 17
			d.GPUCompute.Pools[1].Placements[0].NodeCount = 17
		}},
		{"tray-id", func(d *Decl) {
			d.GPUCompute.Pools[1].Nodes = []fixture.GPUNodeOverrideSpec{{Ordinal: 0, TrayID: "same"}, {Ordinal: 1, TrayID: "same"}}
		}},
		{"partition-overlap", func(d *Decl) {
			d.GPUCompute.Racks[1].NVLink.Partitions = []fixture.NVLinkPartitionSpec{{Name: "a", GPUs: []string{"gpu:nvl-node-0000/0"}}, {Name: "b", GPUs: []string{"gpu:nvl-node-0000/0"}}}
		}},
		{"partition-missing", func(d *Decl) {
			d.GPUCompute.Racks[1].NVLink.Partitions = []fixture.NVLinkPartitionSpec{{Name: "a", GPUs: []string{"gpu:nvl-node-0000/0"}}}
		}},
		{"correlation", func(d *Decl) { d.GPUCompute.Racks[1].NVLink.Correlated = true }},
		{"rail-mismatch", func(d *Decl) { d.GPUCompute.Pools[0].NICs[1].Switch = "backend-leaf-1" }},
		{"port-exhaustion", func(d *Decl) { d.GPUCompute.Fabrics[0].Devices[0].Ports = nil }},
		{"port-twice", func(d *Decl) {
			d.GPUCompute.Pools[0].Nodes = []fixture.GPUNodeOverrideSpec{{Ordinal: 0, Attachments: []fixture.GPUNICAttachmentSpec{{NIC: "eth0", Port: "swp21"}}}}
		}},
		{"duplicate-management-ip", func(d *Decl) {
			d.GPUCompute.Racks[0].FacilityDevices = []fixture.GPUDeviceSpec{{Name: "pdu-a", Kind: "pdu", ManagementIP: "10.20.2.1"}}
		}},
		{"baremetal-cloudwatch", func(d *Decl) { v := true; d.Environments[0].Cluster.Observability = &CloudWatchToggle{CloudWatch: &v} }},
		{"baremetal-eks-groups", func(d *Decl) {
			d.Environments[0].Cluster.NodeGroups = []NodeGroupDecl{{Name: "a", InstanceType: "m6i.large"}}
		}},
		{"platform-mixed", func(d *Decl) { d.Environments[0].Cluster.Platform.OS = "al2023" }},
		{"host-cluster-double", func(d *Decl) {
			d.Hosts = []HostDecl{{Name: "pcie-node-0000", CPUs: 64, MemoryGB: 512, IP: "10.10.0.10"}}
		}},
		{"node-two-clusters", func(d *Decl) {
			p := d.GPUCompute.Pools[0]
			p.Name = "other"
			p.KubernetesCluster = "other-cluster"
			p.Placements[0].SlotStart = 20
			d.GPUCompute.Pools = append(d.GPUCompute.Pools, p)
			e := d.Environments[0]
			e.Name = "other"
			c := *e.Cluster
			c.Name = "other-cluster"
			e.Cluster = &c
			d.Environments = append(d.Environments, e)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decl
			if err := yaml.Unmarshal(data, &d); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&d)
			if _, err := gpuDeclLoad(t, d, reg); err == nil {
				t.Fatal("invalid blueprint accepted")
			}
		})
	}
	// Custom partitions replace, rather than coexist with, the default owner.
	var d Decl
	_ = yaml.Unmarshal(data, &d)
	keys := r.GPU.Domains[0].GPUKeys
	d.GPUCompute.Racks[1].NVLink.Partitions = []fixture.NVLinkPartitionSpec{{Name: "custom", GPUs: keys}}
	custom, err := gpuDeclLoad(t, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(custom.GPU.Domains[0].Partitions) != 1 || custom.GPU.Domains[0].Partitions[0].Name != "custom" {
		t.Fatal("default partition survived custom declaration")
	}
	// Independent blueprints with disjoint physical nodes cannot alias a scheduler.
	x := gpuMinimalDecl()
	a, err := gpuDeclLoad(t, x, reg)
	if err != nil {
		t.Fatal(err)
	}
	x.Name = "other-blueprint"
	x.GPUCompute.Racks[0].Name = "rack-b"
	x.GPUCompute.Pools[0].HostnamePrefix = "node-b"
	x.GPUCompute.Pools[0].Placements[0].RackKey = "rack:site-a/rack-b"
	b, err := gpuDeclLoad(t, x, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSet([]*Resolved{a, b}); err == nil || !strings.Contains(err.Error(), "scheduler") {
		t.Fatalf("scheduler identity collision not caught: %v", err)
	}
	// A disabled/unselected facility rack still owns its physical endpoint identity.
	x = gpuMinimalDecl()
	x.GPUCompute.Schedulers = nil
	x.GPUCompute.Racks = append(x.GPUCompute.Racks, fixture.GPURackSpec{Name: "orphan-a", Site: "site-a", Shape: "pcie", Cooling: "air", FacilityDevices: []fixture.GPUDeviceSpec{{Name: "orphan-pdu-a", Kind: "pdu", ManagementIP: "10.99.0.1"}}})
	a, err = gpuDeclLoad(t, x, reg)
	if err != nil {
		t.Fatal(err)
	}
	x.Name = "other-blueprint"
	x.GPUCompute.Pools[0].HostnamePrefix = "node-b"
	x.GPUCompute.Pools[0].Placements[0].RackKey = "rack:site-a/rack-b"
	x.GPUCompute.Racks[0].Name = "rack-b"
	x.GPUCompute.Racks[1].Name = "orphan-b"
	x.GPUCompute.Racks[1].FacilityDevices[0].Name = "orphan-pdu-b"
	b, err = gpuDeclLoad(t, x, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSet([]*Resolved{a, b}); err == nil || !strings.Contains(err.Error(), "IP") {
		t.Fatalf("unselected facility endpoint identity collision not caught: %v", err)
	}
}
func gpuLocalRegistry(t *testing.T, axis failuremode.Axis) *core.Registry {
	r := testRegistry(t)
	modes := fixture.GPUFailureModes("snmp_exporter")
	modes = append(modes, failuremode.Mode{Name: "test_collection_outage", Axis: axis})
	r.RegisterConstruct(core.ConstructReg{Kind: "snmp_exporter", Group: core.GroupIntegration, Scope: core.ScopeSubstrate, NewConfig: func() any { return &struct{}{} }, FailureModes: modes, Build: func(_ any, fx *fixture.Set) (core.Construct, error) {
		for _, d := range fx.GPU.Devices() {
			if d.Kind == "pdu" && d.Collection != nil && d.Collection.Module != "test_pdu_profile" {
				return nil, fmt.Errorf("test-only sourced profile compatibility mismatch")
			}
		}
		return nil, nil
	}})
	return r
}
func TestGPULocalCollectionModeBlueprintBoundary(t *testing.T) {
	d := gpuMinimalDecl()
	profile := &fixture.GPUCollectionSpec{Protocol: "snmp", Module: "test_pdu_profile", Port: 161, Source: gpuTestSource()}
	d.GPUCompute.Racks[0].FacilityDevices = []fixture.GPUDeviceSpec{{Name: "pdu-a", Kind: "pdu", Collection: profile}}
	var node yaml.Node
	_ = yaml.Unmarshal([]byte("gpu_pools: [pool-a]\n"), &node)
	d.Integrations = map[string]yaml.Node{"snmp_exporter": *node.Content[0]}
	d.Scenarios = []ScenarioDecl{{Name: "local-outage", Effects: []EffectDecl{{Mode: "test_collection_outage", Target: "device:pdu-a", Intensity: 1}}}}
	reg := gpuLocalRegistry(t, fixture.AxisGPUDevice)
	r, err := gpuDeclLoad(t, d, reg)
	if err != nil {
		t.Fatal(err)
	}
	sel := r.Constructs[0].Fixtures.GPU
	if sel.CoversTarget("snmp_exporter", "test_collection_outage", "device:pdu-a") {
		t.Fatal("local mode entered shared coverage")
	}
	for _, x := range sel.Targets("snmp_exporter") {
		if x.Key == "device:pdu-a" {
			t.Fatal("PDU local key leaked into shared target union")
		}
	}
	if !sel.CoversTarget("snmp_exporter", "gpu_cooling_fault", "rack:site-a/rack-a") {
		t.Fatal("shared cooling coverage changed")
	}
	cr, _ := reg.Construct("snmp_exporter")
	if _, err := cr.Build(r.Constructs[0].Config, r.Constructs[0].Fixtures); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100000, 0)
	base := fixture.GPUAllocationPlan(r.GPU, at, nil)
	local := fixture.GPUAllocationPlan(r.GPU, at, func(_ time.Time, m, k string) (bool, float64) {
		return m == "test_collection_outage" && k == "device:pdu-a", 1
	})
	if len(local.Faults) != 0 {
		t.Fatal("local collection entered shared fault capture")
	}
	pb, err := fixture.GPUOperatingPoints(r.GPU, base)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := fixture.GPUOperatingPoints(r.GPU, local)
	if err != nil {
		t.Fatal(err)
	}
	bb, _ := yaml.Marshal(pb)
	bl, _ := yaml.Marshal(pl)
	if string(bb) != string(bl) {
		t.Fatal("local collection affected physics")
	}
	tests := []struct {
		name   string
		mutate func(*Decl)
		axis   failuremode.Axis
	}{
		{"missing-profile", func(d *Decl) { d.GPUCompute.Racks[0].FacilityDevices[0].Collection = nil }, fixture.AxisGPUDevice},
		{"unrelated", func(d *Decl) {
			d.GPUCompute.Racks = append(d.GPUCompute.Racks, fixture.GPURackSpec{Name: "rack-b", Site: "site-a", Shape: "pcie", Cooling: "air", FacilityDevices: []fixture.GPUDeviceSpec{{Name: "pdu-b", Kind: "pdu", Collection: profile}}})
			d.Scenarios[0].Effects[0].Target = "device:pdu-b"
		}, fixture.AxisGPUDevice},
		{"missing-module", func(d *Decl) {
			unit := 1
			d.GPUCompute.Racks[0].FacilityDevices[0].Collection = &fixture.GPUCollectionSpec{Protocol: "modbus", Port: 502, ModbusUnit: &unit, Source: gpuTestSource()}
		}, fixture.AxisGPUDevice},
		{"wrong-axis", func(*Decl) {}, fixture.AxisGPURack},
		{"unregistered-mode", func(d *Decl) { d.Scenarios[0].Effects[0].Mode = "unregistered_local_outage" }, fixture.AxisGPUDevice},
		{"disabled", func(d *Decl) {
			var n yaml.Node
			_ = yaml.Unmarshal([]byte("enabled: false\ngpu_pools: [pool-a]\n"), &n)
			d.Integrations["snmp_exporter"] = *n.Content[0]
		}, fixture.AxisGPUDevice},
		{"unknown-selector", func(d *Decl) {
			var n yaml.Node
			_ = yaml.Unmarshal([]byte("gpu_pools: [missing]\n"), &n)
			d.Integrations["snmp_exporter"] = *n.Content[0]
		}, fixture.AxisGPUDevice},
		{"duplicate-selector", func(d *Decl) {
			var n yaml.Node
			_ = yaml.Unmarshal([]byte("gpu_pools: [pool-a, pool-a]\n"), &n)
			d.Integrations["snmp_exporter"] = *n.Content[0]
		}, fixture.AxisGPUDevice},
		{"fanout", func(d *Decl) {
			var n yaml.Node
			_ = yaml.Unmarshal([]byte("for_each_env: true\ngpu_pools: [pool-a]\n"), &n)
			d.Integrations["snmp_exporter"] = *n.Content[0]
		}, fixture.AxisGPUDevice},
	}
	original, _ := yaml.Marshal(d)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var x Decl
			_ = yaml.Unmarshal(original, &x)
			tc.mutate(&x)
			if _, err := gpuDeclLoad(t, x, gpuLocalRegistry(t, tc.axis)); err == nil {
				t.Fatal("invalid local coverage accepted")
			}
		})
	}
	// New axes participate in the existing exact-scope wildcard expansion.
	var wild Decl
	_ = yaml.Unmarshal(original, &wild)
	wild.Scenarios = append(wild.Scenarios, ScenarioDecl{Name: "shared-cooling", Effects: []EffectDecl{{Mode: "gpu_cooling_fault", Target: "gpu_rack:*", Intensity: 0.5}}})
	if _, err := gpuDeclLoad(t, wild, reg); err != nil {
		t.Fatalf("GPU wildcard rejected: %v", err)
	}
	// Explicit registration under a wrong kind cannot reinterpret a shared mode as local.
	wrong := testRegistry(t)
	wrong.RegisterConstruct(core.ConstructReg{Kind: "snmp_exporter", Group: core.GroupIntegration, Scope: core.ScopeSubstrate, NewConfig: func() any { return &struct{}{} }, Build: func(any, *fixture.Set) (core.Construct, error) { return nil, nil }, FailureModes: []failuremode.Mode{{Name: "gpu_fallen_off_bus", Axis: fixture.AxisGPU}}})
	var wrongDecl Decl
	_ = yaml.Unmarshal(original, &wrongDecl)
	wrongDecl.Scenarios[0].Effects[0] = EffectDecl{Mode: "gpu_fallen_off_bus", Target: "gpu:node-a-0000/0", Intensity: 1}
	if _, err := gpuDeclLoad(t, wrongDecl, wrong); err == nil {
		t.Fatal("shared mode assigned to another kind reinterpreted as local")
	}
	// Generic loading does not claim module support; only the consumer builder does.
	var x Decl
	_ = yaml.Unmarshal(original, &x)
	x.GPUCompute.Racks[0].FacilityDevices[0].Collection.Module = "unsupported_test_profile"
	rr, err := gpuDeclLoad(t, x, reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cr.Build(rr.Constructs[0].Config, rr.Constructs[0].Fixtures); err == nil {
		t.Fatal("consumer builder did not validate sourced profile")
	}
}

// Root rescue counterexamples independently derived from the frozen contract.
func TestGPURescueFractionalMerge(t *testing.T) {
	d := gpuMinimalDecl()
	b, _ := yaml.Marshal(d)
	merged := strings.Replace(string(b), "node_count: 1", "<<: {node_count: 1.5}", 1)
	r, e := Load([]byte(merged), testRegistry(t))
	if e == nil {
		t.Fatalf("fractional merged node_count accepted and truncated: %d nodes", len(r.GPU.Nodes))
	}
}
func TestGPURescueVASTStorageOnly(t *testing.T) {
	data, e := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if e != nil {
		t.Fatal(e)
	}
	var d Decl
	if e = yaml.Unmarshal(data, &d); e != nil {
		t.Fatal(e)
	}
	reg := testRegistry(t)
	reg.RegisterConstruct(core.ConstructReg{Kind: "vast", Group: core.GroupIntegration, Scope: core.ScopeSubstrate, NewConfig: func() any { return &struct{}{} }, Build: func(any, *fixture.Set) (core.Construct, error) { return nil, nil }, FailureModes: fixture.GPUFailureModes("vast")})
	var node yaml.Node
	yaml.Unmarshal([]byte("gpu_storage_clusters: [storage-a]"), &node)
	d.Integrations = map[string]yaml.Node{"vast": *node.Content[0]}
	d.Scenarios = []ScenarioDecl{{Name: "storage-slow", Effects: []EffectDecl{{Mode: "gpu_storage_latency", Target: "storage:storage-a", Intensity: 1}}}}
	if _, e = gpuDeclLoad(t, d, reg); e != nil {
		t.Fatalf("selected storage consumer cannot cover its own storage fault: %v", e)
	}
}
func TestGPURescueHostSiblingCoverage(t *testing.T) {
	d := gpuMinimalDecl()
	p := &d.GPUCompute.Pools[0]
	p.NodeCount = 2
	p.Placements[0].NodeCount = 2
	d.Hosts = []HostDecl{{Name: "node-a-0000", CPUs: 64, MemoryGB: 512}}
	d.Scenarios = []ScenarioDecl{{Name: "uncollected-host", Effects: []EffectDecl{{Mode: "gpu_fallen_off_bus", Target: "gpu:node-a-0001/0", Intensity: 1}}}}
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{Kind: KindHost, Scope: core.ScopeSubstrate, NewConfig: func() any { return &testHostConfig{} }, Build: func(any, *fixture.Set) (core.Construct, error) { return nil, nil }, FailureModes: fixture.GPUFailureModes("host")})
	if _, e := gpuDeclLoad(t, d, reg); e == nil {
		t.Fatal("host collector falsely covers uncollected sibling GPU")
	}
	d.Scenarios[0].Effects[0].Target = "gpu:node-a-0000/0"
	if _, e := gpuDeclLoad(t, d, reg); e != nil {
		t.Fatalf("host failed to cover its own GPU: %v", e)
	}
}
func TestGPURescueEKSOrder(t *testing.T) {
	d := gpuMinimalDecl()
	groups := []fixture.NodeGroupSpec{{Name: "general", InstanceType: "m6i.large", Desired: 10}}
	nodes := fixture.DeriveNodes("seed", "eks-cl", groups, "us-east-1", 0)
	before := append([]fixture.Node(nil), nodes...)
	cl := &fixture.Cluster{Name: "eks-cl", Type: "eks", Seed: "seed", Region: "us-east-1", Nodes: nodes, NodeGroups: groups}
	_, e := fixture.BuildGPUTopology("seed", *d.GPUCompute, map[string]*fixture.Cluster{cl.Name: cl}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, cl.Nodes) {
		t.Fatalf("unrelated EKS node order changed; resolved first=%s live first=%s", cl.Nodes[0].Hostname, fixture.LiveNodes(cl, func(_ string, n int) int { return n })[0].Hostname)
	}
}

func TestGPURescueMalformedPCI(t *testing.T) {
	d := gpuMinimalDecl()
	d.GPUCompute.Pools[0].GPUs = []fixture.GPUOverrideSpec{{Slot: 0, PCIBusID: "00000000:20:00.0garbage"}}
	r, e := gpuDeclLoad(t, d, testRegistry(t))
	if e == nil {
		t.Fatalf("malformed PCI suffix accepted and silently normalized: %s", r.GPU.Nodes[0].GPUs[0].PCIBusID)
	}
}

func TestGPURescueMergedInputPrecedence(t *testing.T) {
	d := gpuMinimalDecl()
	b, e := yaml.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	var prefix string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "node_count: 1" {
			prefix = line[:len(line)-len(strings.TrimLeft(line, " "))]
			break
		}
	}
	source := strings.Replace(string(b), "node_count: 1", "<<: {node_count: 1.5}\n"+prefix+"node_count: 1", 1)
	if _, e := Load([]byte(source), testRegistry(t)); e != nil {
		t.Fatalf("explicit whole integer must override merged fraction: %v", e)
	}
	// Merge the entire gpu_compute subtree so raw discovery cannot skip it.
	source = "<<:\n"
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		source += "  " + line + "\n"
	}
	source = strings.Replace(source, "node_count: 1", "node_count: 1.5", 1)
	if _, e := Load([]byte(source), testRegistry(t)); e == nil {
		t.Fatal("top-level merged GPU subtree bypassed integer validation")
	}
}
func TestGPURescueHostInstanceScope(t *testing.T) {
	d := gpuMinimalDecl()
	p := &d.GPUCompute.Pools[0]
	p.NodeCount = 2
	p.Placements[0].NodeCount = 2
	d.Hosts = []HostDecl{{Name: "node-a-0000", CPUs: 64, MemoryGB: 512}, {Name: "node-a-0001", CPUs: 64, MemoryGB: 512}}
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{Kind: KindHost, Scope: core.ScopeSubstrate, NewConfig: func() any { return &testHostConfig{} }, Build: func(any, *fixture.Set) (core.Construct, error) { return nil, nil }, FailureModes: fixture.GPUFailureModes("host")})
	r, e := gpuDeclLoad(t, d, reg)
	if e != nil {
		t.Fatal(e)
	}
	for _, ci := range r.Constructs {
		if ci.Kind != KindHost {
			continue
		}
		own := "gpu:" + ci.Fixtures.Host.Hostname + "/0"
		other := "gpu:node-a-0001/0"
		if ci.Fixtures.Host.Hostname == "node-a-0001" {
			other = "gpu:node-a-0000/0"
		}
		if !gpuInstanceOwnsTarget(ci, own) || gpuInstanceOwnsTarget(ci, other) {
			t.Fatal("invoking host did not retain its own canonical physical scope")
		}
	}
}
