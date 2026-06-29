// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// TestBuildNodesDelegatesToDeriveNodes is the byte-parity guard for the buildNodes→DeriveNodesFloor
// delegation. The resolved cl.Nodes MUST equal fixture.DeriveNodesFloor called with the SAME inputs
// the resolver carries on the cluster fixture (Seed/Name/NodeGroups/Region + the live pod total +
// env-weighted floor). This is load-bearing: constructs re-derive the node set live from exactly
// these fields, so resolved identities must reproduce byte-for-byte.
func TestBuildNodesDelegatesToDeriveNodes(t *testing.T) {
	r := load(t, minimalYAML)
	cl := findCluster(t, r, "mini-prod-use1")

	// The resolver must have populated the live-derivation fields.
	if cl.Seed != "mini" {
		t.Fatalf("cl.Seed = %q, want bare blueprint name %q (load-bearing for live derivation)", cl.Seed, "mini")
	}
	if cl.Region != "us-east-1" {
		t.Fatalf("cl.Region = %q, want %q", cl.Region, "us-east-1")
	}
	if len(cl.NodeGroups) != 1 || cl.NodeGroups[0].Name != "general" || cl.NodeGroups[0].InstanceType != "m6i.xlarge" {
		t.Fatalf("cl.NodeGroups not populated from decl: %+v", cl.NodeGroups)
	}

	// Recompute the pod total exactly as the resolver does (Σ live replicas of placed workloads).
	pods := 0
	for _, w := range cl.Workloads {
		pods += w.Replicas
	}
	// minimalYAML prod env has default weight=1.0 → floor=6.
	floor := fixture.EnvNodeFloor(cl.Env.Weight)
	want := fixture.DeriveNodesFloor(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, pods, floor)
	if !reflect.DeepEqual(cl.Nodes, want) {
		t.Fatalf("resolved cl.Nodes != DeriveNodesFloor(same inputs, floor=%d):\n got=%+v\nwant=%+v", floor, cl.Nodes, want)
	}
}

// TestBuildNodesParityPinnedDesired confirms the delegation also preserves the explicit-desired
// (pinned) path byte-for-byte.
func TestBuildNodesParityPinnedDesired(t *testing.T) {
	y := strings.Replace(minimalYAML, "instance_type: m6i.xlarge", "instance_type: m6i.xlarge, desired: 4", 1)
	cl := findCluster(t, load(t, y), "mini-prod-use1")
	pods := 0
	for _, w := range cl.Workloads {
		pods += w.Replicas
	}
	want := fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, pods)
	if !reflect.DeepEqual(cl.Nodes, want) {
		t.Fatalf("pinned-desired parity mismatch:\n got=%+v\nwant=%+v", cl.Nodes, want)
	}
	if len(cl.Nodes) != 4 {
		t.Fatalf("pinned desired=4 should yield 4 nodes, got %d", len(cl.Nodes))
	}
}

// TestVocabularyUnionsRegistry asserts vocabulary() returns reg.AllFailureModes() — the union of
// FailureModes across registered construct + workload kinds.
func TestVocabularyUnionsRegistry(t *testing.T) {
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "x", Doc: "t", Scope: core.ScopeSubstrate,
		NewConfig:    func() any { return &struct{}{} },
		Build:        func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "m1", Axis: failuremode.AxisDatabase}},
	})
	reg.RegisterWorkload(core.WorkloadReg{
		Kind: "w", Doc: "t",
		NewConfig:    func() any { return &struct{}{} },
		Build:        func(cfg any, b core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: []failuremode.Mode{{Name: "m2", Axis: failuremode.AxisWorkload}},
	})
	got := vocabulary(reg)
	if !reflect.DeepEqual(got, reg.AllFailureModes()) {
		t.Fatalf("vocabulary != reg.AllFailureModes(): %+v vs %+v", got, reg.AllFailureModes())
	}
	names := map[string]bool{}
	for _, m := range got {
		names[m.Name] = true
	}
	if !names["m1"] || !names["m2"] {
		t.Fatalf("vocabulary missing modes: %+v", got)
	}
}

// scenarioRegistry returns a registry with a populated failure-mode vocabulary. testRegistry now
// mirrors the catalog vocabulary (web_service workload-axis modes + dbo11y_postgres database-axis
// modes), so resolve-level scenario tests run against a real vocabulary WITHOUT depending on the
// catalog wiring lane (which populates FailureModes on the production registry later).
func scenarioRegistry(t *testing.T) *core.Registry {
	t.Helper()
	return testRegistry(t)
}

func loadWith(t *testing.T, reg *core.Registry, y string) (*Resolved, error) {
	t.Helper()
	return Load([]byte(y), reg)
}

