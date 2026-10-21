// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The TLS boundary uses backend-valid wire fixtures, not an echo of Specs.
// Its state transitions require the exact fixed routes and request contracts.
// Referenced evaluator-one is a seeded regex evaluator at version 2026-01-01;
// the action fixture starts with rule-one and collection-one already present.
// No live endpoint, credential, tenant discovery or lifecycle is exercised.
func resourceTLS(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/mount/api/plugins/grafana-agento11y-app/resources/eval/") || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("incorrect management boundary")
			http.Error(w, "boundary", 400)
			return
		}
		for _, key := range []string{"X-Scope-OrgID", "X-Grafana-User", "X-Forwarded-User"} {
			if r.Header.Get(key) != "" {
				t.Errorf("unexpected trusted header")
			}
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(ClientOptions{BaseURL: server.URL + "/mount", Token: "fixture-token", ExpectedTenantID: "fixture-tenant", HTTPClient: server.Client(), Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func resourceReply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func resourceBody(t *testing.T, r *http.Request, want string) {
	t.Helper()
	var actual, expected any
	if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
		t.Errorf("request decode: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("body = %#v; want %#v", actual, expected)
	}
}
func resourcePath(r *http.Request) string {
	return strings.TrimPrefix(r.URL.Path, "/mount"+managementPrefix)
}

const resourceTime = "2026-01-02T03:04:05Z"

func ruleWire(id string, enabled bool) map[string]any {
	return map[string]any{"tenant_id": "fixture-tenant", "rule_id": id, "enabled": enabled, "selector": "user_visible_turn", "match": map[string]any{}, "sample_rate": 0, "evaluator_ids": []string{"evaluator-one"}, "execution_mode": "parallel", "created_by": "fixture-actor", "updated_by": "fixture-actor", "created_at": resourceTime, "updated_at": resourceTime}
}
func collectionWire(id string) map[string]any {
	return map[string]any{"tenant_id": "fixture-tenant", "collection_id": id, "name": "Fictional review", "created_by": "fixture-actor", "updated_by": "fixture-actor", "created_at": resourceTime, "updated_at": resourceTime, "member_count": 0}
}
func actionWire(id string, enabled bool) map[string]any {
	return map[string]any{"tenant_id": "fixture-tenant", "action_id": id, "rule_id": "rule-one", "enabled": enabled, "condition": map[string]any{"kind": "all_evaluators_pass"}, "action_config": map[string]any{"kind": "add_to_collection", "collection_ids": []string{"collection-one"}}, "created_by": "fixture-actor", "updated_by": "fixture-actor", "created_at": resourceTime, "updated_at": resourceTime}
}
func versionWire(published bool) map[string]any {
	return map[string]any{"tenant_id": "fixture-tenant", "suite_id": "suite-one", "version": "v1", "test_case_count": 0, "published": published, "created_at": resourceTime, "created_by": "fixture-actor"}
}
func suiteWire(id string) map[string]any {
	return map[string]any{"tenant_id": "fixture-tenant", "suite_id": id, "name": "Fictional checkpoint", "created_at": resourceTime, "updated_at": resourceTime}
}
func caseWire(id string) map[string]any {
	return caseInputWire(id, map[string]any{"text": "A fictional answer."})
}
func caseInputWire(id string, input map[string]any) map[string]any {
	// Pinned case_sanitization.go hashes these canonical content fields, excluding
	// identity/artifacts/evidence, with empty optional maps/tags normalized to null.
	content, _ := json.Marshal(map[string]any{"name": "", "description": "", "category": "", "tags": nil, "input": input, "expected": nil, "metadata": nil})
	evidence := map[string]any{"policy": "secrets-email.v2", "changed_paths": []string{}, "requires_review": false, "content_digest": fmt.Sprintf("sha256:%x", sha256.Sum256(content))}
	return map[string]any{"tenant_id": "fixture-tenant", "suite_id": "suite-one", "suite_version": "v1", "test_case_id": id, "input": input, "metadata": map[string]any{"sigil.sanitization": evidence}, "created_at": resourceTime, "updated_at": resourceTime}
}
func bindingWire() map[string]any {
	return map[string]any{"binding_id": "binding-one", "evaluator_id": "evaluator-one", "evaluator_version": "2026-01-01", "input_mappings": map[string]string{"response": "test_case.input.text"}}
}
func page(w http.ResponseWriter, items []any, next string) {
	resourceReply(w, map[string]any{"items": items, "next_cursor": next})
}
func mustResource[T any](t *testing.T, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestResourceRulesTLS(t *testing.T) {
	var mu sync.Mutex
	created, enabled, deleted := false, false, false
	posts, patches, gets, pages := 0, 0, 0, 0
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + resourcePath(r) {
		case "POST /rules":
			resourceBody(t, r, `{"rule_id":"rule-one","enabled":false,"selector":"user_visible_turn","match":{},"sample_rate":0,"evaluator_ids":["evaluator-one"],"execution_mode":"parallel","alert_rule_uids":[],"filterable_tag_keys":[]}`)
			created = true
			enabled = false
			posts++
			resourceReply(w, ruleWire("rule-one", enabled))
		case "GET /rules/rule-one":
			gets++
			if !created || deleted {
				http.NotFound(w, r)
				return
			}
			resourceReply(w, ruleWire("rule-one", enabled))
		case "GET /rules":
			pages++
			if r.URL.Query().Get("limit") != "100" {
				t.Error("missing bounded page limit")
			}
			if r.URL.Query().Get("cursor") == "" {
				page(w, []any{ruleWire("rule-one", enabled)}, "7")
			} else if r.URL.Query().Get("cursor") == "7" {
				page(w, []any{ruleWire("rule-two", true)}, "")
			} else {
				t.Error("wrong cursor")
				http.Error(w, "cursor", 400)
			}
		case "PATCH /rules/rule-one":
			resourceBody(t, r, `{"enabled":false}`)
			if !created || deleted {
				t.Error("disable nonexistent rule")
			}
			enabled = false
			patches++
			resourceReply(w, ruleWire("rule-one", enabled))
		case "DELETE /rules/rule-one":
			if enabled {
				t.Error("test deleted before disable")
			}
			deleted = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected rule route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	no := false
	zero := 0.0
	empty := map[string]any{}
	names := []string{}
	e, err := client.CreateRule(ctx, RuleSpec{RuleID: "rule-one", Enabled: &no, Selector: "user_visible_turn", Match: &empty, SampleRate: &zero, EvaluatorIDs: []string{"evaluator-one"}, ExecutionMode: "parallel", AlertRuleUIDs: &names, FilterableTagKeys: &names})
	if e = mustResource(t, e, err); e.Enabled || e.SampleRate != 0 || e.Match == nil {
		t.Fatal("lost explicit false/zero/empty")
	}
	e, err = client.GetRule(ctx, "rule-one")
	mustResource(t, e, err)
	all, err := client.ListRules(ctx)
	all = mustResource(t, all, err)
	if len(all) != 2 || all[1].RuleID != "rule-two" {
		t.Fatal("incomplete rules")
	}
	// Simulate a pre-existing enabled consumer before the safety-only operation.
	mu.Lock()
	enabled = true
	mu.Unlock()
	e, err = client.DisableRule(ctx, "rule-one")
	if e = mustResource(t, e, err); e.Enabled {
		t.Fatal("disable had no effect")
	}
	if err := client.DeleteRule(ctx, "rule-one"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !deleted || posts != 1 || patches != 1 || gets != 1 || pages != 2 {
		t.Fatal("missing rule effects")
	}
}

func TestResourceCollectionsTLS(t *testing.T) {
	var mu sync.Mutex
	created, deleted := false, false
	posts, gets, pages := 0, 0, 0
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + resourcePath(r) {
		case "POST /collections":
			resourceBody(t, r, `{"name":"Fictional review","description":""}`)
			created = true
			posts++
			resourceReply(w, collectionWire("collection-one"))
		case "GET /collections/collection-one":
			if !created || deleted {
				t.Error("missing generated collection")
			}
			gets++
			resourceReply(w, collectionWire("collection-one"))
		case "GET /collections":
			pages++
			if r.URL.Query().Get("cursor") == "" {
				page(w, []any{collectionWire("collection-one")}, "collection-one")
			} else if r.URL.Query().Get("cursor") == "collection-one" {
				page(w, []any{collectionWire("collection-two")}, "")
			} else {
				t.Error("wrong collection cursor")
				http.Error(w, "cursor", 400)
			}
		case "DELETE /collections/collection-one":
			deleted = true
			w.WriteHeader(204)
		default:
			t.Error("unexpected collection route")
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	empty := ""
	e, err := client.CreateCollection(ctx, CollectionSpec{Name: "Fictional review", Description: &empty})
	e = mustResource(t, e, err)
	if e.CollectionID != "collection-one" || e.MemberCount != 0 {
		t.Fatal("generated collection identity/count lost")
	}
	e, err = client.GetCollection(ctx, e.CollectionID)
	mustResource(t, e, err)
	all, err := client.ListCollections(ctx)
	all = mustResource(t, all, err)
	if len(all) != 2 || all[1].CollectionID != "collection-two" {
		t.Fatal("incomplete collections")
	}
	if err := client.DeleteCollection(ctx, e.CollectionID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !deleted || posts != 1 || gets != 1 || pages != 2 {
		t.Fatal("missing collection effects")
	}
}

func TestResourceActionsTLS(t *testing.T) {
	var mu sync.Mutex
	created, enabled, deleted := false, false, false
	posts, gets, lists, patches := 0, 0, 0, 0
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + resourcePath(r) {
		case "POST /rules/rule-one/actions":
			resourceBody(t, r, `{"condition":{"kind":"all_evaluators_pass"},"action_config":{"kind":"add_to_collection","collection_ids":["collection-one"]},"enabled":false}`)
			created = true
			posts++
			resourceReply(w, actionWire("action-one", enabled))
		case "GET /rules/rule-one/actions/action-one":
			if !created {
				t.Error("missing action")
			}
			gets++
			resourceReply(w, actionWire("action-one", enabled))
		case "GET /rules/rule-one/actions":
			lists++
			if r.URL.RawQuery != "" {
				t.Error("invented pagination")
			}
			resourceReply(w, map[string]any{"items": []any{actionWire("action-one", enabled), actionWire("action-two", true)}})
		case "PATCH /rules/rule-one/actions/action-one":
			resourceBody(t, r, `{"enabled":false}`)
			enabled = false
			patches++
			resourceReply(w, actionWire("action-one", enabled))
		case "DELETE /rules/rule-one/actions/action-one":
			if enabled {
				t.Error("test deleted before disable")
			}
			deleted = true
			w.WriteHeader(204)
		default:
			t.Error("unexpected action route")
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	no := false
	e, err := client.CreateRuleAction(ctx, "rule-one", RuleActionSpec{Condition: RuleActionCondition{Kind: "all_evaluators_pass"}, ActionConfig: RuleActionConfig{Kind: "add_to_collection", CollectionIDs: []string{"collection-one"}}, Enabled: &no})
	e = mustResource(t, e, err)
	if e.ActionID != "action-one" || e.RuleID != "rule-one" || e.Enabled {
		t.Fatal("action identity/false lost")
	}
	e, err = client.GetRuleAction(ctx, "rule-one", e.ActionID)
	mustResource(t, e, err)
	all, err := client.ListRuleActions(ctx, "rule-one")
	all = mustResource(t, all, err)
	if len(all) != 2 {
		t.Fatal("incomplete actions")
	}
	mu.Lock()
	enabled = true
	mu.Unlock()
	e, err = client.DisableRuleAction(ctx, "rule-one", e.ActionID)
	if e = mustResource(t, e, err); e.Enabled {
		t.Fatal("disable had no effect")
	}
	if err := client.DeleteRuleAction(ctx, "rule-one", e.ActionID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !deleted || posts != 1 || gets != 1 || lists != 1 || patches != 1 {
		t.Fatal("missing action effects")
	}
}

func TestResourceSuiteCheckpointTLS(t *testing.T) {
	var mu sync.Mutex
	created, draft, caseCreated, bound, published := false, false, false, false, false
	calls := map[string]int{}
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := r.Method + " " + resourcePath(r)
		calls[key]++
		switch key {
		case "POST /test-suites":
			resourceBody(t, r, `{"suite_id":"suite-one","name":"Fictional checkpoint","description":"","tags":[]}`)
			created = true
			resourceReply(w, suiteWire("suite-one"))
		case "GET /test-suites/suite-one":
			if !created {
				t.Error("suite missing")
			}
			s := suiteWire("suite-one")
			if draft {
				v := versionWire(published)
				if caseCreated {
					v["test_case_count"] = 1
				}
				if bound {
					v["bindings"] = []any{bindingWire()}
				}
				s["versions"] = []any{v}
			}
			resourceReply(w, s)
		case "GET /test-suites":
			if r.URL.Query().Get("cursor") == "" {
				page(w, []any{suiteWire("suite-one")}, "8")
			} else if r.URL.Query().Get("cursor") == "8" {
				page(w, []any{suiteWire("suite-two")}, "")
			} else {
				t.Error("wrong suite cursor")
				http.Error(w, "cursor", 400)
			}
		case "POST /test-suites/suite-one/versions":
			if !created || draft {
				t.Error("not a fresh draft")
			}
			resourceBody(t, r, `{"changelog":"","empty_draft":true}`)
			draft = true
			resourceReply(w, versionWire(false))
		case "POST /test-suites/suite-one/versions/v1/test-cases":
			if !draft || published || caseCreated {
				t.Error("case not create-only")
			}
			resourceBody(t, r, `{"test_case_id":"case-one","name":"","tags":[],"input":{"text":"A fictional answer."},"expected":{},"metadata":{},"artifact_refs":[],"sanitization_reviewed":false,"sanitization_content_digest":"" ,"create_only":true}`)
			caseCreated = true
			resourceReply(w, caseWire("case-one"))
		case "GET /test-suites/suite-one/versions/v1/test-cases/case-one":
			if !caseCreated {
				t.Error("case missing")
			}
			resourceReply(w, caseWire("case-one"))
		case "GET /test-suites/suite-one/versions/v1/test-cases":
			if r.URL.Query().Get("cursor") == "" {
				page(w, []any{caseWire("case-one")}, "9")
			} else if r.URL.Query().Get("cursor") == "9" {
				page(w, []any{}, "")
			} else {
				t.Error("wrong case cursor")
				http.Error(w, "cursor", 400)
			}
		case "GET /test-suites/suite-one/versions/v1/bindings":
			items := []any{}
			if bound {
				items = append(items, bindingWire())
			}
			resourceReply(w, map[string]any{"items": items})
		case "PUT /test-suites/suite-one/versions/v1/bindings":
			if !draft || bound || published {
				t.Error("not initial bindings")
			}
			resourceBody(t, r, `{"bindings":[{"binding_id":"binding-one","evaluator_id":"evaluator-one","evaluator_version":"2026-01-01","input_mappings":{"response":"test_case.input.text"}}]}`)
			bound = true
			resourceReply(w, map[string]any{"items": []any{bindingWire()}})
		case "POST /test-suites/suite-one/versions/v1:publish":
			if !caseCreated || !bound || published {
				t.Error("invalid publication")
			}
			resourceBody(t, r, `{"reviewed_publication":{"cases":[{"test_case_id":"case-one","input":{"text":"A fictional answer."},"tags":[],"expected":{},"metadata":{},"artifact_refs":[]}],"bindings":[{"binding_id":"binding-one","evaluator_id":"evaluator-one","evaluator_version":"2026-01-01","input_mappings":{"response":"test_case.input.text"}}]}}`)
			published = true
			v := versionWire(true)
			v["test_case_count"] = 1
			v["bindings"] = []any{bindingWire()}
			v["published_by"] = "fixture-actor"
			v["published_at"] = resourceTime
			resourceReply(w, v)
		default:
			t.Errorf("unexpected suite route %s", key)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	empty := ""
	tags := []string{}
	e, err := client.CreateSuite(ctx, SuiteSpec{SuiteID: "suite-one", Name: "Fictional checkpoint", Description: &empty, Tags: &tags})
	mustResource(t, e, err)
	e, err = client.GetSuite(ctx, "suite-one")
	mustResource(t, e, err)
	suites, err := client.ListSuites(ctx)
	suites = mustResource(t, suites, err)
	if len(suites) != 2 {
		t.Fatal("incomplete suite list")
	}
	v, err := client.CreateDraft(ctx, "suite-one", DraftSpec{Changelog: &empty})
	v = mustResource(t, v, err)
	if v.Version != "v1" || v.Published {
		t.Fatal("wrong generated draft")
	}
	obj := map[string]any{}
	refs := []ArtifactRef{}
	no := false
	testCase, err := client.CreateCase(ctx, "suite-one", "v1", CaseSpec{TestCaseID: "case-one", Name: &empty, Tags: &tags, Input: map[string]any{"text": "A fictional answer."}, Expected: &obj, Metadata: &obj, ArtifactRefs: &refs, SanitizationReviewed: &no, SanitizationContentDigest: &empty})
	testCase = mustResource(t, testCase, err)
	if testCase.Metadata["sigil.sanitization"] == nil {
		t.Fatal("sanitization evidence lost")
	}
	testCase, err = client.GetCase(ctx, "suite-one", "v1", "case-one")
	mustResource(t, testCase, err)
	cases, err := client.ListCases(ctx, "suite-one", "v1")
	cases = mustResource(t, cases, err)
	if len(cases) != 1 {
		t.Fatal("incomplete cases")
	}
	binding := SuiteBinding{BindingID: "binding-one", EvaluatorID: "evaluator-one", EvaluatorVersion: "2026-01-01", InputMappings: map[string]string{"response": "test_case.input.text"}}
	bindings, err := client.PutInitialBindings(ctx, "suite-one", "v1", []SuiteBinding{binding})
	bindings = mustResource(t, bindings, err)
	if len(bindings) != 1 {
		t.Fatal("bindings not observed")
	}
	bindings, err = client.GetBindings(ctx, "suite-one", "v1")
	mustResource(t, bindings, err)
	if _, err := client.PutInitialBindings(ctx, "suite-one", "v1", []SuiteBinding{}); err == nil {
		t.Fatal("replaced populated draft")
	}
	review := PublicationReview{Cases: []CaseSnapshot{{TestCaseID: "case-one", Input: map[string]any{"text": "A fictional answer."}, Tags: []string{}, Expected: map[string]any{}, Metadata: map[string]any{}, ArtifactRefs: []ArtifactRef{}}}, Bindings: []SuiteBinding{binding}}
	v, err = client.Publish(ctx, "suite-one", "v1", review)
	v = mustResource(t, v, err)
	if !v.Published || v.TestCaseCount != 1 {
		t.Fatal("publication not observed")
	}
	e, err = client.GetSuite(ctx, "suite-one")
	e = mustResource(t, e, err)
	if len(e.Versions) != 1 || !e.Versions[0].Published || len(e.Versions[0].Bindings) != 1 {
		t.Fatal("checkpoint readback incomplete")
	}
	if _, err := client.PutInitialBindings(ctx, "suite-one", "v1", []SuiteBinding{}); err == nil {
		t.Fatal("replaced published bindings")
	}
	mu.Lock()
	defer mu.Unlock()
	if !published || calls["PUT /test-suites/suite-one/versions/v1/bindings"] != 1 || calls["POST /test-suites/suite-one/versions/v1:publish"] != 1 || calls["GET /test-suites/suite-one/versions/v1/test-cases"] != 2 {
		t.Fatal("missing checkpoint effects")
	}
}

// The public convenience method keeps its read/read/write boundary. The private
// mutation seam must issue just one PUT after its caller has done those reads.
// These are request/receipt observations, not lifecycle freshness/custody proof.
func TestResourceInitialBindingsSingleMutationTLS(t *testing.T) {
	for _, entry := range []string{"public", "single mutation"} {
		for _, contradictory := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/contradictory=%t", entry, contradictory), func(t *testing.T) {
				var mu sync.Mutex
				var calls []string
				committed := false
				client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					key := r.Method + " " + resourcePath(r)
					calls = append(calls, key)
					if r.URL.RawQuery != "" {
						t.Error("unexpected binding query")
					}
					switch key {
					case "GET /test-suites/suite-one":
						suite := suiteWire("suite-one")
						suite["versions"] = []any{versionWire(false)}
						resourceReply(w, suite)
					case "GET /test-suites/suite-one/versions/v1/bindings":
						resourceReply(w, map[string]any{"items": []any{}})
					case "PUT /test-suites/suite-one/versions/v1/bindings":
						resourceBody(t, r, `{"bindings":[{"binding_id":" binding-two ","evaluator_id":" evaluator-one ","evaluator_version":" 2026-01-01 ","input_mappings":{" context ":" test_case.input.text "},"report_role":" diagnostic "},{"binding_id":"binding-one","evaluator_id":"evaluator-one","evaluator_version":"2026-01-01","input_mappings":{"response":"test_case.input.text"}}]}`)
						if committed {
							t.Error("mutation automatically retried")
						}
						// The modeled write has committed before the success receipt,
						// including when that receipt lies about the same binding ID.
						committed = true
						one := bindingWire()
						if contradictory {
							one["input_mappings"] = map[string]string{"response": "test_case.expected.text"}
						}
						two := map[string]any{"binding_id": "binding-two", "evaluator_id": "evaluator-one", "evaluator_version": "2026-01-01", "input_mappings": map[string]string{"context": "test_case.input.text"}, "report_role": "diagnostic"}
						resourceReply(w, map[string]any{"items": []any{one, two}})
					default:
						t.Errorf("unexpected initial binding request %s", key)
						http.NotFound(w, r)
					}
				})
				bindings := []SuiteBinding{
					{BindingID: " binding-two ", EvaluatorID: " evaluator-one ", EvaluatorVersion: " 2026-01-01 ", InputMappings: map[string]string{" context ": " test_case.input.text "}, ReportRole: " diagnostic "},
					{BindingID: "binding-one", EvaluatorID: "evaluator-one", EvaluatorVersion: "2026-01-01", InputMappings: map[string]string{"response": "test_case.input.text"}},
				}
				put := client.PutInitialBindings
				if entry == "single mutation" {
					put = client.putInitialBindingsOnce
				}
				observed, err := put(context.Background(), "suite-one", "v1", bindings)
				if contradictory {
					if err == nil || observed != nil {
						t.Error("contradictory committed binding receipt accepted")
					}
				} else if err != nil || len(observed) != 2 || observed[0].BindingID != "binding-one" {
					t.Errorf("normalized complete receipt lost: %v", err)
				}
				if bindings[0].BindingID != " binding-two " || bindings[0].InputMappings[" context "] != " test_case.input.text " {
					t.Error("normalization mutated authored bindings")
				}
				want := []string{"PUT /test-suites/suite-one/versions/v1/bindings"}
				if entry == "public" {
					want = append([]string{"GET /test-suites/suite-one", "GET /test-suites/suite-one/versions/v1/bindings"}, want...)
				}
				mu.Lock()
				defer mu.Unlock()
				if !committed || !reflect.DeepEqual(calls, want) {
					t.Fatalf("request boundary: committed=%t calls=%v; want %v", committed, calls, want)
				}
			})
		}
	}
}

// Observe each family's real TLS response with contradictions and missing fields.
// These are not transport-helper tests: they pin the new scoped typed decoders.
func TestResourceContradictoryObservationsTLS(t *testing.T) {
	tests := []struct {
		name   string
		wire   func() map[string]any
		mutate func(map[string]any)
		call   func(*Client) error
	}{
		{"rule tenant", func() map[string]any { return ruleWire("rule-one", true) }, func(m map[string]any) { m["tenant_id"] = "other-tenant" }, func(c *Client) error { _, err := c.GetRule(context.Background(), "rule-one"); return err }},
		{"rule id", func() map[string]any { return ruleWire("rule-two", true) }, func(m map[string]any) {}, func(c *Client) error { _, err := c.GetRule(context.Background(), "rule-one"); return err }},
		{"rule missing false", func() map[string]any { return ruleWire("rule-one", false) }, func(m map[string]any) { delete(m, "enabled") }, func(c *Client) error { _, err := c.GetRule(context.Background(), "rule-one"); return err }},
		{"rule disable unchanged", func() map[string]any { return ruleWire("rule-one", true) }, func(m map[string]any) {}, func(c *Client) error { _, err := c.DisableRule(context.Background(), "rule-one"); return err }},
		{"collection generated empty", func() map[string]any { return collectionWire("") }, func(m map[string]any) {}, func(c *Client) error {
			_, err := c.CreateCollection(context.Background(), CollectionSpec{Name: "Fictional review"})
			return err
		}},
		{"collection tenant", func() map[string]any { return collectionWire("collection-one") }, func(m map[string]any) { m["tenant_id"] = "other-tenant" }, func(c *Client) error { _, err := c.GetCollection(context.Background(), "collection-one"); return err }},
		{"collection count missing", func() map[string]any { return collectionWire("collection-one") }, func(m map[string]any) { delete(m, "member_count") }, func(c *Client) error { _, err := c.GetCollection(context.Background(), "collection-one"); return err }},
		{"action parent", func() map[string]any { return actionWire("action-one", false) }, func(m map[string]any) { m["rule_id"] = "other-rule" }, func(c *Client) error {
			_, err := c.GetRuleAction(context.Background(), "rule-one", "action-one")
			return err
		}},
		{"action disable unchanged", func() map[string]any { return actionWire("action-one", true) }, func(m map[string]any) {}, func(c *Client) error {
			_, err := c.DisableRuleAction(context.Background(), "rule-one", "action-one")
			return err
		}},
		{"action unsupported score", func() map[string]any { return actionWire("action-one", false) }, func(m map[string]any) {
			m["condition"] = map[string]any{"kind": "score_value", "score": map[string]any{}}
		}, func(c *Client) error {
			_, err := c.GetRuleAction(context.Background(), "rule-one", "action-one")
			return err
		}},
		{"suite nested tenant", func() map[string]any { return suiteWire("suite-one") }, func(m map[string]any) {
			v := versionWire(false)
			v["tenant_id"] = "other-tenant"
			m["versions"] = []any{v}
		}, func(c *Client) error { _, err := c.GetSuite(context.Background(), "suite-one"); return err }},
		{"draft generated version", func() map[string]any { return versionWire(false) }, func(m map[string]any) { m["version"] = "" }, func(c *Client) error {
			_, err := c.CreateDraft(context.Background(), "suite-one", DraftSpec{})
			return err
		}},
		{"draft not empty", func() map[string]any { return versionWire(false) }, func(m map[string]any) { m["test_case_count"] = 1 }, func(c *Client) error {
			_, err := c.CreateDraft(context.Background(), "suite-one", DraftSpec{})
			return err
		}},
		{"case parent", func() map[string]any { return caseWire("case-one") }, func(m map[string]any) { m["suite_version"] = "v2" }, func(c *Client) error {
			_, err := c.GetCase(context.Background(), "suite-one", "v1", "case-one")
			return err
		}},
		{"case tenant null", func() map[string]any { return caseWire("case-one") }, func(m map[string]any) { m["tenant_id"] = nil }, func(c *Client) error {
			_, err := c.GetCase(context.Background(), "suite-one", "v1", "case-one")
			return err
		}},
		{"case missing input", func() map[string]any { return caseWire("case-one") }, func(m map[string]any) { delete(m, "input") }, func(c *Client) error {
			_, err := c.GetCase(context.Background(), "suite-one", "v1", "case-one")
			return err
		}},
		{"bindings null mappings", func() map[string]any { return map[string]any{"items": []any{bindingWire()}} }, func(m map[string]any) { m["items"].([]any)[0].(map[string]any)["input_mappings"] = nil }, func(c *Client) error { _, err := c.GetBindings(context.Background(), "suite-one", "v1"); return err }},
		{"publish unchanged", func() map[string]any { return versionWire(false) }, func(m map[string]any) {}, func(c *Client) error {
			_, err := c.Publish(context.Background(), "suite-one", "v1", PublicationReview{Cases: []CaseSnapshot{}, Bindings: []SuiteBinding{}})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) { m := tt.wire(); tt.mutate(m); resourceReply(w, m) })
			if err := tt.call(client); err == nil {
				t.Fatal("contradictory observation accepted")
			}
		})
	}
}

// A mutation may have committed even when its success receipt contradicts the
// request. Reject that receipt, never retry it or fabricate case inventory.
func TestResourceReceiptIntegrityTLS(t *testing.T) {
	changes := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"evaluator", func(b map[string]any) { b["evaluator_id"] = "evaluator-two" }},
		{"version", func(b map[string]any) { b["evaluator_version"] = "2026-02-01" }},
		{"mappings", func(b map[string]any) { b["input_mappings"] = map[string]string{"response": "test_case.expected.text"} }},
		{"report role", func(b map[string]any) { b["report_role"] = "diagnostic" }},
	}
	for _, operation := range []string{"initial", "single", "publish"} {
		names := []string{"evaluator", "version", "mappings", "report role"}
		if operation == "publish" {
			names = append(names, "count", "missing bindings")
		}
		for _, name := range names {
			t.Run(operation+"/"+name, func(t *testing.T) {
				var mu sync.Mutex
				committed, published := false, false
				writes := 0
				client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch r.Method + " " + resourcePath(r) {
					case "GET /test-suites/suite-one":
						s := suiteWire("suite-one")
						s["versions"] = []any{versionWire(false)}
						resourceReply(w, s)
					case "GET /test-suites/suite-one/versions/v1/bindings":
						resourceReply(w, map[string]any{"items": []any{}})
					case "PUT /test-suites/suite-one/versions/v1/bindings":
						if operation == "publish" || committed {
							t.Error("unexpected/repeated initial write")
						}
						resourceBody(t, r, `{"bindings":[{"binding_id":"binding-one","evaluator_id":"evaluator-one","evaluator_version":"2026-01-01","input_mappings":{"response":"test_case.input.text"}}]}`)
						committed = true
						writes++
						b := bindingWire()
						for _, change := range changes {
							if change.name == name {
								change.mutate(b)
							}
						}
						resourceReply(w, map[string]any{"items": []any{b}})
					case "POST /test-suites/suite-one/versions/v1:publish":
						if operation != "publish" || published {
							t.Error("unexpected/repeated publish")
						}
						resourceBody(t, r, `{"reviewed_publication":{"cases":[{"test_case_id":"case-one","input":{"text":"A fictional answer."},"tags":null,"expected":null,"metadata":null,"artifact_refs":null}],"bindings":[{"binding_id":"binding-one","evaluator_id":"evaluator-one","evaluator_version":"2026-01-01","input_mappings":{"response":"test_case.input.text"}}]}}`)
						// The remote checkpoint is published before its contradictory receipt.
						committed = true
						published = true
						writes++
						v := versionWire(true)
						v["test_case_count"] = 1
						b := bindingWire()
						for _, change := range changes {
							if change.name == name {
								change.mutate(b)
							}
						}
						v["bindings"] = []any{b}
						v["published_by"] = "fixture-actor"
						v["published_at"] = resourceTime
						if name == "count" {
							v["test_case_count"] = 0
						}
						if name == "missing bindings" {
							delete(v, "bindings")
						}
						resourceReply(w, v)
					default:
						t.Errorf("unexpected receipt route %s %s", r.Method, resourcePath(r))
						http.NotFound(w, r)
					}
				})
				binding := SuiteBinding{BindingID: "binding-one", EvaluatorID: "evaluator-one", EvaluatorVersion: "2026-01-01", InputMappings: map[string]string{"response": "test_case.input.text"}}
				var err error
				if operation == "initial" {
					_, err = client.PutInitialBindings(context.Background(), "suite-one", "v1", []SuiteBinding{binding})
				} else if operation == "single" {
					_, err = client.putInitialBindingsOnce(context.Background(), "suite-one", "v1", []SuiteBinding{binding})
				} else {
					_, err = client.Publish(context.Background(), "suite-one", "v1", PublicationReview{Cases: []CaseSnapshot{{TestCaseID: "case-one", Input: map[string]any{"text": "A fictional answer."}}}, Bindings: []SuiteBinding{binding}})
				}
				if err == nil {
					t.Error("contradictory committed receipt accepted")
				}
				mu.Lock()
				defer mu.Unlock()
				if !committed || writes != 1 || operation == "publish" && !published {
					t.Fatal("mutation did not commit exactly once")
				}
			})
		}
	}
}

