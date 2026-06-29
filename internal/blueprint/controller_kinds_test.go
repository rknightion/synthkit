// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
)

// ctlAppCfg is a permissive app config carrying the controller-kind fields on service nodes, so the
// strict decoder accepts the test YAML without importing internal/workload/app.
type ctlAppCfg struct {
	Services []struct {
		Name         string   `yaml:"name"`
		Type         string   `yaml:"type"`
		Runtime      string   `yaml:"runtime"`
		Entry        bool     `yaml:"entry"`
		Replicas     int      `yaml:"replicas"`
		Calls        []string `yaml:"calls"`
		External     bool     `yaml:"external"`
		Controller   string   `yaml:"controller"`
		HPA          bool     `yaml:"hpa"`
		VolumeClaims []string `yaml:"volume_claims"`
	} `yaml:"services"`
	Traffic struct {
		OffPeakRPS float64 `yaml:"off_peak_rps"`
		PeakRPS    float64 `yaml:"peak_rps"`
	} `yaml:"traffic"`
}

func ctlRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg := testRegistry(t)
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "app", Doc: "test",
		NewConfig:    func() any { return &ctlAppCfg{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}},
	})
	// A generic in-cluster integration (an exporter/agent deployed into the estate) for the
	// runs_in_cluster placement tests. Not Cloudflare — that's an edge integration, not in-cluster.
	type ricCfg struct {
		Endpoint string `yaml:"endpoint"`
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "metrics_exporter", Doc: "in-cluster exporter integration", Scope: core.ScopeSubstrate,
		Group:     core.GroupIntegration,
		NewConfig: func() any { return &ricCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	return reg
}

func clusterOf(t *testing.T, r *Resolved) *fixture.Cluster {
	t.Helper()
	for _, ci := range r.Constructs {
		if ci.Kind == "k8s_cluster" && ci.Fixtures.Cluster != nil {
			return ci.Fixtures.Cluster
		}
	}
	t.Fatal("no k8s_cluster resolved")
	return nil
}

func wlByName(cl *fixture.Cluster, name string) (fixture.Workload, bool) {
	for _, w := range cl.Workloads {
		if w.Name == name {
			return w, true
		}
	}
	return fixture.Workload{}, false
}

// Phase 3b: a non-traced app service node may declare controller: statefulset (allowed) and
// controller: daemonset is rejected on an app service node (Q4: app nodes are the traced lane).
const ctlAppYAML = `
name: ctl-mini
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0mini01, nat_gateways: 2 }
    cluster:
      type: eks
      name: ctl-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
workloads:
  - type: app
    name: demo
    runs_on: ctl-prod-use1
    services:
      - { name: web-fe, type: frontend, runtime: node, entry: true, calls: [api] }
      - { name: api, type: web, runtime: python, replicas: 3, calls: [statepg] }
      - { name: statepg, type: db, replicas: 2, controller: statefulset, volume_claims: [datadir], hpa: true }
`

func TestAppNodeControllerStatefulSet(t *testing.T) {
	r, err := Load([]byte(ctlAppYAML), ctlRegistry(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cl := clusterOf(t, r)
	sps, ok := wlByName(cl, "statepg")
	if !ok {
		t.Fatal("statepg workload not placed")
	}
	if sps.Controller != "statefulset" {
		t.Errorf("statepg Controller=%q want statefulset", sps.Controller)
	}
	if !sps.HasHPA {
		t.Error("statepg HasHPA=false want true")
	}
	if len(sps.VolumeClaims) != 1 || sps.VolumeClaims[0] != "datadir" {
		t.Errorf("statepg VolumeClaims=%v want [datadir]", sps.VolumeClaims)
	}
	if sps.Replicas != 2 {
		t.Errorf("statepg Replicas=%d want 2", sps.Replicas)
	}
	// StatefulSet pods use ordinal names (no hash); PodNames[0] must be replica-0.
	if len(sps.PodNames) != 2 || sps.PodNames[0] != "statepg-0" || sps.PodNames[1] != "statepg-1" {
		t.Errorf("statepg PodNames=%v want [statepg-0 statepg-1]", sps.PodNames)
	}
	// A plain web node stays deployment (controller "").
	api, _ := wlByName(cl, "api")
	if api.Controller != "" {
		t.Errorf("api Controller=%q want \"\" (deployment)", api.Controller)
	}
	if strings.Count(api.PodNames[0], "-") < 2 {
		t.Errorf("api pod name %q is not ReplicaSet form", api.PodNames[0])
	}
}

const ctlDaemonAppYAML = `
name: ctl-dsapp
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0mini01, nat_gateways: 2 }
    cluster:
      type: eks
      name: ctl-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
workloads:
  - type: app
    name: demo
    runs_on: ctl-prod-use1
    services:
      - { name: web-fe, type: frontend, runtime: node, entry: true, calls: [agent] }
      - { name: agent, type: web, runtime: go, controller: daemonset }
`

func TestAppNodeDaemonsetRejected(t *testing.T) {
	_, err := Load([]byte(ctlDaemonAppYAML), ctlRegistry(t))
	if err == nil {
		t.Fatal("expected error: daemonset controller on an app service node")
	}
	if !strings.Contains(err.Error(), "daemonset") {
		t.Errorf("error %q does not mention daemonset", err)
	}
}

// Phase 3a: runs_in_cluster places the integration's deployed component as a cluster workload on
// each env's cluster, fanning over for_each_env, with the declared controller/namespace/replicas.
func loadCtl(t *testing.T, y string) *Resolved {
	t.Helper()
	r, err := Load([]byte(y), ctlRegistry(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func loadCtlErr(t *testing.T, y string) error {
	t.Helper()
	_, err := Load([]byte(y), ctlRegistry(t))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	return err
}

const runsInClusterYAML = `
name: ric
environments:
  - { name: dev, weight: 0.2, cloud: {provider: aws, account_id: "1", region: r, vpc_id: v},
      cluster: {type: eks, name: c-dev, node_groups: [{name: g, instance_type: m6i.large}]} }
  - { name: prod, cloud: {provider: aws, account_id: "2", region: r, vpc_id: v},
      cluster: {type: eks, name: c-prod, node_groups: [{name: g, instance_type: m6i.large}]} }
integrations:
  metrics_exporter:
    for_each_env: true
    endpoint: ":9100"
    runs_in_cluster:
      name: node-exporter
      namespace: monitoring
      controller: daemonset
`

func TestRunsInClusterPlacesPerEnv(t *testing.T) {
	r := loadCtl(t, runsInClusterYAML)
	for _, clName := range []string{"c-dev", "c-prod"} {
		cl := findCluster(t, r, clName)
		w, ok := wlByName(cl, "node-exporter")
		if !ok {
			t.Fatalf("%s: runs_in_cluster workload %q not placed", clName, "node-exporter")
		}
		if w.Namespace != "monitoring" {
			t.Errorf("%s: namespace=%q want monitoring", clName, w.Namespace)
		}
		if w.Controller != "daemonset" {
			t.Errorf("%s: controller=%q want daemonset", clName, w.Controller)
		}
		// DaemonSet replicas are zeroed at resolve time (one pod per node); pods are per-node.
		if w.Replicas != 0 {
			t.Errorf("%s: daemonset Replicas=%d want 0 (zeroed)", clName, w.Replicas)
		}
		if len(w.PodNames) != len(cl.Nodes) || len(cl.Nodes) == 0 {
			t.Errorf("%s: daemonset PodNames=%d want one per node (%d)", clName, len(w.PodNames), len(cl.Nodes))
		}
	}
	// The integration construct itself still resolves (config decoded, runs_in_cluster stripped).
	n := 0
	for _, ci := range r.Constructs {
		if ci.Kind == "metrics_exporter" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("metrics_exporter integration instances = %d, want 2 (per-env fan)", n)
	}
}

const runsInClusterDeployYAML = `
name: ric2
environments:
  - { name: prod, cloud: {provider: aws, account_id: "2", region: r, vpc_id: v},
      cluster: {type: eks, name: c-prod, node_groups: [{name: g, instance_type: m6i.large}]} }
integrations:
  metrics_exporter:
    for_each_env: true
    endpoint: ":9100"
    runs_in_cluster:
      name: kube-state-metrics
      namespace: monitoring
      replicas: 2
      hpa: true
`

func TestRunsInClusterDeploymentDefaults(t *testing.T) {
	r := loadCtl(t, runsInClusterDeployYAML)
	cl := findCluster(t, r, "c-prod")
	w, ok := wlByName(cl, "kube-state-metrics")
	if !ok {
		t.Fatal("kube-state-metrics not placed (explicit name should win over key)")
	}
	if w.Controller != "" {
		t.Errorf("controller=%q want \"\" (deployment default)", w.Controller)
	}
	if w.Replicas != 2 || len(w.PodNames) != 2 {
		t.Errorf("replicas=%d podnames=%d want 2/2", w.Replicas, len(w.PodNames))
	}
	if !w.HasHPA {
		t.Error("HasHPA=false want true")
	}
}

func TestRunsInClusterRequiresNamespace(t *testing.T) {
	err := loadCtlErr(t, `
name: ric3
environments:
  - { name: prod, cloud: {provider: aws, account_id: "2", region: r, vpc_id: v},
      cluster: {type: eks, name: c-prod, node_groups: [{name: g, instance_type: m6i.large}]} }
integrations:
  metrics_exporter:
    for_each_env: true
    endpoint: ":9100"
    runs_in_cluster: { replicas: 1 }
`)
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error %q does not mention namespace", err)
	}
}

func TestRunsInClusterRequiresFanout(t *testing.T) {
	err := loadCtlErr(t, `
name: ric4
environments:
  - { name: prod, cloud: {provider: aws, account_id: "2", region: r, vpc_id: v},
      cluster: {type: eks, name: c-prod, node_groups: [{name: g, instance_type: m6i.large}]} }
integrations:
  metrics_exporter:
    endpoint: ":9100"
    runs_in_cluster: { namespace: monitoring }
`)
	if !strings.Contains(err.Error(), "for_each_env") && !strings.Contains(err.Error(), "envs") {
		t.Errorf("error %q does not mention for_each_env/envs requirement", err)
	}
}
