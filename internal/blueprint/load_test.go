// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
)

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

// testRegistry builds a registry covering the v1 kinds the resolver emits plus the
// path-1 kinds (addons/features/workloads) the loader decodes configs for.
func testRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	for _, k := range TopologyKinds() {
		if k == KindCWInfra {
			continue // cw_infra is config-decoded (cloud.cloudwatch) — registered below
		}
		// The catalog wiring lane populates FailureModes on the production registry; this test
		// harness mirrors the database-axis vocabulary for the dbo11y_postgres construct so that
		// resolve-level incident/scenario validation has a real vocabulary to check against.
		var modes []failuremode.Mode
		if k == KindDbo11yPostgres {
			modes = []failuremode.Mode{
				{Name: "connection_saturation", Axis: failuremode.AxisDatabase},
				{Name: "replication_lag", Axis: failuremode.AxisDatabase},
				{Name: "lock_contention", Axis: failuremode.AxisDatabase},
				{Name: "slow_query_storm", Axis: failuremode.AxisDatabase},
			}
		}
		reg.RegisterConstruct(core.ConstructReg{
			Kind: k, Doc: "test", Scope: core.ScopeSubstrate,
			NewConfig:    func() any { return &struct{}{} },
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
		Kind: KindCWInfra, Doc: "test", Scope: core.ScopeBlueprint,
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

const minimalYAML = `
name: mini
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0mini01, nat_gateways: 2 }
    cluster:
      type: eks
      name: mini-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [core_dns, { name: cluster_autoscaler, min_nodes: 3, max_nodes: 10 }]
    databases:
      - { engine: postgres, version: "16.2", name: mini-db, observability: { dbo11y: true, digests: 5 } }
    caches:
      - { engine: redis, version: "7.1", name: mini-sessions }
workloads:
  - type: web_service
    name: mini-api
    runs_on: mini-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    calls: [{ db: mini-db }, { cache: mini-sessions }]
    endpoints: [{ route: "GET /v1/ping", error_rate: 0.01, p95_ms: 80 }]
features:
  synthetic_monitoring: { enabled: true, checks: [mini-api-health] }
incidents:
  - { kind: latency_spike, target: mini-api, at: "2026-06-19T14:00", for: 20m, intensity: 0.8 }
`

func load(t *testing.T, y string) *Resolved {
	t.Helper()
	r, err := Load([]byte(y), testRegistry(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func loadErr(t *testing.T, y string) error {
	t.Helper()
	_, err := Load([]byte(y), testRegistry(t))
	if err == nil {
		t.Fatalf("Load succeeded, want error")
	}
	return err
}

func TestLoadMinimalBlueprint(t *testing.T) {
	r := load(t, minimalYAML)
	if r.Name != "mini" || r.Label != "mini" {
		t.Fatalf("name/label = %q/%q (label should default to name)", r.Name, r.Label)
	}
	kinds := map[string]int{}
	for _, ci := range r.Constructs {
		kinds[ci.Kind]++
	}
	want := map[string]int{
		"k8s_cluster": 1, "ec2": 1, "cw_infra": 1,
		"core_dns": 1, "cluster_autoscaler": 1,
		"rds": 1, "dbo11y_postgres": 1, "elasticache": 1,
		"synthetic_monitoring": 1,
	}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("construct kind %q: got %d instances, want %d (all: %v)", k, kinds[k], n, kinds)
		}
	}
	if len(r.Workloads) != 1 || r.Workloads[0].Kind != "web_service" || r.Workloads[0].Name != "mini-api" {
		t.Fatalf("workloads: %+v", r.Workloads)
	}
}

func TestLoadRetainsRawSource(t *testing.T) {
	r := load(t, minimalYAML)
	if r.Source != minimalYAML {
		t.Fatalf("Resolved.Source = %q, want verbatim input YAML", r.Source)
	}
}

func TestUnknownTopLevelFieldFailsLoud(t *testing.T) {
	err := loadErr(t, "name: x\nbogus_key: 1\nenvironments: [{name: prod}]")
	if !strings.Contains(err.Error(), "bogus_key") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should name the unknown field: %v", err)
	}
}

func TestUnknownAddonFailsLoudWithCandidates(t *testing.T) {
	y := strings.Replace(minimalYAML, "core_dns,", "corednszzz,", 1)
	err := loadErr(t, y)
	if !strings.Contains(err.Error(), "corednszzz") {
		t.Fatalf("error should name the unknown addon: %v", err)
	}
}

func TestUnknownAddonConfigFieldFailsLoud(t *testing.T) {
	y := strings.Replace(minimalYAML, "min_nodes: 3", "minnn_nodes: 3", 1)
	if err := loadErr(t, y); !strings.Contains(err.Error(), "cluster_autoscaler") {
		t.Fatalf("error should attribute the bad field to its addon: %v", err)
	}
}

func TestDanglingRunsOnFailsLoud(t *testing.T) {
	y := strings.Replace(minimalYAML, "runs_on: mini-prod-use1", "runs_on: nope", 1)
	err := loadErr(t, y)
	if !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "mini-prod-use1") {
		t.Fatalf("dangling runs_on error should name the bad ref and the available clusters: %v", err)
	}
}

func TestDanglingCallFailsLoud(t *testing.T) {
	y := strings.Replace(minimalYAML, "db: mini-db", "db: ghost-db", 1)
	if err := loadErr(t, y); !strings.Contains(err.Error(), "ghost-db") {
		t.Fatalf("dangling call error: %v", err)
	}
}

func TestDanglingIncidentTargetFailsLoud(t *testing.T) {
	y := strings.Replace(minimalYAML, "target: mini-api", "target: ghost", 1)
	if err := loadErr(t, y); !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("dangling incident target error: %v", err)
	}
}

func TestUnknownWorkloadTypeFailsLoud(t *testing.T) {
	y := strings.Replace(minimalYAML, "type: web_service", "type: warp_drive", 1)
	if err := loadErr(t, y); !strings.Contains(err.Error(), "warp_drive") {
		t.Fatalf("unknown workload type error: %v", err)
	}
}

func TestNodeCountDerivedFromReplicas(t *testing.T) {
	// minimalYAML "prod" env: default weight=1.0 → production floor=6.
	// replicas=2 → pods=2 → max(6, ceil(2/8)=1) = 6 nodes (prod floor).
	r := load(t, minimalYAML)
	cl := findCluster(t, r, "mini-prod-use1")
	if len(cl.Nodes) != 6 {
		t.Fatalf("derived nodes = %d, want 6 (prod floor)", len(cl.Nodes))
	}
	// replicas=56 → pods=56 → max(6, ceil(56/8)=7) = 7 nodes (pod count wins over floor).
	y := strings.Replace(minimalYAML, "replicas: 2", "replicas: 56", 1)
	cl = findCluster(t, load(t, y), "mini-prod-use1")
	if len(cl.Nodes) != 7 {
		t.Fatalf("derived nodes = %d, want 7 (56 pods / 8 per node)", len(cl.Nodes))
	}
}

func TestExplicitDesiredWinsOverDerivation(t *testing.T) {
	y := strings.Replace(minimalYAML, "instance_type: m6i.xlarge", "instance_type: m6i.xlarge, desired: 4", 1)
	cl := findCluster(t, load(t, y), "mini-prod-use1")
	if len(cl.Nodes) != 4 {
		t.Fatalf("explicit desired nodes = %d, want 4", len(cl.Nodes))
	}
}

func TestNodeIdentityIsDeterministicAndEC2Shaped(t *testing.T) {
	a := findCluster(t, load(t, minimalYAML), "mini-prod-use1")
	b := findCluster(t, load(t, minimalYAML), "mini-prod-use1")
	for i := range a.Nodes {
		if a.Nodes[i].InstanceID != b.Nodes[i].InstanceID || a.Nodes[i].Hostname != b.Nodes[i].Hostname {
			t.Fatalf("node identity not deterministic: %+v vs %+v", a.Nodes[i], b.Nodes[i])
		}
		if !strings.HasPrefix(a.Nodes[i].InstanceID, "i-") || len(a.Nodes[i].InstanceID) != 19 {
			t.Fatalf("instance id shape: %q", a.Nodes[i].InstanceID)
		}
		if !strings.Contains(a.Nodes[i].Hostname, "us-east-1.compute.internal") {
			t.Fatalf("hostname shape: %q", a.Nodes[i].Hostname)
		}
	}
}

func TestWorkloadPlacementSharedWithCluster(t *testing.T) {
	r := load(t, minimalYAML)
	cl := findCluster(t, r, "mini-prod-use1")
	if len(cl.Workloads) != 1 || cl.Workloads[0].Name != "mini-api" || cl.Workloads[0].Replicas != 2 {
		t.Fatalf("cluster workload placement: %+v", cl.Workloads)
	}
	if len(cl.Workloads[0].PodNames) != 2 {
		t.Fatalf("pod names: %v", cl.Workloads[0].PodNames)
	}
	for _, p := range cl.Workloads[0].PodNames {
		if !strings.HasPrefix(p, "mini-api-") {
			t.Fatalf("pod name shape: %q", p)
		}
	}
	// The workload instance must see the SAME cluster fixture (pointer-shared identity).
	if r.Workloads[0].Cluster != cl {
		t.Fatalf("workload bound to a different cluster fixture copy")
	}
}

func TestDBFixtureSharedBetweenRDSAndDbo11y(t *testing.T) {
	r := load(t, minimalYAML)
	var rdsFx, dboFx *fixture.DB
	for _, ci := range r.Constructs {
		switch ci.Kind {
		case "rds":
			rdsFx = ci.Fixtures.DB
		case "dbo11y_postgres":
			dboFx = ci.Fixtures.DB
		}
	}
	if rdsFx == nil || dboFx == nil || rdsFx != dboFx {
		t.Fatalf("rds and dbo11y must share ONE fixture.DB pointer: %p vs %p", rdsFx, dboFx)
	}
	if rdsFx.Name != "mini-db" || len(rdsFx.ServerID) != 64 {
		t.Fatalf("db fixture identity: %+v", rdsFx)
	}
	if len(rdsFx.Queries) != 5 {
		t.Fatalf("digests knob: got %d queries, want 5", len(rdsFx.Queries))
	}
	if !strings.HasPrefix(rdsFx.InstanceKey, "postgresql://") {
		t.Fatalf("pg instance key form: %q", rdsFx.InstanceKey)
	}
}

func TestNoDbo11yWithoutObservability(t *testing.T) {
	y := strings.Replace(minimalYAML, ", observability: { dbo11y: true, digests: 5 }", "", 1)
	dbKinds := dbConstructKinds(load(t, y))
	if dbKinds["dbo11y_postgres"] != 0 {
		t.Fatalf("dbo11y instance emitted without observability block")
	}
	// Omitting the block ⇒ CloudWatch only (the back-compat default).
	if dbKinds["rds"] != 1 {
		t.Fatalf("omitting observability should still emit RDS CloudWatch; got rds=%d", dbKinds["rds"])
	}
}

// dbConstructKinds counts the per-DB construct kinds in a resolved blueprint.
func dbConstructKinds(r *Resolved) map[string]int {
	out := map[string]int{}
	for _, ci := range r.Constructs {
		switch ci.Kind {
		case "rds", "dbo11y_postgres", "dbo11y_mysql":
			out[ci.Kind]++
		}
	}
	return out
}

// constructKinds counts every construct kind in a resolved blueprint.
func constructKinds(r *Resolved) map[string]int {
	out := map[string]int{}
	for _, ci := range r.Constructs {
		out[ci.Kind]++
	}
	return out
}

// TestCacheEmissionSwitch: observability.cloudwatch:false drops the elasticache lane but
// the cache remains a resolvable workload call-target.
func TestCacheEmissionSwitch(t *testing.T) {
	// Default → elasticache emitted.
	if constructKinds(load(t, minimalYAML))["elasticache"] != 1 {
		t.Fatalf("default cache should emit elasticache")
	}
	// cloudwatch:false → no elasticache, but the cache still resolves as a call target.
	y := strings.Replace(minimalYAML,
		"{ engine: redis, version: \"7.1\", name: mini-sessions }",
		"{ engine: redis, version: \"7.1\", name: mini-sessions, observability: { cloudwatch: false } }", 1)
	r := load(t, y)
	if constructKinds(r)["elasticache"] != 0 {
		t.Errorf("cloudwatch:false cache should emit no elasticache construct")
	}
	var cacheCall bool
	for _, c := range r.Workloads[0].Calls {
		if c.Kind == "cache" && c.Cache != nil && c.Cache.Name == "mini-sessions" {
			cacheCall = true
		}
	}
	if !cacheCall {
		t.Errorf("call-target-only cache lost its call-target identity")
	}
}

// TestClusterEC2EmissionSwitch: cluster observability.cloudwatch:false drops the per-node
// ec2 CloudWatch lane while k8s_cluster (the in-cluster substrate) still emits.
func TestClusterEC2EmissionSwitch(t *testing.T) {
	if constructKinds(load(t, minimalYAML))["ec2"] != 1 {
		t.Fatalf("default cluster should emit ec2")
	}
	y := strings.Replace(minimalYAML,
		"name: mini-prod-use1\n",
		"name: mini-prod-use1\n      observability: { cloudwatch: false }\n", 1)
	r := load(t, y)
	k := constructKinds(r)
	if k["ec2"] != 0 {
		t.Errorf("cloudwatch:false cluster should emit no ec2 construct; got %d", k["ec2"])
	}
	if k["k8s_cluster"] != 1 {
		t.Errorf("k8s_cluster must still emit regardless of the EC2 switch; got %d", k["k8s_cluster"])
	}
}

// TestIntegrationsSection: a construct with GroupIntegration declares under integrations:
// and resolves to an instance, exactly like a feature does under features:.
func TestIntegrationsSection(t *testing.T) {
	y := minimalYAML + "integrations:\n  cloudflare: { enabled: true, zone: mini.example.com }\n"
	r := load(t, y)
	if constructKinds(r)["cloudflare"] != 1 {
		t.Fatalf("cloudflare declared under integrations: should resolve; got %v", constructKinds(r))
	}
}

// TestSectionGroupEnforcement: a kind must be declared in the section matching its Group.
// A Grafana Cloud feature under integrations:, or an external source under features:, is
// rejected loudly (the split is enforced, not merely conventional).
func TestSectionGroupEnforcement(t *testing.T) {
	// cloudflare (integration) wrongly placed under features: → error.
	wrong1 := minimalYAML + ""
	wrong1 = strings.Replace(wrong1,
		"  synthetic_monitoring: { enabled: true, checks: [mini-api-health] }",
		"  synthetic_monitoring: { enabled: true, checks: [mini-api-health] }\n  cloudflare: { enabled: true, zone: x }", 1)
	if err := loadErr(t, wrong1); err == nil || !strings.Contains(err.Error(), `"features" section`) || !strings.Contains(err.Error(), "cloudflare") {
		t.Fatalf("cloudflare under features: should be rejected; got %v", err)
	}
	// synthetic_monitoring (feature) wrongly placed under integrations: → error.
	wrong2 := minimalYAML + "integrations:\n  synthetic_monitoring: { enabled: true, checks: [x] }\n"
	if err := loadErr(t, wrong2); err == nil || !strings.Contains(err.Error(), `"integrations" section`) || !strings.Contains(err.Error(), "synthetic_monitoring") {
		t.Fatalf("synthetic_monitoring under integrations: should be rejected; got %v", err)
	}
}

// TestCloudWatchSubFamilyWiring: the cloud.cloudwatch block reaches the cw_infra config
// (valid keys load; unknown keys fail strict decode — proving the wiring is live).
func TestCloudWatchSubFamilyWiring(t *testing.T) {
	withCW := func(block string) string {
		return strings.Replace(minimalYAML, "nat_gateways: 2 }", "nat_gateways: 2, cloudwatch: "+block+" }", 1)
	}
	// Valid per-family toggles load fine.
	if _, err := Load([]byte(withCW("{ nlb: false, ebs: true, nat_gateway: false, firehose: false }")), testRegistry(t)); err != nil {
		t.Fatalf("valid cloudwatch block should load: %v", err)
	}
	// An unknown sub-family key fails strict decode (the block is wired to cw_infra's config).
	_, err := Load([]byte(withCW("{ bogus_family: true }")), testRegistry(t))
	if err == nil {
		t.Fatalf("unknown cloudwatch key should fail strict decode")
	}
}

// withObservability swaps mini-db's observability block for the given inline YAML.
func withObservability(obs string) string {
	return strings.Replace(minimalYAML,
		"observability: { dbo11y: true, digests: 5 }", obs, 1)
}

// TestObservabilityEmissionSwitch exercises all four cloudwatch×dbo11y combinations —
// the emission switch gates which CONSTRUCTS the database declaration fans into.
func TestObservabilityEmissionSwitch(t *testing.T) {
	cases := []struct {
		name string
		obs  string // observability block (or "" to omit it entirely)
		rds  int
		dbo  int
	}{
		{"defaults (omitted) → CloudWatch only", "", 1, 0},
		{"dbo11y only", "observability: { cloudwatch: false, dbo11y: true }", 0, 1},
		{"both lanes", "observability: { cloudwatch: true, dbo11y: true }", 1, 1},
		{"call-target only (neither)", "observability: { cloudwatch: false }", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := minimalYAML
			if tc.obs == "" {
				y = strings.Replace(minimalYAML, ", observability: { dbo11y: true, digests: 5 }", "", 1)
			} else {
				y = withObservability(tc.obs)
			}
			kinds := dbConstructKinds(load(t, y))
			if kinds["rds"] != tc.rds {
				t.Errorf("rds = %d, want %d", kinds["rds"], tc.rds)
			}
			if kinds["dbo11y_postgres"] != tc.dbo {
				t.Errorf("dbo11y_postgres = %d, want %d", kinds["dbo11y_postgres"], tc.dbo)
			}
		})
	}
}

