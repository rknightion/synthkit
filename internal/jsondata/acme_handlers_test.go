// SPDX-License-Identifier: AGPL-3.0-only

package jsondata

// TestAcmeRoutes verifies that each of the 9 acme-ai-platform Infinity endpoints returns:
//   - HTTP 200
//   - Valid JSON
//   - The expected root selector key
//   - At least one row in the root array
//   - The required column keys present in the first row
//
// For /acme/golden_thread the three root keys are each verified separately.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
)

// makeAcmeSource builds a fakeSource with a handful of requests carrying the AI correlation
// fields (Model, Provider, PortkeyTraceID) that /acme/golden_thread uses.
//
// Workload is prefixed with the blueprint name so fakeSource.Recent("acme-ai-platform", ...) returns
// this request (fakeSource filters by strings.HasPrefix(workload, blueprint)).
func makeAcmeSource() Source {
	c := ledger.NewCorrelation()
	rq := &ledger.Request{
		Correlation: c,
		Workload:    "acme-ai-platform-app", // prefix "acme-ai-platform" so fakeSource.Recent matches
		Env:         "PRD",
		Cluster:     "acme-eks-1",
		Route:       "POST /v1/assist",
		Model:       "gpt-4o",
		Provider:    "azure-openai",
		Start:       time.Now().Add(-3 * time.Minute),
		Duration:    400 * time.Millisecond,
		Outcome:     ledger.OutcomeSuccess,
		StatusCode:  200,
	}
	return &fakeSource{
		blueprints: []string{"acme-ai-platform"},
		reqs:       []*ledger.Request{rq},
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────────────────────────

func assertRoute(t *testing.T, h interface {
	ServeHTTP(rw interface{ Header() interface{} }, r interface{})
}, path, rootKey string, requiredCols []string) {
	t.Helper()
}

// getJSON issues a GET, asserts 200, parses JSON, and returns the top-level map.
func getJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	h := newTestServer(makeAcmeSource())
	rec := get(h, path)
	if rec.Code != 200 {
		t.Fatalf("GET %s: want 200, got %d\nbody: %s", path, rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("GET %s: invalid JSON: %v\nbody: %s", path, err, rec.Body.String())
	}
	return payload
}

// assertRootArray extracts the named root key as []any, asserts at least one element, and returns
// the first element cast to map[string]any. Fails the test immediately on shape mismatch.
func assertRootArray(t *testing.T, payload map[string]any, path, rootKey string) map[string]any {
	t.Helper()
	raw, ok := payload[rootKey]
	if !ok {
		t.Fatalf("GET %s: missing root key %q in payload keys %v", path, rootKey, payloadKeys(payload))
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("GET %s: root key %q is %T, want []any", path, rootKey, raw)
	}
	if len(arr) == 0 {
		t.Fatalf("GET %s: root %q array is empty", path, rootKey)
	}
	row, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("GET %s: first element of %q is %T, want map[string]any", path, rootKey, arr[0])
	}
	return row
}

// assertCols checks that all required column keys are present in row.
func assertCols(t *testing.T, path, rootKey string, row map[string]any, cols []string) {
	t.Helper()
	for _, col := range cols {
		if _, ok := row[col]; !ok {
			t.Errorf("GET %s root=%q: missing column %q; got keys: %v", path, rootKey, col, mapKeys(row))
		}
	}
}

func payloadKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func mapKeys(m map[string]any) []string { return payloadKeys(m) }

// ── /acme/golden_thread ──────────────────────────────────────────────────────────────────────────

func TestAcmeGoldenThread_Request(t *testing.T) {
	path := "/acme/golden_thread"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "request")
	assertCols(t, path, "request", row, []string{"use_case", "context", "env", "model", "provider", "started_at"})
}

func TestAcmeGoldenThread_CorrelationKeys(t *testing.T) {
	path := "/acme/golden_thread"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "correlation_keys")
	assertCols(t, path, "correlation_keys", row,
		[]string{"correlation_id", "trace_id", "portkey_trace_id", "span_id", "traceparent"})

	// Verify traceparent is a well-formed W3C trace-context header: "00-<traceID>-<spanID>-01"
	// (4 dash-separated parts; first part is "00").
	tp, ok := row["traceparent"].(string)
	if !ok {
		t.Fatalf("%s correlation_keys[0].traceparent is %T, want string", path, row["traceparent"])
	}
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		t.Errorf("%s traceparent %q: want 4 dash-separated parts, got %d", path, tp, len(parts))
	} else if parts[0] != "00" {
		t.Errorf("%s traceparent %q: want version prefix \"00\", got %q", path, tp, parts[0])
	}
}

func TestAcmeGoldenThread_Hops(t *testing.T) {
	path := "/acme/golden_thread"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "hops")
	assertCols(t, path, "hops", row,
		[]string{"hop", "service", "span_name", "signal", "span_id", "parent_span_id", "keys"})

	// Verify the hops array has 10 elements (the full journey).
	arr := payload["hops"].([]any)
	if len(arr) != 10 {
		t.Errorf("/acme/golden_thread: want 10 hops, got %d", len(arr))
	}
}

