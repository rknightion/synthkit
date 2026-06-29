// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
)

// testAppCfg is a permissive app config so strict decode accepts the service-graph YAML in the
// placement test (the loader stays decoupled from the real internal/workload/app Config).
type testAppCfg struct {
	Services []struct {
		Name      string                     `yaml:"name"`
		Type      string                     `yaml:"type"`
		Runtime   string                     `yaml:"runtime"`
		Entry     bool                       `yaml:"entry"`
		Replicas  int                        `yaml:"replicas"`
		Calls     []string                   `yaml:"calls"`
		External  bool                       `yaml:"external"`
		Resources *fixture.WorkloadResources `yaml:"resources"`
		Namespace string                     `yaml:"namespace"`
	} `yaml:"services"`
	Traffic struct {
		OffPeakRPS float64 `yaml:"off_peak_rps"`
		PeakRPS    float64 `yaml:"peak_rps"`
	} `yaml:"traffic"`
}

const appPlacementYAML = `
name: app-mini
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0mini01, nat_gateways: 2 }
    cluster:
      type: eks
      name: mini-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
workloads:
  - type: app
    name: demo
    runs_on: mini-prod-use1
    services:
      - { name: web-fe, type: frontend, runtime: node, entry: true, calls: [api] }
      - { name: api, type: web, runtime: python, replicas: 4, calls: [pg] }
      - { name: pg, type: db, replicas: 2 }
`

// TestAppServiceNodePlacement: an app workload places ONE cluster workload PER service node (each
// its own deployment), so the cluster's pod total — and the derived node count + the fleet-manager
// instances that key off it — sums per service node (per-service scaling cascade, §6.6). The app
// instance name is NOT itself a placement.
func TestAppServiceNodePlacement(t *testing.T) {
	reg := testRegistry(t)
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "app", Doc: "test",
		NewConfig:    func() any { return &testAppCfg{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}},
	})

	r, err := Load([]byte(appPlacementYAML), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var cl *fixture.Cluster
	for _, wi := range r.Workloads {
		if wi.Kind == "app" {
			cl = wi.Cluster
		}
	}
	if cl == nil {
		t.Fatal("no app workload instance / cluster resolved")
	}

	gotReplicas := map[string]int{}
	gotRuntime := map[string]string{}
	for _, w := range cl.Workloads {
		gotReplicas[w.Name] = w.Replicas
		gotRuntime[w.Name] = w.Runtime
	}
	want := map[string]int{"web-fe": defaultReplicas, "api": 4, "pg": 2}
	for name, rep := range want {
		if gotReplicas[name] != rep {
			t.Errorf("node %q placement replicas=%d want %d", name, gotReplicas[name], rep)
		}
	}
	// Runtime is propagated from the ServiceNode onto the fixture placement (the shared
	// pod-runtime identity pod-aware constructs join on); a node without a runtime is "" (I13).
	wantRuntime := map[string]string{"web-fe": "node", "api": "python", "pg": ""}
	for name, rt := range wantRuntime {
		if gotRuntime[name] != rt {
			t.Errorf("node %q placement runtime=%q want %q", name, gotRuntime[name], rt)
		}
	}
	// the app instance name "demo" is NOT a cluster placement (only its nodes are).
	if _, bad := gotReplicas["demo"]; bad {
		t.Error("the app workload itself must not be a cluster placement — only its service nodes")
	}

	// node cascade: totalPods = 2+4+2 = 8; prod env (default weight=1.0) → floor=6;
	// max(6, ceil(8/8)=1) = 6 nodes.
	totalPods := 0
	for _, w := range cl.Workloads {
		totalPods += w.Replicas
	}
	if totalPods != 8 {
		t.Errorf("cluster totalPods=%d want 8 (sum of service-node replicas)", totalPods)
	}
	if len(cl.Nodes) != 6 {
		t.Errorf("derived node count=%d want 6 (prod floor from 8 pods)", len(cl.Nodes))
	}
}