// integRegistry is testRegistry plus a stub GroupIntegration construct ("test_integration")
// for the per-env fan-out tests (Spec 3 Task 2/3).
func integRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg := testRegistry(t)
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "test_integration", Doc: "test", Scope: core.ScopeSubstrate, Group: core.GroupIntegration,
		NewConfig: func() any { return &struct{}{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	return reg
}

func TestIntegrationFansPerEnv(t *testing.T) {
	r, err := loadWith(t, integRegistry(t), `
name: t
environments:
  - { name: dev, weight: 0.2 }
  - { name: prod }
integrations:
  test_integration:
    for_each_env: true
`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ci := range r.Constructs {
		if ci.Kind != "test_integration" {
			continue
		}
		if ci.Fixtures == nil || ci.Fixtures.Env == nil {
			t.Fatalf("fanned integration missing Env fixture: %+v", ci)
		}
		got[ci.Fixtures.Env.Name] = true
	}
	if len(got) != 2 || !got["dev"] || !got["prod"] {
		t.Fatalf("want one test_integration per env {dev,prod}, got %v", got)
	}
}

func TestIntegrationNoFanoutSingleAggregate(t *testing.T) {
	r, err := loadWith(t, integRegistry(t), `
name: t
environments: [{name: dev}, {name: prod}]
integrations: { test_integration: {} }
`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ci := range r.Constructs {
		if ci.Kind != "test_integration" {
			continue
		}
		n++
		if ci.Fixtures.Env != nil {
			t.Fatalf("aggregate (non-fanned) integration must have nil Env, got %q", ci.Fixtures.Env.Name)
		}
	}
	if n != 1 {
		t.Fatalf("non-fanned integration must be a single instance, got %d", n)
	}
}

func TestWorkloadFansPerEnvBoundToCluster(t *testing.T) {
	r := load(t, `
name: t
environments:
  - { name: dev, cloud: {provider: aws, account_id: "1", region: r, vpc_id: v},
      cluster: {type: eks, name: c-dev, node_groups: [{name: g, instance_type: m6i.large}]} }
  - { name: prod, cloud: {provider: aws, account_id: "2", region: r, vpc_id: v},
      cluster: {type: eks, name: c-prod, node_groups: [{name: g, instance_type: m6i.large}]} }
workloads:
  - { type: web_service, name: app, for_each_env: true, calls: [{agent: planner}] }
`)
	got := map[string]string{}
	for _, w := range r.Workloads {
		got[w.Name] = w.Cluster.Name
	}
	if len(got) != 2 || got["app-dev"] != "c-dev" || got["app-prod"] != "c-prod" {
		t.Fatalf("fanned workloads not bound per-env (want app-dev→c-dev, app-prod→c-prod): %v", got)
	}
	// each fanned instance must be its own correlation domain: distinct Env
	for _, w := range r.Workloads {
		if w.Env == nil {
			t.Fatalf("fanned instance %q has nil Env", w.Name)
		}
	}
}

// TestWorkloadInstanceCarriesEnvDatabases asserts each resolved WorkloadInstance carries its
// env's declared databases (as resolved *fixture.DB). This is the plumbing that lets an app
// workload's db-leaf node resolve to its env's RDS instance (server.address / db.namespace on
// the db-client span) without the workload package importing the blueprint or minting identity.
func TestWorkloadInstanceCarriesEnvDatabases(t *testing.T) {
	r := load(t, `
name: t
environments:
  - { name: BVE, cloud: {provider: aws, account_id: "1", region: us-east-1, vpc_id: v},
      cluster: {type: eks, name: c-bve, node_groups: [{name: g, instance_type: m6i.large}]},
      databases: [{engine: postgres, version: "16.4", name: envpg-bve}] }
  - { name: PRD, cloud: {provider: aws, account_id: "2", region: us-east-1, vpc_id: v},
      cluster: {type: eks, name: c-prd, node_groups: [{name: g, instance_type: m6i.large}]},
      databases: [{engine: postgres, version: "16.4", name: envpg-prd}] }
workloads:
  - { type: web_service, name: app, for_each_env: true, calls: [{agent: planner}] }
`)
	got := map[string][]string{} // workload name → its env's db names
	for _, w := range r.Workloads {
		for _, db := range w.Databases {
			got[w.Name] = append(got[w.Name], db.Name)
		}
	}
	if len(got["app-BVE"]) != 1 || got["app-BVE"][0] != "envpg-bve" {
		t.Errorf("app-BVE Databases = %v, want [envpg-bve]", got["app-BVE"])
	}
	if len(got["app-PRD"]) != 1 || got["app-PRD"][0] != "envpg-prd" {
		t.Errorf("app-PRD Databases = %v, want [envpg-prd]", got["app-PRD"])
	}
}

func TestFannedWorkloadRejectsDBCall(t *testing.T) {
	err := loadErr(t, `
name: t
environments:
  - { name: dev, cloud: {provider: aws, account_id: "1", region: r, vpc_id: v},
      cluster: {type: eks, name: c-dev, node_groups: [{name: g, instance_type: m6i.large}]},
      databases: [{engine: postgres, name: db-dev}] }
workloads:
  - { type: web_service, name: app, for_each_env: true, calls: [{db: db-dev}] }
`)
	if err == nil || !strings.Contains(err.Error(), "may not declare a db/cache") {
		t.Fatalf("expected db/cache rejection for fanned workload, got %v", err)
	}
}

func TestFannedWorkloadRejectsRunsOn(t *testing.T) {
	err := loadErr(t, `
name: t
environments:
  - { name: dev, cloud: {provider: aws, account_id: "1", region: r, vpc_id: v},
      cluster: {type: eks, name: c-dev, node_groups: [{name: g, instance_type: m6i.large}]} }
workloads:
  - { type: web_service, name: app, for_each_env: true, runs_on: c-dev, calls: [{agent: planner}] }
`)
	if err == nil || !strings.Contains(err.Error(), "must not set `runs_on`") {
		t.Fatalf("expected runs_on rejection for fanned workload, got %v", err)
	}
}

// TestResolveScenariosPopulated: a valid scenarios: block resolves into Resolved.Scenarios and
// Targets is non-empty.
func TestResolveScenariosPopulated(t *testing.T) {
	y := minimalYAML + `
scenarios:
  - name: db_meltdown
    title: Database meltdown
    summary: connections saturate
    effects:
      - { mode: connection_saturation, target: mini-db, intensity: 0.7 }
      - { mode: latency_spike, target: mini-api }
`
	r, err := loadWith(t, scenarioRegistry(t), y)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Targets) == 0 {
		t.Fatalf("Targets empty")
	}
	if len(r.Scenarios) != 1 {
		t.Fatalf("Scenarios = %d, want 1: %+v", len(r.Scenarios), r.Scenarios)
	}
	sc := r.Scenarios[0]
	if sc.Name != "db_meltdown" || sc.Title != "Database meltdown" || sc.Summary != "connections saturate" {
		t.Fatalf("scenario fields: %+v", sc)
	}
	if len(sc.Effects) != 2 {
		t.Fatalf("scenario effects: %+v", sc.Effects)
	}
}

// TestResolveScenarioTitleDefaultsToName: an omitted title falls back to the name.
func TestResolveScenarioTitleDefaultsToName(t *testing.T) {
	y := minimalYAML + `
scenarios:
  - name: only_name
    effects:
      - { mode: latency_spike, target: mini-api }
`
	r, err := loadWith(t, scenarioRegistry(t), y)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Scenarios[0].Title != "only_name" {
		t.Fatalf("title should default to name: %+v", r.Scenarios[0])
	}
}

// TestResolveScenarioUnknownModeFails: an effect naming an unknown mode is rejected at load.
func TestResolveScenarioUnknownModeFails(t *testing.T) {
	y := minimalYAML + `
scenarios:
  - name: bad
    effects:
      - { mode: warp_core_breach, target: mini-api }
`
	_, err := loadWith(t, scenarioRegistry(t), y)
	if err == nil || !strings.Contains(err.Error(), "warp_core_breach") {
		t.Fatalf("unknown mode should fail naming it: %v", err)
	}
}

// TestResolveScenarioModeWrongAxisFails: connection_saturation (database) on a workload target.
func TestResolveScenarioModeWrongAxisFails(t *testing.T) {
	y := minimalYAML + `
scenarios:
  - name: bad
    effects:
      - { mode: connection_saturation, target: mini-api }
`
	_, err := loadWith(t, scenarioRegistry(t), y)
	if err == nil || !strings.Contains(err.Error(), "mini-api") {
		t.Fatalf("mode on wrong axis should fail: %v", err)
	}
}

// TestResolveDuplicateScenarioFails: two scenarios with the same name are rejected.
func TestResolveDuplicateScenarioFails(t *testing.T) {
	y := minimalYAML + `
scenarios:
  - name: dup
    effects: [{ mode: latency_spike, target: mini-api }]
  - name: dup
    effects: [{ mode: error_burst, target: mini-api }]
`
	_, err := loadWith(t, scenarioRegistry(t), y)
	if err == nil || !strings.Contains(err.Error(), "dup") {
		t.Fatalf("duplicate scenario name should fail: %v", err)
	}
}

// scenarioDecl is a minimal Decl with one workload, one db, and a db_meltdown scenario, used to
// exercise resolve()'s incident loop directly. We call resolve() (not Load()) on purpose: the
// load.go validateDecl precondition still requires `kind + at + for` on every incident and so
// rejects a scenario-ref incident BEFORE resolve runs — see the report's deferred-work note. resolve
// is the unit under test here, so we drive it directly with a pre-validated Decl.
func scenarioDecl(incidents []IncidentDecl) *Decl {
	return &Decl{
		Name:      "mini",
		Workloads: []WorkloadDecl{{Type: "web_service", Name: "mini-api", RunsOn: "c"}},
		Environments: []EnvDecl{{
			Name:      "prod",
			Cloud:     &CloudDecl{Provider: "aws", AccountID: "111122223333", Region: "us-east-1", VpcID: "vpc-0test"},
			Cluster:   &ClusterDecl{Type: "eks", Name: "c", NodeGroups: []NodeGroupDecl{{Name: "general", InstanceType: "m6i.large"}}},
			Databases: []DatabaseDecl{{Engine: "postgres", Name: "mini-db", Observability: &ObservabilityDecl{Dbo11y: true}}},
		}},
		Scenarios: []ScenarioDecl{{
			Name: "db_meltdown",
			Effects: []EffectDecl{
				{Mode: "connection_saturation", Target: "mini-db", Intensity: 0.7},
				{Mode: "latency_spike", Target: "mini-api"},
			},
		}},
		Incidents: incidents,
	}
}

// TestResolveIncidentScenarioRef: an incident referencing a scenario expands every effect into a
// schedule entry sharing the incident's at/for. (Drives resolve() directly — see scenarioDecl.)
func TestResolveIncidentScenarioRef(t *testing.T) {
	d := scenarioDecl([]IncidentDecl{{Scenario: "db_meltdown", At: "2026-06-19T14:00", For: "20m"}})
	r, err := resolve(d, scenarioRegistry(t))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := map[string]bool{
		"connection_saturation@2026-06-19T14:00/20m#0.7@mini-db": true,
		// latency_spike effect has no declared intensity → defaults to 1.0.
		"latency_spike@2026-06-19T14:00/20m#1@mini-api": true,
	}
	got := map[string]bool{}
	for _, e := range r.Incidents {
		got[e] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("missing schedule entry %q (got %v)", w, r.Incidents)
		}
	}
	if len(r.Incidents) != 2 {
		t.Errorf("expected 2 schedule entries, got %d: %v", len(r.Incidents), r.Incidents)
	}
}

// TestResolveIncidentInterval: an incident with Every (and no At) resolves to an "every<dur>"
// interval schedule entry, which the shape engine Evals active/inactive on its epoch phase.
func TestResolveIncidentInterval(t *testing.T) {
	d := scenarioDecl([]IncidentDecl{{Kind: "latency_spike", Target: "mini-api", Every: "10m", For: "5m", Intensity: 0.2}})
	r, err := resolve(d, scenarioRegistry(t))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "latency_spike@every10m/5m#0.2@mini-api"
	if len(r.Incidents) != 1 || r.Incidents[0] != want {
		t.Fatalf("interval incident = %v, want [%q]", r.Incidents, want)
	}
	// The entry must Eval as an interval window: active in the first 5m of each 10m period.
	e := shape.New("UTC", r.Incidents)
	base := int64(1_750_000_800)
	base -= base % 600
	if ok, inten := e.Eval(time.Unix(base+60, 0), "latency_spike", "mini-api"); !ok || inten != 0.2 {
		t.Fatalf("interval entry must fire in first half: ok=%v inten=%v", ok, inten)
	}
	if ok, _ := e.Eval(time.Unix(base+420, 0), "latency_spike", "mini-api"); ok {
		t.Fatal("interval entry must be inactive in second half (offset 7m)")
	}
}

// TestResolveIncidentEveryAndAtMutuallyExclusive: setting both Every and At is rejected at load.
func TestResolveIncidentEveryAndAtMutuallyExclusive(t *testing.T) {
	d := scenarioDecl([]IncidentDecl{{Kind: "latency_spike", Target: "mini-api", Every: "10m", At: "2026-06-19T14:00", For: "5m"}})
	_, err := resolve(d, scenarioRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "every") {
		t.Fatalf("setting both every and at should fail naming 'every': %v", err)
	}
}

// TestResolveIncidentEveryInvalid: a malformed Every duration is rejected at load.
func TestResolveIncidentEveryInvalid(t *testing.T) {
	d := scenarioDecl([]IncidentDecl{{Kind: "latency_spike", Target: "mini-api", Every: "notaduration", For: "5m"}})
	_, err := resolve(d, scenarioRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "every") {
		t.Fatalf("invalid every should fail naming 'every': %v", err)
	}
}

// TestResolveIncidentScenarioRefUnknown: referencing a missing scenario fails loudly.
func TestResolveIncidentScenarioRefUnknown(t *testing.T) {
	d := scenarioDecl([]IncidentDecl{{Scenario: "ghost", At: "2026-06-19T14:00", For: "20m"}})
	_, err := resolve(d, scenarioRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("unknown scenario ref should fail naming it: %v", err)
	}
}

// TestResolveIncidentScenarioAndKindMutuallyExclusive: setting both scenario and kind is rejected.
func TestResolveIncidentScenarioAndKindMutuallyExclusive(t *testing.T) {
	y := strings.Replace(minimalYAML,
		`  - { kind: latency_spike, target: mini-api, at: "2026-06-19T14:00", for: 20m, intensity: 0.8 }`,
		`  - { kind: latency_spike, scenario: s, at: "2026-06-19T14:00", for: 20m }`, 1) + `
scenarios:
  - name: s
    effects: [{ mode: latency_spike, target: mini-api }]
`
	_, err := loadWith(t, scenarioRegistry(t), y)
	if err == nil || !strings.Contains(err.Error(), "scenario") {
		t.Fatalf("incident with both scenario and kind should fail: %v", err)
	}
}

// ── SK-4 / SK-5: instance_class resolver tests ───────────────────────────────────────────────────

// findDB finds the fixture.DB for the given DB name from a Resolved struct.
func findDB(t *testing.T, r *Resolved, name string) *fixture.DB {
	t.Helper()
	for _, ci := range r.Constructs {
		if ci.Fixtures.DB != nil && ci.Fixtures.DB.Name == name {
			return ci.Fixtures.DB
		}
	}
	t.Fatalf("DB fixture %q not found in constructs", name)
	return nil
}

// findCacheFixture finds the fixture.Cache for the given cache name from a Resolved struct.
func findCacheFixture(t *testing.T, r *Resolved, name string) *fixture.Cache {
	t.Helper()
	for _, ci := range r.Constructs {
		if ci.Fixtures.Cache != nil && ci.Fixtures.Cache.Name == name {
			return ci.Fixtures.Cache
		}
	}
	t.Fatalf("Cache fixture %q not found in constructs", name)
	return nil
}

// TestDBInstanceClassDefaultApplied verifies the resolver applies the default "db.t3.medium"
// when instance_class is omitted from the databases: declaration (SK-4, requirement a).
func TestDBInstanceClassDefaultApplied(t *testing.T) {
	r := load(t, minimalYAML)
	db := findDB(t, r, "mini-db")
	if db.InstanceClass != "db.t3.medium" {
		t.Errorf("DB.InstanceClass = %q, want default %q", db.InstanceClass, "db.t3.medium")
	}
}

// TestCacheInstanceClassDefaultApplied verifies the resolver applies the default "cache.r6g.large"
// when instance_class is omitted from the caches: declaration (SK-5, requirement a).
func TestCacheInstanceClassDefaultApplied(t *testing.T) {
	r := load(t, minimalYAML)
	cache := findCacheFixture(t, r, "mini-sessions")
	if cache.InstanceClass != "cache.r6g.large" {
		t.Errorf("Cache.InstanceClass = %q, want default %q", cache.InstanceClass, "cache.r6g.large")
	}
}

// TestDBInstanceClassFlowsFromYAML verifies that a non-default instance_class in the blueprint
// YAML flows through to fixture.DB.InstanceClass (SK-4, requirement b).
func TestDBInstanceClassFlowsFromYAML(t *testing.T) {
	y := strings.Replace(minimalYAML,
		`{ engine: postgres, version: "16.2", name: mini-db, observability: { dbo11y: true, digests: 5 } }`,
		`{ engine: postgres, version: "16.2", name: mini-db, instance_class: db.r6g.large, observability: { dbo11y: true, digests: 5 } }`,
		1)
	r := load(t, y)
	db := findDB(t, r, "mini-db")
	if db.InstanceClass != "db.r6g.large" {
		t.Errorf("DB.InstanceClass = %q, want %q (from YAML)", db.InstanceClass, "db.r6g.large")
	}
}

// TestCacheInstanceClassFlowsFromYAML verifies that a non-default instance_class in the blueprint
// YAML flows through to fixture.Cache.InstanceClass (SK-5, requirement b).
func TestCacheInstanceClassFlowsFromYAML(t *testing.T) {
	y := strings.Replace(minimalYAML,
		`{ engine: redis, version: "7.1", name: mini-sessions }`,
		`{ engine: redis, version: "7.1", name: mini-sessions, instance_class: cache.t3.micro }`,
		1)
	r := load(t, y)
	cache := findCacheFixture(t, r, "mini-sessions")
	if cache.InstanceClass != "cache.t3.micro" {
		t.Errorf("Cache.InstanceClass = %q, want %q (from YAML)", cache.InstanceClass, "cache.t3.micro")
	}
}

// TestResolveIncidentAxisWildcardExpands: a single-mode incident with an axis-wildcard target
// expands into one schedule entry per matching instance.
func TestResolveIncidentAxisWildcardExpands(t *testing.T) {
	y := strings.Replace(minimalYAML,
		`  - { kind: latency_spike, target: mini-api, at: "2026-06-19T14:00", for: 20m, intensity: 0.8 }`,
		`  - { kind: connection_saturation, target: "database:*", at: "2026-06-19T14:00", for: 20m }`, 1)
	r, err := loadWith(t, scenarioRegistry(t), y)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// minimalYAML has one database (mini-db).
	want := "connection_saturation@2026-06-19T14:00/20m@mini-db"
	if len(r.Incidents) != 1 || r.Incidents[0] != want {
		t.Fatalf("axis-wildcard incident expansion = %v, want [%q]", r.Incidents, want)
	}
}

// svcViaYAML adds a second web_service ("mini-pay") on the same cluster and rewires mini-api's
// calls to: service→mini-pay, then db→mini-db nested via mini-pay. Built off minimalYAML so all
// other wiring (cluster/db/cache/registry) is already valid.
func svcViaYAML() string {
	y := strings.Replace(minimalYAML,
		"workloads:\n  - type: web_service\n    name: mini-api",
		"workloads:\n  - type: web_service\n    name: mini-pay\n    runs_on: mini-prod-use1\n  - type: web_service\n    name: mini-api", 1)
	return strings.Replace(y,
		"calls: [{ db: mini-db }, { cache: mini-sessions }]",
		"calls: [{ service: mini-pay }, { db: mini-db, via: mini-pay }]", 1)
}

func TestResolve_ServiceAndViaWiring(t *testing.T) {
	r := load(t, svcViaYAML())
	var api *WorkloadInstance
	for i := range r.Workloads {
		if r.Workloads[i].Name == "mini-api" {
			api = &r.Workloads[i]
		}
	}
	if api == nil {
		t.Fatal("mini-api workload not resolved")
	}
	if len(api.Calls) != 2 {
		t.Fatalf("len(Calls)=%d, want 2", len(api.Calls))
	}
	if api.Calls[0].Kind != "service" || api.Calls[0].Service != "mini-pay" || api.Calls[0].ParentHop != -1 {
		t.Fatalf("call[0]=%+v, want service/mini-pay/parent=-1", api.Calls[0])
	}
	if api.Calls[1].Kind != "db" || api.Calls[1].ParentHop != 0 {
		t.Fatalf("call[1]=%+v, want db nested under hop 0", api.Calls[1])
	}
}

func TestResolve_ServiceUnknownWorkloadRejected(t *testing.T) {
	bp := strings.Replace(svcViaYAML(), "{ service: mini-pay }", "{ service: nope }", 1)
	if err := loadErr(t, bp); !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected unknown-service error mentioning 'nope', got %v", err)
	}
}

