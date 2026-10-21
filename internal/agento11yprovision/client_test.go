// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	provision "github.com/rknightion/synthkit/internal/agento11yprovision"
)

const prefix = "/grafana/api/plugins/grafana-agento11y-app/resources/eval"
const evaluatorJSON = `{"tenant_id":"test-tenant","evaluator_id":"clean_input","version":"1","kind":"regex","config":{"pattern":"unsafe","target":"input","reject":true},"output_keys":[{"key":"clean","type":"bool","pass_value":false}]}`

// The pinned hook handler requires an effect even when disabled. This rule
// keeps its existing evaluator while clearing optional filter/redaction state.
const hookJSON = `{"tenant_id":"test-tenant","rule_id":"input_guard","enabled":false,"phase":"preflight","priority":0,"selector":"all","match":{},"evaluator_ids":["clean_input"],"action_on_fail":"deny","short_circuit":false}`

func pointer[T any](v T) *T { return &v }

func evaluatorSpec() provision.EvaluatorSpec {
	return provision.EvaluatorSpec{EvaluatorID: "clean_input", Version: "1", Kind: "regex",
		Config:     map[string]any{"pattern": "unsafe", "target": "input", "reject": true},
		OutputKeys: []provision.OutputKey{{Key: "clean", Type: "bool", Enum: []string{}, PassMatch: []string{}, PassValue: pointer(false)}}}
}

func hookSpec() provision.HookRuleSpec {
	return provision.HookRuleSpec{RuleID: "input_guard", Enabled: pointer(false), Phase: "preflight", Priority: pointer(0),
		Selector: "all", Match: map[string]any{}, EvaluatorIDs: []string{"clean_input"}, ActionOnFail: "deny", ShortCircuit: pointer(false),
		ToolFilter: &provision.HookToolFilter{BlockedNames: []string{}}, Redact: &provision.HookRedactConfig{Patterns: []provision.HookRedactPattern{}}}
}