// externalPlacementYAML declares a `gateway` service marked external: true (a remote/managed
// service, e.g. a SaaS gateway in another team's estate). It must emit its connected trace hop
// but NOT be placed as a k8s deployment on the caller's cluster, and must NOT be a scaling target.
const externalPlacementYAML = `
name: app-ext
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0ext01, nat_gateways: 2 }
    cluster:
      type: eks
      name: ext-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
workloads:
  - type: app
    name: demo
    runs_on: ext-prod-use1
    services:
      - { name: web-fe, type: frontend, runtime: node, entry: true, calls: [gw] }
      - { name: gw, type: gateway, runtime: node, external: true }
`

// TestExternalServiceNodeNotPlaced: an external service node is excluded from cluster placement
// (no kube_* deployment) and from the scaling/failure target set, while non-external siblings are
// placed normally. This is the gateway-as-remote model (e.g. a managed Portkey gateway).
func TestExternalServiceNodeNotPlaced(t *testing.T) {
	reg := testRegistry(t)
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "app", Doc: "test",
		NewConfig:    func() any { return &testAppCfg{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}},
	})

	r, err := Load([]byte(externalPlacementYAML), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var cl *fixture.Cluster
	for _, wi := range r.Workloads {
		if wi.Kind == "app" {
			cl = wi.Cluster
		}
	}
	if cl == nil {
		t.Fatal("no app workload instance / cluster resolved")
	}

	for _, w := range cl.Workloads {
		if w.Name == "gw" {
			t.Error("external service node \"gw\" must NOT be placed as a cluster deployment")
		}
	}
	// the non-external sibling IS placed.
	var sawFE bool
	for _, w := range cl.Workloads {
		if w.Name == "web-fe" {
			sawFE = true
		}
	}
	if !sawFE {
		t.Error("non-external service node \"web-fe\" must be placed as a cluster deployment")
	}
	// the external node is NOT a scaling/failure target.
	for _, tg := range r.Targets {
		if tg.Name == "gw" {
			t.Error("external service node \"gw\" must NOT be a scaling/failure target")
		}
	}
}

// resourcesPlacementYAML pins a service node's container requests/limits via the `resources:` block;
// a sibling omits it (must resolve to a nil Resources → k8scluster size-class defaults apply).
const resourcesPlacementYAML = `
name: app-res
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0res01, nat_gateways: 2 }
    cluster:
      type: eks
      name: res-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
workloads:
  - type: app
    name: demo
    runs_on: res-prod-use1
    services:
      - name: web-fe
        type: frontend
        runtime: node
        entry: true
        calls: [api]
        resources: { cpu_request: 0.3, cpu_limit: 0.9, mem_request: 104857600, mem_limit: 268435456, cpu_usage_base: 0.27 }
      - { name: api, type: web, runtime: python, replicas: 2 }
`

// TestServiceNodeResourcesFlow: a `resources:` block on a service node flows to
// fixture.Workload.Resources; a node that omits it leaves Resources nil (defaults apply).
func TestServiceNodeResourcesFlow(t *testing.T) {
	reg := testRegistry(t)
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "app", Doc: "test",
		NewConfig:    func() any { return &testAppCfg{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}},
	})

	r, err := Load([]byte(resourcesPlacementYAML), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var cl *fixture.Cluster
	for _, wi := range r.Workloads {
		if wi.Kind == "app" {
			cl = wi.Cluster
		}
	}
	if cl == nil {
		t.Fatal("no app workload instance / cluster resolved")
	}

	byName := map[string]*fixture.Workload{}
	for i := range cl.Workloads {
		byName[cl.Workloads[i].Name] = &cl.Workloads[i]
	}

	fe := byName["web-fe"]
	if fe == nil || fe.Resources == nil {
		t.Fatalf("web-fe Resources not populated: %+v", fe)
	}
	want := fixture.WorkloadResources{CPURequest: 0.3, CPULimit: 0.9, MemRequest: 104857600, MemLimit: 268435456, CPUUsageBase: 0.27}
	if *fe.Resources != want {
		t.Errorf("web-fe Resources = %+v, want %+v", *fe.Resources, want)
	}

	// The sibling that omits `resources:` must leave Resources nil (back-compatible defaults).
	api := byName["api"]
	if api == nil {
		t.Fatal("api node not placed")
	}
	if api.Resources != nil {
		t.Errorf("api Resources = %+v, want nil (omitted ⇒ defaults)", *api.Resources)
	}
}

