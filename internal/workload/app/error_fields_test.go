// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// causalErrorRefs are the log refs that only a failure or a cause sets; on a line with neither they
// resolve empty and the field is omitted, never written as "".
var causalErrorRefs = []string{"error_type", "error_message", "exception_type", "fault_resource", "fault_mode", "upstream", "error_code"}

// A healthy line carries no absent error field: an empty ref is omitted (AGENTS.md: an absent
// dimension is omitted, never ""), on a causal app and on the same app without resources, while
// every ref that resolves keeps its field.
func TestHealthyLogLinesOmitAbsentErrorFields(t *testing.T) {
	opted := causalApps(t, causalResources, loggedIntakeApp)[0]
	legacy := causalApps(t, causalResources, loggedLegacyIntakeApp)[0]
	for name, w := range map[string]*Workload{"causal": opted, "legacy": legacy} {
		eng := shape.New("UTC", nil)
		reqs := mintAt(w, eng, causalNow)
		p := project(t, w, eng, reqs)
		healthy := 0
		for _, r := range reqs {
			if r.Outcome != ledger.OutcomeSuccess || r.Fault != nil {
				continue
			}
			for _, l := range p.linesFor("intake-api", r) {
				healthy++
				for _, k := range causalErrorRefs {
					if v, has := l.body[k]; has {
						t.Fatalf("%s: healthy line carries %s=%q: %+v", name, k, v, l.body)
					}
				}
				if l.body["status"] != "200" || l.body["outcome"] != "success" {
					t.Fatalf("%s: healthy line lost a resolved field: %+v", name, l.body)
				}
			}
		}
		if healthy == 0 {
			t.Fatalf("%s: no healthy lines", name)
		}
	}
}

// backgroundWorkerApp is a bound function's invocation workload: a worker drawing its requests
// from a queue's served rate, with a background error share and every causal ref logged. The 0.3
// rate is a test value chosen so one tick holds failures; it models nothing.
var backgroundWorkerApp = `
traffic: {from_resource: validation-queue}
services:
  - name: validation-worker
    entry: true
    worker: true
    error_rate: 0.3
    calls: [external-validator-node]` + causalLogBody + `
  - {name: external-validator-node, external: true, uses: [external-validator]}
`

// A background failure of a bound function's invocation (the entry's own error_rate draw on an
// app whose traffic is a causal resource's served rate) is a causal failure with the error_rate
// cause: its CONSUMER root is ERROR with error.type and a status message naming the service and
// the drawn code, and its entry line carries the same error_type and error_message. The healthy
// callee keeps its fields absent.
func TestBoundBackgroundFailureCarriesErrorFields(t *testing.T) {
	ws := causalApps(t, causalResources, intakeApp, backgroundWorkerApp)
	w := ws[1]
	eng := shape.New("UTC", nil)
	w.AdvanceCausal(causalNow, eng)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	failed := 0
	for _, r := range reqs {
		if r.Outcome == ledger.OutcomeSuccess {
			continue
		}
		failed++
		wantType, wantMsg := strconv.Itoa(r.StatusCode), "validation-worker: "+r.ErrorKind
		if r.Fault == nil || r.Fault.Mode != "error_rate" || r.Fault.ErrorType != wantType || r.Fault.Message != wantMsg {
			t.Fatalf("background failure fault %+v, want error_rate %s %q", r.Fault, wantType, wantMsg)
		}
		root := p.spans[r.SpanID]
		if root.Kind != otlp.KindConsumer || root.Status != otlp.StatusError || root.Attrs["error.type"] != wantType || root.StatusMsg != wantMsg {
			t.Fatalf("background root: kind %v status %v attrs %v msg %q", root.Kind, root.Status, root.Attrs, root.StatusMsg)
		}
		in := p.linesFor("validation-worker", r)
		if len(in) != 1 || in[0].level != logLevel(r.Outcome) || in[0].body["error_type"] != wantType || in[0].body["error_message"] != wantMsg ||
			in[0].body["fault_mode"] != "error_rate" || in[0].body["error_code"] != r.ErrorKind {
			t.Fatalf("background entry lines %+v", in)
		}
		for _, k := range []string{"exception_type", "fault_resource", "upstream"} {
			if v, has := in[0].body[k]; has {
				t.Fatalf("background entry line carries absent %s=%q", k, v)
			}
		}
		for _, l := range p.linesFor("external-validator-node", r) {
			for _, k := range causalErrorRefs {
				if v, has := l.body[k]; has {
					t.Fatalf("healthy callee line carries %s=%q", k, v)
				}
			}
		}
	}
	if failed == 0 {
		t.Fatal("no background failures minted")
	}
}

// errorMixIntakeApp is loggedIntakeApp declaring an error_mix whose 503 entries are the codes a
// pool rejection's 503 draws from.
var errorMixIntakeApp = strings.Replace(loggedIntakeApp, "traffic: {off_peak_rps: 5, peak_rps: 60}",
	`traffic:
  off_peak_rps: 5
  peak_rps: 60
  error_mix:
    - {status: 400, code: VALIDATION_ERROR, weight: 5}
    - {status: 503, code: SERVICE_UNAVAILABLE, weight: 3}
    - {status: 503, code: POOL_BUSY, weight: 1}`, 1)

// A causal failure takes its error code from the declared error_mix entries of its status, weighted
// and drawn from the request's own hash (no engine draw), as a baseline failure does; a status the
// mix does not declare keeps the fault's type.
func TestCausalFailureDrawsErrorCodeFromErrorMix(t *testing.T) {
	if errorMixIntakeApp == loggedIntakeApp {
		t.Fatal("the error_mix edit did not apply")
	}
	w := causalApps(t, causalResources, errorMixIntakeApp)[0]
	eng := incident(causal.ModeDependencySlow, "records-db", 1)
	reqs := mintAt(w, eng, causalNow)
	p := project(t, w, eng, reqs)
	codes := map[string]int{}
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "intake-db-pool" || r.StatusCode != 503 {
			continue
		}
		codes[r.ErrorKind]++
		in := p.linesFor("intake-api", r)
		if len(in) != 1 || in[0].body["error_code"] != r.ErrorKind {
			t.Fatalf("pool entry lines %+v, want error_code %q", in, r.ErrorKind)
		}
	}
	if len(codes) != 2 || codes["SERVICE_UNAVAILABLE"] <= codes["POOL_BUSY"] || codes["POOL_BUSY"] == 0 {
		t.Fatalf("pool rejection codes %v, want both declared 503 codes, weighted 3:1", codes)
	}

	// A status the mix does not declare (the rule's 422) keeps the fault's own type.
	w = causalApps(t, causalResources, errorMixIntakeApp)[0]
	eng = incident(causal.ModeRuleRegression, "completeness-rule", 1)
	rule := 0
	for _, r := range mintAt(w, eng, causalNow) {
		if r.Fault == nil || r.Fault.Kind != string(causal.KindRule) {
			continue
		}
		rule++
		if r.ErrorKind != r.Fault.ErrorType {
			t.Fatalf("undeclared 422 took code %q, want the fault's type %q", r.ErrorKind, r.Fault.ErrorType)
		}
	}
	if rule == 0 {
		t.Fatal("no rule rejections minted")
	}
}
