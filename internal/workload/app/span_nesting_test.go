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

// nestingViolationsExcept checks windows for spans with a parent present in the batch.
// nestingRequestViolations also rejects unexpected parentless spans. A blocking child must start
// at or after its parent and end at or before it. CONSUMER spans and explicitly exempt spans
// (by span id) may end later, but must still start inside their parent.
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

// nestingRequestViolations exempts only the ledger's exact request roots from requiring a
// parent. Timeout and CONSUMER exceptions relax the end rule, never the parent requirement.
func nestingRequestViolations(batch []otlp.Resource, reqs []*ledger.Request, exempt map[string]bool) []string {
	roots := map[[2]string]bool{}
	for _, r := range reqs {
		roots[[2]string{r.TraceID, r.SpanID}] = true
	}
	out := nestingViolationsExcept(batch, exempt)
	for _, res := range batch {
		for _, sp := range res.Spans {
			if sp.ParentID == "" && !roots[[2]string{sp.TraceID, sp.SpanID}] {
				out = append(out, fmt.Sprintf("%q is parentless but is not a request root", sp.Name))
			}
		}
	}
	return out
}

// TestNestingRejectsParentlessChildren checks the nesting oracle itself: losing a child's parent
// must not turn it into an exempt request root, even for timeout and asynchronous spans.
func TestNestingRejectsParentlessChildren(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	root := otlp.Span{TraceID: "trace", SpanID: "root", Name: "entry", Kind: otlp.KindServer, Start: now, End: now.Add(time.Second)}
	for _, kind := range []otlp.SpanKind{otlp.KindClient, otlp.KindServer, otlp.KindConsumer} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			child := otlp.Span{TraceID: root.TraceID, SpanID: "child", Name: "child", Kind: kind, Start: now, End: now.Add(2 * time.Second)}
			batch := []otlp.Resource{{Spans: []otlp.Span{root, child}}}
			reqs := []*ledger.Request{{TraceID: root.TraceID, SpanID: root.SpanID}}
			exempt := map[string]bool{child.SpanID: kind == otlp.KindServer}
			if v := nestingRequestViolations(batch, reqs, exempt); len(v) != 1 || v[0] != `"child" is parentless but is not a request root` {
				t.Fatalf("want only the parentless child violation, got %v", v)
			}
			child.ParentID = root.SpanID
			batch[0].Spans[1] = child
			want := 0
			if kind == otlp.KindClient {
				want = 1 // a blocking CLIENT overrun is never a timeout-server exception
			}
			if v := nestingRequestViolations(batch, reqs, exempt); len(v) != want {
				t.Fatalf("attached child: got %v, want %d violations", v, want)
			}
			if v := nestingRequestViolations(batch, reqs, nil); kind != otlp.KindConsumer && len(v) != 1 || kind == otlp.KindConsumer && len(v) != 0 {
				t.Fatalf("only CONSUMER may overrun without an explicit timeout exemption: %v", v)
			}
			batch[0].Spans[1].End = root.End
			batch[0].Spans[1].Start = now.Add(-time.Nanosecond)
			if v := nestingRequestViolations(batch, reqs, exempt); len(v) != 1 {
				t.Fatalf("end-exempt child starting before its parent must fail: %v", v)
			}
		})
	}
	// A matching span ID in a different trace is not an admitted request root.
	batch := []otlp.Resource{{Spans: []otlp.Span{root}}}
	reqs := []*ledger.Request{{TraceID: "other-trace", SpanID: root.SpanID}}
	if v := nestingRequestViolations(batch, reqs, nil); len(v) != 1 {
		t.Fatalf("request root exemption leaked across traces: %v", v)
	}
}

// TestShippedAppSpansNestInsideTheirParent mints a few hundred healthy requests for every app
// declaration in the shipped blueprints and requires every child span to sit inside its parent's
// window on the declaration's selected timeline. Declarations without causal resources use legacy
// nesting; causal fault cases are also covered by TestCausalSpansNestInsideTheirParent.
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
				if v := nestingRequestViolations(traces.batch, []*ledger.Request{r}, nil); len(v) > 0 {
					t.Fatalf("request %s (%d hops): %d span(s) outside their parent, e.g. %s", r.TraceID, len(r.Calls), len(v), v[0])
				}
			}
		})
	}
}

// TestCausalSpansNestInsideTheirParent holds the causal timeline to the same rule: healthy and
// touched requests (slowed, retried, timed out, rejected) keep every span inside its parent. The
// callee SERVER span of a timed-out call keeps its own duration after the caller has given up;
// CONSUMER spans may also end later. Both must still have a parent and start inside its window.
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
			if v := nestingRequestViolations(p.all, reqs, timedOut); len(v) > 0 {
				t.Fatalf("%d span(s) outside their parent, e.g. %s", len(v), v[0])
			}
		})
	}
}
