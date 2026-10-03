// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"testing"
	"time"
)

// This exhaustive cross-package proof belongs in the plain integration tier.
// Fast HGX guard, identity, selection and fault checks remain race-covered.
// testWsCfg is the package-level config type for the "web_service" workload registered
// in testRegistry. Declared at package level so tests can cast to *testWsCfg without
// a type mismatch (inline type declarations in different functions are distinct types).
type testWsCfg struct {
	Tracing bool `yaml:"tracing"`
	RUM     bool `yaml:"rum"`
	Traffic struct {
		Shape      string  `yaml:"shape"`
		OffPeakRPS float64 `yaml:"off_peak_rps"`
		PeakRPS    float64 `yaml:"peak_rps"`
	} `yaml:"traffic"`
	Endpoints []struct {
		Route     string  `yaml:"route"`
		ErrorRate float64 `yaml:"error_rate"`
		P95Ms     float64 `yaml:"p95_ms"`
	} `yaml:"endpoints"`
}

type testK8sConfig struct {
	OTelCollectorProm bool `yaml:"otel_collector_prom"`
	OTel              *struct {
		Metrics bool `yaml:"metrics"`
	} `yaml:"otel"`
	PrometheusOperatorRemoteWrite *struct {
		Prometheus        string `yaml:"prometheus"`
		PrometheusReplica string `yaml:"prometheus_replica"`
	} `yaml:"prometheus_operator_remote_write"`
	DefaultAllowLists *struct {
		ClusterMetrics bool   `yaml:"cluster_metrics"`
		NodeExporter   string `yaml:"node_exporter"`
	} `yaml:"default_allow_lists"`
}

type testHostConfig struct {
	OTel *struct {
		Metrics bool `yaml:"metrics"`
	} `yaml:"otel"`
}

