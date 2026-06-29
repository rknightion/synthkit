// SPDX-License-Identifier: AGPL-3.0-only

package jsondata

// TestNoContentFieldsAnywhere is the signals/00-canon.md [slug: content-strip] (I23) gate: no endpoint may surface
// request/response body-content (prompt/completion text, run inputs/outputs/messages,
// document bodies). Identifiers, status codes, and fact fields only.
//
// This test mirrors the predecessor's content_strip_test.go discipline and walks every
// marshalled JSON payload for the forbidden-key set.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
)

func TestNoContentFieldsAnywhere(t *testing.T) {
	now := time.Unix(1_750_000_000, 0).UTC()

	rq := &ledger.Request{
		Correlation: ledger.NewCorrelation(),
		Workload:    "acme-api",
		Env:         "prod",
		Cluster:     "acme-prod",
		Route:       "GET /v1/items",
		Start:       now.Add(-time.Minute),
		Duration:    250 * time.Millisecond,
		Outcome:     ledger.OutcomeSuccess,
		StatusCode:  200,
	}
	src := &fakeSource{
		blueprints: []string{"acme"},
		reqs:       []*ledger.Request{rq},
	}
	h := NewServer(src)

	// Collect all served JSON bodies.
	type endpointBody struct {
		name string
		body []byte
	}
	var bodies []endpointBody

	for _, path := range []string{"/", "/blueprints", "/golden_thread_sample", "/requests"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		h.ServeHTTP(rec, req)
		bodies = append(bodies, endpointBody{name: path, body: rec.Body.Bytes()})
	}

	// Forbidden JSON field keys = unambiguous content carriers. The closing quote
	// prevents false-positives: e.g. "prompt_tokens" does NOT match `"prompt"`.
	forbidden := []string{
		`"inputs"`, `"outputs"`, `"messages"`,
		`"prompt"`, `"completion"`,
		`"prompt_text"`, `"completion_text"`,
		`"message_content"`,
		`"body"`, `"request_body"`, `"response_body"`,
	}

	for _, ep := range bodies {
		for _, f := range forbidden {
			if strings.Contains(string(ep.body), f) {
				t.Errorf("endpoint %s leaked a content field key %s", ep.name, f)
			}
		}
	}
}

// TestJSONRoundTrip validates that every served payload is valid JSON (no truncation,
// no partial writes).
func TestJSONRoundTrip(t *testing.T) {
	rq := &ledger.Request{
		Correlation: ledger.NewCorrelation(),
		Workload:    "acme-api",
		Env:         "prod",
		Cluster:     "acme-prod",
		Route:       "GET /v1/items",
		Start:       time.Now().Add(-time.Minute),
		Duration:    250 * time.Millisecond,
		Outcome:     ledger.OutcomeSuccess,
		StatusCode:  200,
	}
	src := &fakeSource{blueprints: []string{"acme"}, reqs: []*ledger.Request{rq}}
	h := NewServer(src)

	paths := []string{"/", "/blueprints", "/golden_thread_sample", "/requests", "/healthz"}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		h.ServeHTTP(rec, req)
		b := rec.Body.Bytes()
		if p == "/healthz" {
			continue // plain text, not JSON
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Errorf("endpoint %s produced invalid JSON: %v\nbody: %s", p, err, b)
		}
	}
}
