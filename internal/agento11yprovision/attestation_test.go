// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

func proofDigest(c byte) Digest { return Digest(strings.Repeat(string(c), 64)) }
func proofBinding() Binding {
	return Binding{"deployment", "state", "contract-1", "source-1", Destination{"https://example.invalid:443", "/sub", "org", managementPrefix, "tenant", "https://runtime.invalid/ingest", "tenant"}}
}
func proofInput(t *testing.T) PlanInput {
	t.Helper()
	spec, err := NewAuthoredSpec([]byte(`{"pattern":"fictional","number":1e2,"array":[],"object":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	a := Origin{"blueprint", "planner", 0}
	b := Origin{"blueprint", "source", 0}
	in := PlanInput{Binding: proofBinding(), Mode: ModeApply,
		Declarations: []DeclarationInput{
			{a, core.AgentDeclaration{Name: "Planner", Service: core.AgentServiceIdentity{Name: "planner"}, ProducerMode: core.AgentProducerReal}},
			{b, core.AgentDeclaration{Name: "Source", Service: core.AgentServiceIdentity{Name: "source"}, ProducerMode: core.AgentProducerReal}},
			{Origin{"other", "synthetic", 0}, core.AgentDeclaration{Name: "Context", Service: core.AgentServiceIdentity{Name: "context"}, ProducerMode: core.AgentProducerSynthetic}},
			{Origin{"other", "real", 0}, core.AgentDeclaration{Name: "Other", Service: core.AgentServiceIdentity{Name: "other"}, ProducerMode: core.AgentProducerReal}},
		}, Targets: []Origin{a, b}}
	in.Resources = []PlannedResource{
		{Key: "e", Origin: a, Coordinate: Coordinate{Kind: KindEvaluator, ID: "e-1", Version: "v1"}, Spec: spec, Namespaces: []string{"planner"}},
		{Key: "c", Origin: a, Coordinate: Coordinate{Kind: KindCollection, Unassigned: true}, Spec: spec, Namespaces: []string{"planner"}},
		{Key: "r", Origin: a, Coordinate: Coordinate{Kind: KindRule, ID: "r-1"}, Spec: spec, Dependencies: []string{"e"}, Namespaces: []string{"planner"}},
		{Key: "a", Origin: a, Coordinate: Coordinate{Kind: KindAction, ParentID: "r-1", Unassigned: true}, Spec: spec, Dependencies: []string{"r", "c"}, Namespaces: []string{"planner"}},
	}
	for i, r := range in.Resources {
		in.Steps = append(in.Steps, PlannedStep{uint32(i + 1), r.Key, OpCreate})
	}
	return in
}

// This independent operator document builder deliberately does not use the
// production wire mirrors: it pins the private approval boundary's actual keys.
func proofApprovalJSON(t *testing.T, p Preview, id, construction string, issued, expires time.Time, recovery any) []byte {
	t.Helper()
	b := proofBinding()
	d := b.Destination
	var pv map[string]json.RawMessage
	if err := json.Unmarshal(p.JSON(), &pv); err != nil {
		t.Fatal(err)
	}
	// Preserve the public preview's integer head, including near-overflow CAS
	// controls; a map[string]any roundtrip would silently coerce it to float64.
	base := pv["base"]
	var mode string
	var names []string
	var inventory string
	json.Unmarshal(pv["mode"], &mode)
	json.Unmarshal(pv["namespaces"], &names)
	json.Unmarshal(pv["inventory"], &inventory)
	ev := []any{map[string]any{"id": "reviewed-evidence", "digest": proofDigest('e')}}
	assertions := map[string]any{"authorized_operator": true, "effective_destination": true, "all_affected_emitters_stopped": true, "keep_stopped_until_receipt_accepted": true, "exclusive_namespace": true, "outstanding_work_may_finish": true, "non_atomic_initial_publication": true, "permanent_records_may_remain": true, "no_synthetic_reentry": true, "destination_evidence": ev, "runtime_evidence": ev, "stop_evidence": ev, "namespace_evidence": ev, "foreign": []any{}}
	obj := map[string]any{"schema": 1, "approval_id": id, "operator": "operator", "construction_id": construction, "issued_ms": issued.UnixMilli(), "expires_ms": expires.UnixMilli(), "binding": map[string]any{"deployment_id": b.DeploymentID, "state_id": b.StateID, "contract_revision": b.ContractRevision, "source_revision": b.SourceRevision, "destination": map[string]any{"grafana_origin": d.GrafanaOrigin, "deployment_path": d.DeploymentPath, "grafana_org": d.GrafanaOrg, "plugin_route": d.PluginRoute, "effective_tenant": d.EffectiveTenant, "runtime_endpoint": d.RuntimeEndpoint, "runtime_tenant": d.RuntimeTenant}}, "input": p.plan.Digest(), "preview": p.Digest(), "inventory": inventory, "base": base, "mode": mode, "namespaces": names, "assertions": assertions, "recovery": recovery}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFoundationPlanTargetsAndAuthoredObjects(t *testing.T) {
	in := proofInput(t)
	p, err := NewPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Declarations[0].Declaration.Name = "changed"
	in.Resources[0].Namespaces[0] = "context"
	pv, err := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Namespaces []string `json:"namespaces"`
	}
	if err = json.Unmarshal(pv.JSON(), &v); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(v.Namespaces) != "[planner source]" {
		t.Fatalf("target leak/omission: %v", v.Namespaces)
	}
	cases := map[string]func(*PlanInput){
		"synthetic target":           func(i *PlanInput) { i.Targets = append(i.Targets, i.Declarations[2].Origin) },
		"context namespace":          func(i *PlanInput) { i.Resources[0].Namespaces = []string{"context"} },
		"origin substitution":        func(i *PlanInput) { i.Resources[0].Origin = i.Declarations[3].Origin },
		"target origin substitution": func(i *PlanInput) { i.Resources[0].Origin = i.Targets[1] },
		"case collision":             func(i *PlanInput) { i.Declarations[2].Declaration.Name = "planner" },
		"child collision":            func(i *PlanInput) { i.Declarations[0].Declaration.ProjectedChildNames = []string{"Source"} },
		"nonliteral":                 func(i *PlanInput) { i.Declarations[0].Declaration.Name = "Plan*" },
		"unsupported disabled phase": func(i *PlanInput) { i.Declarations[0].Declaration.HookPolicy.Phases = []string{"postflight"} },
		"apply disable":              func(i *PlanInput) { i.Steps[0].Operation = OpDisable },
		"foreign action parent":      func(i *PlanInput) { i.Resources[3].Coordinate.ParentID = "foreign" },
		"fabricated generated ID": func(i *PlanInput) {
			i.Resources[1].Coordinate.Unassigned = false
			i.Resources[1].Coordinate.ID = "invented"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			i := proofInput(t)
			mutate(&i)
			if _, e := NewPlan(i); e == nil {
				t.Fatal("invalid plan admitted")
			}
		})
	}
	for _, raw := range []string{`[]`, `{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, `{} {}`, string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'})} {
		if _, e := NewAuthoredSpec([]byte(raw)); e == nil {
			t.Fatalf("bad object accepted: %q", raw)
		}
	}
	raw := []byte(`{"n":1e2,"a":[],"o":{}}`)
	s, e := NewAuthoredSpec(raw)
	if e != nil {
		t.Fatal(e)
	}
	raw[2] = 'z'
	out := s.JSON()
	out[2] = 'z'
	if string(s.JSON()) != `{"a":[],"n":1e2,"o":{}}` {
		t.Fatal("object/number/alias lost")
	}
	if _, e = NewAuthoredSpec([]byte(`{"x":"` + strings.Repeat("a", custodySpecBytes) + `"}`)); !errors.Is(e, ErrCapacity) {
		t.Fatalf("spec capacity: %v", e)
	}
}

func TestFoundationStructuredApproval(t *testing.T) {
	p, e := NewPlan(proofInput(t))
	if e != nil {
		t.Fatal(e)
	}
	pv, e := NewPreview(p, ModeApply, Base{Pristine: true}, proofDigest('1'))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	raw := proofApprovalJSON(t, pv, "approval", "", now.Add(-time.Second), now.Add(time.Minute), nil)
	if _, e = DecodeApproval(raw); e != nil {
		t.Fatal(e)
	}
	for _, replace := range [][2]string{{`"schema":1`, `"schema":9`}, {`"authorized_operator":true`, `"authorized_operator":false`}, {`"namespaces":["planner","source"]`, `"namespaces":["context"]`}} {
		altered := []byte(strings.Replace(string(raw), replace[0], replace[1], 1))
		a, e := DecodeApproval(altered)
		if e == nil && replace[0] != `"namespaces":["planner","source"]` {
			t.Fatal("invalid structured assertion accepted")
		}
		if e == nil && a.document.Namespaces[0] != "context" {
			t.Fatal("test replacement failed")
		}
	}
	if _, e = DecodeApproval(append(raw, []byte(` {}`)...)); e == nil {
		t.Fatal("trailing approval accepted")
	}
}

func TestFoundationExactDestinationAndUnicode(t *testing.T) {
	for _, endpoint := range []string{"https://runtime.invalid/ingest#", "https://runtime.invalid/ing est", "https://runtime.invalid/ingest?", "https://runtime.invalid/a/../ingest"} {
		in := proofInput(t)
		in.Binding.Destination.RuntimeEndpoint = endpoint
		if _, e := NewPlan(in); e == nil {
			t.Errorf("destination silently rebound: %q", endpoint)
		}
	}
	if _, e := NewAuthoredSpec([]byte(`{"text":"\ud800"}`)); e == nil {
		t.Error("unpaired surrogate silently replaced")
	}
	if _, e := NewAuthoredSpec([]byte(`{"text":"\ud800\udc00"}`)); e != nil {
		t.Fatal("valid Unicode pair rejected", e)
	}
}