// TestCallTargetOnlyDBStillResolves proves a both-lanes-off DB still resolves as a
// workload call-target (its fixture identity is intact even with no infra telemetry).
func TestCallTargetOnlyDBStillResolves(t *testing.T) {
	y := withObservability("observability: { cloudwatch: false }")
	r := load(t, y)
	w := r.Workloads[0]
	if w.Calls[0].DB == nil || w.Calls[0].DB.Name != "mini-db" {
		t.Fatalf("call-target-only DB lost its fixture identity: %+v", w.Calls[0])
	}
}

func TestCallsResolveToSharedFixtures(t *testing.T) {
	r := load(t, minimalYAML)
	w := r.Workloads[0]
	if len(w.Calls) != 2 {
		t.Fatalf("calls: %+v", w.Calls)
	}
	if w.Calls[0].Kind != "db" || w.Calls[0].DB == nil || w.Calls[0].DB.Name != "mini-db" {
		t.Fatalf("db call: %+v", w.Calls[0])
	}
	if w.Calls[1].Kind != "cache" || w.Calls[1].Cache == nil || w.Calls[1].Cache.Name != "mini-sessions" {
		t.Fatalf("cache call: %+v", w.Calls[1])
	}
}

func TestNATGatewayIDsResolved(t *testing.T) {
	r := load(t, minimalYAML)
	cl := findCluster(t, r, "mini-prod-use1")
	if got := len(cl.Cloud.NATGatewayIDs); got != 2 {
		t.Fatalf("nat gateways resolved = %d, want 2", got)
	}
	if !strings.HasPrefix(cl.Cloud.NATGatewayIDs[0], "nat-") {
		t.Fatalf("nat id shape: %q", cl.Cloud.NATGatewayIDs[0])
	}
}