func TestResolve_ViaForwardReferenceRejected(t *testing.T) {
	// via points at a target that has not appeared yet in the list (db before the service it nests under).
	bp := strings.Replace(svcViaYAML(),
		"calls: [{ service: mini-pay }, { db: mini-db, via: mini-pay }]",
		"calls: [{ db: mini-db, via: mini-pay }, { service: mini-pay }]", 1)
	if err := loadErr(t, bp); !strings.Contains(err.Error(), "via") {
		t.Fatalf("expected via forward-reference error, got %v", err)
	}
}

// ── Task H: DocDB/Neptune engine discriminator + AOSS/MWAA/Glue cloud-pass ──────────────────────

// docdbYAML is a minimal blueprint with one DocDB database (engine: docdb, cloudwatch: true).
// Used by the engine-discriminator tests — the rest of the declaration is the same as minimalYAML
// but with no dbo11y (docdb does not support it).
const docdbYAML = `
name: mini
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0mini01, nat_gateways: 2 }
    cluster:
      type: eks
      name: mini-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [core_dns]
    databases:
      - { engine: docdb, name: mini-docdb, observability: { cloudwatch: true, dbo11y: false } }
    caches:
      - { engine: redis, version: "7.1", name: mini-sessions }
workloads:
  - type: web_service
    name: mini-api
    runs_on: mini-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    calls: [{ cache: mini-sessions }]
    endpoints: [{ route: "GET /v1/ping", error_rate: 0.01, p95_ms: 80 }]
features:
  synthetic_monitoring: { enabled: true, checks: [mini-api-health] }
incidents:
  - { kind: latency_spike, target: mini-api, at: "2026-06-19T14:00", for: 20m, intensity: 0.8 }
`