// Source normalization trims all binding scalars and mapping keys/selectors and
// sorts by binding ID. These controls must not turn legitimate receipts red.
func TestResourceNormalizedReceiptsTLS(t *testing.T) {
	for _, operation := range []string{"initial", "publish"} {
		t.Run(operation, func(t *testing.T) {
			var mu sync.Mutex
			committed, writes := false, 0
			client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method + " " + resourcePath(r) {
				case "GET /test-suites/suite-one":
					s := suiteWire("suite-one")
					s["versions"] = []any{versionWire(false)}
					resourceReply(w, s)
				case "GET /test-suites/suite-one/versions/v1/bindings":
					resourceReply(w, map[string]any{"items": []any{}})
				case "PUT /test-suites/suite-one/versions/v1/bindings", "POST /test-suites/suite-one/versions/v1:publish":
					if committed {
						t.Error("repeated write")
					}
					committed = true
					writes++
					// Pin the padded request independently of implementation normalization.
					var request struct {
						Bindings []json.RawMessage `json:"bindings"`
						Review   *struct {
							Bindings []json.RawMessage `json:"bindings"`
						} `json:"reviewed_publication"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					bindings := request.Bindings
					if operation == "publish" {
						if request.Review == nil {
							t.Error("missing review")
							http.Error(w, "missing review", 400)
							return
						}
						bindings = request.Review.Bindings
					}
					if len(bindings) != 2 {
						t.Error("missing complete bindings")
					} else {
						var first map[string]any
						_ = json.Unmarshal(bindings[0], &first)
						if first["binding_id"] != " binding-two " {
							t.Error("fixture did not submit unsorted padded bindings")
						}
					}
					one := bindingWire()
					two := map[string]any{"binding_id": "binding-two", "evaluator_id": "evaluator-one", "evaluator_version": "2026-01-01", "input_mappings": map[string]string{"context": "test_case.input.text"}, "report_role": "diagnostic"}
					if operation == "initial" {
						resourceReply(w, map[string]any{"items": []any{one, two}})
					} else {
						v := versionWire(true)
						v["test_case_count"] = 1
						v["bindings"] = []any{one, two}
						v["published_by"] = "fixture-actor"
						v["published_at"] = resourceTime
						resourceReply(w, v)
					}
				default:
					t.Error("unexpected normalized receipt route")
					http.NotFound(w, r)
				}
			})
			bindings := []SuiteBinding{
				{BindingID: " binding-two ", EvaluatorID: " evaluator-one ", EvaluatorVersion: " 2026-01-01 ", InputMappings: map[string]string{" context ": " test_case.input.text "}, ReportRole: " diagnostic "},
				{BindingID: " binding-one ", EvaluatorID: " evaluator-one ", EvaluatorVersion: " 2026-01-01 ", InputMappings: map[string]string{" response ": " test_case.input.text "}, ReportRole: " "},
			}
			if operation == "initial" {
				observed, err := client.PutInitialBindings(context.Background(), "suite-one", "v1", bindings)
				observed = mustResource(t, observed, err)
				if len(observed) != 2 || observed[0].BindingID != "binding-one" {
					t.Fatal("source-sorted receipt lost")
				}
			} else {
				observed, err := client.Publish(context.Background(), "suite-one", "v1", PublicationReview{Cases: []CaseSnapshot{{TestCaseID: "case-one", Input: map[string]any{"text": "A fictional answer."}}}, Bindings: bindings})
				observed = mustResource(t, observed, err)
				if !observed.Published || len(observed.Bindings) != 2 {
					t.Fatal("source-normalized publication lost")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !committed || writes != 1 {
				t.Fatal("missing normalized remote commit")
			}
		})
	}
}

func TestResourceInitialBindingsIdentityTLS(t *testing.T) {
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + resourcePath(r) {
		case "GET /test-suites/suite-one":
			suite := suiteWire("suite-one")
			suite["versions"] = []any{versionWire(false)}
			resourceReply(w, suite)
		case "GET /test-suites/suite-one/versions/v1/bindings":
			resourceReply(w, map[string]any{"items": []any{}})
		case "PUT /test-suites/suite-one/versions/v1/bindings":
			b := bindingWire()
			b["binding_id"] = "other-binding"
			resourceReply(w, map[string]any{"items": []any{b}})
		default:
			t.Error("unexpected initial binding route")
			http.NotFound(w, r)
		}
	})
	_, err := client.PutInitialBindings(context.Background(), "suite-one", "v1", []SuiteBinding{{BindingID: "binding-one", EvaluatorID: "evaluator-one", EvaluatorVersion: "2026-01-01", InputMappings: map[string]string{"response": "test_case.input.text"}}})
	if err == nil {
		t.Fatal("returned binding identity differs from request")
	}
}

func TestResourceOptionalSourceFieldsTLS(t *testing.T) {
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		switch resourcePath(r) {
		case "/test-suites/suite-one/versions/v1/test-cases/case-one":
			m := caseWire("case-one")
			delete(m, "tenant_id")
			delete(m, "suite_id")
			delete(m, "suite_version")
			delete(m, "created_at")
			delete(m, "updated_at")
			resourceReply(w, m)
		case "/test-suites/suite-one/versions":
			m := versionWire(false)
			delete(m, "tenant_id")
			resourceReply(w, m)
		default:
			http.NotFound(w, r)
		}
	})
	e, err := client.GetCase(context.Background(), "suite-one", "v1", "case-one")
	mustResource(t, e, err)
	v, err := client.CreateDraft(context.Background(), "suite-one", DraftSpec{})
	mustResource(t, v, err)
}

func TestResourceScopedInventoriesTLS(t *testing.T) {
	for _, family := range []string{"actions", "suites", "cases", "bindings"} {
		t.Run(family, func(t *testing.T) {
			client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
				var wire any
				switch family {
				case "actions":
					a := actionWire("action-one", false)
					a["rule_id"] = "other-rule"
					wire = a
				case "suites":
					s := suiteWire("suite-one")
					v := versionWire(false)
					v["suite_id"] = "other-suite"
					s["versions"] = []any{v}
					wire = s
				case "cases":
					a := caseWire("case-one")
					a["suite_id"] = "other-suite"
					wire = a
				case "bindings":
					wire = bindingWire()
				}
				// Binding identity duplicates reject the whole nonpaginated inventory.
				items := []any{wire}
				if family == "bindings" {
					items = append(items, wire)
				}
				if family == "actions" || family == "bindings" {
					resourceReply(w, map[string]any{"items": items})
				} else {
					page(w, items, "")
				}
			})
			var err error
			ctx := context.Background()
			switch family {
			case "actions":
				_, err = client.ListRuleActions(ctx, "rule-one")
			case "suites":
				_, err = client.ListSuites(ctx)
			case "cases":
				_, err = client.ListCases(ctx, "suite-one", "v1")
			case "bindings":
				_, err = client.GetBindings(ctx, "suite-one", "v1")
			}
			if err == nil {
				t.Fatal("invalid scoped inventory accepted")
			}
		})
	}
}

// Open managed objects must survive observation without rounding JSON integers.
func TestResourceNumericFidelityTLS(t *testing.T) {
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) {
		wire := caseInputWire("case-one", map[string]any{"number": json.Number("9007199254740993"), "zero": json.Number("0"), "flag": false, "empty": []any{}})
		resourceReply(w, wire)
	})
	observed, err := client.GetCase(context.Background(), "suite-one", "v1", "case-one")
	observed = mustResource(t, observed, err)
	encoded, err := json.Marshal(observed.Input)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"empty":[],"flag":false,"number":9007199254740993,"zero":0}` {
		t.Fatalf("lossy managed object: %s", encoded)
	}
}

func TestResourceNoUnreviewedMutationTLS(t *testing.T) {
	calls := 0
	client := resourceTLS(t, func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected mutation", 500) })
	ctx := context.Background()
	checks := []func() error{
		func() error { _, err := client.CreateSuite(ctx, SuiteSpec{Name: "missing caller ID"}); return err },
		func() error {
			_, err := client.CreateCase(ctx, "suite-one", "v1", CaseSpec{TestCaseID: "case-one", Input: map[string]any{"text": "fiction"}, ArtifactRefs: &[]ArtifactRef{{ArtifactID: "artifact-one"}}})
			return err
		},
		func() error { _, err := client.Publish(ctx, "suite-one", "v1", PublicationReview{}); return err },
		func() error { _, err := client.PutInitialBindings(ctx, "suite-one", "v1", nil); return err },
		func() error { _, err := client.putInitialBindingsOnce(ctx, "suite-one", "v1", nil); return err },
		func() error {
			_, err := client.putInitialBindingsOnce(ctx, "../suite", "v1", []SuiteBinding{})
			return err
		},
		func() error {
			_, err := client.putInitialBindingsOnce(ctx, "suite-one", "v01", []SuiteBinding{})
			return err
		},
		func() error {
			_, err := client.putInitialBindingsOnce(ctx, "suite-one", "v1", []SuiteBinding{{BindingID: "binding-one", EvaluatorID: "evaluator-one", EvaluatorVersion: "2026-01-01", InputMappings: map[string]string{"response": "test_case.input.text", " response ": "test_case.expected.text"}}})
			return err
		},
		func() error {
			_, err := client.CreateRuleAction(ctx, "rule-one", RuleActionSpec{Condition: RuleActionCondition{Kind: "score_value"}})
			return err
		},
	}
	for i, check := range checks {
		if err := check(); err == nil {
			t.Fatal(fmt.Sprintf("unadmitted mutation %d accepted", i))
		}
	}
	if calls != 0 {
		t.Fatal("unadmitted mutation dispatched")
	}
}
