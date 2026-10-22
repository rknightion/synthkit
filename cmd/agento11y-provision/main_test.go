// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Fictional source-only declarations; no Build/corpus loading/emission occurs.
const commandBlueprint = `name: fictional
 environments: []
`
const applicationBlueprint = `name: fictional
 environments:
 - name: test
   cloud: {provider: aws, account_id: "000000000000", region: eu-west-1, vpc_id: vpc-fictional}
   cluster: {type: eks, name: fictional-cluster, emit: false, node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]}
 workloads:
 - type: app
   name: pipeline
   runs_on: fictional-cluster
   otel: {metrics: true}
   services:
   - name: coordinator
     type: agent
     runtime: go
     entry: true
     calls: [checker]
     agent_observability: {name: Planner, sdk: sdk-go, provider: openai, models: [gpt-4.1-mini], corpus: fictional-unopened}
   - name: checker
     type: agent
     runtime: go
     agent_observability: {name: Checker, sdk: sdk-go, provider: openai, models: [gpt-4.1-mini], corpus: fictional-unopened}
   agent_pipeline:
     name: fictional-flow
     evaluation_mode: real
     request_kinds: [{name: single, weight: 1}]
     stages: [{name: draft, service: coordinator, operation: generation}, {name: review, service: checker, operation: generation}]
     review: {stage: review, revise_stage: draft, max_revisions: 0}
     outcomes: [{name: ready, weight: 1, review_passed: true}]
     escalated_outcome: ready
`
const managementYAML = `agent_observability:
 closures:
 - key: closure
   agent: {instance: pipeline, agent_name: Planner}
   evaluators:
   - key: e
     spec: {evaluator_id: eval-fresh, version: "1", kind: regex, description: " Fictional check ", config: '{"pattern":" fiction ","reject":false}', output_keys: [{key: passes, type: bool, pass_value: false}]}
   collections:
   - key: c
     spec: {name: " Fictional collection ", description: ""}
   rules:
   - key: r
     spec: {rule_id: rule-fresh, enabled: true, selector: user_visible_turn, match: '{"agent_name":"Planner"}', sample_rate: 0, evaluator_ids: [e], execution_mode: parallel, alert_rule_uids: [], filterable_tag_keys: []}
   hooks:
   - key: h
     spec: {rule_id: hook-fresh, enabled: true, phase: preflight, priority: 0, selector: all, match: {agent_name: Planner}, evaluator_ids: [e], action_on_fail: deny, short_circuit: false, blocked_tools: [fictional_blocked], redact: [{id: redact-one, regex: fictional_secret}]}
   actions:
   - key: a
     parent_rule: r
     spec: {condition: {kind: all_evaluators_pass}, action_config: {kind: add_to_collection, collection_ids: [c]}, enabled: true}
`

