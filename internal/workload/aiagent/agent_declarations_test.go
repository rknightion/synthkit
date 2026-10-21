// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

func declarationCallback(t *testing.T) func(any, core.Binding) ([]core.AgentDeclaration, error) {
	t.Helper()
	cb := Registration().AgentDeclarations
	if cb == nil {
		t.Fatal("legacy agent declaration callback is missing")
	}
	return cb
}

type declarationNativeCapture struct{ exports []sigil.Export }

func (c *declarationNativeCapture) Write(_ context.Context, e []sigil.Export) error {
	c.exports = append(c.exports, e...)
	return nil
}

type declarationTraceCapture struct{ resources []otlp.Resource }

func (c *declarationTraceCapture) Write(_ context.Context, r []otlp.Resource) error {
	c.resources = append(c.resources, r...)
	return nil
}

// Discovery must describe actual ProjectBatch generations/resources, not its own
// expansion helper. One bounded turn always dispatches both effective child kinds.
func TestAgentDeclarationsNativeParity(t *testing.T) {
	cb := declarationCallback(t)
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "explicit"}[explicit], func(t *testing.T) {
			cfg := &Config{}
			if explicit {
				cfg.Resource = ResourceID{ServiceName: "fictional-runtime", ServiceNamespace: "agents"}
			}
			archetypes := []string{archCodingClaudeCode, archCodingCodex, archGeneralOrchestration, archGeneralAutonomousLoop, archGeneralMultiturn, archGeneralSingleShot}
			for i, a := range archetypes {
				cfg.Agents = append(cfg.Agents, AgentDecl{Name: a, Archetype: a, SDK: sigil.SDKGo, Provider: "openai", Models: []string{"model-a", "model-b"}, Subagents: []string{"peer"}, Activity: Activity{TurnsP50: 1, TurnsP95: 1}})
				if i == 2 {
					cfg.Agents[i].Provider = "anthropic"
				}
			}
			// A top-level peer intentionally has different identity from the child called
			// "peer". Child declarations must inherit the producing parent, not this row.
			cfg.Agents = append(cfg.Agents, AgentDecl{Name: "peer", Archetype: archGeneralSingleShot, SDK: sigil.SDKGo, Provider: "bedrock", Models: []string{"peer-model"}, Activity: Activity{TurnsP50: 1, TurnsP95: 1}})
			decls, err := cb(cfg, core.Binding{Name: "fleet"})
			if err != nil {
				t.Fatal(err)
			}
			if len(decls) != len(cfg.Agents) {
				t.Fatalf("declarations %d, want %d including zero activity", len(decls), len(cfg.Agents))
			}
			w, err := Registration().Build(cfg, core.Binding{Name: "fleet"})
			if err != nil {
				t.Fatal(err)
			}
			for i, a := range cfg.Agents {
				d := decls[i]
				wantService := cfg.Resource.ServiceName
				if !explicit && (i == 0 || i == 1) {
					wantService = "agento11y"
				}
				if d.Name != a.Name || d.Service.Name != wantService || d.Service.Namespace != cfg.Resource.ServiceNamespace || d.ProducerMode != core.AgentProducerSynthetic || d.HookPolicy.Enabled || len(d.HookPolicy.Phases) != 0 || d.HookPolicy.Timeout != 0 || d.HookPolicy.FailOpen {
					t.Fatalf("wrong structural declaration: %+v", d)
				}
				var children []string
				if i == 0 {
					children = []string{a.Name + "/peer"}
				}
				if i == 2 {
					children = []string{"peer"}
				}
				if !slices.Equal(d.ProjectedChildNames, children) {
					t.Fatalf("%s children %v want %v", a.Name, d.ProjectedChildNames, children)
				}
				names := append([]string{a.Name}, children...)
				for _, model := range a.Models {
					native := &declarationNativeCapture{}
					traces := &declarationTraceCapture{}
					r := fixedReq("parity-"+a.Name+model, time.Second)
					r.Route = a.Name
					r.Provider = a.Provider
					r.Model = model
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					err = w.ProjectBatch(ctx, r.Start, &core.World{Shape: shape.New("", nil), Sigil: native, Traces: traces}, []*ledger.Request{r})
					cancel()
					if err != nil {
						t.Fatal(err)
					}
					seen := map[string]bool{}
					for _, e := range native.exports {
						for _, g := range e.Generations {
							if !slices.Contains(names, g.AgentName) {
								t.Fatalf("unclaimed emitted name %q", g.AgentName)
							}
							if !slices.Contains(d.Models, core.AgentModelPair{Provider: g.Provider, Model: g.Model}) {
								t.Fatalf("unclaimed emitted model %s/%s", g.Provider, g.Model)
							}
							seen[g.AgentName] = true
							found := false
							for _, res := range traces.resources {
								for _, span := range res.Spans {
									if span.TraceID == g.TraceID && span.SpanID == g.SpanID {
										found = true
										service, _ := res.Attrs["service.name"].(string)
										ns, _ := res.Attrs["service.namespace"].(string)
										if service != d.Service.Name || ns != d.Service.Namespace {
											t.Fatalf("generation %s resource %q/%q differs from claim %+v", g.AgentName, service, ns, d.Service)
										}
									}
								}
							}
							if !found {
								t.Fatal("native generation missing actual trace/resource identity")
							}
						}
					}
					if len(seen) != len(names) {
						t.Fatalf("native emission missing claimed parent/children: %v want %v", seen, names)
					}
				}
			}
		})
	}
}

func TestAgentDeclarationsDefensiveAndInvalid(t *testing.T) {
	cb := declarationCallback(t)
	cfg := &Config{Agents: []AgentDecl{{Name: "parent", Archetype: archCodingClaudeCode, Provider: "openai", Models: []string{"one", "two"}, Subagents: []string{"child"}}}}
	got, err := cb(cfg, core.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.AgentDeclaration{{Name: "parent", Service: core.AgentServiceIdentity{Name: "agento11y"}, ProducerMode: core.AgentProducerSynthetic, Models: []core.AgentModelPair{{Provider: "openai", Model: "one"}, {Provider: "openai", Model: "two"}}, ProjectedChildNames: []string{"parent/child"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claims %+v want %+v", got, want)
	}
	cfg.Agents[0].Models[0] = "changed"
	cfg.Agents[0].Subagents[0] = "changed"
	if !reflect.DeepEqual(got, want) {
		t.Fatal("config mutation changed returned claims")
	}
	got[0].Models[1].Model = "output-mutation"
	got[0].ProjectedChildNames[0] = "output-mutation"
	again, err := cb(cfg, core.Binding{})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Models[1].Model != "two" || again[0].ProjectedChildNames[0] != "parent/changed" {
		t.Fatal("output aliases callback/config")
	}
	empty, err := cb(&Config{Agents: []AgentDecl{{Name: "no-model-default", Archetype: archGeneralSingleShot}}}, core.Binding{})
	if err != nil || len(empty) != 1 || len(empty[0].Models) != 0 || empty[0].Service.Name != "" {
		t.Fatalf("invented service/model: %+v %v", empty, err)
	}
	for _, bad := range []any{nil, (*Config)(nil), "wrong", &Config{Agents: []AgentDecl{{Name: "a", Archetype: "unknown"}}}, &Config{Agents: []AgentDecl{{Archetype: archCodingCodex}}}, &Config{Agents: []AgentDecl{{Name: "a", Archetype: archCodingCodex}, {Name: "a", Archetype: archCodingCodex}}}} {
		if _, err := cb(bad, core.Binding{}); err == nil {
			t.Fatalf("ambiguous/unsupported config accepted: %#v", bad)
		}
	}
}
