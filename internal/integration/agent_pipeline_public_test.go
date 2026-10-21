// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/workload/app"
)

// TestAgentPipelineUnwiredEntryPoints tests the construction fence, NOT a
// receipt or real-only custody attestation. Root must only inject the required
// collaborator after actual admission; ordinary paths have no collaborator.
func TestAgentPipelineUnwiredEntryPoints(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/e2e-agent-pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "hooks: {enabled: true,", "hooks: {enabled: false,")
	text = strings.ReplaceAll(text, "ratings: {sample_rate: 1}", "ratings: {sample_rate: 0}")
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := res.Workloads[0].Config.(*app.Config)
	if cfg.AgentPipeline.Ratings.SampleRate != 0 || cfg.Services[0].AgentObservability.Hooks.Enabled {
		t.Fatal("disabled/zero falsifier did not modify fixture")
	}
	for _, entry := range []string{"boot", "live", "restore"} {
		t.Run(entry, func(t *testing.T) {
			sc := &proofSink{}
			r := runner.New(runner.Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}, Sigil: sc, OTLPMetrics: &proofMetrics{}}, reg, runner.Options{})
			lb := control.LiveBlueprint{Name: "test/" + res.Name, Namespace: "test", YAML: text}
			var rejection error
			switch entry {
			case "boot":
				rejection = r.AddBlueprint(res)
			case "live":
				rejection = r.ApplyLiveBlueprint(lb, func() error { t.Error("persist called before required construction dependency"); return nil })
			case "restore":
				path := filepath.Join(t.TempDir(), "state.json")
				store := control.NewStore(path)
				store.Update(func(s *control.State) { s.LiveBlueprints = []control.LiveBlueprint{lb} })
				restored := control.NewStore(path)
				r.ApplyControl(restored.Snapshot())
				skips := r.LiveRestoreSkips()
				if len(skips) == 1 && strings.Contains(skips[0].Reason, "GenerationStages") {
					return
				}
			}
			if entry != "restore" && rejection != nil && strings.Contains(rejection.Error(), "GenerationStages") {
				return
			}
			// The old candidate is actually reachable, not just missing an incidental
			// validation error. Exercise the ordinary scheduler and flush its writers.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.RunOnce(ctx, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)); err != nil {
				t.Logf("ordinary runtime result: %v", err)
			}
			n := 0
			for _, e := range sc.exports {
				n += len(e.Generations)
			}
			t.Errorf("unwired %s reachable with hooks disabled and ratings zero: running=%v generations=%d rejection=%v skips=%v", entry, r.Blueprints(), n, rejection, r.LiveRestoreSkips())
		})
	}
}

type proofStages struct{}

func (proofStages) BeforeToolResult(_ context.Context, _ sigil.GenerationStage, r sigil.ToolResult) (sigil.ToolResultDecision, error) {
	r.Content = "[REDACTED]"
	return sigil.ToolResultDecision{Allowed: true, Transformed: &r}, nil
}
func (proofStages) AfterGeneration(context.Context, sigil.Generation) error { return nil }

type proofSink struct{ exports []sigil.Export }

func (c *proofSink) Write(_ context.Context, e []sigil.Export) error {
	c.exports = append(c.exports, e...)
	return nil
}
func (*proofSink) WriteConversationRating(context.Context, string, sigil.ConversationRatingInput) error {
	return nil
}

type proofMetrics struct{ r []otlp.MetricResource }

func (c *proofMetrics) Write(_ context.Context, r []otlp.MetricResource) error {
	c.r = append(c.r, r...)
	return nil
}

// This proof intentionally injects collaborators at Build, because the root has
// not installed claims/custody/runtime adapters in the CLI yet. It does exercise
// the real public loader/resolver and actual KSM emitter independently of app.
func TestAgentPipelinePublicPlacement(t *testing.T) {
	data, err := os.ReadFile("../../e2e/fixtures/e2e-agent-pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err = blueprint.ValidateSet([]*blueprint.Resolved{res}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	mc := &coretest.MetricCapture{}
	tc := &coretest.TraceCapture{}
	native := &proofMetrics{}
	sc := &proofSink{}
	world := &core.World{Shape: shape.New("", nil), Metrics: mc, Logs: &coretest.LogCapture{}, Traces: tc, OTLPMetrics: native, Sigil: sc}
	ksm := false
	for _, ci := range res.Constructs {
		if ci.Kind != "k8s_cluster" {
			continue
		}
		cr, ok := reg.Construct(ci.Kind)
		if !ok {
			t.Fatal("registry missing k8s")
		}
		c, err := cr.Build(ci.Config, ci.Fixtures)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Tick(ctx, now, world); err != nil {
			t.Fatal(err)
		}
		ksm = true
	}
	if !ksm {
		t.Fatal("fixture has no actual KSM emitter")
	}
	wi := res.Workloads[0]
	wr, _ := reg.Workload(wi.Kind)
	binding := core.Binding{Name: wi.Name, Cluster: wi.Cluster, Env: wi.Env, GenerationStages: proofStages{}, Ratings: sc}
	declarations, err := wr.AgentDeclarations(wi.Config, binding)
	if err != nil || len(declarations) != 5 {
		t.Fatalf("declarations %d err=%v", len(declarations), err)
	}
	w, err := wr.Build(wi.Config, binding)
	if err != nil {
		t.Fatal(err)
	}
	reqs := w.Minter().Mint(now, 1, world.Shape)
	if len(reqs) != 1 {
		t.Fatalf("forced fixture minted %d", len(reqs))
	}
	if err = w.ProjectBatch(ctx, now, world, reqs); err != nil {
		t.Fatal(err)
	}
	if err = w.Tick(ctx, now, world); err != nil {
		t.Fatal(err)
	}
	pods := map[string]bool{}
	for _, s := range mc.All() {
		if s.Name == "kube_pod_info" {
			pods[s.Labels["cluster"]+"/"+s.Labels["namespace"]+"/"+s.Labels["pod"]] = true
		}
	}
	if len(pods) == 0 {
		t.Fatal("no actual kube_pod_info")
	}
	services := map[string]bool{}
	for _, r := range tc.Resources {
		service, _ := r.Attrs["service.name"].(string)
		if service == "" {
			continue
		}
		services[service] = true
		key := r.Attrs["k8s.cluster.name"].(string) + "/" + r.Attrs["k8s.namespace.name"].(string) + "/" + r.Attrs["k8s.pod.name"].(string)
		if !pods[key] {
			t.Errorf("OTLP resource not in emitted KSM: %s", key)
		}
	}
	if len(services) != 5 {
		t.Fatalf("actual service trace resources: %v", services)
	}
	for _, r := range native.r {
		key := r.Attrs["k8s.cluster.name"].(string) + "/" + r.Attrs["k8s.namespace.name"].(string) + "/" + r.Attrs["k8s.pod.name"].(string)
		if !pods[key] {
			t.Error("native resource not in KSM", key)
		}
	}
	// Honest root integration limitation: AddBlueprint cannot provide the required
	// stages/ratings yet, and must not silently accept a fake receipt or omit hooks.
	root := runner.New(runner.Sinks{Metrics: mc, Traces: tc, Sigil: sc, OTLPMetrics: native}, reg, runner.Options{})
	if err := root.AddBlueprint(res); err == nil {
		t.Fatal("unwired root unexpectedly built enabled hooks without collaborator")
	}
	t.Logf("actual KSM triples=%d; trace services=%d; native model resources=%d; unconfigured root rejected as required", len(pods), len(services), len(native.r))
}
