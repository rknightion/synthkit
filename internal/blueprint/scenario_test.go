// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"testing"

	"github.com/rknightion/synthkit/internal/failuremode"
)

func ptrBool(b bool) *bool { return &b }

func TestBuildTargetsTagsAxesAndScalable(t *testing.T) {
	// Construct a minimal Decl with one workload, one rds-lane DB, one dbo11y-only DB, one cache.
	d := &Decl{
		Name:      "t",
		Workloads: []WorkloadDecl{{Type: "web_service", Name: "api", RunsOn: "c", Replicas: 4}},
		Environments: []EnvDecl{{
			Name: "prod",
			Databases: []DatabaseDecl{
				{Engine: "postgres", Name: "app-db"}, // rds lane default-on
				{Engine: "postgres", Name: "obs-db", Observability: &ObservabilityDecl{CloudWatch: ptrBool(false), Dbo11y: true}},
			},
			Caches: []CacheDecl{{Engine: "redis", Name: "sess"}},
		}},
	}
	targets := buildTargets(d)
	byName := map[string]Target{}
	for _, tg := range targets {
		byName[tg.Name] = tg
	}
	if byName["api"].Axis != failuremode.AxisWorkload || byName["api"].Scalable == nil || byName["api"].Scalable.Dimension != "replicas" {
		t.Errorf("api should be replicas-scalable: %+v", byName["api"])
	}
	if byName["api"].Scalable.Default != 4 || byName["api"].Scalable.Min != replicaMin || byName["api"].Scalable.Max != replicaMax {
		t.Errorf("api scalable bounds wrong: %+v", byName["api"].Scalable)
	}
	// v1: only workloads are live-scalable. Databases/caches are addressable failure targets only.
	if byName["app-db"].Axis != failuremode.AxisDatabase || byName["app-db"].Scalable != nil {
		t.Errorf("app-db: database axis, NOT scalable in v1: %+v", byName["app-db"])
	}
	if byName["obs-db"].Scalable != nil {
		t.Errorf("obs-db must not be scalable: %+v", byName["obs-db"])
	}
	if byName["sess"].Axis != failuremode.AxisCache || byName["sess"].Scalable != nil {
		t.Errorf("sess: cache axis, not scalable: %+v", byName["sess"])
	}
}

// TestBuildTargetsClusterAndCloud asserts cluster (cluster axis) and cloud (cloud axis, env-named)
// targets are enumerated and are NOT scalable in v1.
func TestBuildTargetsClusterAndCloud(t *testing.T) {
	d := &Decl{
		Name: "t",
		Environments: []EnvDecl{{
			Name:    "prod",
			Cloud:   &CloudDecl{Provider: "aws", Region: "us-east-1"},
			Cluster: &ClusterDecl{Type: "eks", Name: "prod-use1"},
		}},
	}
	byName := map[string]Target{}
	for _, tg := range buildTargets(d) {
		byName[tg.Name] = tg
	}
	if byName["prod-use1"].Axis != failuremode.AxisCluster || byName["prod-use1"].Scalable != nil {
		t.Errorf("cluster: cluster axis, not scalable: %+v", byName["prod-use1"])
	}
	if byName["prod"].Axis != failuremode.AxisCloud || byName["prod"].Scalable != nil {
		t.Errorf("cloud (env-named): cloud axis, not scalable: %+v", byName["prod"])
	}
}

// TestBuildTargetsDefaultReplicas: a workload with replicas omitted (<=0) defaults to 2.
func TestBuildTargetsDefaultReplicas(t *testing.T) {
	d := &Decl{
		Name:      "t",
		Workloads: []WorkloadDecl{{Type: "web_service", Name: "api", RunsOn: "c"}},
	}
	tgs := buildTargets(d)
	if len(tgs) != 1 || tgs[0].Scalable == nil || tgs[0].Scalable.Default != 2 {
		t.Fatalf("omitted replicas should default to 2: %+v", tgs)
	}
}