func newClient(t *testing.T, s *httptest.Server, amend func(*provision.ClientOptions)) *provision.Client {
	t.Helper()
	opts := provision.ClientOptions{BaseURL: s.URL + "/grafana/", Token: "bearer-canary", ExpectedTenantID: "test-tenant", HTTPClient: s.Client(), Timeout: 2 * time.Second}
	if amend != nil {
		amend(&opts)
	}
	c, err := provision.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func jsonResponse(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = io.WriteString(w, value)
}

func assertJSON(t *testing.T, r *http.Request, expected string) {
	t.Helper()
	body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil {
		t.Error(err)
		return
	}
	var got, want any
	if json.Unmarshal(body, &got) != nil || json.Unmarshal([]byte(expected), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected request JSON: %s; expected %s", body, expected)
	}
}

// The fixed handler rejects the wrong prefix/method/auth/body instead of echoing
// the implementation's own request back as a supposedly expected observation.
func TestClientContract(t *testing.T) {
	type step struct{ method, path, query, body, response string }
	steps := []step{
		{"POST", "/evaluators", "", `{"evaluator_id":"clean_input","version":"1","kind":"regex","config":{"pattern":"unsafe","target":"input","reject":true},"output_keys":[{"key":"clean","type":"bool","enum":[],"pass_match":[],"pass_value":false}]}`, evaluatorJSON},
		{"GET", "/evaluators/clean_input", "", "", evaluatorJSON},
		{"GET", "/evaluators", "limit=100", "", `{"items":[` + evaluatorJSON + `],"next_cursor":""}`},
		{"POST", "/hook-rules", "", `{"rule_id":"input_guard","enabled":false,"phase":"preflight","priority":0,"selector":"all","match":{},"evaluator_ids":["clean_input"],"action_on_fail":"deny","short_circuit":false,"tool_filter":{"blocked_names":[]},"redact":{"patterns":[]}}`, hookJSON},
		{"GET", "/hook-rules/input_guard", "", "", hookJSON},
		{"PUT", "/hook-rules/input_guard", "", `{"rule_id":"input_guard","enabled":false,"phase":"preflight","priority":0,"selector":"all","match":{},"evaluator_ids":["clean_input"],"action_on_fail":"deny","short_circuit":false,"tool_filter":{"blocked_names":[]},"redact":{"patterns":[]}}`, hookJSON},
		{"GET", "/hook-rules", "limit=100", "", `{"items":[` + hookJSON + `],"next_cursor":""}`},
		{"DELETE", "/hook-rules/input_guard", "", "", ""},
		{"DELETE", "/evaluators/clean_input", "", "", ""},
	}
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(calls.Add(1)) - 1
		if i >= len(steps) {
			t.Error("unexpected extra request (possible mutation retry)")
			http.Error(w, "unexpected", 500)
			return
		}
		want := steps[i]
		if r.URL.Path != prefix+want.path || r.Method != want.method || r.URL.RawQuery != want.query {
			t.Errorf("wrong request: %s %s?%s; expected %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery, want.method, prefix+want.path, want.query)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer bearer-canary" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Scope-OrgID") != "" || r.Header.Get("X-Grafana-User") != "" || r.Header.Get("X-Grafana-Trusted-Actor") != "" {
			t.Error("wrong auth or caller-forged identity headers")
			http.Error(w, "unauthorized", 401)
			return
		}
		if want.body != "" {
			assertJSON(t, r, want.body)
		} else if body, _ := io.ReadAll(r.Body); len(body) != 0 {
			t.Error("body on read/delete")
		}
		if want.response == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jsonResponse(w, want.response)
	}))
	defer s.Close()
	c := newClient(t, s, nil)
	ctx := context.Background()
	if e, err := c.CreateEvaluator(ctx, evaluatorSpec()); err != nil || e.EvaluatorID != "clean_input" || e.OutputKeys[0].PassValue == nil || *e.OutputKeys[0].PassValue {
		t.Fatalf("create evaluator: %#v, %v", e, err)
	}
	if _, err := c.GetEvaluator(ctx, "clean_input"); err != nil {
		t.Fatal(err)
	}
	if list, err := c.ListEvaluators(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list evaluators: %v, %v", list, err)
	}
	if h, err := c.CreateHookRule(ctx, hookSpec()); err != nil || h.Enabled || h.ShortCircuit || h.Match == nil || h.EvaluatorIDs == nil {
		t.Fatalf("create hook: %#v, %v", h, err)
	}
	if _, err := c.GetHookRule(ctx, "input_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReplaceHookRule(ctx, "input_guard", hookSpec()); err != nil {
		t.Fatal(err)
	}
	if list, err := c.ListHookRules(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list hooks: %v, %v", list, err)
	}
	if err := c.DeleteHookRule(ctx, "input_guard"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteEvaluator(ctx, "clean_input"); err != nil {
		t.Fatal(err)
	}
	if int(calls.Load()) != len(steps) {
		t.Fatalf("observed %d calls, expected %d", calls.Load(), len(steps))
	}
}