func TestAlloyVersionCanonicalized(t *testing.T) {
	y := strings.Replace(minimalYAML, "node_groups:", "k8s_monitoring: { enabled: true, chart_version: \"4.1.4\", alloy: true, alloy_version: \"1.16.3\" }\n      node_groups:", 1)
	cl := findCluster(t, load(t, y), "mini-prod-use1")
	if cl.K8sMonitoring.AlloyVersion != "v1.16.3" {
		t.Fatalf("alloy version not canonicalized: %q", cl.K8sMonitoring.AlloyVersion)
	}
}

func TestIncidentScheduleEntries(t *testing.T) {
	r := load(t, minimalYAML)
	if len(r.Incidents) != 1 {
		t.Fatalf("incidents: %v", r.Incidents)
	}
	want := "latency_spike@2026-06-19T14:00/20m#0.8@mini-api"
	if r.Incidents[0] != want {
		t.Fatalf("incident schedule entry = %q, want %q", r.Incidents[0], want)
	}
}

func TestEnvDefaults(t *testing.T) {
	r := load(t, minimalYAML)
	env := r.Workloads[0].Env
	if env.Name != "prod" || env.Weight != 1.0 || env.NonProd {
		t.Fatalf("env defaults: %+v (prod should be production, weight 1.0)", env)
	}
	y := strings.Replace(minimalYAML, "- name: prod", "- name: staging", 1)
	y = strings.Replace(y, "mini-prod-use1", "mini-stg-use1", 2)
	env = load(t, y).Workloads[0].Env
	if !env.NonProd {
		t.Fatalf("non-prod default: %+v (staging should be NonProd)", env)
	}
}

