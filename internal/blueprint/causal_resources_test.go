// SPDX-License-Identifier: AGPL-3.0-only

package blueprint_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/runner"
)

// probeConfig is a workload Config that consumes the causal set and declares a retrying edge
// into one resource, standing in for the app workload (wired in a later lane).
type probeConfig struct {
	Resource string  `yaml:"resource"`
	Share    float64 `yaml:"share"`
	set      causal.Set
	used     int
	declared int
	seen     causal.Set
	env      string
}

func (c *probeConfig) UseResources(s causal.Set) { c.set = s; c.used++ }

func (c *probeConfig) CausalLoads(s causal.Set, env string) ([]causal.Load, error) {
	c.declared++
	c.seen = s
	c.env = env
	if c.Resource == "" {
		return nil, nil
	}
	r, _, ok := s.Resolve(c.Resource, env)
	if !ok {
		return nil, nil
	}
	return []causal.Load{{Resource: r.Name, Share: c.Share, Policy: causal.CallPolicy{Retries: 2, RetryOn: []string{"throttle"}}}}, nil
}

func causalRegistry() *core.Registry {
	reg := runner.Catalog()
	reg.RegisterWorkload(core.WorkloadReg{Kind: "causal_probe", Doc: "test",
		NewConfig:    func() any { return &probeConfig{} },
		Build:        func(any, core.Binding) (core.Workload, error) { return nil, nil },
		FailureModes: causal.FailureModes,
	})
	return reg
}

const causalBlueprint = `name: causal-fixture
environments:
  - name: prod
    cloud: { provider: aws, account_id: "111122223333", region: eu-west-1, vpc_id: vpc-0abc }
    cluster:
      type: eks
      name: causal-prod
      emit: false
      node_groups: [{ name: general, instance_type: m6i.large, desired: 3 }]
    databases:
      - { name: records-db, engine: postgres }
    resources:
      - { name: records-db, kind: dependency, latency_ms: 15, slow_factor: 12 }
      - name: orders-db-pool
        kind: pool
        size: 10
        hold_ms: 20
        held_across: records-db
        acquire_timeout_ms: 3000
        demand: { off_peak_rps: 5, peak_rps: 60 }
      - { name: store-prod, kind: dependency, latency_ms: 4 }
      - name: enrich-fn-concurrency
        kind: concurrency
        size: 20
        hold_ms: 900
        demand: { off_peak_rps: 2, peak_rps: 15 }
  - name: dev
    weight: 0.25
    cloud: { provider: aws, account_id: "444455556666", region: eu-west-1, vpc_id: vpc-0def }
    resources:
      - { name: store-dev, kind: dependency, latency_ms: 4 }
workloads:
  - type: causal_probe
    name: probe
    runs_on: causal-prod
    resource: enrich-fn-concurrency
    share: 0.6
scenarios:
  - name: db-slow
    effects: [{ mode: dependency_slow, target: records-db }]
  - name: db-saturated
    effects: [{ mode: connection_saturation, target: records-db }]
  - name: stores-slow
    effects: [{ mode: dependency_slow, target: store }]
incidents:
  - { kind: pool_exhausted, target: "resource:*", at: "10:00", for: 10m, intensity: 0.5 }
  - { kind: dependency_slow, target: store, at: "11:00", for: 10m }
  - { scenario: db-slow, at: "12:00", for: 10m }
`

func loadCausal(t *testing.T, text string) (*blueprint.Resolved, error) {
	t.Helper()
	return blueprint.Load([]byte(text), causalRegistry())
}