func privateFile(t *testing.T, path string, raw []byte) string {
	t.Helper()
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func fixtureBinding(t *testing.T, origin string) []byte {
	t.Helper()
	raw, e := json.Marshal(map[string]any{"deployment_id": "fictional-deployment", "state_id": "fictional-state", "contract_revision": "agento11yprovision-state-3264k-v2", "source_revision": "sigil-bc963fb09-engine-inventory404-v1", "destination": map[string]string{"grafana_origin": origin, "deployment_path": "/sub", "grafana_org": "fictional-org", "plugin_route": "/api/plugins/grafana-agento11y-app/resources/eval", "effective_tenant": "fictional-tenant", "runtime_endpoint": "https://runtime.example.invalid/api/v1/generations:export", "runtime_tenant": "fictional-runtime"}})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func fixtureApproval(t *testing.T, preview []byte, id, construction string, recovery any) []byte {
	t.Helper()
	var v map[string]any
	if json.Unmarshal(preview, &v) != nil {
		t.Fatal("preview decode")
	}
	now := time.Now()
	v["approval_id"] = id
	v["operator"] = "fictional-operator"
	v["construction_id"] = construction
	v["issued_ms"] = now.Add(-time.Second).UnixMilli()
	v["expires_ms"] = now.Add(time.Minute).UnixMilli()
	// The preview digest is the existing private-wire hash, not JSON reencoding.
	v["preview"] = fixtureHash("preview", preview)
	evidence := []any{map[string]string{"id": "independent-fictional-evidence", "digest": strings.Repeat("a", 64)}}
	v["assertions"] = map[string]any{"authorized_operator": true, "effective_destination": true, "all_affected_emitters_stopped": true, "keep_stopped_until_receipt_accepted": true, "exclusive_namespace": true, "outstanding_work_may_finish": true, "non_atomic_initial_publication": true, "permanent_records_may_remain": true, "no_synthetic_reentry": true, "destination_evidence": evidence, "runtime_evidence": evidence, "stop_evidence": evidence, "namespace_evidence": evidence, "foreign": []any{}}
	v["recovery"] = recovery
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestCommandFlagMatrixAndPrivateInputs(t *testing.T) {
	// Resolve the platform temp root: a symlinked ancestor is refused by design.
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	binding := privateFile(t, filepath.Join(dir, "binding"), fixtureBinding(t, "https://management.example.invalid"))
	bp := privateFile(t, filepath.Join(dir, "blueprint"), []byte(strings.ReplaceAll(applicationBlueprint, "\n ", "\n")+managementYAML))
	base := []string{"--blueprint-file", bp, "--blueprint-namespace", "", "--binding-file", binding}
	invoke := func(args []string) int {
		t.Helper()
		var out, err bytes.Buffer
		code := runArgs(context.Background(), args, strings.NewReader(""), &out, &err)
		if strings.Contains(out.String()+err.String(), dir) || strings.Contains(out.String()+err.String(), "example.invalid") {
			t.Fatal("private paths/destination leaked")
		}
		return code
	}
	if code := invoke(base); code != 0 {
		t.Fatalf("offline declaration plan refused %d", code)
	}
	for _, extra := range [][]string{{"--state-dir", dir}, {"--token-file", binding}, {"--expect-enrolled=false"}, {"--mode", "apply"}, {"--construction-id", "old"}, {"--approval-file", binding}, {"--out", binding}, {"--binding-file", binding}, {"--token", "secret-canary"}} {
		if code := invoke(append(append([]string{}, base...), extra...)); code != 2 {
			t.Fatalf("inapplicable/unsafe input accepted: %v = %d", extra, code)
		}
	}
	if e := os.Chmod(binding, 0644); e != nil {
		t.Fatal(e)
	}
	if invoke(base) != 2 {
		t.Fatal("public Binding file accepted")
	}
	if e := os.Chmod(binding, 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "link")
	if os.Symlink(binding, link) != nil {
		t.Fatal("symlink")
	}
	symlinkArgs := append([]string{}, base...)
	symlinkArgs[len(symlinkArgs)-1] = link
	if invoke(symlinkArgs) != 2 {
		t.Fatal("symlink private input accepted")
	}
	// Every ancestor is walked by descriptor: a private nested parent is accepted,
	// the same parent made group/world-writable without sticky, or reached through
	// a symlinked directory, is refused before any decode.
	nested := filepath.Join(dir, "nested")
	if e := os.Mkdir(nested, 0700); e != nil {
		t.Fatal(e)
	}
	nestedBinding := privateFile(t, filepath.Join(nested, "binding"), fixtureBinding(t, "https://management.example.invalid"))
	nestedArgs := append([]string{}, base...)
	nestedArgs[len(nestedArgs)-1] = nestedBinding
	if code := invoke(nestedArgs); code != 0 {
		t.Fatalf("private nested parent refused %d", code)
	}
	parentLink := filepath.Join(dir, "parent-link")
	if os.Symlink(nested, parentLink) != nil {
		t.Fatal("parent symlink")
	}
	nestedArgs[len(nestedArgs)-1] = filepath.Join(parentLink, "binding")
	if invoke(nestedArgs) != 2 {
		t.Fatal("private input through a symlinked parent accepted")
	}
	if e := os.Chmod(nested, 0777); e != nil {
		t.Fatal(e)
	}
	nestedArgs[len(nestedArgs)-1] = nestedBinding
	if invoke(nestedArgs) != 2 {
		t.Fatal("private input under a world-writable non-sticky parent accepted")
	}
	if e := os.Chmod(nested, 0700); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--help"}, {"--version"}} {
		if invoke(args) != 0 {
			t.Fatal("pure metadata refused")
		}
	}
}

// A lock held by another process past the bounded wait is a refusal (exit 2):
// nothing was attempted, so it is not the unknown outcome of exit 4.
func TestLockTimeoutRefusesWithoutAttempt(t *testing.T) {
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(dir, "state")
	if e = os.Mkdir(state, 0700); e != nil {
		t.Fatal(e)
	}
	lock, e := os.OpenFile(filepath.Join(state, "lock"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	binding := privateFile(t, filepath.Join(dir, "binding"), fixtureBinding(t, "https://management.example.invalid"))
	bp := privateFile(t, filepath.Join(dir, "blueprint"), []byte(strings.ReplaceAll(applicationBlueprint, "\n ", "\n")+managementYAML))
	token := privateFile(t, filepath.Join(dir, "token"), []byte("fixture-bearer-canary"))
	var out, errOut bytes.Buffer
	code := runArgs(context.Background(), []string{"preview", "--binding-file", binding, "--state-dir", state, "--token-file", token, "--out", filepath.Join(dir, "preview"), "--blueprint-file", bp, "--blueprint-namespace", ""}, strings.NewReader(""), &out, &errOut)
	if code != 2 {
		t.Fatalf("lock timeout exit=%d want 2 (%s)", code, errOut.String())
	}
	if _, e = os.Stat(filepath.Join(dir, "preview")); !os.IsNotExist(e) {
		t.Fatal("lock timeout wrote output")
	}
}
