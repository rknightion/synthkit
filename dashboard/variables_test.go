// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"strings"
	"testing"
)

func TestClusterVarOptions(t *testing.T) {
	m := &Manifest{Clusters: []ClusterRef{{Name: "a"}, {Name: "b"}}}
	opts := clusterVarOptions(m)
	if len(opts) != 2 || opts[0] != "a" || opts[1] != "b" {
		t.Errorf("clusterVarOptions = %v, want [a b]", opts)
	}
}

func TestEnvVarOptions(t *testing.T) {
	m := &Manifest{Environments: []EnvRef{{Name: "prod"}, {Name: "staging"}}}
	opts := envVarOptions(m)
	if len(opts) != 2 || opts[0] != "prod" || opts[1] != "staging" {
		t.Errorf("envVarOptions = %v, want [prod staging]", opts)
	}
}

func TestAccountVarOptions(t *testing.T) {
	m := &Manifest{Accounts: []AccountRef{{ID: "111122223333"}, {ID: "444455556666"}}}
	opts := accountVarOptions(m)
	if len(opts) != 2 || opts[0] != "111122223333" || opts[1] != "444455556666" {
		t.Errorf("accountVarOptions = %v, want [111122223333 444455556666]", opts)
	}
}

func TestClusterVarBuilder(t *testing.T) {
	m := &Manifest{Clusters: []ClusterRef{{Name: "prod-use1"}, {Name: "stg-use1"}}}
	b := ClusterVar(m)
	if b == nil {
		t.Fatal("ClusterVar returned nil")
	}
	kind, err := b.Build()
	if err != nil {
		t.Fatalf("ClusterVar.Build: %v", err)
	}
	if kind.Spec.Name != "cluster" {
		t.Errorf("name = %q, want cluster", kind.Spec.Name)
	}
	if !kind.Spec.Multi {
		t.Errorf("Multi should be true")
	}
	if !kind.Spec.IncludeAll {
		t.Errorf("IncludeAll should be true")
	}
	if len(kind.Spec.Options) != 2 {
		t.Errorf("options len = %d, want 2", len(kind.Spec.Options))
	}
}

func TestEnvVarBuilder(t *testing.T) {
	m := &Manifest{Environments: []EnvRef{{Name: "prod"}, {Name: "staging"}}}
	b := EnvVar(m)
	if b == nil {
		t.Fatal("EnvVar returned nil")
	}
	kind, err := b.Build()
	if err != nil {
		t.Fatalf("EnvVar.Build: %v", err)
	}
	if kind.Spec.Name != "env" {
		t.Errorf("name = %q, want env", kind.Spec.Name)
	}
	if !kind.Spec.Multi {
		t.Errorf("Multi should be true")
	}
	if !kind.Spec.IncludeAll {
		t.Errorf("IncludeAll should be true")
	}
}

func TestLabelValuesVarBuilder(t *testing.T) {
	expr := `label_values(portkey_api_requests_total, metadata_use_case)`
	b := LabelValuesVar("use_case", "Use case", expr)
	kind, err := b.Build()
	if err != nil {
		t.Fatalf("LabelValuesVar.Build: %v", err)
	}
	if kind.Spec.Name != "use_case" {
		t.Errorf("name = %q, want use_case", kind.Spec.Name)
	}
	if !kind.Spec.Multi || !kind.Spec.IncludeAll {
		t.Errorf("want Multi+IncludeAll")
	}
	if kind.Spec.AllValue == nil || *kind.Spec.AllValue != ".*" {
		t.Errorf("AllValue should be .*, got %v", kind.Spec.AllValue)
	}
	js := string(mustJSON(t, kind))
	for _, want := range []string{`"group":"prometheus"`, expr, `"qryType"`} {
		if !strings.Contains(js, want) {
			t.Errorf("label-values var JSON missing %q: %s", want, js)
		}
	}
}

func TestConstVarBuilder(t *testing.T) {
	kind, err := ConstVar("blueprint", "acme-ai-platform").Build()
	if err != nil {
		t.Fatalf("ConstVar.Build: %v", err)
	}
	if kind.Spec.Name != "blueprint" {
		t.Errorf("name = %q, want blueprint", kind.Spec.Name)
	}
	if kind.Spec.Query != "acme-ai-platform" {
		t.Errorf("query = %q, want acme-ai-platform", kind.Spec.Query)
	}
}

func TestTextVarBuilder(t *testing.T) {
	kind, err := TextVar("trace_id", "Trace ID", "").Build()
	if err != nil {
		t.Fatalf("TextVar.Build: %v", err)
	}
	if kind.Spec.Name != "trace_id" {
		t.Errorf("name = %q, want trace_id", kind.Spec.Name)
	}
}

func TestAccountVarBuilder(t *testing.T) {
	m := &Manifest{Accounts: []AccountRef{{ID: "111122223333"}}}
	b := AccountVar(m)
	if b == nil {
		t.Fatal("AccountVar returned nil")
	}
	kind, err := b.Build()
	if err != nil {
		t.Fatalf("AccountVar.Build: %v", err)
	}
	if kind.Spec.Name != "account" {
		t.Errorf("name = %q, want account", kind.Spec.Name)
	}
	if !kind.Spec.Multi {
		t.Errorf("Multi should be true")
	}
	if !kind.Spec.IncludeAll {
		t.Errorf("IncludeAll should be true")
	}
}
