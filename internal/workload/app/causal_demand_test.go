// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// appArrivals counts what one minute of the intake app's requests actually delivers to each
// capacity resource it uses: the pool sees every request the quota admits, the function every
// enrich-fn attempt, the queue every publisher call.
func appArrivals(reqs []*ledger.Request) map[string]float64 {
	out := map[string]float64{}
	for _, r := range reqs {
		out["intake-quota"]++
		if !(r.Fault != nil && r.Fault.Resource == "intake-quota" && len(r.Calls) == 0) {
			out["intake-db-pool"]++
		}
		out["enrich-fn-concurrency"] += float64(len(callsTo(r, "enrich-fn")))
		out["validation-queue"] += float64(len(callsTo(r, "validation-publisher")))
	}
	return out
}

// throttleOnlyIntake is intakeApp with the L11 stories' intake -> enrich-fn policy: throttles
// resent, a 502 from the function not.
var throttleOnlyIntake = strings.Replace(intakeApp, "retry_on: [throttle, 5xx]", "retry_on: [throttle]", 1)

// A bound construct's volume follows the calls that reach its resource (SKT-0168.09.33). The
// lambda, SQS and Solace constructs count the shared model's window, so the model's arrivals over
// the minute must equal what the app's requests deliver: under every story, an upstream rejection
// or an unreachable node lowers the downstream resource's volume to the calls that get there
// (story 1: the queue gets nothing while the publisher is unreachable). Healthy, it is the
// declared demand. A caller's resends of a failure deeper in the callee's subtree count too
// (SKT-0195): under intakeApp's 5xx policy, story 1's three enrich-fn attempts per request are
// 10800 function arrivals a minute, not 3600.
func TestCausalBoundVolumeFollowsAdmittedCalls(t *testing.T) {
	for _, app := range []struct{ name, doc string }{
		{"throttle only", throttleOnlyIntake},
		{"throttle and 5xx", intakeApp},
	} {
		t.Run(app.name, func(t *testing.T) { boundVolumeStories(t, app.doc) })
	}
}

func boundVolumeStories(t *testing.T, appDoc string) {
	for _, tc := range []struct {
		name string
		eng  func() *shape.Engine
		zero string // a resource no call reaches
	}{
		{"healthy", func() *shape.Engine { return shape.New("UTC", nil) }, ""},
		{"story 1 certificate", func() *shape.Engine { return incident(causal.ModeCredentialExpired, "partner-api-cert", 1) }, "validation-queue"},
		{"story 2 throttle", func() *shape.Engine { return incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 0.7) }, ""},
		{"story 3 rule", func() *shape.Engine { return incident(causal.ModeRuleRegression, "completeness-rule", 1) }, ""},
		{"story 5 pool", func() *shape.Engine { return incident(causal.ModeDependencySlow, "records-db", 1) }, ""},
		{"story 5 quota", func() *shape.Engine { return incident(causal.ModeQuotaReduced, "intake-quota", 0.5) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := causalApps(t, causalResources, appDoc)[0]
			reqs := mintAt(w, tc.eng(), causalNow)
			got := appArrivals(reqs)
			model := w.m.causal.model
			for _, name := range []string{"intake-quota", "intake-db-pool", "enrich-fn-concurrency", "validation-queue"} {
				win, ok := model.Window(name, causalNow.Add(-time.Minute), causalNow)
				if !ok {
					t.Fatalf("no window for %s", name)
				}
				// The app's count is a sample of the model's shares: allow 3 binomial sigmas.
				tol := 3*math.Sqrt(math.Max(win.Arrivals, 1)) + 0.01*win.Arrivals
				if math.Abs(win.Arrivals-got[name]) > tol {
					t.Errorf("%s: model arrivals %.0f, app delivered %.0f (tolerance %.0f)", name, win.Arrivals, got[name], tol)
				}
			}
			if tc.zero != "" {
				if win, _ := model.Window(tc.zero, causalNow.Add(-time.Minute), causalNow); win.Arrivals != 0 || got[tc.zero] != 0 {
					t.Errorf("%s: model arrivals %v, app %v; want none (no call reaches it)", tc.zero, win.Arrivals, got[tc.zero])
				}
			}
		})
	}
}