func TestValidateSetRejectsCollisions(t *testing.T) {
	a := load(t, minimalYAML)
	b := load(t, strings.ReplaceAll(minimalYAML, "mini", "maxi"))
	if err := ValidateSet([]*Resolved{a, b}); err != nil {
		t.Fatalf("disjoint blueprints must validate: %v", err)
	}
	// Same cluster name in two blueprints = collision.
	c := load(t, strings.Replace(strings.ReplaceAll(minimalYAML, "mini", "other"), "other-prod-use1", "mini-prod-use1", 2))
	if err := ValidateSet([]*Resolved{a, c}); err == nil || !strings.Contains(err.Error(), "mini-prod-use1") {
		t.Fatalf("cluster collision not rejected: %v", err)
	}
	if err := ValidateSet([]*Resolved{a, a}); err == nil {
		t.Fatalf("duplicate blueprint name not rejected")
	}
}

func TestFeatureEnabledFalseSkipsInstance(t *testing.T) {
	y := strings.Replace(minimalYAML, "enabled: true", "enabled: false", 1)
	r := load(t, y)
	for _, ci := range r.Constructs {
		if ci.Kind == "synthetic_monitoring" {
			t.Fatalf("disabled feature still instantiated")
		}
	}
}

// Task 4 — M8/G5: TestWorkloadMinterWiringFields with real wsCfg cast + Traffic.PeakRPS.
func TestWorkloadMinterWiringFields(t *testing.T) {
	r := load(t, minimalYAML)
	w := r.Workloads[0]
	if w.Replicas != 2 {
		t.Fatalf("replicas wiring: %d", w.Replicas)
	}
	if w.Config == nil {
		t.Fatalf("workload config not decoded")
	}
	// testWsCfg is the package-level type registered by testRegistry. Assert the concrete
	// type and that Traffic.PeakRPS round-tripped from YAML (minimalYAML: peak_rps: 40).
	cfg, ok := w.Config.(*testWsCfg)
	if !ok {
		t.Fatalf("workload config type = %T, want *testWsCfg", w.Config)
	}
	if cfg.Traffic.PeakRPS != 40 {
		t.Fatalf("Traffic.PeakRPS = %v, want 40 (from minimalYAML peak_rps: 40)", cfg.Traffic.PeakRPS)
	}
}