// TestAppServiceNodesClusterDistinctPodNames: an `app` workload's service-node deployments are
// placed by their RAW service name (NOT env-suffixed), so the SAME deployment lands on every
// for_each_env cluster. Their pod names must still differ per cluster — real EKS fleets never
// share pod hashes across clusters (the cluster is only a label, never part of the pod string).
// Mirrors TestSubstrateWorkloadsClusterDistinctPodNames for the app (deployment) lane.
func TestAppServiceNodesClusterDistinctPodNames(t *testing.T) {
	reg := testRegistry(t)
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "app", Doc: "test",
		NewConfig:    func() any { return &testAppCfg{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}},
	})
	y := `
name: appdistinct
environments:
  - name: a
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0a, nat_gateways: 0 }
    cluster: { type: eks, name: clus-a, node_groups: [{ name: general, instance_type: m6i.xlarge }] }
  - name: b
    cloud: { provider: aws, account_id: "111122223333", region: us-east-2, vpc_id: vpc-0b, nat_gateways: 0 }
    cluster: { type: eks, name: clus-b, node_groups: [{ name: general, instance_type: m6i.xlarge }] }
workloads:
  - type: app
    name: demo
    for_each_env: true
    services:
      - { name: web-fe, type: frontend, runtime: node, entry: true, calls: [api] }
      - { name: api, type: web, runtime: python, replicas: 2 }
`
	r, err := Load([]byte(y), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	apiPod := func(clusterName string) string {
		cl := findCluster(t, r, clusterName)
		for _, w := range cl.Workloads {
			if w.Name == "api" {
				if len(w.PodNames) == 0 {
					t.Fatalf("%s: api has no PodNames", clusterName)
				}
				return w.PodNames[0]
			}
		}
		t.Fatalf("%s: no api workload placed", clusterName)
		return ""
	}
	a, b := apiPod("clus-a"), apiPod("clus-b")
	if a == b {
		t.Errorf("api pod names must differ across clusters, both = %q", a)
	}
}

// TestServiceNodeNamespaceField: a service node with an explicit `namespace:` is placed into that
// namespace (not defaulted to the node name); a node that omits `namespace:` still defaults to its
// own node name (back-compat). The dedup check must see the custom namespace.
func TestServiceNodeNamespaceField(t *testing.T) {
	reg := testRegistry(t)
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "app", Doc: "test",
		NewConfig:    func() any { return &testAppCfg{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}},
	})

	y := `
name: app-ns
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0ns01, nat_gateways: 0 }
    cluster:
      type: eks
      name: ns-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
workloads:
  - type: app
    name: demo
    runs_on: ns-prod-use1
    services:
      - name: frontend
        type: frontend
        runtime: node
        entry: true
        namespace: ui
        calls: [backend]
      - name: backend
        type: web
        runtime: go
        namespace: common-services
      - name: worker
        type: worker
        runtime: go
`
	r, err := Load([]byte(y), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var cl *fixture.Cluster
	for _, wi := range r.Workloads {
		if wi.Kind == "app" {
			cl = wi.Cluster
		}
	}
	if cl == nil {
		t.Fatal("no app workload instance / cluster resolved")
	}

	byName := map[string]fixture.Workload{}
	for _, w := range cl.Workloads {
		byName[w.Name] = w
	}

	// frontend: namespace: ui (explicit)
	fe, ok := byName["frontend"]
	if !ok {
		t.Fatal("frontend node not placed on cluster")
	}
	if fe.Namespace != "ui" {
		t.Errorf("frontend Namespace = %q, want %q (explicit namespace field)", fe.Namespace, "ui")
	}

	// backend: namespace: common-services (explicit)
	be, ok := byName["backend"]
	if !ok {
		t.Fatal("backend node not placed on cluster")
	}
	if be.Namespace != "common-services" {
		t.Errorf("backend Namespace = %q, want %q (explicit namespace field)", be.Namespace, "common-services")
	}

	// worker: no namespace declared — must default to the node name
	wk, ok := byName["worker"]
	if !ok {
		t.Fatal("worker node not placed on cluster")
	}
	if wk.Namespace != "worker" {
		t.Errorf("worker Namespace = %q, want %q (default = node name)", wk.Namespace, "worker")
	}
}
