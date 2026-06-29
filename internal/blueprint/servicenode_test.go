// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"testing"

	"github.com/rknightion/synthkit/internal/failuremode"

	yaml "gopkg.in/yaml.v3"
)

// appDecl parses one `app` workload (with a service graph) into a Decl for target enumeration.
func appWorkload(t *testing.T) WorkloadDecl {
	t.Helper()
	var w WorkloadDecl
	if err := yaml.Unmarshal([]byte(`
type: app
name: demo-app
services:
  - name: frontend
    type: frontend
  - name: backend
    type: web
    replicas: 3
  - name: gateway
    type: gateway
`), &w); err != nil {
		t.Fatalf("unmarshal app workload: %v", err)
	}
	return w
}

func TestBuildTargets_AppServiceNodes(t *testing.T) {
	d := &Decl{Workloads: []WorkloadDecl{appWorkload(t)}}
	targets := buildTargets(d)

	byName := map[string]Target{}
	for _, tg := range targets {
		byName[tg.Name] = tg
	}

	// each declared service node is an AxisService target
	for _, n := range []string{"frontend", "backend", "gateway"} {
		tg, ok := byName[n]
		if !ok {
			t.Fatalf("service node %q missing from targets", n)
		}
		if tg.Axis != failuremode.AxisService {
			t.Errorf("node %q axis=%q want service", n, tg.Axis)
		}
		if tg.Scalable == nil || tg.Scalable.Dimension != "replicas" {
			t.Errorf("node %q must be replica-scalable", n)
		}
	}
	// declared replicas flow into the node's scale default; omitted → defaultReplicas
	if got := byName["backend"].Scalable.Default; got != 3 {
		t.Errorf("backend default replicas=%d want 3", got)
	}
	if got := byName["frontend"].Scalable.Default; got != defaultReplicas {
		t.Errorf("frontend default replicas=%d want %d", got, defaultReplicas)
	}
	// the app workload itself is a failure target on AxisWorkload but NOT workload-level scalable
	// (scaling is per service node — design §6.6).
	app, ok := byName["demo-app"]
	if !ok || app.Axis != failuremode.AxisWorkload {
		t.Fatalf("app workload target missing/wrong axis: %+v", app)
	}
	if app.Scalable != nil {
		t.Errorf("app workload must not be workload-level scalable (nodes scale): %+v", app.Scalable)
	}
}

func TestBuildTargets_NodeNameCollisionRejected(t *testing.T) {
	// a service node named "gateway" AND a cluster named "gateway" → ambiguous across axes.
	d := &Decl{
		Workloads: []WorkloadDecl{appWorkload(t)},
		Environments: []EnvDecl{{
			Name:    "prod",
			Cluster: &ClusterDecl{Name: "gateway"},
		}},
	}
	if _, err := targetIndex(buildTargets(d)); err == nil {
		t.Fatal("a node name colliding with a cluster name must be rejected at load")
	}
}

func TestValidateEffect_AxisService(t *testing.T) {
	if !knownAxes[failuremode.AxisService] {
		t.Fatal("AxisService must be a known axis (for service:* wildcards)")
	}
	d := &Decl{Workloads: []WorkloadDecl{appWorkload(t)}}
	axes, err := targetIndex(buildTargets(d))
	if err != nil {
		t.Fatalf("targetIndex: %v", err)
	}
	vocab := []failuremode.Mode{{Name: "latency_storm", Axis: failuremode.AxisService}}
	multi := failuremode.MultiAxis(vocab)

	// named node target validates
	if err := validateEffect(EffectDecl{Mode: "latency_storm", Target: "backend"}, axes, vocab, multi); err != nil {
		t.Errorf("latency_storm@backend should validate: %v", err)
	}
	// service:* wildcard validates
	if err := validateEffect(EffectDecl{Mode: "latency_storm", Target: "service:*"}, axes, vocab, multi); err != nil {
		t.Errorf("latency_storm@service:* should validate: %v", err)
	}
	// unknown node rejected
	if err := validateEffect(EffectDecl{Mode: "latency_storm", Target: "nope"}, axes, vocab, multi); err == nil {
		t.Error("latency_storm@nope must be rejected")
	}
}