func findCluster(t *testing.T, r *Resolved, name string) *fixture.Cluster {
	t.Helper()
	for _, ci := range r.Constructs {
		if ci.Kind == "k8s_cluster" && ci.Fixtures.Cluster != nil && ci.Fixtures.Cluster.Name == name {
			return ci.Fixtures.Cluster
		}
	}
	t.Fatalf("cluster %q not found", name)
	return nil
}

// Task 1 — M8/G1: TestValidateSetRejectsCrossBlueprint exercises label, database, cache, and
// workload cross-blueprint collisions by building *Resolved structs directly (no YAML surgery).
func TestValidateSetRejectsCrossBlueprint(t *testing.T) {
	reg := testRegistry(t)

	// Helper to build a minimal *Resolved for collision testing.
	makeResolved := func(name, label string) *Resolved {
		_, err := Load([]byte(`
name: `+name+`
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122220001", region: us-east-1, vpc_id: vpc-test01, nat_gateways: 0 }
workloads: []
`), reg)
		if err != nil {
			// We build Resolved directly when we need specific fixtures.
			_ = err
		}
		return &Resolved{Name: name, Label: label}
	}
	_ = makeResolved // helper defined for reference

	t.Run("label collision", func(t *testing.T) {
		a := &Resolved{Name: "bp-a", Label: "shared-label"}
		b := &Resolved{Name: "bp-b", Label: "shared-label"}
		err := ValidateSet([]*Resolved{a, b})
		if err == nil || !strings.Contains(err.Error(), "shared-label") {
			t.Fatalf("label collision not rejected: %v", err)
		}
	})

	t.Run("database collision", func(t *testing.T) {
		dbFx := &fixture.DB{Name: "shared-db", Engine: "postgres"}
		fxA := &fixture.Set{DB: dbFx}
		fxB := &fixture.Set{DB: dbFx}
		a := &Resolved{
			Name:  "bp-a",
			Label: "bp-a",
			Constructs: []ConstructInstance{
				{Kind: KindRDS, Name: "shared-db", Fixtures: fxA},
			},
		}
		b := &Resolved{
			Name:  "bp-b",
			Label: "bp-b",
			Constructs: []ConstructInstance{
				{Kind: KindRDS, Name: "shared-db", Fixtures: fxB},
			},
		}
		err := ValidateSet([]*Resolved{a, b})
		if err == nil || !strings.Contains(err.Error(), "shared-db") {
			t.Fatalf("database collision not rejected: %v", err)
		}
	})

	t.Run("cache collision", func(t *testing.T) {
		cacheFx := &fixture.Cache{Name: "shared-cache", Engine: "redis"}
		fxA := &fixture.Set{Cache: cacheFx}
		fxB := &fixture.Set{Cache: cacheFx}
		a := &Resolved{
			Name:  "bp-a",
			Label: "bp-a",
			Constructs: []ConstructInstance{
				{Kind: KindElastiCache, Name: "shared-cache", Fixtures: fxA},
			},
		}
		b := &Resolved{
			Name:  "bp-b",
			Label: "bp-b",
			Constructs: []ConstructInstance{
				{Kind: KindElastiCache, Name: "shared-cache", Fixtures: fxB},
			},
		}
		err := ValidateSet([]*Resolved{a, b})
		if err == nil || !strings.Contains(err.Error(), "shared-cache") {
			t.Fatalf("cache collision not rejected: %v", err)
		}
	})

	t.Run("workload collision", func(t *testing.T) {
		a := &Resolved{
			Name:      "bp-a",
			Label:     "bp-a",
			Workloads: []WorkloadInstance{{Kind: "web_service", Name: "shared-api"}},
		}
		b := &Resolved{
			Name:      "bp-b",
			Label:     "bp-b",
			Workloads: []WorkloadInstance{{Kind: "web_service", Name: "shared-api"}},
		}
		err := ValidateSet([]*Resolved{a, b})
		if err == nil || !strings.Contains(err.Error(), "shared-api") {
			t.Fatalf("workload collision not rejected: %v", err)
		}
	})

	t.Run("disjoint blueprints pass", func(t *testing.T) {
		a := &Resolved{Name: "bp-a", Label: "bp-a"}
		b := &Resolved{Name: "bp-b", Label: "bp-b"}
		if err := ValidateSet([]*Resolved{a, b}); err != nil {
			t.Fatalf("disjoint blueprints should validate: %v", err)
		}
	})
}

