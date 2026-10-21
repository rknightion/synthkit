// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	p "github.com/rknightion/synthkit/internal/agento11yprovision"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
)

// All ordinary constructors are process-effect tripwires during declaration
// discovery. Config decoding/defaults and callbacks are the real registrations.
func declarationRegistry() *core.Registry {
	source := Catalog()
	reg := core.NewRegistry()
	for _, kind := range source.ConstructKinds() {
		cr, _ := source.Construct(kind)
		cr.Build = func(any, *fixture.Set) (core.Construct, error) { panic("discovery built construct") }
		reg.RegisterConstruct(cr)
	}
	for _, kind := range source.WorkloadKinds() {
		wr, _ := source.Workload(kind)
		wr.Build = func(any, core.Binding) (core.Workload, error) { panic("discovery built workload") }
		reg.RegisterWorkload(wr)
	}
	return reg
}

type declarationNoHTTP struct{}

func (declarationNoHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	panic("declaration discovery performed HTTP")
}

const declarationYAML = `name: roster
environments:
 - name: dev
   cloud: {provider: aws, account_id: "111122223333", region: eu-west-1, vpc_id: vpc-dev}
   cluster: {type: eks, name: dev-cluster, node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]}
   databases: [{name: records, engine: postgres}]
 - name: prod
   cloud: {provider: aws, account_id: "111122223334", region: eu-west-1, vpc_id: vpc-prod}
   cluster: {type: eks, name: prod-cluster, node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]}
workloads:
 - type: ai_agent
   name: fleet
   for_each_env: true
   agents:
    - {name: legacy, archetype: coding_claude_code, provider: openai, models: [one, two], subagents: [peer], activity: {sessions_per_min: 0}}
    - {name: context, archetype: general_single_shot, provider: bedrock, models: []}
 - type: web_service
   name: api
   runs_on: dev-cluster
   rum: true
   calls: [{db: records}]
`