// neptuneYAML is identical to docdbYAML but uses engine: neptune.
const neptuneYAML = `
name: mini
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0mini01, nat_gateways: 2 }
    cluster:
      type: eks
      name: mini-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [core_dns]
    databases:
      - { engine: neptune, name: mini-neptune, observability: { cloudwatch: true, dbo11y: false } }
    caches:
      - { engine: redis, version: "7.1", name: mini-sessions }
workloads:
  - type: web_service
    name: mini-api
    runs_on: mini-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    calls: [{ cache: mini-sessions }]
    endpoints: [{ route: "GET /v1/ping", error_rate: 0.01, p95_ms: 80 }]
features:
  synthetic_monitoring: { enabled: true, checks: [mini-api-health] }
incidents:
  - { kind: latency_spike, target: mini-api, at: "2026-06-19T14:00", for: 20m, intensity: 0.8 }
`

// cloudServicesYAML has cloud.aoss + cloud.mwaa + cloud.glue blocks (no cluster; cloud-services
// only). The testRegistry for these must register aoss/mwaa/glue kinds.
const cloudServicesYAML = `
name: mini
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-0mini01
      aoss:
        collections: [my-collection]
      mwaa:
        environments: [my-env]
      glue:
        jobs: [my-etl-job]
workloads: []
`

// Identity-bearing test configs for the cloud-service kinds — at package scope so resolver
// tests can read back the decoded values (proves the cloud.<service> yaml.Node → strictDecode
// → construct Config identity seam, C2).
type aossCfg struct {
	Collections []string `yaml:"collections"`
}
type mwaaCfg struct {
	Environments []string `yaml:"environments"`
}
type glueCfg struct {
	Jobs []string `yaml:"jobs"`
}