// Task 2 — M8/G2: unknown fields under a workload config and under a feature config must fail
// with a strict-decode error naming the bad field.
func TestUnknownWorkloadConfigFieldFailsLoud(t *testing.T) {
	// Insert an unknown key into the web_service workload config block.
	y := strings.Replace(minimalYAML,
		"traffic: { off_peak_rps: 5, peak_rps: 40 }",
		"traffic: { off_peak_rps: 5, peak_rps: 40 }\n    bogus_wl_field: 99", 1)
	err := loadErr(t, y)
	// Require the offending field name specifically — an unrelated "not found" error
	// must NOT satisfy this test (that would let a regression in strict decoding pass).
	if !strings.Contains(err.Error(), "bogus_wl_field") {
		t.Fatalf("unknown workload config field should fail loudly naming the field, got: %v", err)
	}
}

func TestUnknownFeatureConfigFieldFailsLoud(t *testing.T) {
	// Insert an unknown key into the synthetic_monitoring feature config block.
	y := strings.Replace(minimalYAML,
		"synthetic_monitoring: { enabled: true, checks: [mini-api-health] }",
		"synthetic_monitoring: { enabled: true, checks: [mini-api-health], bogus_feat_field: true }", 1)
	err := loadErr(t, y)
	// Require the offending field name specifically (see TestUnknownWorkloadConfigFieldFailsLoud).
	if !strings.Contains(err.Error(), "bogus_feat_field") {
		t.Fatalf("unknown feature config field should fail loudly naming the field, got: %v", err)
	}
}

// Task 3 — M8/G3: a call with BOTH db and cache set, and one with NEITHER, must fail loudly.
func TestAmbiguousCallDecl(t *testing.T) {
	t.Run("both db and cache set", func(t *testing.T) {
		y := strings.Replace(minimalYAML,
			"calls: [{ db: mini-db }, { cache: mini-sessions }]",
			"calls: [{ db: mini-db, cache: mini-sessions }]", 1)
		err := loadErr(t, y)
		if !strings.Contains(err.Error(), "exactly one of db|cache") {
			t.Fatalf("both db+cache call should fail: %v", err)
		}
	})

	t.Run("neither db nor cache set", func(t *testing.T) {
		y := strings.Replace(minimalYAML,
			"calls: [{ db: mini-db }, { cache: mini-sessions }]",
			"calls: [{}]", 1)
		err := loadErr(t, y)
		if !strings.Contains(err.Error(), "exactly one of db|cache") {
			t.Fatalf("empty call decl should fail: %v", err)
		}
	})
}