// Verify correlation IDs from the Source appear in the golden_thread payload.
func TestAcmeGoldenThread_CorrelationIDFromSource(t *testing.T) {
	// Use the SAME source instance for both the server and the expected-ID lookup.
	src := makeAcmeSource().(*fakeSource)
	h := newTestServer(src)
	rec := get(h, "/acme/golden_thread")
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	corrID := src.reqs[0].CorrelationID
	if !strings.Contains(rec.Body.String(), corrID) {
		t.Errorf("/acme/golden_thread: correlation_id %q not found in body", corrID)
	}
}

// ── /v1/analytics/groups/metadata ────────────────────────────────────────────────────────────────

func TestAnalyticsMetadata(t *testing.T) {
	path := "/v1/analytics/groups/metadata"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "data")
	assertCols(t, path, "data", row,
		[]string{"group_key", "requests", "cost", "total_tokens", "latency_p99",
			"error_rate", "cache_hit_rate", "rescued_requests"})

	arr := payload["data"].([]any)
	if len(arr) < len(acmeUseCases) {
		t.Errorf("%s: want at least %d rows (one per use_case), got %d", path, len(acmeUseCases), len(arr))
	}
}

// ── /v1/analytics/groups/ai-models ───────────────────────────────────────────────────────────────

func TestAnalyticsModels(t *testing.T) {
	path := "/v1/analytics/groups/ai-models"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "data")
	assertCols(t, path, "data", row,
		[]string{"group_key", "requests", "cost", "total_tokens", "latency_p99",
			"error_rate", "cache_hit_rate", "rescued_requests"})

	arr := payload["data"].([]any)
	if len(arr) < len(acmeModels) {
		t.Errorf("%s: want at least %d rows (one per model), got %d", path, len(acmeModels), len(arr))
	}
}

// ── /v1/configs ───────────────────────────────────────────────────────────────────────────────────

func TestConfigs(t *testing.T) {
	path := "/v1/configs"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "providers")
	assertCols(t, path, "providers", row, []string{"slug", "provider", "region", "status"})

	arr := payload["providers"].([]any)
	if len(arr) < 3 {
		t.Errorf("%s: want at least 3 provider rows, got %d", path, len(arr))
	}
}

// ── /v1/prompts ───────────────────────────────────────────────────────────────────────────────────

func TestPrompts(t *testing.T) {
	path := "/v1/prompts"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "data")
	assertCols(t, path, "data", row, []string{"slug", "version", "status", "env_label", "last_approved"})

	arr := payload["data"].([]any)
	if len(arr) < 4 {
		t.Errorf("%s: want at least 4 prompt rows, got %d", path, len(arr))
	}
}

// ── /api/v1/runs/query ────────────────────────────────────────────────────────────────────────────

func TestRunsQuery(t *testing.T) {
	path := "/api/v1/runs/query"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "runs")
	// Top-level columns.
	assertCols(t, path, "runs", row,
		[]string{"id", "name", "run_type", "status", "start_time", "total_tokens", "total_cost"})

	// Nested extra.metadata object must be present and contain the required sub-fields.
	extra, ok := row["extra"].(map[string]any)
	if !ok {
		t.Fatalf("%s runs[0].extra is %T, want map", path, row["extra"])
	}
	meta, ok := extra["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("%s runs[0].extra.metadata is %T, want map", path, extra["metadata"])
	}
	for _, col := range []string{"otel_trace_id", "correlation_id", "portkey_trace_id", "use_case"} {
		if _, ok := meta[col]; !ok {
			t.Errorf("%s runs[0].extra.metadata missing %q; got keys: %v", path, col, mapKeys(meta))
		}
	}

	// Nested feedback_stats.faithfulness.avg.
	fb, ok := row["feedback_stats"].(map[string]any)
	if !ok {
		t.Fatalf("%s runs[0].feedback_stats is %T, want map", path, row["feedback_stats"])
	}
	faith, ok := fb["faithfulness"].(map[string]any)
	if !ok {
		t.Fatalf("%s runs[0].feedback_stats.faithfulness is %T, want map", path, fb["faithfulness"])
	}
	if _, ok := faith["avg"]; !ok {
		t.Errorf("%s runs[0].feedback_stats.faithfulness.avg missing", path)
	}
	compl, ok := fb["completeness"].(map[string]any)
	if !ok {
		t.Fatalf("%s runs[0].feedback_stats.completeness is %T, want map", path, fb["completeness"])
	}
	if _, ok := compl["avg"]; !ok {
		t.Errorf("%s runs[0].feedback_stats.completeness.avg missing", path)
	}

	arr := payload["runs"].([]any)
	if len(arr) < 4 {
		t.Errorf("%s: want at least 4 runs, got %d", path, len(arr))
	}
}

// ── /api/v1/sessions ─────────────────────────────────────────────────────────────────────────────

