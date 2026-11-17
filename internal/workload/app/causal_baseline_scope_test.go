// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// A slowdown in one branch must not turn the normal baseline error/warning logs
// of an unrelated branch into successful per-hop logs.
func TestCausalBaselineDecorationOwnPath(t *testing.T) {
	for _, status := range []int{400, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			doc := fmt.Sprintf(`
traffic:
  off_peak_rps: 5
  peak_rps: 5
  error_mix: [{status: %d, code: BASELINE, weight: 1}]
services:
  - name: front
    entry: true
    error_rate: 0.5
    calls: [quiet, slow]`, status) + causalLogBody + `
  - name: quiet
    error_rate: 0` + causalLogBody + `
  - name: slow
    error_rate: 0
    uses: [dep]` + causalLogBody + "\n"
			resources := `- {name: dep, kind: dependency, latency_ms: 100, slow_factor: 4}`
			var healthyFailed int
			for _, mode := range []string{"", causal.ModeDependencySlow, causal.ModeDependencyUnavailable} {
				faulted := mode != ""
				w := causalApps(t, resources, doc)[0]
				eng := shape.New("UTC", nil)
				if faulted {
					eng = incident(mode, "dep", 1)
				}
				reqs := mintAt(w, eng, causalNow)
				p := project(t, w, eng, reqs)
				var failures, successes, own int
				for _, r := range reqs {
					failed := r.Outcome != ledger.OutcomeSuccess
					if failed {
						failures++
					} else {
						successes++
					}
					quiet := p.linesFor("quiet", r)
					if len(quiet) != 1 {
						t.Fatalf("quiet: %d lines", len(quiet))
					}
					want := "info"
					if failed && mode != causal.ModeDependencyUnavailable {
						want = logLevel(r.Outcome)
					}
					if quiet[0].level != want {
						t.Fatalf("unrelated quiet baseline %d: level %s, want %s (faulted=%v)", r.StatusCode, quiet[0].level, want, faulted)
					}
					if failed && mode != causal.ModeDependencyUnavailable && quiet[0].field("error_code") != "BASELINE" {
						t.Fatal("lost baseline error code")
					}
					for _, sp := range p.byReq[r.TraceID] {
						if sp.End.After(w.renderStart(r).Add(r.Duration)) || sp.End.Before(sp.Start) {
							t.Fatal("inconsistent span timing")
						}
					}
					slow := p.linesFor("slow", r)
					if mode == causal.ModeDependencyUnavailable {
						own++
						c := callsTo(r, "slow")[0]
						sp := p.spans[c.SpanID]
						if len(slow) != 0 || c.PeerSpanID != "" || r.Fault == nil || r.Fault.Resource != "dep" || sp.Status != otlp.StatusError || sp.Attrs[attrErrorType] == nil {
							t.Fatal("transport failure lost own-path decoration or logged an unreached callee")
						}
						continue
					}
					if len(slow) != 1 {
						t.Fatalf("slow: %d lines", len(slow))
					}
					if faulted {
						own++
						if slow[0].level != "info" || slow[0].field("fault_resource") != "dep" || slow[0].field("fault_mode") != mode {
							t.Fatalf("own-path slowdown log: %+v", slow[0])
						}
						c := callsTo(r, "slow")[0]
						if !slow[0].t.Equal(w.renderStart(r).Add(c.Offset + c.Duration)) {
							t.Fatal("slow log does not end with own hop")
						}
					}
				}
				t.Logf("status=%d mode=%q failures=%d successes=%d own-path=%d", status, mode, failures, successes, own)
				if failures == 0 || mode != causal.ModeDependencyUnavailable && successes == 0 {
					t.Fatalf("nonzero controls: failures=%d successes=%d", failures, successes)
				}
				if !faulted {
					healthyFailed = failures
				} else if own == 0 {
					t.Fatal("no own-path causal logs")
				}
			}
			if healthyFailed == 0 {
				t.Fatal("missing healthy baseline failures")
			}
		})
	}
}
