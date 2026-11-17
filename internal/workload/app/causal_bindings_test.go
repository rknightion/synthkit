// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
)

func TestServiceCallersAreTransitiveSortedAndCycleSafe(t *testing.T) {
	services := []ServiceNode{
		{Name: "edge", Calls: []string{"api", "search"}},
		{Name: "api", Calls: []string{"db", "search"}},
		{Name: "search", Calls: []string{"db"}},
		{Name: "db"},
		{Name: "loop-a", Calls: []string{"loop-b"}},
		{Name: "loop-b", Calls: []string{"loop-a"}},
	}
	got := serviceCallers(services)
	want := map[string][]string{
		"api":    {"edge"},
		"search": {"api", "edge"},
		"db":     {"api", "edge", "search"},
		"loop-a": {"loop-b"},
		"loop-b": {"loop-a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("callers = %v, want %v", got, want)
	}
}

// Every `uses` entry is one binding with the node's emitted identity, and a node that uses nothing
// binds nothing. The resource name stays as declared: the resolver applies the environment rule.
func TestResourceBindingsListEveryUse(t *testing.T) {
	cfg := &Config{Services: []ServiceNode{
		{Name: "api", Uses: []string{"api-pool", "intake-quota"}, Calls: []string{"records"}},
		{Name: "records", Uses: []string{"records-db"}},
		{Name: "plain"},
	}}
	got := cfg.ResourceBindings(causal.BindContext{Env: "prod"})
	want := []causal.Binding{
		{Resource: "api-pool", Service: "api", Identity: map[string]string{"service": "api", "deployment_environment_name": "prod"}},
		{Resource: "intake-quota", Service: "api", Identity: map[string]string{"service": "api", "deployment_environment_name": "prod"}},
		{Resource: "records-db", Service: "records", Callers: []string{"api"}, Identity: map[string]string{"service": "records", "deployment_environment_name": "prod"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings = %+v, want %+v", got, want)
	}
	// No environment: the label is absent, never empty.
	if id := cfg.ResourceBindings(causal.BindContext{})[0].Identity; len(id) != 1 || id["service"] != "api" {
		t.Fatalf("identity without an environment = %v", id)
	}
}