func TestTargetIndexRejectsAmbiguousNames(t *testing.T) {
	// Same name on two axes is ambiguous.
	targets := []Target{
		{Name: "dup", Axis: failuremode.AxisDatabase},
		{Name: "dup", Axis: failuremode.AxisCache},
	}
	if _, err := targetIndex(targets); err == nil {
		t.Errorf("duplicate name across axes must be rejected")
	}
	// Same name + same axis is not a collision (idempotent claim).
	ok := []Target{
		{Name: "a", Axis: failuremode.AxisWorkload},
		{Name: "a", Axis: failuremode.AxisWorkload},
		{Name: "b", Axis: failuremode.AxisDatabase},
	}
	idx, err := targetIndex(ok)
	if err != nil {
		t.Fatalf("same-axis duplicate should not error: %v", err)
	}
	if idx["a"] != failuremode.AxisWorkload || idx["b"] != failuremode.AxisDatabase {
		t.Errorf("index wrong: %v", idx)
	}
}

func TestValidateEffectMatrix(t *testing.T) {
	axes := map[string]failuremode.Axis{"app-db": failuremode.AxisDatabase, "api": failuremode.AxisWorkload}
	vocab := []failuremode.Mode{
		{Name: "latency_spike", Axis: failuremode.AxisWorkload},
		{Name: "latency_spike", Axis: failuremode.AxisDatabase},
		{Name: "connection_saturation", Axis: failuremode.AxisDatabase},
	}
	cases := []struct {
		name    string
		eff     EffectDecl
		wantErr bool
	}{
		{"named ok", EffectDecl{Mode: "connection_saturation", Target: "app-db", Intensity: 0.5}, false},
		{"axis wildcard ok", EffectDecl{Mode: "connection_saturation", Target: "database:*"}, false},
		{"unknown target", EffectDecl{Mode: "connection_saturation", Target: "nope"}, true},
		{"mode not on target axis", EffectDecl{Mode: "connection_saturation", Target: "api"}, true},
		{"empty target multi-axis", EffectDecl{Mode: "latency_spike", Target: ""}, true},
		{"empty target single-axis", EffectDecl{Mode: "connection_saturation", Target: ""}, false},
		{"intensity out of range", EffectDecl{Mode: "connection_saturation", Target: "app-db", Intensity: 1.5}, true},
		{"bad axis wildcard", EffectDecl{Mode: "connection_saturation", Target: "bogus:*"}, true},
		{"unknown mode empty target", EffectDecl{Mode: "ghost", Target: ""}, true},
		{"wildcard mode not on axis", EffectDecl{Mode: "connection_saturation", Target: "workload:*"}, true},
		{"negative intensity", EffectDecl{Mode: "connection_saturation", Target: "app-db", Intensity: -0.1}, true},
	}
	for _, c := range cases {
		err := validateEffect(c.eff, axes, vocab, failuremode.MultiAxis(vocab))
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestScheduleEntry(t *testing.T) {
	cases := []struct {
		kind, at, forDur string
		intensity        float64
		scope            string
		want             string
	}{
		{"latency_spike", "2026-06-19T14:00", "20m", 0.8, "mini-api", "latency_spike@2026-06-19T14:00/20m#0.8@mini-api"},
		{"latency_spike", "2026-06-19T14:00", "20m", 0, "", "latency_spike@2026-06-19T14:00/20m"},
		{"oom_kill", "t", "5m", 1, "", "oom_kill@t/5m#1"},
		{"oom_kill", "t", "5m", 0, "cl", "oom_kill@t/5m@cl"},
	}
	for _, c := range cases {
		got := scheduleEntry(c.kind, c.at, c.forDur, c.intensity, c.scope)
		if got != c.want {
			t.Errorf("scheduleEntry(%q,%q,%q,%v,%q) = %q, want %q", c.kind, c.at, c.forDur, c.intensity, c.scope, got, c.want)
		}
	}
}

func TestExpandScopes(t *testing.T) {
	targets := []Target{
		{Name: "db-a", Axis: failuremode.AxisDatabase},
		{Name: "db-b", Axis: failuremode.AxisDatabase},
		{Name: "api", Axis: failuremode.AxisWorkload},
	}
	// Empty target → the engine's un-scoped match.
	if got := expandScopes("", targets); len(got) != 1 || got[0] != "" {
		t.Errorf(`expandScopes("") = %v, want [""]`, got)
	}
	// Named target → just that name.
	if got := expandScopes("api", targets); len(got) != 1 || got[0] != "api" {
		t.Errorf(`expandScopes("api") = %v, want ["api"]`, got)
	}
	// Axis wildcard → every target name of that axis.
	got := expandScopes("database:*", targets)
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	if len(got) != 2 || !set["db-a"] || !set["db-b"] {
		t.Errorf(`expandScopes("database:*") = %v, want [db-a db-b]`, got)
	}
}