// Source contract: WALStore.ListEvaluators returns row-ID ordered versions,
// whereas GetEvaluator orders updated_at DESC, id DESC. At limit100 a legitimate
// next page needs 100 rows first, not a one-row self-modeled pagination fixture.
func TestEvaluatorVersionRowsAndLatestAreDifferentContracts(t *testing.T) {
	version10 := strings.Replace(evaluatorJSON, `"version":"1"`, `"version":"10"`, 1)
	version2 := strings.Replace(evaluatorJSON, `"version":"1"`, `"version":"2"`, 1)
	firstPage := []string{version2}
	for i := 2; i <= 100; i++ {
		firstPage = append(firstPage, strings.Replace(evaluatorJSON, `"evaluator_id":"clean_input"`, fmt.Sprintf(`"evaluator_id":"history_%03d"`, i), 1))
	}
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate-exact-version=%v", duplicate), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer bearer-canary" {
					t.Error("wrong list/get method or bearer")
					http.Error(w, "unexpected", 400)
					return
				}
				switch {
				case n == 1 && r.URL.Path == prefix+"/evaluators" && r.URL.RawQuery == "limit=100":
					jsonResponse(w, `{"items":[`+strings.Join(firstPage, ",")+`],"next_cursor":"100"}`)
				case n == 2 && r.URL.Path == prefix+"/evaluators" && r.URL.RawQuery == "cursor=100&limit=100":
					last := version10
					if duplicate {
						last = version2 // adversarial exact duplicate, not another valid version
					}
					jsonResponse(w, `{"items":[`+last+`],"next_cursor":""}`)
				case n == 3 && !duplicate && r.URL.Path == prefix+"/evaluators/clean_input" && r.URL.RawQuery == "":
					jsonResponse(w, version10) // most recently updated row, not lexical max "2"
				default:
					t.Errorf("unexpected request %d: %s", n, r.URL.String())
					http.Error(w, "unexpected", 400)
				}
			}))
			defer s.Close()
			c := newClient(t, s, nil)
			rows, err := c.ListEvaluators(context.Background())
			if duplicate {
				assertSanitized(t, err, s.URL)
				if rows != nil || calls.Load() != 2 {
					t.Fatalf("exact duplicate must reject without partial inventory: rows=%d calls=%d", len(rows), calls.Load())
				}
				return
			}
			if err != nil {
				t.Fatalf("valid distinct versions across pages rejected: %v", err)
			}
			if len(rows) != 101 || rows[0].EvaluatorID != "clean_input" || rows[0].Version != "2" || rows[100].EvaluatorID != "clean_input" || rows[100].Version != "10" {
				t.Fatalf("all version rows must survive in backend order: count=%d", len(rows))
			}
			for i := 1; i < 100; i++ {
				if rows[i].EvaluatorID != fmt.Sprintf("history_%03d", i+1) || rows[i].Version != "1" {
					t.Fatalf("row %d reordered/dropped: %#v", i, rows[i])
				}
			}
			latest, err := c.GetEvaluator(context.Background(), "clean_input")
			if err != nil || latest.Version != "10" || calls.Load() != 3 {
				t.Fatalf("GET must observe server latest independently of LIST: %#v, %v", latest, err)
			}
		})
	}
}