func TestSessions(t *testing.T) {
	path := "/api/v1/sessions"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "sessions")

	// Top-level name field.
	if _, ok := row["name"]; !ok {
		t.Fatalf("%s sessions[0] missing 'name'", path)
	}

	// Nested stats object.
	stats, ok := row["stats"].(map[string]any)
	if !ok {
		t.Fatalf("%s sessions[0].stats is %T, want map", path, row["stats"])
	}
	for _, col := range []string{"run_count", "latency_p50", "latency_p99", "error_rate", "total_tokens", "total_cost"} {
		if _, ok := stats[col]; !ok {
			t.Errorf("%s sessions[0].stats missing %q; got keys: %v", path, col, mapKeys(stats))
		}
	}

	// Nested stats.feedback_stats.faithfulness.avg.
	fb, ok := stats["feedback_stats"].(map[string]any)
	if !ok {
		t.Fatalf("%s sessions[0].stats.feedback_stats is %T, want map", path, stats["feedback_stats"])
	}
	faith, ok := fb["faithfulness"].(map[string]any)
	if !ok {
		t.Fatalf("%s sessions[0].stats.feedback_stats.faithfulness is %T, want map", path, fb["faithfulness"])
	}
	if _, ok := faith["avg"]; !ok {
		t.Errorf("%s sessions[0].stats.feedback_stats.faithfulness.avg missing", path)
	}

	arr := payload["sessions"].([]any)
	if len(arr) < len(acmeProjects) {
		t.Errorf("%s: want at least %d sessions (one per project), got %d",
			path, len(acmeProjects), len(arr))
	}
}

// ── /acme/aaef_kpis ──────────────────────────────────────────────────────────────────────────────

func TestAAEFKPIs(t *testing.T) {
	path := "/acme/aaef_kpis"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "kpis")
	assertCols(t, path, "kpis", row,
		[]string{"context", "period", "tue_score", "mcr_score", "spi_score", "css_score"})

	arr := payload["kpis"].([]any)
	wantRows := len(aaefContexts) * len(aaefPeriods)
	if len(arr) < wantRows {
		t.Errorf("%s: want at least %d rows (contexts × periods), got %d", path, wantRows, len(arr))
	}
}

// ── /acme/eval_scorecard ─────────────────────────────────────────────────────────────────────────

func TestEvalScorecard(t *testing.T) {
	path := "/acme/eval_scorecard"
	payload := getJSON(t, path)
	row := assertRootArray(t, payload, path, "scorecard")
	assertCols(t, path, "scorecard", row,
		[]string{"project", "env", "use_case", "value", "op", "threshold", "gate", "status", "metric"})

	arr := payload["scorecard"].([]any)
	// 4 projects × 2 envs × 5 gates = 40 rows.
	wantRows := len(acmeProjects) * 2 * len(evalGates)
	if len(arr) < wantRows {
		t.Errorf("%s: want at least %d rows, got %d", path, wantRows, len(arr))
	}

	// Verify that at least one "fail" row exists (TST1 env has degraded scores).
	hasFail := false
	for _, elem := range arr {
		if m, ok := elem.(map[string]any); ok {
			if m["status"] == "fail" {
				hasFail = true
				break
			}
		}
	}
	if !hasFail {
		t.Error("/acme/eval_scorecard: expected at least one fail row (TST1 degraded scores)")
	}
}

// ── POST method guard for new routes (I26) ────────────────────────────────────────────────────────

func TestNewRoutesGETOnly(t *testing.T) {
	h := newTestServer(makeAcmeSource())
	paths := []string{
		"/acme/golden_thread",
		"/v1/analytics/groups/metadata",
		"/v1/analytics/groups/ai-models",
		"/v1/configs",
		"/v1/prompts",
		"/api/v1/runs/query",
		"/api/v1/sessions",
		"/acme/aaef_kpis",
		"/acme/eval_scorecard",
	}
	for _, p := range paths {
		rec := post(h, p)
		if rec.Code != 405 {
			t.Errorf("POST %s: want 405, got %d", p, rec.Code)
		}
	}
}

// ── content-strip coverage for new routes (I23) ───────────────────────────────────────────────────

func TestNewRoutesNoContentFields(t *testing.T) {
	h := newTestServer(makeAcmeSource())
	forbidden := []string{
		`"inputs"`, `"outputs"`, `"messages"`,
		`"prompt"`, `"completion"`,
		`"prompt_text"`, `"completion_text"`,
		`"message_content"`,
		`"body"`, `"request_body"`, `"response_body"`,
	}
	paths := []string{
		"/acme/golden_thread",
		"/v1/analytics/groups/metadata",
		"/v1/analytics/groups/ai-models",
		"/v1/configs",
		"/v1/prompts",
		"/api/v1/runs/query",
		"/api/v1/sessions",
		"/acme/aaef_kpis",
		"/acme/eval_scorecard",
	}
	for _, p := range paths {
		rec := get(h, p)
		body := rec.Body.String()
		for _, f := range forbidden {
			if strings.Contains(body, f) {
				t.Errorf("GET %s leaked content field %s", p, f)
			}
		}
	}
}