func TestRegistrationAgentDeclarationsFullSet(t *testing.T) {
	reg := declarationRegistry()
	res, err := blueprint.LoadNamespaced([]byte(declarationYAML), "local", reg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../e2e/fixtures/e2e-agent-pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// A nonexistent corpus must not be opened by the real app callback. No Build
	// would succeed with it, even if runtime collaborators were fabricated.
	raw = []byte(strings.ReplaceAll(string(raw), "regulated_document_authoring", "missing-structural-only-corpus"))
	app, err := blueprint.Load(raw, reg)
	if err != nil {
		t.Fatal(err)
	}
	set := []*blueprint.Resolved{res, app}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = declarationNoHTTP{}
	defer func() { http.DefaultTransport = oldTransport }()
	got, err := registrationAgentDeclarations(reg, set)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 9 {
		t.Fatalf("full set has %d claims, want 5 real and 4 inactive synthetic", len(got))
	}
	if set[0] != res || set[1] != app {
		t.Fatal("discovery reordered caller set")
	}
	for i, name := range []string{"planner", "retriever", "writer", "reviewer", "finalizer"} {
		d := got[i]
		if d.Origin != (p.Origin{Blueprint: "e2e-agent-pipeline", Instance: "pipeline-test", Declaration: uint32(i)}) || d.Declaration.Name != name || d.Declaration.ProducerMode != core.AgentProducerReal {
			t.Fatalf("real origin/declaration: %+v", d)
		}
	}
	if len(got[1].Declaration.Models) != 0 || got[1].Declaration.Service != (core.AgentServiceIdentity{Name: "source", Namespace: "test"}) {
		t.Fatalf("source-only claim lost: %+v", got[1])
	}
	for i, instance := range []string{"fleet-dev", "fleet-prod"} {
		for j, name := range []string{"legacy", "context"} {
			d := got[5+i*2+j]
			if d.Origin != (p.Origin{Blueprint: "local/roster", Instance: instance, Declaration: uint32(j)}) || d.Declaration.Name != name || d.Declaration.ProducerMode != core.AgentProducerSynthetic {
				t.Fatalf("inactive fanned context lost: %+v", d)
			}
			if j == 0 && (d.Declaration.Service.Name != "agento11y" || !reflect.DeepEqual(d.Declaration.ProjectedChildNames, []string{"legacy/peer"})) {
				t.Fatalf("effective child/default lost: %+v", d)
			}
		}
	}
	// The explicit web_service empty callback was invoked (otherwise nil coverage
	// would fail). Check the resolver's full binding independently of the helper.
	wi := res.Workloads[2]
	b := structuralWorkloadBinding(res, wi)
	if b.Name != "api" || b.Seed != "local/roster:api" || b.Replicas != 2 || b.Env != wi.Env || b.Cluster != wi.Cluster || b.Env.Name != "dev" || len(b.Calls) != 1 || b.Calls[0].DB.Name != "records" || len(b.Databases) != 1 || b.Databases[0].Name != "records" {
		t.Fatalf("structural binding differs from resolved/defaulted identity: %+v", b)
	}
	if b.RUM != nil || b.GenerationStages != nil || b.Ratings != nil {
		t.Fatal("runtime collaborator in discovery binding")
	}
}

func TestRegistrationAgentDeclarationsErrorsAndAliasing(t *testing.T) {
	failure := errors.New("callback failed")
	for _, which := range []string{"nil", "error", "missing", "borrowed"} {
		t.Run(which, func(t *testing.T) {
			reg := core.NewRegistry()
			borrowed := []core.AgentDeclaration{{Name: "a", Models: []core.AgentModelPair{{Model: "model"}}, ProjectedChildNames: []string{"child"}, HookPolicy: core.AgentHookPolicy{Phases: []string{"preflight"}}}}
			wr := core.WorkloadReg{Kind: "probe", NewConfig: func() any { return &struct{}{} }, Build: func(any, core.Binding) (core.Workload, error) { panic("build") }}
			if which == "error" {
				wr.AgentDeclarations = func(_ any, b core.Binding) ([]core.AgentDeclaration, error) {
					if b.Name == "first" {
						return borrowed, nil
					}
					return borrowed, failure
				}
			}
			if which == "borrowed" {
				wr.AgentDeclarations = func(_ any, b core.Binding) ([]core.AgentDeclaration, error) {
					if b.Name != "probe" || b.Seed != "bp:probe" || b.Replicas != 3 || b.RUM != nil || b.GenerationStages != nil || b.Ratings != nil {
						t.Fatalf("bad callback binding %+v", b)
					}
					return borrowed, nil
				}
			}
			if which != "missing" {
				reg.RegisterWorkload(wr)
			}
			bp := &blueprint.Resolved{Name: "bp", Workloads: []blueprint.WorkloadInstance{{Name: "probe", Kind: "probe", Config: &struct{}{}, Replicas: 3}}}
			if which == "error" {
				bp.Workloads = append([]blueprint.WorkloadInstance{{Name: "first", Kind: "probe", Config: &struct{}{}}}, bp.Workloads...)
			}
			got, err := registrationAgentDeclarations(reg, []*blueprint.Resolved{bp})
			if which != "borrowed" {
				if err == nil || got != nil {
					t.Fatal("missing/error callback returned usable partial claims")
				}
				if which == "error" && !errors.Is(err, failure) {
					t.Fatal("callback error lost")
				}
				return
			}
			if err != nil || len(got) != 1 {
				t.Fatalf("borrowed callback discovery: %v %v", got, err)
			}
			got[0].Declaration.Models[0].Model = "mutation"
			got[0].Declaration.ProjectedChildNames[0] = "mutation"
			got[0].Declaration.HookPolicy.Phases[0] = "mutation"
			if borrowed[0].Models[0].Model != "model" || borrowed[0].ProjectedChildNames[0] != "child" || borrowed[0].HookPolicy.Phases[0] != "preflight" {
				t.Fatal("discovery leaked callback-owned slices")
			}
		})
	}
	if got, err := registrationAgentDeclarations(nil, nil); err == nil || got != nil {
		t.Fatal("nil registry accepted")
	}
	reg := Catalog()
	for _, set := range [][]*blueprint.Resolved{{nil}, {{Name: "dup"}, {Name: "dup"}}, {{Name: "x", Workloads: []blueprint.WorkloadInstance{{Name: "w"}, {Name: "w"}}}}} {
		if got, err := registrationAgentDeclarations(reg, set); err == nil || got != nil {
			t.Fatalf("ambiguous set accepted: %v", set)
		}
	}
}

func TestStructuralBindingPreservesBuildRUM(t *testing.T) {
	// Exercise the actual AddBlueprint -> buildBlueprint collaborator handoff,
	// not a second call to structuralWorkloadBinding as a stand-in for Build.
	reg := core.NewRegistry()
	var received core.Binding
	reg.RegisterWorkload(core.WorkloadReg{Kind: "probe", NewConfig: func() any { return &struct{}{} }, MetricProducer: fixedMetricProducer(producerPromRW), Build: func(_ any, b core.Binding) (core.Workload, error) {
		received = b
		return &fakeWorkload{name: b.Name}, nil
	}})
	env := &fixture.Env{Name: "dev"}
	cluster := &fixture.Cluster{Name: "cluster"}
	db := &fixture.DB{Name: "records"}
	wi := blueprint.WorkloadInstance{Kind: "probe", Name: "probe", Replicas: 4, Env: env, Cluster: cluster, Databases: []*fixture.DB{db}, Calls: []fixture.CallTarget{{Kind: "db", DB: db}}, RUM: true}
	r := New(Sinks{RUM: &fakeRUMSink{}}, reg, Options{})
	if err := r.AddBlueprint(&blueprint.Resolved{Name: "bp", Label: "bp", Timezone: "UTC", Workloads: []blueprint.WorkloadInstance{wi}}); err != nil {
		t.Fatal(err)
	}
	if received.Name != "probe" || received.Seed != "bp:probe" || received.Replicas != 4 || received.Env != env || received.Cluster != cluster || received.Databases[0] != db || received.Calls[0].DB != db || received.RUM == nil || received.GenerationStages != nil || received.Ratings != nil {
		t.Fatalf("Build binding changed: %+v", received)
	}
	// No producer or sink is run; queued RUM is only a construction collaborator.
}
