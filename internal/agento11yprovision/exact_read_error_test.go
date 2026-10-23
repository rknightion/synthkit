// SPDX-License-Identifier: AGPL-3.0-only

//go:build darwin || linux

package agento11yprovision

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Interrupt only the selected exact GET, not inventory or an earlier prefix
// verification. Waiting on the caller (rather than the HTTP client's child
// context) exercises engineReadExact's actual caller-deadline return.
type exactReadDeadlineTransport struct {
	next      http.RoundTripper
	caller    context.Context
	remaining int
	hit       bool
}

func (d *exactReadDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/evaluators/eval-fresh") {
		d.remaining--
		if d.remaining == 0 {
			d.hit = true
			<-d.caller.Done()
			return nil, d.caller.Err()
		}
	}
	return d.next.RoundTrip(r)
}

func TestEngineExactReadDeadlineCause(t *testing.T) {
	for _, site := range []string{"prefix", "preflight", "before", "receipt", "cleanup-before"} {
		t.Run(site, func(t *testing.T) {
			f := newEngineTLS(t, false)
			// One independently declarable evaluator is sufficient for each seam.
			f.input.Resources = f.input.Resources[:1]
			f.input.Steps = f.input.Steps[:1]
			p := f.plan()
			if _, err := f.apply(p); err != nil {
				t.Fatal(err)
			}
			s := engineSnapshot(t, f)
			inv, err := engineInventoryRead(context.Background(), f.client, p, s)
			if err != nil {
				t.Fatal(err)
			}
			writes := len(f.writes)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			d := &exactReadDeadlineTransport{next: f.client.http.Transport, caller: ctx, remaining: 1}
			if site == "receipt" {
				// Receipt first verifies the owned prefix, then reads the member.
				d.remaining = 2
			}
			f.client.http.Transport = d
			r := p.plan.input.Resources[0]
			switch site {
			case "prefix":
				err = engineVerifyPrefix(ctx, f.client, p, s, inv, "")
			case "preflight":
				err = enginePreflight(ctx, f.client, p, Snapshot{}, inv, ModeApply, nil)
			case "before":
				_, _, _, err = engineBefore(ctx, f.client, p, Snapshot{}, inv, r, OpCreate)
			case "receipt":
				_, err = engineReceipt(ctx, f.client, p, s, "", inv.Digest, Preview{}, Approval{})
			case "cleanup-before":
				_, _, _, err = engineCleanupBefore(ctx, f.client, p, s, inv, r, OpDelete)
			}
			if !d.hit || len(f.writes) != writes {
				t.Fatalf("exact read not interrupted or refusal mutated backend: hit=%v writes=%v", d.hit, f.writes)
			}
			if os.Getenv("SYNTHKIT_EXACT_READ_ASSERTION_CHILD") == "1" {
				// The existing lifecycle assertion must reject this conflict, not
				// pass merely because its outer identity is ErrConflict.
				requireEngineError(t, site, err, ErrConflict)
				return
			}
			if !errors.Is(err, ErrConflict) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("want conflict AND deadline at %s, got %v", site, err)
			}
			childCtx, childCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer childCancel()
			cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestEngineExactReadDeadlineCause$/^"+site+"$", "-test.timeout=8s")
			cmd.Env = append(os.Environ(), "SYNTHKIT_EXACT_READ_ASSERTION_CHILD=1")
			out, childErr := cmd.CombinedOutput()
			if childCtx.Err() != nil || childErr == nil || !strings.Contains(string(out), "ended by a deadline or cancellation, not the behaviour under test") {
				t.Fatalf("lifecycle assertion did not reject hidden deadline: %v\n%s", childErr, out)
			}
		})
	}
}