// testRegistry builds a registry covering the v1 kinds the resolver emits plus the
// path-1 kinds (addons/features/workloads) the loader decodes configs for.
func testRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	for _, k := range blueprint.TopologyKinds() {
		if k == blueprint.KindCWInfra {
			continue // cw_infra is config-decoded (cloud.cloudwatch) — registered below
		}
		// The catalog wiring lane populates FailureModes on the production registry; this test
		// harness mirrors the database-axis vocabulary for the dbo11y_postgres construct so that
		// resolve-level incident/scenario validation has a real vocabulary to check against.
		var modes []failuremode.Mode
		if k == blueprint.KindDbo11yPostgres {
			modes = []failuremode.Mode{
				{Name: "connection_saturation", Axis: failuremode.AxisDatabase},
				{Name: "replication_lag", Axis: failuremode.AxisDatabase},
				{Name: "lock_contention", Axis: failuremode.AxisDatabase},
				{Name: "slow_query_storm", Axis: failuremode.AxisDatabase},
			}
		}
		newConfig := func() any { return &struct{}{} }
		if k == blueprint.KindK8sCluster {
			newConfig = func() any { return &testK8sConfig{} }
		} else if k == blueprint.KindHost {
			newConfig = func() any { return &testHostConfig{} }
		}
		reg.RegisterConstruct(core.ConstructReg{
			Kind: k, Doc: "test", Scope: core.ScopeSubstrate,
			NewConfig:    newConfig,
			Build:        func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
			FailureModes: modes,
		})
	}
	// cw_infra takes the per-family emission switches from cloud.cloudwatch. Mirror the
	// real cwinfra.Config yaml tags (the loader test stays decoupled from the construct).
	type cwCfg struct {
		ALBs       int   `yaml:"albs"`
		S3Buckets  int   `yaml:"s3_buckets"`
		Firehose   *bool `yaml:"firehose"`
		NLB        *bool `yaml:"nlb"`
		EBS        *bool `yaml:"ebs"`
		NATGateway *bool `yaml:"nat_gateway"`
		EKS        *bool `yaml:"eks"`
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: blueprint.KindCWInfra, Doc: "test", Scope: core.ScopeBlueprint,
		NewConfig: func() any { return &cwCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	type caCfg struct {
		MinNodes int `yaml:"min_nodes"`
		MaxNodes int `yaml:"max_nodes"`
	}
	for _, k := range []string{"load_balancer_controller", "core_dns", "vpc_cni", "cert_manager", "ebs_csi", "external_dns", "ksm_ingress", "karpenter", "metrics_server", "argocd", "envoy_gateway"} {
		reg.RegisterConstruct(core.ConstructReg{
			Kind: k, Doc: "addon", Scope: core.ScopeSubstrate,
			NewConfig: func() any { return &struct{}{} },
			Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
		})
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "cluster_autoscaler", Doc: "addon", Scope: core.ScopeSubstrate,
		NewConfig: func() any { return &caCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	type smCfg struct {
		Checks []string `yaml:"checks"`
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "synthetic_monitoring", Doc: "feature", Scope: core.ScopeBlueprint,
		Group:     core.GroupFeature,
		NewConfig: func() any { return &smCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	// An integration kind (external source) for the features/integrations split tests.
	type cfCfg struct {
		Zone        string   `yaml:"zone"`
		Colocations []string `yaml:"colocations"`
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "cloudflare", Doc: "integration", Scope: core.ScopeBlueprint,
		Group:     core.GroupIntegration,
		NewConfig: func() any { return &cfCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "web_service", Doc: "test",
		NewConfig: func() any { return &testWsCfg{} },
		Build:     func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{
			{Name: "latency_spike", Axis: failuremode.AxisWorkload},
			{Name: "error_burst", Axis: failuremode.AxisWorkload},
		},
	})
	return reg
}

func gpuTestSource() fixture.GPUFieldSource {
	return fixture.GPUFieldSource{URL: "https://example.invalid/test-only-profile", Revision: "test-only", SHA256: strings.Repeat("a", 64), Section: "test profile fixture; not a vendor contract"}
}
func gpuMinimalDecl() blueprint.Decl {
	spec := &fixture.GPUTopologySpec{}
	spec.Racks = []fixture.GPURackSpec{{Name: "rack-a", Site: "site-a", Shape: "pcie", Cooling: "air", Physics: fixture.GPURackPhysicsSpec{FixedOverheadW: 100}}}
	spec.Pools = []fixture.GPUPoolSpec{{Name: "pool-a", Shape: "pcie", GPUModel: "h100_pcie_80gb", NodeCount: 1, GPUsPerNode: 4, HostnamePrefix: "node-a", Hardware: fixture.GPUNodeHardwareSpec{CPUs: 64, MemoryGiB: 512, Arch: "x86_64"}, Placements: []fixture.GPUPoolPlacementSpec{{RackKey: "rack:site-a/rack-a", NodeCount: 1, SlotStart: 1, NodeHeightU: 4}}, Operating: fixture.GPUOperatingSpec{IdlePowerFraction: 0.12, AmbientTempC: 25, FullPowerRiseC: 40, CoolingFaultRiseC: 15, CoolingPerfLoss: 0.3, CongestionPerfLoss: 0.6, StoragePerfLoss: 0.5, NodeBasePowerW: 250, NodeDynamicPerCoreW: 2, NodeDynamicPerNICW: 10}}}
	workload := fixture.GPUWorkloadSpec{Name: "workload-a", Project: "project-a", Role: "training", Cycle: "6h", PendingFor: "1m", RunningMin: "1h", RunningMax: "2h", Demand: fixture.GPUDemandSpec{UtilizationBase: 0.5, MemoryReservedFraction: 0.5}, Workers: []fixture.GPUWorkerSpec{{Name: "worker-a", Pool: "pool-a", GPUSlots: []int{0}}}}
	spec.Schedulers = []fixture.GPUSchedulerSpec{{Name: "scheduler-a", Kind: "runai", Pools: []string{"pool-a"}, Projects: []fixture.GPUProjectSpec{{Name: "project-a", GPUQuota: 4}}, Workloads: []fixture.GPUWorkloadSpec{workload}}}
	return blueprint.Decl{Name: "gpu-minimal", GPUCompute: spec}
}
func gpuDeclLoad(t *testing.T, d blueprint.Decl, reg *core.Registry) (*blueprint.Resolved, error) {
	t.Helper()
	b, e := yaml.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	return blueprint.Load(b, reg)
}

// This identical function is executed against immutable old Go blobs and the
// candidate. It freezes actual allocation time/fault inputs, not a randomized CLI.
func TestHGXLegacyFixedInputProof(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/ai-factory-fixture.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := testRegistry(t)
	load := func(raw []byte) *blueprint.Resolved {
		t.Helper()
		r, e := blueprint.Load(raw, reg)
		if e != nil {
			t.Fatal(e)
		}
		if e = blueprint.ValidateSet([]*blueprint.Resolved{r}); e != nil {
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
			var d blueprint.Decl
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
			if e = blueprint.ValidateSet([]*blueprint.Resolved{r}); e != nil {
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
