// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

const mixApp = `
traffic:
  off_peak_rps: 100
  peak_rps: 100
  success_status: 201
  error_mix:
    - {status: 400, code: VALIDATION_ERROR, weight: 1}
services:
  - {name: intake, entry: true, error_rate: 0.023, calls: [store]}
  - {name: store, type: db}
`

// SKT-0168.09.18: a declared success status and error mix replace 200 and the 50/30/20 split,
// with the code recorded on failed requests, and take the same engine draws as the default.
func TestAppOutcomeMix(t *testing.T) {
	w := buildApp(t, decodeStrict(t, mixApp))
	def := buildApp(t, decodeStrict(t, strings.Replace(strings.Replace(mixApp, "  success_status: 201\n", "", 1),
		"  error_mix:\n    - {status: 400, code: VALIDATION_ERROR, weight: 1}\n", "", 1)))
	eng, deng := shape.New("UTC", nil), shape.New("UTC", nil)
	reqs := mintMany(w, eng, 5)
	dreqs := mintMany(def, deng, 5)
	if a, b := eng.Float64(), deng.Float64(); a != b {
		t.Fatalf("a declared mix changed the engine draw sequence: %v vs %v", a, b)
	}
	failed := 0
	for i, r := range reqs {
		switch r.Outcome {
		case ledger.OutcomeSuccess:
			if r.StatusCode != 201 || r.ErrorKind != "" {
				t.Fatalf("success status %d kind %q, want 201", r.StatusCode, r.ErrorKind)
			}
		default:
			failed++
			if r.StatusCode != 400 || r.ErrorKind != "VALIDATION_ERROR" || r.Outcome != ledger.OutcomeClientError {
				t.Fatalf("failure %d %q %v, want 400 VALIDATION_ERROR client error", r.StatusCode, r.ErrorKind, r.Outcome)
			}
		}
		if (r.Outcome == ledger.OutcomeSuccess) != (dreqs[i].Outcome == ledger.OutcomeSuccess) {
			t.Fatalf("request %d: the mix changed which requests fail", i)
		}
		if dreqs[i].Outcome == ledger.OutcomeSuccess && dreqs[i].StatusCode != 200 {
			t.Fatalf("default success status %d", dreqs[i].StatusCode)
		}
	}
	if share := float64(failed) / float64(len(reqs)); math.Abs(share-0.023) > 0.004 {
		t.Errorf("failure share %.4f, want 0.023", share)
	}
}

func TestAppOutcomeMixWeightsAndValidation(t *testing.T) {
	doc := strings.Replace(mixApp, "    - {status: 400, code: VALIDATION_ERROR, weight: 1}\n",
		"    - {status: 400, code: VALIDATION_ERROR, weight: 3}\n    - {status: 503, weight: 1}\n", 1)
	doc = strings.Replace(doc, "error_rate: 0.023", "error_rate: 1", 1)
	w := buildApp(t, decodeStrict(t, doc))
	counts := map[string]int{}
	for _, r := range mintMany(w, shape.New("UTC", nil), 2) {
		counts[r.ErrorKind]++
		if r.ErrorKind == "503" && r.Outcome != ledger.OutcomeServerError {
			t.Fatalf("503 outcome %v", r.Outcome)
		}
	}
	total := float64(counts["VALIDATION_ERROR"] + counts["503"])
	if share := float64(counts["VALIDATION_ERROR"]) / total; math.Abs(share-0.75) > 0.03 {
		t.Errorf("weight 3:1 gave VALIDATION_ERROR share %.3f", share)
	}
	for _, tc := range []struct{ old, new, want string }{
		{"success_status: 201", "success_status: 302", "success_status 302"},
		{"status: 400,", "status: 302,", "status 302"},
		{"weight: 1}", "weight: 0}", "weight must be"},
	} {
		cfg := decodeStrict(t, strings.Replace(mixApp, tc.old, tc.new, 1))
		_, err := build(cfg, core.Binding{Name: "x", Env: coretest.Env(), Cluster: coretest.Cluster()})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.new, err, tc.want)
		}
	}
}
