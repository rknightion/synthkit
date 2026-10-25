// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// nestingViolations lists every span that is not inside its parent's window. A child of the same
// trace must start at or after its parent and end at or before it: a blocking call cannot outlive
// the span waiting on it. Only a producer or consumer edge may cross a window (an asynchronous
// hand-off), so a CONSUMER child is exempt from the end rule.
func nestingViolations(batch []otlp.Resource) []string { return nestingViolationsExcept(batch, nil) }

// nestingViolationsExcept is nestingViolations, leaving the end rule off for the spans in exempt
// (by span id); they must still start inside their parent.
func nestingViolationsExcept(batch []otlp.Resource, exempt map[string]bool) []string {
	byID := map[string]otlp.Span{}
	for _, res := range batch {
		for _, sp := range res.Spans {
			byID[sp.SpanID] = sp
		}
	}
	var out []string
	for _, res := range batch {
		for _, sp := range res.Spans {
			if sp.ParentID == "" {
				continue
			}
			p, ok := byID[sp.ParentID]
			if !ok {
				continue
			}
			if sp.Start.Before(p.Start) {
				out = append(out, fmt.Sprintf("%q starts %v before its parent %q", sp.Name, p.Start.Sub(sp.Start), p.Name))
			}
			if sp.Kind != otlp.KindConsumer && !exempt[sp.SpanID] && sp.End.After(p.End) {
				out = append(out, fmt.Sprintf("%q ends %v after its parent %q", sp.Name, sp.End.Sub(p.End), p.Name))
			}
		}
	}
	return out
}

// TestShippedAppSpansNestInsideTheirParent mints a few hundred healthy requests for every app
// declaration in the shipped blueprints and requires every child span to sit inside its parent's
// window, on the legacy nesting. The causal timeline is covered by TestCausalSpansNestInsideTheirParent.
func TestShippedAppSpansNestInsideTheirParent(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	for _, tc := range appParityDeclarations(t) {
		t.Run(tc.key, func(t *testing.T) {
			previous := cryptorand.Reader
			cryptorand.Reader = newParityRandomReader(tc.key)
			defer func() { cryptorand.Reader = previous }()

			var decs []*fixture.DB
			seen := map[string]bool{}
			for _, svc := range tc.cfg.Services {
				if svc.DBInstance == "" {
					continue
				}
				for _, name := range []string{svc.DBInstance, svc.DBInstance + "-prod"} {
					if !seen[name] {
						seen[name] = true
						decs = append(decs, &fixture.DB{Engine: "postgres", Name: name, InstanceKey: "postgresql://db.internal:5432/app", Env: coretest.Env(), Cloud: coretest.Cloud()})
					}
				}
			}
			w, err := build(tc.cfg, core.Binding{Name: "app-nesting", Seed: tc.key, Env: coretest.Env(), Cluster: coretest.Cluster(), Databases: decs})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			workload := w.(*Workload)
			traces := &appParityTraceWriter{}
			world := &core.World{Shape: shape.New("", nil), Traces: traces}
			mintShape := shape.New("", nil)
			var batch []*ledger.Request
			for i := 0; i < 300; i++ {
				batch = append(batch, workload.m.mintOne(now, mintShape))
			}
			for _, r := range batch {
				traces.batch = nil
				if err := workload.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
					t.Fatalf("ProjectBatch: %v", err)
				}
				if v := nestingViolations(traces.batch); len(v) > 0 {
					t.Fatalf("request %s (%d hops): %d span(s) outside their parent, e.g. %s", r.TraceID, len(r.Calls), len(v), v[0])
				}
			}
		})
	}
}

// TestCausalSpansNestInsideTheirParent holds the causal timeline to the same rule: healthy and
// touched requests (slowed, retried, timed out, rejected) keep every span inside its parent. The
// one exception is the callee SERVER span of a timed-out call, which keeps its own duration after
// the caller has given up.
func TestCausalSpansNestInsideTheirParent(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	for _, c := range []struct {
		name, mode, target string
		intensity          float64
	}{
		{"healthy", causal.ModeDependencySlow, "records-db", 0},
		{"dependency_slow", causal.ModeDependencySlow, "records-db", 1},
		{"credential_expired", causal.ModeCredentialExpired, "partner-api-cert", 1},
		{"pool_exhausted", causal.ModePoolExhausted, "intake-db-pool", 1},
		{"dependency_unavailable", causal.ModeDependencyUnavailable, "partner-api", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng := incident(c.mode, c.target, c.intensity)
			reqs := mintAt(w, eng, causalNow)
			if len(reqs) > 200 {
				reqs = reqs[:200]
			}
			p := project(t, w, eng, reqs)
			timedOut := map[string]bool{}
			for _, r := range reqs {
				for _, call := range r.Calls {
					if call.TimedOut {
						timedOut[call.PeerSpanID] = true
					}
				}
			}
			if v := nestingViolationsExcept(p.all, timedOut); len(v) > 0 {
				t.Fatalf("%d span(s) outside their parent, e.g. %s", len(v), v[0])
			}
		})
	}
}
