// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// These names and codes are application-owned fixture declarations, not catalogue fields.
const handledWorker = `
traffic:
  from_resource: validation-queue
  error_mix:
    - {status: 422, code: RECORD_INCOMPLETE, weight: 1}
    - {status: 500, code: EXECUTION_FAILED, weight: 1}
services:
  - name: validation-worker
    type: worker
    entry: true
    worker: true
    error_rate: 0
    uses: [completeness-rule]
    spans:
      - name_template: process validation-queue
        attributes:
          fixture.result: {ref: outcome}
          fixture.code: {ref: error_code}
          fixture.draw: {float_range: {min: 0, max: 1}}
` + causalLogBody

func TestHandledRuleWorkerProjection(t *testing.T) {
	var healthyDraws []any
	var healthyTail float64
	for _, tc := range []struct {
		name         string
		status       otlp.StatusCode
		code, result string
		rule500      bool
		active       bool
	}{
		{"healthy", otlp.StatusUnset, "", "success", false, false},
		{"handled", otlp.StatusUnset, "RECORD_INCOMPLETE", "client_error", false, true},
		{"execution", otlp.StatusError, "EXECUTION_FAILED", "server_error", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := strings.Replace(causalResources, "reject_share: 0.35", "reject_share: 1", 1)
			if tc.rule500 {
				resources = strings.Replace(resources, "status: 422", "status: 500", 1)
			}
			w := causalApps(t, resources, handledWorker)[0]
			eng := shape.New("UTC", nil)
			if tc.active {
				eng = incident(causal.ModeRuleRegression, "completeness-rule", 1)
			}
			reqs := mintAt(w, eng, causalNow)
			if len(reqs) == 0 {
				t.Fatal("no worker requests")
			}
			p := project(t, w, eng, reqs)
			var draws []any
			for _, r := range reqs {
				sp := p.spans[r.SpanID]
				draws = append(draws, sp.Attrs["fixture.draw"])
				if sp.Status != tc.status || sp.Kind != otlp.KindConsumer || sp.Name != "process validation-queue" || sp.ParentID != "" {
					t.Fatalf("%s root: %+v", tc.name, sp)
				}
				if sp.Attrs["fixture.result"] != tc.result || sp.Attrs["fixture.code"] != tc.code {
					t.Fatalf("authored marker/code: %+v, want %s/%s", sp.Attrs, tc.result, tc.code)
				}
				if tc.status == otlp.StatusUnset {
					if _, ok := sp.Attrs[attrErrorType]; ok {
						t.Fatalf("UNSET root carries error.type: %+v", sp)
					}
					if sp.StatusMsg != "" {
						t.Fatalf("UNSET root description %q", sp.StatusMsg)
					}
				} else if sp.Attrs[attrErrorType] == nil || sp.StatusMsg == "" {
					t.Fatalf("execution failure lost error evidence: %+v", sp)
				}
				for k := range sp.Attrs {
					if strings.HasPrefix(k, "http.") {
						t.Fatalf("worker carries HTTP attribute %s", k)
					}
				}
				if tc.active {
					lines := p.lines["validation-worker"]
					found := false
					for _, l := range lines {
						if l.field("span_id") == r.SpanID {
							found = true
							want := "warn"
							if tc.rule500 {
								want = "error"
							}
							if l.level != want || l.field("error_code") != tc.code {
								t.Fatalf("log %+v, want %s/%s", l, want, tc.code)
							}
						}
					}
					if !found {
						t.Fatal("no correlated rejection log")
					}
				}
			}
			tail := eng.Float64()
			if !tc.active {
				healthyDraws, healthyTail = draws, tail
			} else if !reflect.DeepEqual(draws, healthyDraws) || tail != healthyTail {
				t.Fatal("handled/execution projection shifted authored or later engine draws")
			}
		})
	}
}

// No blanket 4xx exemption: token/transport/timeouts and execution failures remain errors.
func TestHandledRuleClassificationIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		kind, mode string
		status     int
		want       otlp.StatusCode
	}{
		{"rule", "rule_regression", 422, otlp.StatusUnset},
		{"rule", "rule_regression", 500, otlp.StatusError},
		{"credential", "token_expired", 401, otlp.StatusError},
		{"dependency", "dependency_unavailable", 0, otlp.StatusError},
		{"rule", "timeout", 504, otlp.StatusError},
		{"", "error_rate", 422, otlp.StatusError},
	} {
		r := &ledger.Request{Causal: true, Outcome: ledger.OutcomeClientError, StatusCode: tc.status, Fault: &ledger.Fault{Kind: tc.kind, Mode: tc.mode}}
		for _, kind := range []otlp.SpanKind{otlp.KindConsumer, otlp.KindInternal, otlp.KindServer} {
			if got := rootStatus(kind, protoNonHTTP, r); got != tc.want {
				t.Errorf("%+v root kind %v: got %v", tc, kind, got)
			}
		}
		r.Calls = []ledger.Call{{Failed: true, ParentHopIndex: -1, StatusCode: tc.status, Fault: r.Fault, TimedOut: tc.mode == "timeout"}}
		if got := serverStatus(r, 0, protoNonHTTP); got != tc.want {
			t.Errorf("%+v server: got %v", tc, got)
		}
	}
}
