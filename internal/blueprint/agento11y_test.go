// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"bytes"
	"strings"
	"testing"

	p "github.com/rknightion/synthkit/internal/agento11yprovision"
)

const authoringHeader = "name: fictional\nenvironments: [{name: test}]\n"
const authoringBody = `agent_observability:
 closures:
 - key: closure
   agent: {instance: worker, agent_name: Planner}
   evaluators:
   - key: eval
     spec: {evaluator_id: e, version: "1", kind: regex, config: '{"pattern":"fiction","reject":false}', output_keys: [{key: passes, type: bool, pass_value: false}]}
   hooks:
   - key: hook
     spec: {rule_id: h, priority: 0, enabled: false, short_circuit: false, match: '{"agent_name":"Planner"}', evaluator_ids: [eval], blocked_tools: [], redact: []}
`

func TestAuthoredYAMLRetainsAliasesAndLexemes(t *testing.T) {
	res, e := Load([]byte(authoringHeader+authoringBody), testRegistry(t))
	if e != nil {
		t.Fatal(e)
	}
	specs := res.AgentObservability.AuthoredSpecs()
	want, e := p.NewAuthoredSpec([]byte(`{"rule_id":"h","priority":0,"enabled":false,"short_circuit":false,"match":{"agent_name":"Planner"},"evaluator_ids":["eval"],"tool_filter":{"blocked_names":[]},"redact":{"patterns":[]}}`))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(specs["hook"].JSON(), want.JSON()) {
		t.Fatal("single alias translation lost absence/zero/false/empty arrays")
	}
	typed := res.AgentObservability.Closures[0].Hooks[0].Spec
	if typed.Enabled == nil || *typed.Enabled || typed.Priority == nil || *typed.Priority != 0 || typed.ShortCircuit == nil || *typed.ShortCircuit {
		t.Fatal("typed presence lost")
	}
	delete(specs, "hook")
	raw := res.AgentObservability.AuthoredSpecs()["hook"].JSON()
	raw[0] = 'x'
	if !bytes.Equal(res.AgentObservability.AuthoredSpecs()["hook"].JSON(), want.JSON()) {
		t.Fatal("retained approval objects mutable through accessor")
	}
	// A C-supported case nesting must not lose capacity to authoring wrappers.
	nested := `{"leaf":1.00}`
	for range 9 {
		nested = `{"nested":` + nested + `}`
	}
	body := `agent_observability: {closures: [{key: c, agent: {instance: i, agent_name: a}, suites: [{key: s, spec: {suite_id: s, name: Fiction}, draft: {key: d, spec: {}, cases: [{key: k, spec: {test_case_id: k, input: ` + nested + `}}], bindings: {key: b, bindings: []}, publication: {key: p, spec: {cases: [], bindings: []}}}}]}]}`
	res, e = Load([]byte(authoringHeader+body), testRegistry(t))
	if e != nil {
		t.Fatalf("supported spec depth/lexeme lost to wrappers: %v", e)
	}
	if !strings.Contains(string(res.AgentObservability.AuthoredSpecs()["k"].JSON()), "1.00") {
		t.Fatal("numeric lexeme was reconstructed from float64")
	}
}
func TestAuthoredYAMLRejectsAmbiguity(t *testing.T) {
	for name, body := range map[string]string{
		"case-alias":     strings.Replace(authoringBody, "rule_id: h", "Rule_ID: h", 1),
		"unknown":        strings.Replace(authoringBody, "priority: 0", "priority: 0, update: true", 1),
		"duplicate":      strings.Replace(authoringBody, "priority: 0", "priority: 0, priority: 1", 1),
		"null":           strings.Replace(authoringBody, "enabled: false", "enabled: null", 1),
		"fraction-loss":  strings.Replace(authoringBody, "priority: 0", "priority: 0.5", 1),
		"anchor":         strings.Replace(authoringBody, "config: '", "config: &config '", 1),
		"alias-conflict": strings.Replace(authoringBody, "blocked_tools: []", "blocked_tools: [], tool_filter: {blocked_names: []}", 1),
		"json-duplicate": strings.Replace(authoringBody, `"pattern":"fiction"`, `"pattern":"fiction","pattern":"other"`, 1),
		"double-string":  strings.Replace(authoringBody, `'{"pattern":"fiction","reject":false}'`, `'"double"'`, 1),
		"hex":            strings.Replace(authoringBody, "priority: 0", "priority: 0x10", 1),
		"tag":            strings.Replace(authoringBody, "priority: 0", "priority: !!str 0", 1),
		"trailing":       authoringBody + "---\nname: hidden\n",
		"null-root":      "agent_observability: null\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := Load([]byte(authoringHeader+body), testRegistry(t)); e == nil {
				t.Fatal("ambiguous/lossy authoring accepted")
			}
		})
	}
}