// The management client's private deadline must survive even while the engine's
// caller remains active. Block the real TLS handler, not a substitute transport.
func TestEngineExactReadClientDeadlineCause(t *testing.T) {
	for _, site := range []string{"prefix", "preflight", "before", "receipt", "cleanup-before"} {
		t.Run(site, func(t *testing.T) {
			f := newEngineTLS(t, false)
			f.input.Resources = f.input.Resources[:1]
			f.input.Steps = f.input.Steps[:1]
			var remaining atomic.Int32
			hit := make(chan struct{}, 1)
			original := f.server.Config.Handler
			// Install before the fixture's first request; arm only after inventory.
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/evaluators/eval-fresh") && remaining.Load() > 0 {
					if remaining.Add(-1) == 0 {
						hit <- struct{}{}
						select {
						case <-r.Context().Done():
						case <-time.After(3 * time.Second):
						}
						return
					}
				}
				original.ServeHTTP(w, r)
			})
			p := f.plan()
			if _, err := f.apply(p); err != nil {
				t.Fatal(err)
			}
			s := engineSnapshot(t, f)
			inv, err := engineInventoryRead(context.Background(), f.client, p, s)
			if err != nil {
				t.Fatal(err)
			}
			writes := len(f.writes)
			remaining.Store(1)
			if site == "receipt" {
				remaining.Store(2)
			}
			f.client.timeout = 200 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			r := p.plan.input.Resources[0]
			switch site {
			case "prefix":
				err = engineVerifyPrefix(ctx, f.client, p, s, inv, "")
			case "preflight":
				err = enginePreflight(ctx, f.client, p, Snapshot{}, inv, ModeApply, nil)
			case "before":
				_, _, _, err = engineBefore(ctx, f.client, p, Snapshot{}, inv, r, OpCreate)
			case "receipt":
				_, err = engineReceipt(ctx, f.client, p, s, "", inv.Digest, Preview{}, Approval{})
			case "cleanup-before":
				_, _, _, err = engineCleanupBefore(ctx, f.client, p, s, inv, r, OpDelete)
			}
			select {
			case <-hit:
			default:
				t.Fatal("selected exact GET did not reach real TLS handler")
			}
			if ctx.Err() != nil || len(f.writes) != writes {
				t.Fatalf("caller expired or refusal mutated backend: caller=%v writes=%v", ctx.Err(), f.writes)
			}
			if os.Getenv("SYNTHKIT_EXACT_CLIENT_ASSERTION_CHILD") == "1" {
				requireEngineError(t, site, err, ErrConflict)
				return
			}
			if !errors.Is(err, ErrConflict) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("want conflict AND client deadline at %s with active caller, got %v", site, err)
			}
			for _, private := range []string{f.server.URL, "fixture-token", "eval-fresh", "Authorization"} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("exact-read error exposes request data: %v", err)
				}
			}
			childCtx, childCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer childCancel()
			cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestEngineExactReadClientDeadlineCause$/^"+site+"$", "-test.timeout=8s")
			cmd.Env = append(os.Environ(), "SYNTHKIT_EXACT_CLIENT_ASSERTION_CHILD=1")
			out, childErr := cmd.CombinedOutput()
			if childCtx.Err() != nil || childErr == nil || !strings.Contains(string(out), "ended by a deadline or cancellation, not the behaviour under test") {
				t.Fatalf("lifecycle assertion did not reject client deadline: %v\n%s", childErr, out)
			}
		})
	}
}

func TestEngineExactReadPresenceMismatchConflictOnly(t *testing.T) {
	f := newEngineTLS(t, false)
	f.input.Resources = f.input.Resources[:1]
	f.input.Steps = f.input.Steps[:1]
	p := f.plan()
	if _, err := f.apply(p); err != nil {
		t.Fatal(err)
	}
	s := engineSnapshot(t, f)
	r := p.plan.input.Resources[0]
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	inv, err := engineInventoryRead(ctx, f.client, p, s)
	if err != nil {
		t.Fatal(err)
	}
	// A successful present read where absence is required has no read cause.
	err = enginePreflight(ctx, f.client, p, Snapshot{}, inv, ModeApply, nil)
	if err != ErrConflict {
		t.Fatalf("preflight presence mismatch: want bare conflict, got %v", err)
	}
	_, _, _, err = engineBefore(ctx, f.client, p, Snapshot{}, inv, r, OpCreate)
	if err != ErrConflict {
		t.Fatalf("before presence mismatch: want bare conflict, got %v", err)
	}
	f.mu.Lock()
	delete(f.evals, r.Coordinate.ID)
	f.mu.Unlock()
	inv, err = engineInventoryRead(ctx, f.client, p, s)
	if err != nil {
		t.Fatal(err)
	}
	// Inventory-backed absence is likewise a successful read, not a cause.
	err = engineVerifyPrefix(ctx, f.client, p, s, inv, "")
	if err != ErrConflict {
		t.Fatalf("prefix absence mismatch: want bare conflict, got %v", err)
	}
	_, _, _, err = engineCleanupBefore(ctx, f.client, p, s, inv, r, OpDelete)
	if err != ErrConflict {
		t.Fatalf("cleanup absence mismatch: want bare conflict, got %v", err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("refusal mutated backend: %v", f.writes)
	}
}