// Task 5 — M9: TestZeroConstructWarnings exercises the zeroConstructWarnings helper directly
// by building *Resolved and *Decl structs without going through Load (no log output needed).
func TestZeroConstructWarnings(t *testing.T) {
	t.Run("DB with KindRDS → 0 warnings", func(t *testing.T) {
		dbFx := &fixture.DB{Name: "my-db", Engine: "postgres"}
		r := &Resolved{
			Name:  "bp",
			Label: "bp",
			Constructs: []ConstructInstance{
				{Kind: KindRDS, Name: "my-db", Fixtures: &fixture.Set{DB: dbFx}},
			},
		}
		d := &Decl{
			Name: "bp",
			Environments: []EnvDecl{
				{Name: "prod", Databases: []DatabaseDecl{{Name: "my-db", Engine: "postgres"}}},
			},
		}
		warns := zeroConstructWarnings(r, d)
		if len(warns) != 0 {
			t.Fatalf("DB with KindRDS construct should produce 0 warnings, got: %v", warns)
		}
	})

	t.Run("DB with no construct → 1 warning naming it", func(t *testing.T) {
		r := &Resolved{Name: "bp", Label: "bp"}
		d := &Decl{
			Name: "bp",
			Environments: []EnvDecl{
				{Name: "prod", Databases: []DatabaseDecl{{Name: "call-only-db", Engine: "postgres"}}},
			},
		}
		warns := zeroConstructWarnings(r, d)
		if len(warns) != 1 {
			t.Fatalf("DB with no construct should produce 1 warning, got: %v", warns)
		}
		if !strings.Contains(warns[0], "call-only-db") || !strings.Contains(warns[0], "call-target only") {
			t.Fatalf("warning should name the DB and say call-target only: %q", warns[0])
		}
	})

	t.Run("cache with KindElastiCache → 0 warnings", func(t *testing.T) {
		cacheFx := &fixture.Cache{Name: "my-cache", Engine: "redis"}
		r := &Resolved{
			Name:  "bp",
			Label: "bp",
			Constructs: []ConstructInstance{
				{Kind: KindElastiCache, Name: "my-cache", Fixtures: &fixture.Set{Cache: cacheFx}},
			},
		}
		d := &Decl{
			Name: "bp",
			Environments: []EnvDecl{
				{Name: "prod", Caches: []CacheDecl{{Name: "my-cache", Engine: "redis"}}},
			},
		}
		warns := zeroConstructWarnings(r, d)
		if len(warns) != 0 {
			t.Fatalf("cache with KindElastiCache construct should produce 0 warnings, got: %v", warns)
		}
	})

	t.Run("cache with no construct → 1 warning naming it", func(t *testing.T) {
		r := &Resolved{Name: "bp", Label: "bp"}
		d := &Decl{
			Name: "bp",
			Environments: []EnvDecl{
				{Name: "prod", Caches: []CacheDecl{{Name: "call-only-cache", Engine: "redis"}}},
			},
		}
		warns := zeroConstructWarnings(r, d)
		if len(warns) != 1 {
			t.Fatalf("cache with no construct should produce 1 warning, got: %v", warns)
		}
		if !strings.Contains(warns[0], "call-only-cache") || !strings.Contains(warns[0], "call-target only") {
			t.Fatalf("warning should name the cache and say call-target only: %q", warns[0])
		}
	})

	t.Run("dbo11y-only DB → 0 warnings (has a construct)", func(t *testing.T) {
		dbFx := &fixture.DB{Name: "dbo-db", Engine: "postgres"}
		r := &Resolved{
			Name:  "bp",
			Label: "bp",
			Constructs: []ConstructInstance{
				{Kind: KindDbo11yPostgres, Name: "dbo-db", Fixtures: &fixture.Set{DB: dbFx}},
			},
		}
		d := &Decl{
			Name: "bp",
			Environments: []EnvDecl{
				{Name: "prod", Databases: []DatabaseDecl{{Name: "dbo-db", Engine: "postgres"}}},
			},
		}
		warns := zeroConstructWarnings(r, d)
		if len(warns) != 0 {
			t.Fatalf("dbo11y-only DB should produce 0 warnings, got: %v", warns)
		}
	})
}

// TestZeroConstructWarningLoggedThroughLoad is the end-to-end companion to
// TestZeroConstructWarnings: it drives the warning through the real Load() path and asserts
// the INFO line actually reaches the standard logger (resolve.go wires zeroConstructWarnings
// → log.Printf). The helper-level test proves WHAT we warn about; this proves we DO log it.
//
// NOTE: this rebinds the process-global log output, so it must not run concurrently with other
// log-capturing tests — the blueprint package does not call t.Parallel(), so this is safe today.
func TestZeroConstructWarningLoggedThroughLoad(t *testing.T) {
	// mini-db declared with cloudwatch:false and no dbo11y → call-target only, zero constructs.
	y := strings.Replace(minimalYAML,
		"observability: { dbo11y: true, digests: 5 }",
		"observability: { cloudwatch: false }", 1)

	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

	load(t, y) // must succeed — a call-target-only DB is informational, not an error

	out := buf.String()
	if !strings.Contains(out, "mini-db") || !strings.Contains(out, "call-target only") {
		t.Fatalf("Load should log an INFO line naming the call-target-only DB; got log output:\n%s", out)
	}
}