func TestCreateEvaluatorChecksNormalizedVersionCoordinate(t *testing.T) {
	for _, tc := range []struct {
		name, requested, expectedRequest string
		contradictory                    bool
	}{
		{"contradictory", "2", `{"evaluator_id":"clean_input","version":"2","kind":"regex","config":{"pattern":"unsafe","target":"input","reject":true},"output_keys":[{"key":"clean","type":"bool","enum":[],"pass_match":[],"pass_value":false}]}`, true},
		{"trimmed-match", " \t2\n", `{"evaluator_id":"clean_input","version":" \t2\n","kind":"regex","config":{"pattern":"unsafe","target":"input","reject":true},"output_keys":[{"key":"clean","type":"bool","enum":[],"pass_match":[],"pass_value":false}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != prefix+"/evaluators" || r.Header.Get("Authorization") != "Bearer bearer-canary" {
					t.Error("wrong create route/method/bearer")
					http.Error(w, "unexpected", 400)
					return
				}
				assertJSON(t, r, tc.expectedRequest)
				if tc.contradictory {
					jsonResponse(w, evaluatorJSON) // version1 contradicts requested version2
				} else {
					jsonResponse(w, strings.Replace(evaluatorJSON, `"version":"1"`, `"version":"2"`, 1))
				}
			}))
			defer s.Close()
			c := newClient(t, s, nil)
			spec := evaluatorSpec()
			spec.Version = tc.requested
			e, err := c.CreateEvaluator(context.Background(), spec)
			if tc.contradictory {
				assertSanitized(t, err, s.URL)
				if e.EvaluatorID != "" || e.Version != "" {
					t.Fatal("contradictory create returned an entity")
				}
			} else if err != nil || e.Version != "2" {
				t.Fatalf("backend TrimSpace-normalized version rejected: %#v, %v", e, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("create was retried: %d calls", calls.Load())
			}
		})
	}
}

// The pinned handler accepts empty evaluator_ids when real tool/redact effects
// exist; enabled:false alone never makes a zero-effect hook a valid positive.
func TestToolRedactOnlyHookAllowsEmptyEvaluatorIDs(t *testing.T) {
	const response = `{"tenant_id":"test-tenant","rule_id":"input_guard","enabled":false,"phase":"preflight","priority":0,"selector":"all","match":{},"evaluator_ids":[],"action_on_fail":"deny","short_circuit":false,"tool_filter":{"blocked_names":["delete_*"]},"redact":{"patterns":[{"id":"mask","regex":"secret"}]}}`
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != prefix+"/hook-rules" || r.Header.Get("Authorization") != "Bearer bearer-canary" {
			t.Error("wrong effect-only hook route/method/bearer")
			http.Error(w, "unexpected", 400)
			return
		}
		assertJSON(t, r, `{"rule_id":"input_guard","enabled":false,"phase":"preflight","priority":0,"selector":"all","match":{},"evaluator_ids":[],"action_on_fail":"deny","short_circuit":false,"tool_filter":{"blocked_names":["delete_*"]},"redact":{"patterns":[{"id":"mask","regex":"secret"}]}}`)
		jsonResponse(w, response)
	}))
	defer s.Close()
	c := newClient(t, s, nil)
	spec := hookSpec()
	spec.EvaluatorIDs = []string{}
	spec.ToolFilter = &provision.HookToolFilter{BlockedNames: []string{"delete_*"}}
	spec.Redact = &provision.HookRedactConfig{Patterns: []provision.HookRedactPattern{{ID: "mask", Regex: "secret"}}}
	h, err := c.CreateHookRule(context.Background(), spec)
	if err != nil || h.EvaluatorIDs == nil || len(h.EvaluatorIDs) != 0 || h.Enabled || h.ShortCircuit || h.Priority != 0 || h.ToolFilter == nil || h.Redact == nil {
		t.Fatalf("valid effect-only hook failed: %#v, %v", h, err)
	}
	if calls.Load() != 1 {
		t.Fatal("effect-only hook create retried")
	}
}

func TestHTTPFailuresAreSanitizedStatusObservationsWithoutRetry(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "body-canary bearer-canary test-tenant", status)
			}))
			defer s.Close()
			c := newClient(t, s, nil)
			var err error
			if status == http.StatusNotFound {
				_, err = c.GetEvaluator(context.Background(), "clean_input")
			} else {
				_, err = c.CreateEvaluator(context.Background(), evaluatorSpec())
			}
			var observed *provision.HTTPError
			if !errors.As(err, &observed) || observed.StatusCode != status {
				t.Fatalf("expected typed status %d before JSON decoding, got %v", status, err)
			}
			assertSanitized(t, err, s.URL)
			if calls.Load() != 1 {
				t.Fatalf("mutation retried %d times", calls.Load())
			}
		})
	}
}

func assertSanitized(t *testing.T, err error, endpoint string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected failure")
	}
	for _, forbidden := range []string{"body-canary", "bearer-canary", "test-tenant", endpoint} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error leaked sensitive input: %s", err)
		}
	}
}

func TestSuccessMustContainBoundedCorrectEntities(t *testing.T) {
	cases := []struct {
		name, body string
		hook       bool
		limit      int64
	}{
		{"empty", "", false, 0},
		{"html", "<html>body-canary</html>", false, 0},
		{"malformed", `{"tenant_id":`, false, 0},
		{"trailing", evaluatorJSON + `{}`, false, 0},
		{"missing", `{}`, false, 0},
		{"unknown-entity-field", strings.TrimSuffix(evaluatorJSON, "}") + `,"unexpected":"body-canary"}`, false, 0},
		{"unsupported-hook-field", strings.TrimSuffix(hookJSON, "}") + `,"policy_service":{"id":"foreign"}}`, true, 0},
		{"null-config", strings.Replace(evaluatorJSON, `"config":{"pattern":"unsafe","target":"input","reject":true}`, `"config":null`, 1), false, 0},
		{"missing-config", strings.Replace(evaluatorJSON, `"config":{"pattern":"unsafe","target":"input","reject":true},`, "", 1), false, 0},
		{"wrong-tenant", strings.Replace(evaluatorJSON, "test-tenant", "foreign-tenant", 1), false, 0},
		{"wrong-id", strings.Replace(evaluatorJSON, "clean_input", "foreign_id", 1), false, 0},
		{"oversize", evaluatorJSON, false, 32},
		{"wrong-hook-tenant", strings.Replace(hookJSON, "test-tenant", "foreign-tenant", 1), true, 0},
		{"wrong-hook-id", strings.Replace(hookJSON, "input_guard", "foreign_id", 1), true, 0},
		{"missing-false", strings.Replace(hookJSON, `"enabled":false,`, "", 1), true, 0},
		{"missing-short-circuit", strings.Replace(hookJSON, `,"short_circuit":false`, "", 1), true, 0},
		{"wrong-type", strings.Replace(hookJSON, `"enabled":false`, `"enabled":"false"`, 1), true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				jsonResponse(w, tc.body)
			}))
			defer s.Close()
			c := newClient(t, s, func(o *provision.ClientOptions) { o.MaxResponseBytes = tc.limit })
			var err error
			if tc.hook {
				_, err = c.GetHookRule(context.Background(), "input_guard")
			} else {
				_, err = c.GetEvaluator(context.Background(), "clean_input")
			}
			assertSanitized(t, err, s.URL)
			if calls.Load() != 1 {
				t.Fatalf("expected real HTTP observation once, got %d", calls.Load())
			}
		})
	}
}

func TestListRequiresCompleteBoundedNonrepeatingEnvelope(t *testing.T) {
	for _, hook := range []bool{false, true} {
		entity := evaluatorJSON
		if hook {
			entity = hookJSON
		}
		for _, mode := range []string{"missing-items", "null-items", "missing-cursor", "null-cursor", "unknown-envelope-field", "bad-item", "foreign-item", "repeat-cursor", "repeat-id", "untrusted-cursor", "page-limit"} {
			t.Run(fmt.Sprintf("hook=%v/%s", hook, mode), func(t *testing.T) {
				var calls atomic.Int32
				s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					body := `{"items":[],"next_cursor":""}`
					switch mode {
					case "missing-items":
						body = `{"next_cursor":""}`
					case "null-items":
						body = `{"items":null,"next_cursor":""}`
					case "missing-cursor":
						body = `{"items":[]}`
					case "null-cursor":
						body = `{"items":[],"next_cursor":null}`
					case "unknown-envelope-field":
						body = `{"items":[],"next_cursor":"","unexpected":"body-canary"}`
					case "bad-item":
						body = `{"items":[{}],"next_cursor":""}`
					case "foreign-item":
						body = `{"items":[` + strings.Replace(entity, "test-tenant", "foreign", 1) + `],"next_cursor":""}`
					case "repeat-cursor":
						body = `{"items":[],"next_cursor":"same"}`
					case "repeat-id":
						body = `{"items":[` + entity + `],"next_cursor":"same"}`
					case "untrusted-cursor":
						if n == 1 {
							// Deliberately adversarial, not a backend-valid decimal cursor.
							body = `{"items":[` + entity + `],"next_cursor":"next /?+"}`
						} else {
							if r.URL.RawQuery != "cursor=next+%2F%3F%2B&limit=100" {
								t.Error("untrusted cursor was not query-escaped")
							}
							http.Error(w, "body-canary", http.StatusBadRequest)
							return
						}
					case "page-limit":
						body = fmt.Sprintf(`{"items":[],"next_cursor":"%d"}`, n)
					}
					jsonResponse(w, body)
				}))
				defer s.Close()
				c := newClient(t, s, nil)
				var err error
				if hook {
					var got []provision.HookRule
					got, err = c.ListHookRules(context.Background())
					if got != nil {
						t.Error("partial list escaped on failure")
					}
				} else {
					var got []provision.Evaluator
					got, err = c.ListEvaluators(context.Background())
					if got != nil {
						t.Error("partial list escaped on failure")
					}
				}
				assertSanitized(t, err, s.URL)
				expected := int32(1)
				if mode == "repeat-cursor" || mode == "repeat-id" || mode == "untrusted-cursor" {
					expected = 2
				}
				if mode == "page-limit" {
					expected = 100
				}
				if calls.Load() != expected {
					t.Fatalf("bounded list requests: got %d, expected %d", calls.Load(), expected)
				}
			})
		}
	}
}

func TestRedirectsAreNotFollowedAndInjectedClientIsUnchanged(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/login?body-canary", http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	injected := s.Client()
	injected.Timeout = time.Minute
	var redirects atomic.Int32
	injected.CheckRedirect = func(*http.Request, []*http.Request) error { redirects.Add(1); return errors.New("body-canary") }
	c := newClient(t, s, func(o *provision.ClientOptions) { o.HTTPClient = injected })
	_, err := c.CreateHookRule(context.Background(), hookSpec())
	var observed *provision.HTTPError
	if !errors.As(err, &observed) || observed.StatusCode != http.StatusTemporaryRedirect || calls.Load() != 1 || redirects.Load() != 0 {
		t.Fatalf("redirect/retry was not stopped: error=%v requests=%d redirects=%d", err, calls.Load(), redirects.Load())
	}
	assertSanitized(t, err, s.URL)
	if injected.Timeout != time.Minute || injected.CheckRedirect(nil, nil) == nil || redirects.Load() != 1 {
		t.Fatal("injected client was modified")
	}
}

func TestDeadlineCancellationAndTransportFailuresAreBounded(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer s.Close()
	c := newClient(t, s, func(o *provision.ClientOptions) { o.Timeout = 50 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.GetEvaluator(ctx, "clean_input")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("client deadline not enforced: %v", err)
	}
	assertSanitized(t, err, s.URL)
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	_, err = c.GetHookRule(cancelled, "input_guard")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}

	// Close a real TLS listener to exercise net/http's URL-bearing transport
	// failure, rather than returning a fabricated client error from a mock.
	closed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	broken := newClient(t, closed, nil)
	closed.Close()
	_, err = broken.CreateEvaluator(context.Background(), evaluatorSpec())
	assertSanitized(t, err, closed.URL)
}

func TestRejectsUnsafeConfigurationAndIdentityBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	for _, base := range []string{strings.Replace(s.URL, "https:", "http:", 1), s.URL + "?body-canary", s.URL + "#body-canary", strings.Replace(s.URL, "https://", "https://bearer-canary@", 1), s.URL + "/../escape", s.URL + "/%2Fescape"} {
		_, err := provision.NewClient(provision.ClientOptions{BaseURL: base, Token: "bearer-canary", ExpectedTenantID: "test-tenant"})
		assertSanitized(t, err, s.URL)
	}
	c := newClient(t, s, nil)
	for _, id := range []string{"", ".", "..", "../escape", "id?x=y", "id/other", "id\n", " id"} {
		_, err := c.GetEvaluator(context.Background(), id)
		assertSanitized(t, err, s.URL)
		_, err = c.GetHookRule(context.Background(), id)
		assertSanitized(t, err, s.URL)
	}
	_, err := c.ReplaceHookRule(context.Background(), "other", hookSpec())
	assertSanitized(t, err, s.URL)
	if calls.Load() != 0 {
		t.Fatal("unsafe input reached HTTP")
	}
}