func TestResourcesLoadAsTargetsAndInject(t *testing.T) {
	r, err := loadCausal(t, causalBlueprint)
	if err != nil {
		t.Fatal(err)
	}
	var res []string
	for _, tg := range r.Targets {
		if tg.Axis == causal.AxisResource {
			res = append(res, tg.Name)
		}
	}
	want := []string{"records-db", "orders-db-pool", "store-prod", "enrich-fn-concurrency", "store-dev", "store"}
	if !slices.Equal(res, want) {
		t.Fatalf("resource targets %v, want %v", res, want)
	}
	// records-db is both a database and a resource target; each mode resolved on its own axis.
	if !slices.ContainsFunc(r.Targets, func(tg blueprint.Target) bool { return tg.Name == "records-db" && tg.Axis == "database" }) {
		t.Fatal("database target records-db lost")
	}
	var probe *probeConfig
	for _, w := range r.Workloads {
		if p, ok := w.Config.(*probeConfig); ok {
			probe = p
		}
	}
	if probe == nil || probe.used != 1 || probe.declared != 1 {
		t.Fatalf("probe consumer/declarer calls: %+v", probe)
	}
	if probe.env != "prod" {
		t.Fatalf("declarer env %q, want the instance's environment prod", probe.env)
	}
	if len(probe.seen.Loads) != 0 {
		t.Fatal("declarer saw loads; it must receive the declared set alone")
	}
	s := probe.set
	if len(s.Envs) != 2 || s.Envs[0].Env != "prod" || s.Envs[0].Weight != 1 || s.Envs[0].NonProd ||
		s.Envs[1].Env != "dev" || s.Envs[1].Weight != 0.25 || !s.Envs[1].NonProd || len(s.Envs[0].Resources) != 4 {
		t.Fatalf("injected set %+v", s)
	}
	if len(s.Loads) != 1 || s.Loads[0].Resource != "enrich-fn-concurrency" || s.Loads[0].Share != 0.6 {
		t.Fatalf("injected loads %+v", s.Loads)
	}
	if _, err := causal.Compile(s); err != nil {
		t.Fatalf("injected set does not compile: %v", err)
	}
	// resource:* covers the declared resources, not the alias; a base name is one scope that the
	// model reads for every <base>-<env> resource.
	var wildcard, alias, scenario []string
	for _, inc := range r.Incidents {
		switch {
		case strings.HasPrefix(inc, "pool_exhausted@"):
			wildcard = append(wildcard, inc[strings.LastIndex(inc, "@")+1:])
		case strings.HasPrefix(inc, "dependency_slow@11:00"):
			alias = append(alias, inc[strings.LastIndex(inc, "@")+1:])
		case strings.HasPrefix(inc, "dependency_slow@12:00"):
			scenario = append(scenario, inc[strings.LastIndex(inc, "@")+1:])
		}
	}
	if !slices.Equal(wildcard, []string{"records-db", "orders-db-pool", "store-prod", "enrich-fn-concurrency", "store-dev"}) {
		t.Fatalf("resource:* expanded to %v", wildcard)
	}
	if !slices.Equal(alias, []string{"store"}) || !slices.Equal(scenario, []string{"records-db"}) {
		t.Fatalf("alias %v scenario %v; incidents %v", alias, scenario, r.Incidents)
	}
}

func TestResourceDeclarationErrors(t *testing.T) {
	cases := map[string]func(string) string{
		"held_across \"nope\" names no declared resource": func(s string) string {
			return strings.Replace(s, "held_across: records-db", "held_across: nope", 1)
		},
		"declared by environments \"prod\" and \"dev\"": func(s string) string {
			return strings.Replace(s, "name: store-dev,", "name: store-prod,", 1)
		},
		"reached by the base name \"store\"": func(s string) string {
			return strings.Replace(s, "      - { name: store-dev, kind: dependency, latency_ms: 4 }", "      - { name: store-dev, kind: dependency, latency_ms: 4 }\n      - { name: store, kind: dependency }", 1)
		},
		"field bogus not found": func(s string) string {
			return strings.Replace(s, "latency_ms: 15, slow_factor: 12 }", "latency_ms: 15, slow_factor: 12, bogus: 1 }", 1)
		},
		"unknown kind \"cache\"": func(s string) string {
			return strings.Replace(s, "name: store-prod, kind: dependency", "name: store-prod, kind: cache", 1)
		},
		"unknown target \"nope\"": func(s string) string {
			return strings.Replace(s, "target: store, at", "target: nope, at", 1)
		},
		"mode not declared on any of target \"records-db\"'s axes": func(s string) string {
			return strings.Replace(s, "mode: connection_saturation, target: records-db", "mode: oom_kill, target: records-db", 1)
		},
		"more than its declared demand": func(s string) string {
			return strings.Replace(s, "resource: enrich-fn-concurrency\n    share: 0.6",
				"resource: enrich-fn-concurrency\n    share: 0.6\n  - type: causal_probe\n    name: probe2\n    runs_on: causal-prod\n    resource: enrich-fn-concurrency\n    share: 0.6", 1)
		},
	}
	for want, mut := range cases {
		_, err := loadCausal(t, mut(causalBlueprint))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

// Without resources no declarer or consumer is called and no resource target exists.
func TestNoResourcesNoCausalPath(t *testing.T) {
	text := causalBlueprint[:strings.Index(causalBlueprint, "    resources:\n")] +
		"workloads:\n  - type: causal_probe\n    name: probe\n    runs_on: causal-prod\n    resource: enrich-fn-concurrency\n    share: 0.6\n"
	r, err := loadCausal(t, text)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range r.Targets {
		if tg.Axis == causal.AxisResource {
			t.Fatalf("resource target %q without resources", tg.Name)
		}
	}
	p := r.Workloads[0].Config.(*probeConfig)
	if p.used != 0 || p.declared != 0 {
		t.Fatalf("probe called without resources: %+v", p)
	}
}

// No shipped blueprint declares resources yet: each resolves with no resource target, so
// the causal path is dead for all of them (the dump inventory diff proves the bytes).
func TestShippedBlueprintsHaveNoResourceTargets(t *testing.T) {
	files, err := filepath.Glob("../../blueprints/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v %d", err, len(files))
	}
	reg := runner.Catalog()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		r, err := blueprint.Load(data, reg)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, tg := range r.Targets {
			if tg.Axis == causal.AxisResource {
				t.Errorf("%s: unexpected resource target %q", f, tg.Name)
			}
		}
	}
}