func TestResolveControlPlaneAndPodLogsMethod(t *testing.T) {
	r := load(t, `
name: t
environments:
  - name: prod
    weight: 1
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0t01, nat_gateways: 0 }
    cluster:
      type: eks
      name: c1
      node_groups: [{name: win, instance_type: m5.xlarge, os: windows}]
      k8s_monitoring:
        enabled: true
        features: {pod_logs: true}
        control_plane: {kube_proxy: true, api_server: true}
`)
	var cl *fixture.Cluster
	for _, ci := range r.Constructs {
		if ci.Kind == "k8s_cluster" {
			cl = ci.Fixtures.Cluster
			break
		}
	}
	if cl == nil {
		t.Fatal("no k8s_cluster construct resolved")
	}
	if !cl.K8sMonitoring.ControlPlane.KubeProxy {
		t.Fatal("kube_proxy not propagated")
	}
	if !cl.K8sMonitoring.ControlPlane.ApiServer {
		t.Fatal("api_server not propagated")
	}
	if cl.K8sMonitoring.PodLogsMethod != "opentelemetry" {
		t.Fatalf("default method=%q", cl.K8sMonitoring.PodLogsMethod)
	}
	if cl.NodeGroups[0].OS != "windows" {
		t.Fatal("node group OS not propagated")
	}
}

// TestLoadNamespaced verifies the BLOCKER fix: namespacing must happen on Decl.Name
// BEFORE resolve() so the determinism seed + all fixture identities are namespaced.
func TestLoadNamespaced(t *testing.T) {
	// Pinned minimal blueprint: host-only (satisfies validateDecl's ≥1-env-or-host rule).
	miniYAML := []byte("name: mini\nhosts:\n  - name: h1\n    os: linux\n")
	reg := testRegistry(t)

	// "" prefix ⇒ behaves like plain Load.
	r0, err := LoadNamespaced(miniYAML, "", reg)
	if err != nil {
		t.Fatalf("LoadNamespaced(\"\") failed: %v", err)
	}
	if r0.Name != "mini" || r0.Label != "mini" {
		t.Fatalf("empty prefix: name=%q label=%q, want mini/mini", r0.Name, r0.Label)
	}

	// "team-a" prefix ⇒ name and label both prefixed before resolve.
	r, err := LoadNamespaced(miniYAML, "team-a", reg)
	if err != nil {
		t.Fatalf("LoadNamespaced(\"team-a\") failed: %v", err)
	}
	if r.Name != "team-a/mini" {
		t.Fatalf("Name = %q, want team-a/mini", r.Name)
	}
	if r.Label != "team-a/mini" {
		t.Fatalf("Label = %q, want team-a/mini", r.Label)
	}

	// Seed-namespacing assertion (the BLOCKER guard):
	// resolve() sets seed = d.Name (which is now "team-a/mini") then builds the host Set as:
	//   &fixture.Set{Seed: seed + ":host:" + h.Hostname, Host: h}
	// So the host construct's Fixtures.Seed must contain "team-a/mini".
	var hostSeed string
	for _, ci := range r.Constructs {
		if ci.Kind == KindHost {
			hostSeed = ci.Fixtures.Seed
			break
		}
	}
	if hostSeed == "" {
		t.Fatalf("no host construct found in resolved blueprint; constructs: %v", r.Constructs)
	}
	if !strings.Contains(hostSeed, "team-a/mini") {
		t.Fatalf("host fixture Seed = %q — does not contain namespaced name %q (seed was NOT namespaced before resolve)", hostSeed, "team-a/mini")
	}
}

// silence unused-import when ledger types are only referenced in later tests
var _ ledger.Outcome

func TestRegionsParseAndRejectWithTimezone(t *testing.T) {
	// (a) A blueprint with a regions list parses and the resolved blueprint carries 2 regions.
	// A minimal environment (name-only) satisfies the loader; no cluster/workloads needed.
	goodYAML := `
name: regtest
regions:
  - {name: eu, timezone: Europe/Zurich, weight: 0.5}
  - {name: us, timezone: America/New_York, weight: 0.5}
environments:
  - name: prod
`
	res := load(t, goodYAML)
	if len(res.Regions) != 2 {
		t.Fatalf("want 2 regions, got %d: %+v", len(res.Regions), res.Regions)
	}
	if res.Regions[0].Name != "eu" || res.Regions[0].Timezone != "Europe/Zurich" {
		t.Errorf("region[0]: got %+v", res.Regions[0])
	}
	if res.Regions[1].Name != "us" || res.Regions[1].Timezone != "America/New_York" {
		t.Errorf("region[1]: got %+v", res.Regions[1])
	}

	// (b) A blueprint with both timezone and regions must be rejected.
	badYAML := `
name: regtest2
timezone: Europe/Zurich
regions:
  - {name: eu, timezone: Europe/Zurich, weight: 1}
environments:
  - name: prod
`
	if _, err := Load([]byte(badYAML), testRegistry(t)); err == nil {
		t.Error("expected error for timezone+regions conflict, got nil")
	}
}