// taskHRegistry builds a registry for H-tests. It uses testRegistry (which now covers all
// TopologyKinds including docdb/neptune/aoss/mwaa/glue) but with a corrected config for
// the identity-bearing cloud-service kinds (AOSS/MWAA/Glue) that need to decode their
// YAML fields. Since testRegistry registers those with &struct{}{} (strict-decode would
// reject unknown fields like `collections:`), we build a fresh parallel registry for H-tests.
//
// Strategy: clone the minimum needed for cloud-services YAML tests (cw_infra + the 5 new
// kinds + addon stubs + web_service workload) without double-registering anything.
func taskHRegistry(t *testing.T) *core.Registry {
	t.Helper()
	// testRegistry already covers TopologyKinds() — after our changes that now includes
	// docdb/neptune/aoss/mwaa/glue. However, it registers them with &struct{}{} configs,
	// which breaks strict-decode of cloud-service YAML blocks. We therefore build a
	// SEPARATE registry that overrides those three with the correct identity-bearing structs.
	//
	// We cannot simply mutate the result of testRegistry() (RegisterConstruct panics on
	// duplicates). Instead, build a fresh registry that parallels testRegistry but uses
	// the correct config types for the identity-bearing cloud-service constructs.
	reg := core.NewRegistry()
	// Topology kinds with empty configs (fixture-driven): all existing + docdb + neptune.
	for _, k := range []string{
		KindK8sCluster, KindEC2, KindRDS, KindElastiCache,
		KindDbo11yMySQL, KindDbo11yPostgres,
		KindDocDB, KindNeptune,
	} {
		kk := k // capture loop var
		var modes []failuremode.Mode
		if kk == KindDbo11yPostgres {
			modes = []failuremode.Mode{
				{Name: "connection_saturation", Axis: failuremode.AxisDatabase},
				{Name: "replication_lag", Axis: failuremode.AxisDatabase},
				{Name: "lock_contention", Axis: failuremode.AxisDatabase},
				{Name: "slow_query_storm", Axis: failuremode.AxisDatabase},
			}
		}
		reg.RegisterConstruct(core.ConstructReg{
			Kind: kk, Doc: "test", Scope: core.ScopeSubstrate,
			NewConfig:    func() any { return &struct{}{} },
			Build:        func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
			FailureModes: modes,
		})
	}
	// cw_infra: config-decoded (cloud.cloudwatch).
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
	// Cloud-service constructs with identity-bearing configs (types at package scope so the
	// resolver tests can read back the decoded values — proving the C2 seam).
	reg.RegisterConstruct(core.ConstructReg{
		Kind: KindAOSS, Doc: "test", Scope: core.ScopeBlueprint,
		NewConfig: func() any { return &aossCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	reg.RegisterConstruct(core.ConstructReg{
		Kind: KindMWAA, Doc: "test", Scope: core.ScopeBlueprint,
		NewConfig: func() any { return &mwaaCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	reg.RegisterConstruct(core.ConstructReg{
		Kind: KindGlue, Doc: "test", Scope: core.ScopeBlueprint,
		NewConfig: func() any { return &glueCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	// Addon stubs (needed for blueprint YAML that declares addons like core_dns).
	type caCfg struct {
		MinNodes int `yaml:"min_nodes"`
		MaxNodes int `yaml:"max_nodes"`
	}
	for _, k := range []string{"load_balancer_controller", "core_dns", "vpc_cni", "cert_manager", "ebs_csi", "external_dns", "ksm_ingress"} {
		kk := k
		reg.RegisterConstruct(core.ConstructReg{
			Kind: kk, Doc: "addon", Scope: core.ScopeSubstrate,
			NewConfig: func() any { return &struct{}{} },
			Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
		})
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "cluster_autoscaler", Doc: "addon", Scope: core.ScopeSubstrate,
		NewConfig: func() any { return &caCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	// Feature/integration stubs used by the test YAMLs.
	type smCfg struct {
		Checks []string `yaml:"checks"`
	}
	reg.RegisterConstruct(core.ConstructReg{
		Kind: "synthetic_monitoring", Doc: "feature", Scope: core.ScopeBlueprint,
		Group:     core.GroupFeature,
		NewConfig: func() any { return &smCfg{} },
		Build:     func(cfg any, fx *fixture.Set) (core.Construct, error) { return nil, nil },
	})
	// web_service workload (needed for blueprint with workloads).
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

// TestResolveDocDBEngineDiscriminator: engine:docdb with cloudwatch:true resolves Kind "docdb"
// (not "rds"). The engine discriminator in the DB CW pass is the change under test.
func TestResolveDocDBEngineDiscriminator(t *testing.T) {
	r, err := loadWith(t, taskHRegistry(t), docdbYAML)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	kinds := constructKinds(r)
	if kinds[KindDocDB] != 1 {
		t.Errorf("want 1 docdb construct, got %d (all: %v)", kinds[KindDocDB], kinds)
	}
	if kinds[KindRDS] != 0 {
		t.Errorf("docdb database must NOT emit rds construct, got %d rds instances", kinds[KindRDS])
	}
}

// TestResolveNeptuneEngineDiscriminator: engine:neptune with cloudwatch:true resolves Kind "neptune".
func TestResolveNeptuneEngineDiscriminator(t *testing.T) {
	r, err := loadWith(t, taskHRegistry(t), neptuneYAML)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	kinds := constructKinds(r)
	if kinds[KindNeptune] != 1 {
		t.Errorf("want 1 neptune construct, got %d (all: %v)", kinds[KindNeptune], kinds)
	}
	if kinds[KindRDS] != 0 {
		t.Errorf("neptune database must NOT emit rds construct, got %d rds instances", kinds[KindRDS])
	}
}

// TestResolveDocDBDbo11yRejected: engine:docdb with dbo11y:true is rejected at load (I2 guard).
func TestResolveDocDBDbo11yRejected(t *testing.T) {
	y := strings.Replace(docdbYAML, "dbo11y: false", "dbo11y: true", 1)
	_, err := loadWith(t, taskHRegistry(t), y)
	if err == nil {
		t.Fatal("dbo11y:true on docdb engine should be rejected at load")
	}
	if !strings.Contains(err.Error(), "docdb") || !strings.Contains(err.Error(), "dbo11y") {
		t.Fatalf("error should name the engine and dbo11y: %v", err)
	}
}

// TestResolveNeptuneDbo11yRejected: engine:neptune with dbo11y:true is rejected at load (I2 guard).
func TestResolveNeptuneDbo11yRejected(t *testing.T) {
	y := strings.Replace(neptuneYAML, "dbo11y: false", "dbo11y: true", 1)
	_, err := loadWith(t, taskHRegistry(t), y)
	if err == nil {
		t.Fatal("dbo11y:true on neptune engine should be rejected at load")
	}
	if !strings.Contains(err.Error(), "neptune") || !strings.Contains(err.Error(), "dbo11y") {
		t.Fatalf("error should name the engine and dbo11y: %v", err)
	}
}

// TestResolveCloudAOSSBlock: cloud.aoss: block resolves to a KindAOSS construct instance
// with the decoded Collections config.
func TestResolveCloudAOSSBlock(t *testing.T) {
	r, err := loadWith(t, taskHRegistry(t), cloudServicesYAML)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	kinds := constructKinds(r)
	if kinds[KindAOSS] != 1 {
		t.Errorf("want 1 aoss construct, got %d (all: %v)", kinds[KindAOSS], kinds)
	}
	// The cloud.aoss block's identity must thread through yaml.Node → strictDecode → Config (C2).
	var aoss *aossCfg
	for i := range r.Constructs {
		if r.Constructs[i].Kind == KindAOSS {
			aoss, _ = r.Constructs[i].Config.(*aossCfg)
		}
	}
	if aoss == nil || len(aoss.Collections) != 1 || aoss.Collections[0] != "my-collection" {
		t.Fatalf("aoss Config.Collections not decoded from cloud.aoss block: %+v", aoss)
	}
}

// TestResolveCloudMWAABlock: cloud.mwaa: block resolves to a KindMWAA construct instance.
func TestResolveCloudMWAABlock(t *testing.T) {
	r, err := loadWith(t, taskHRegistry(t), cloudServicesYAML)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	kinds := constructKinds(r)
	if kinds[KindMWAA] != 1 {
		t.Errorf("want 1 mwaa construct, got %d (all: %v)", kinds[KindMWAA], kinds)
	}
}

// TestResolveCloudGlueBlock: cloud.glue: block resolves to a KindGlue construct instance.
func TestResolveCloudGlueBlock(t *testing.T) {
	r, err := loadWith(t, taskHRegistry(t), cloudServicesYAML)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	kinds := constructKinds(r)
	if kinds[KindGlue] != 1 {
		t.Errorf("want 1 glue construct, got %d (all: %v)", kinds[KindGlue], kinds)
	}
}

// TestTopologyKindsIncludesNewKinds: TopologyKinds() includes all 5 new construct kinds.
func TestTopologyKindsIncludesNewKinds(t *testing.T) {
	want := map[string]bool{
		KindDocDB:   true,
		KindNeptune: true,
		KindAOSS:    true,
		KindMWAA:    true,
		KindGlue:    true,
	}
	got := map[string]bool{}
	for _, k := range TopologyKinds() {
		got[k] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("TopologyKinds() missing %q (got: %v)", k, TopologyKinds())
		}
	}
}

// TestTopologyKindsIncludesFleetManagement: TopologyKinds() includes fleet_management (A6).
func TestTopologyKindsIncludesFleetManagement(t *testing.T) {
	got := map[string]bool{}
	for _, k := range TopologyKinds() {
		got[k] = true
	}
	if !got[KindFleetManagement] {
		t.Errorf("TopologyKinds() missing %q (got: %v)", KindFleetManagement, TopologyKinds())
	}
}

// TestResolveK8sMonitoringFeatures: the new Features/MetricsReplicas/ReceiverAsDaemonset/
// FleetManagement fields on K8sMonitoringDecl are threaded into the cluster fixture (Task A1).
func TestResolveK8sMonitoringFeatures(t *testing.T) {
	y := strings.Replace(minimalYAML,
		"node_groups: [{ name: general, instance_type: m6i.xlarge }]",
		"node_groups: [{ name: general, instance_type: m6i.xlarge, desired: 3 }]\n      k8s_monitoring:\n        enabled: true\n        alloy: true\n        alloy_version: \"1.10.0\"\n        features:\n          pod_logs: true\n          profiling: false\n          cluster_metrics: true\n        metrics_replicas: 2",
		1)
	r := load(t, y)
	cl := findCluster(t, r, "mini-prod-use1")
	km := cl.K8sMonitoring
	if !km.Features["pod_logs"] {
		t.Errorf("features.pod_logs not threaded: %+v", km.Features)
	}
	if km.Features["profiling"] {
		t.Errorf("features.profiling should be false: %+v", km.Features)
	}
	if !km.Features["cluster_metrics"] {
		t.Errorf("features.cluster_metrics not threaded: %+v", km.Features)
	}
	if km.MetricsReplicas != 2 {
		t.Errorf("MetricsReplicas = %d, want 2", km.MetricsReplicas)
	}
}

// TestResolveEmitsClusterFleetMirror: a cluster with k8s_monitoring.fleet_management:true
// causes the resolver to emit a fleet_management construct instance with the cluster fixture
// attached (Task A6).
func TestResolveEmitsClusterFleetMirror(t *testing.T) {
	y := strings.Replace(minimalYAML,
		"node_groups: [{ name: general, instance_type: m6i.xlarge }]",
		"node_groups: [{ name: general, instance_type: m6i.xlarge }]\n      k8s_monitoring:\n        enabled: true\n        alloy: true\n        fleet_management: true",
		1)
	r := load(t, y)

	var fmInstances []ConstructInstance
	for _, ci := range r.Constructs {
		if ci.Kind == KindFleetManagement {
			fmInstances = append(fmInstances, ci)
		}
	}
	if len(fmInstances) != 1 {
		t.Fatalf("want 1 fleet_management construct instance, got %d (all kinds: %v)", len(fmInstances), constructKinds(r))
	}
	fm := fmInstances[0]
	if fm.Name != "mini-prod-use1/fleet" {
		t.Errorf("fleet_management instance name = %q, want %q", fm.Name, "mini-prod-use1/fleet")
	}
	if fm.Fixtures == nil || fm.Fixtures.Cluster == nil {
		t.Fatalf("fleet_management instance must carry a cluster fixture (got Fixtures=%+v)", fm.Fixtures)
	}
	if fm.Fixtures.Cluster.Name != "mini-prod-use1" {
		t.Errorf("fleet_management fixture cluster name = %q, want %q", fm.Fixtures.Cluster.Name, "mini-prod-use1")
	}
	if !fm.Fixtures.Cluster.K8sMonitoring.FleetManagement {
		t.Errorf("fleet_management fixture cluster.K8sMonitoring.FleetManagement should be true")
	}
}

// TestSubstrateWorkloadsPopulated verifies that after resolution, cl.SubstrateWorkloads
// contains the baseline workloads (coredns, metrics-server) plus addon-derived workloads
// for every declared addon key that AddonWorkloads covers. It also asserts:
//   - each substrate workload has non-empty PodNames
//   - cl.Workloads does NOT contain any of the substrate workload names
//   - node count is unaffected by substrate workloads (addons do NOT inflate node demand)
func TestSubstrateWorkloadsPopulated(t *testing.T) {
	y := `
name: swtest
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0sw01, nat_gateways: 0 }
    cluster:
      type: eks
      name: sw-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [cert_manager, karpenter]
workloads:
  - type: web_service
    name: sw-api
    runs_on: sw-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints: [{ route: "GET /ping", error_rate: 0.01, p95_ms: 80 }]
`
	r := load(t, y)
	cl := findCluster(t, r, "sw-prod-use1")

	// Build an index of substrate workloads by (namespace, name).
	swByKey := map[string]fixture.Workload{}
	for _, w := range cl.SubstrateWorkloads {
		swByKey[w.Namespace+"/"+w.Name] = w
	}

	// Must contain the baseline workloads.
	for _, key := range []string{"kube-system/coredns", "kube-system/metrics-server"} {
		if _, ok := swByKey[key]; !ok {
			t.Errorf("SubstrateWorkloads missing baseline %q; got keys: %v", key, substrateKeys(cl))
		}
	}

	// Must contain cert-manager addon workloads (3 deploys).
	certKeys := []string{
		"cert-manager/cert-manager",
		"cert-manager/cert-manager-webhook",
		"cert-manager/cert-manager-cainjector",
	}
	for _, key := range certKeys {
		if _, ok := swByKey[key]; !ok {
			t.Errorf("SubstrateWorkloads missing cert_manager workload %q; got keys: %v", key, substrateKeys(cl))
		}
	}

	// Must contain karpenter workload.
	if _, ok := swByKey["kube-system/karpenter"]; !ok {
		t.Errorf("SubstrateWorkloads missing karpenter workload; got keys: %v", substrateKeys(cl))
	}

	// Check specific shapes for cert-manager (replicas=2) and karpenter (container="controller").
	if cm, ok := swByKey["cert-manager/cert-manager"]; ok {
		if cm.Replicas != 2 {
			t.Errorf("cert-manager replicas = %d, want 2", cm.Replicas)
		}
	}
	if kp, ok := swByKey["kube-system/karpenter"]; ok {
		if kp.Container != "controller" {
			t.Errorf("karpenter Container = %q, want %q", kp.Container, "controller")
		}
	}

	// Every substrate workload must have non-empty PodNames.
	for _, w := range cl.SubstrateWorkloads {
		if len(w.PodNames) == 0 {
			t.Errorf("SubstrateWorkload %q/%q has empty PodNames", w.Namespace, w.Name)
		}
	}

	// cl.Workloads must NOT contain any substrate workload name.
	wlNames := map[string]bool{}
	for _, w := range cl.Workloads {
		wlNames[w.Name] = true
	}
	for _, sw := range cl.SubstrateWorkloads {
		if wlNames[sw.Name] {
			t.Errorf("substrate workload %q must not appear in cl.Workloads", sw.Name)
		}
	}

	// Node count must equal what it would be without addons (only app workload replicas count).
	// sw-api has replicas=2. prod env has weight=1.0 → floor=6. ceil(2/8)=1 < 6 → 6 nodes.
	if len(cl.Nodes) != 6 {
		t.Errorf("node count = %d, want 6 (substrate workloads must NOT inflate node demand)", len(cl.Nodes))
	}
}

// substrateKeys returns a sorted slice of "namespace/name" strings from cl.SubstrateWorkloads
// for use in test failure messages.
func substrateKeys(cl *fixture.Cluster) []string {
	var keys []string
	for _, w := range cl.SubstrateWorkloads {
		keys = append(keys, w.Namespace+"/"+w.Name)
	}
	return keys
}

// TestSubstrateWorkloadsClusterDistinctPodNames verifies that the SAME addon on two clusters
// gets DISTINCT pod-name hashes (real EKS pod names are cluster-unique). The Pass-2c mint uses
// a cluster-scoped seed (seed:clusterName), so cert-manager-<hash> differs per cluster.
func TestSubstrateWorkloadsClusterDistinctPodNames(t *testing.T) {
	y := `
name: swdistinct
environments:
  - name: a
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0a, nat_gateways: 0 }
    cluster: { type: eks, name: clus-a, node_groups: [{ name: general, instance_type: m6i.xlarge }], addons: [cert_manager] }
  - name: b
    cloud: { provider: aws, account_id: "111122223333", region: us-east-2, vpc_id: vpc-0b, nat_gateways: 0 }
    cluster: { type: eks, name: clus-b, node_groups: [{ name: general, instance_type: m6i.xlarge }], addons: [cert_manager] }
workloads:
  - type: web_service
    name: sw-api
    for_each_env: true
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints: [{ route: "GET /ping", error_rate: 0.01, p95_ms: 80 }]
`
	r := load(t, y)
	podName := func(clusterName string) string {
		cl := findCluster(t, r, clusterName)
		for _, w := range cl.SubstrateWorkloads {
			if w.Name == "cert-manager" {
				if len(w.PodNames) == 0 {
					t.Fatalf("%s: cert-manager has no PodNames", clusterName)
				}
				return w.PodNames[0]
			}
		}
		t.Fatalf("%s: no cert-manager substrate workload", clusterName)
		return ""
	}
	a, b := podName("clus-a"), podName("clus-b")
	if a == b {
		t.Errorf("cert-manager pod names must differ across clusters, both = %q", a)
	}
}

// TestSubstrateDeploymentsClusterDistinctPodNames verifies the three migrated substrate
// Deployments (ebs-csi-controller via the ebs_csi addon, alloy-metrics/alloy-logs via
// k8s_monitoring) get DISTINCT cluster-scoped pod names across two clusters — they used to be
// name-only adds in helpers.go that fell back to the cluster-blind synthPodName hash.
func TestSubstrateDeploymentsClusterDistinctPodNames(t *testing.T) {
	y := `
name: subdistinct
environments:
  - name: a
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0a, nat_gateways: 0 }
    cluster:
      type: eks
      name: clus-a
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [ebs_csi]
      k8s_monitoring: { enabled: true, features: { cluster_metrics: true, pod_logs: true } }
  - name: b
    cloud: { provider: aws, account_id: "111122223333", region: us-east-2, vpc_id: vpc-0b, nat_gateways: 0 }
    cluster:
      type: eks
      name: clus-b
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [ebs_csi]
      k8s_monitoring: { enabled: true, features: { cluster_metrics: true, pod_logs: true } }
workloads:
  - type: web_service
    name: sub-api
    for_each_env: true
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints: [{ route: "GET /ping", error_rate: 0.01, p95_ms: 80 }]
`
	r := load(t, y)
	firstPod := func(clusterName, wl string) string {
		cl := findCluster(t, r, clusterName)
		for _, w := range cl.SubstrateWorkloads {
			if w.Name == wl {
				if len(w.PodNames) == 0 {
					t.Fatalf("%s: %s has no PodNames", clusterName, wl)
				}
				return w.PodNames[0]
			}
		}
		t.Fatalf("%s: no %s substrate workload", clusterName, wl)
		return ""
	}
	// Deployment (ebs-csi-controller) + DaemonSet (alloy-logs) pod NAMES are cluster-distinct
	// (ReplicaSet-hash / node-hash forms folded with the cluster-scoped seed). alloy-metrics is a
	// StatefulSet — its pod names are ordinal-stable (alloy-metrics-0) and therefore IDENTICAL
	// across clusters by design (real k8s); cluster-uniqueness for it rides the pod UID instead.
	for _, wl := range []string{"ebs-csi-controller", "alloy-logs"} {
		a, b := firstPod("clus-a", wl), firstPod("clus-b", wl)
		if a == b {
			t.Errorf("%s pod names must differ across clusters, both = %q", wl, a)
		}
	}
	if a, b := firstPod("clus-a", "alloy-metrics"), firstPod("clus-b", "alloy-metrics"); a != b {
		t.Errorf("alloy-metrics StatefulSet pod names should be ordinal-stable across clusters, got %q vs %q", a, b)
	}
}

// TestPromotedSubstrateWorkloadsShape asserts the three promoted workloads carry the real
// controller/replicas/container/namespace captured from a live reference cluster, and that they are
// ABSENT from cl.Workloads (they must stay out of node-count summers).
func TestPromotedSubstrateWorkloadsShape(t *testing.T) {
	y := `
name: subshape
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0ss, nat_gateways: 0 }
    cluster:
      type: eks
      name: ss-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge, desired: 3 }]
      addons: [ebs_csi]
      k8s_monitoring: { enabled: true, features: { cluster_metrics: true, pod_logs: true } }
workloads:
  - type: web_service
    name: ss-api
    runs_on: ss-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints: [{ route: "GET /ping", error_rate: 0.01, p95_ms: 80 }]
`
	r := load(t, y)
	cl := findCluster(t, r, "ss-prod-use1")
	sub := map[string]fixture.Workload{}
	for _, w := range cl.SubstrateWorkloads {
		sub[w.Name] = w
	}
	type want struct {
		ns, controller, container string
		replicas                  int
	}
	cases := map[string]want{
		"ebs-csi-controller": {ns: "kube-system", controller: "", container: "ebs-plugin", replicas: 2},
		"alloy-metrics":      {ns: "monitoring", controller: "statefulset", container: "alloy", replicas: 1},
		"alloy-logs":         {ns: "monitoring", controller: "daemonset", container: "alloy"},
	}
	for name, w := range cases {
		got, ok := sub[name]
		if !ok {
			t.Errorf("%s missing from SubstrateWorkloads", name)
			continue
		}
		if got.Namespace != w.ns {
			t.Errorf("%s namespace = %q, want %q", name, got.Namespace, w.ns)
		}
		if got.Controller != w.controller {
			t.Errorf("%s controller = %q, want %q", name, got.Controller, w.controller)
		}
		if got.Container != w.container {
			t.Errorf("%s container = %q, want %q", name, got.Container, w.container)
		}
		if w.replicas > 0 && got.Replicas != w.replicas {
			t.Errorf("%s replicas = %d, want %d", name, got.Replicas, w.replicas)
		}
	}
	// alloy-logs is a DaemonSet: one pod per node (3-node cluster ⇒ 3 distinct node-suffixed pods).
	if dl := sub["alloy-logs"]; len(dl.PodNames) != len(cl.Nodes) {
		t.Errorf("alloy-logs PodNames = %d, want one per node (%d)", len(dl.PodNames), len(cl.Nodes))
	}
	// None of the three may appear in cl.Workloads (they must not inflate node demand).
	for _, w := range cl.Workloads {
		switch w.Name {
		case "ebs-csi-controller", "alloy-metrics", "alloy-logs":
			t.Errorf("%s leaked into cl.Workloads (must stay substrate-only)", w.Name)
		}
	}
}

// TestAlloyAbsentWithoutK8sMonitoring verifies alloy-metrics/alloy-logs are NOT added to a
// cluster that does not enable k8s_monitoring (preserving the prior presence condition).
func TestAlloyAbsentWithoutK8sMonitoring(t *testing.T) {
	y := `
name: noalloy
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0na, nat_gateways: 0 }
    cluster:
      type: eks
      name: na-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [ebs_csi]
workloads:
  - type: web_service
    name: na-api
    runs_on: na-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints: [{ route: "GET /ping", error_rate: 0.01, p95_ms: 80 }]
`
	r := load(t, y)
	cl := findCluster(t, r, "na-prod-use1")
	for _, w := range cl.SubstrateWorkloads {
		if w.Name == "alloy-metrics" || w.Name == "alloy-logs" {
			t.Errorf("%s present without k8s_monitoring enabled", w.Name)
		}
	}
	// ebs-csi-controller IS present (its addon is declared).
	found := false
	for _, w := range cl.SubstrateWorkloads {
		if w.Name == "ebs-csi-controller" {
			found = true
		}
	}
	if !found {
		t.Error("ebs-csi-controller missing despite ebs_csi addon declared")
	}
}

// TestSubstrateWorkloadsDeduplicatesBaseline verifies that when metrics_server is declared
// explicitly as an addon AND is in the baseline, it appears only once in SubstrateWorkloads.
func TestSubstrateWorkloadsDeduplicatesBaseline(t *testing.T) {
	y := `
name: swdedup
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: us-east-1, vpc_id: vpc-0sd01, nat_gateways: 0 }
    cluster:
      type: eks
      name: sd-prod-use1
      node_groups: [{ name: general, instance_type: m6i.xlarge }]
      addons: [metrics_server]
workloads:
  - type: web_service
    name: sd-api
    runs_on: sd-prod-use1
    replicas: 2
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints: [{ route: "GET /ping", error_rate: 0.01, p95_ms: 80 }]
`
	r := load(t, y)
	cl := findCluster(t, r, "sd-prod-use1")

	count := 0
	for _, w := range cl.SubstrateWorkloads {
		if w.Namespace == "kube-system" && w.Name == "metrics-server" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("metrics-server appears %d times in SubstrateWorkloads, want exactly 1 (dedup)", count)
	}
}

// TestResolveFleetMirrorRequiresEnabled: k8s_monitoring.fleet_management:true without
// k8s_monitoring.enabled:true is a load-time error (Task A6).
func TestResolveFleetMirrorRequiresEnabled(t *testing.T) {
	// fleet_management:true with enabled:false (omitted = false)
	y := strings.Replace(minimalYAML,
		"node_groups: [{ name: general, instance_type: m6i.xlarge }]",
		"node_groups: [{ name: general, instance_type: m6i.xlarge }]\n      k8s_monitoring:\n        enabled: false\n        fleet_management: true",
		1)
	_, err := loadWith(t, testRegistry(t), y)
	if err == nil {
		t.Fatal("fleet_management:true without enabled:true should be rejected at load")
	}
	if !strings.Contains(err.Error(), "fleet_management") || !strings.Contains(err.Error(), "enabled") {
		t.Errorf("error should mention fleet_management and enabled: %v", err)
	}
}

// TestResolveHostsPass: a top-level hosts: list resolves to one host ConstructInstance
// per declared host, with fixture.Set.Host populated and defaults/unit-conversion applied.
func TestResolveHostsPass(t *testing.T) {
	y := `
name: hf
hosts:
  - { name: camden, os: linux, cpus: 4, memory_gb: 16, metrics_profile: integration }
  - { name: winbox, os: windows }
`
	r := load(t, y)
	var hosts []*fixture.Host
	for _, ci := range r.Constructs {
		if ci.Kind == KindHost {
			if ci.Fixtures == nil || ci.Fixtures.Host == nil {
				t.Fatalf("host construct %q missing fixture.Set.Host", ci.Name)
			}
			hosts = append(hosts, ci.Fixtures.Host)
		}
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 host ConstructInstances, got %d", len(hosts))
	}
	byName := map[string]*fixture.Host{}
	for _, h := range hosts {
		byName[h.Hostname] = h
	}
	camden := byName["camden"]
	if camden == nil || camden.OS != "linux" || camden.NumCPU != 4 || camden.MemTotal != float64(16<<30) || camden.Profile != "integration" {
		t.Fatalf("camden mapped wrong: %+v", camden)
	}
	if !camden.Logs {
		t.Fatalf("camden Logs should default true: %+v", camden)
	}
	if camden.Docker {
		t.Fatalf("camden Docker should default false: %+v", camden)
	}
	win := byName["winbox"]
	// defaults: NumCPU 2, MemTotal 8 GiB, Profile integration
	if win == nil || win.OS != "windows" || win.NumCPU != 2 || win.MemTotal != float64(8<<30) || win.Profile != "integration" {
		t.Fatalf("winbox defaults wrong: %+v", win)
	}
}

// TestResolveHostsDuplicateName: two hosts with the same name in one blueprint → load error.
func TestResolveHostsDuplicateName(t *testing.T) {
	y := `
name: hf
hosts:
  - { name: camden, os: linux }
  - { name: camden, os: linux }
`
	err := loadErr(t, y)
	if !strings.Contains(err.Error(), "camden") {
		t.Fatalf("duplicate hostname not rejected with name in error: %v", err)
	}
}

// TestResolveHostsMacosNormalisedAndDockerRejected: os macos → fixture OS darwin;
// macos + observability.docker:true → load error (no macOS cadvisor lane in v1).
func TestResolveHostsMacosNormalised(t *testing.T) {
	y := `
name: hf
hosts:
  - { name: alex, os: macos }
`
	r := load(t, y)
	var found bool
	for _, ci := range r.Constructs {
		if ci.Kind == KindHost && ci.Fixtures.Host.Hostname == "alex" {
			found = true
			if ci.Fixtures.Host.OS != "darwin" {
				t.Fatalf("macos should normalise to darwin in fixture, got %q", ci.Fixtures.Host.OS)
			}
		}
	}
	if !found {
		t.Fatal("alex host not resolved")
	}
}

func TestResolveHostsMacosDockerRejected(t *testing.T) {
	y := `
name: hf
hosts:
  - { name: alex, os: macos, observability: { docker: true } }
`
	err := loadErr(t, y)
	if !strings.Contains(err.Error(), "docker") || !strings.Contains(err.Error(), "alex") {
		t.Fatalf("macos+docker should be rejected mentioning docker and host: %v", err)
	}
}
