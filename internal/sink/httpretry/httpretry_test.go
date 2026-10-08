// SPDX-License-Identifier: AGPL-3.0-only

package httpretry

import (
	"context"
	"errors"
	"github.com/rknightion/synthkit/internal/ha"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHARealHTTPResponseCancellationCannotBecomeSuccess(t *testing.T) {
	cancelled := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer srv.Close()
	g := ha.NewGate()
	_ = g.Activate(context.Background(), func(context.Context) error { return nil })
	d := Delivery{Gate: g, HTTPTimeout: 30 * time.Millisecond}
	hc := &http.Client{}
	d.ConfigureClient(hc)
	req, _ := http.NewRequest("POST", srv.URL, nil)
	_, err := d.Send(context.Background(), hc, req, func(resp *http.Response) (int, error) {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancelled response read acknowledged as success", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("real HTTP server did not observe cancellation")
	}
	g.Revoke()
	_, err = d.Send(context.Background(), hc, req, func(*http.Response) (int, error) { t.Fatal("post-fence response"); return 0, nil })
	if !errors.Is(err, ha.ErrNotLeader) || calls.Load() != 1 {
		t.Fatal("late HTTP admission", err, calls.Load())
	}
}

func TestRetrySleepPreservesTerminalErrorIdentity(t *testing.T) {
	for _, terminal := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(terminal.Error(), func(t *testing.T) {
			attempts := 0
			p := Policy{MaxElapsed: time.Hour, InitialDelay: time.Second,
				Retryable: func(int, error) bool { return true },
				sleep:     func(context.Context, time.Duration) error { return terminal },
			}
			err := p.Do(context.Background(), func(context.Context) (int, error) {
				attempts++
				return 503, errTransport
			})
			if !errors.Is(err, terminal) || !errors.Is(err, errTransport) || attempts != 1 {
				t.Fatalf("sleep cancellation identity: error=%v attempts=%d", err, attempts)
			}
		})
	}
}

func TestFakeClockRetryBudgetIncludesBoundaryFinalAttempt(t *testing.T) {
	for _, e := range []time.Duration{0, 3 * time.Second} {
		clock := time.Unix(0, 0)
		start := clock
		attempts := 0
		p := Policy{MaxElapsed: e, InitialDelay: 3 * time.Second, Retryable: func(int, error) bool { return true }, now: func() time.Time { return clock }, sleep: func(_ context.Context, d time.Duration) error { clock = clock.Add(d); return nil }}
		err := p.Do(context.Background(), func(context.Context) (int, error) {
			attempts++
			if e == 0 || attempts == 2 {
				clock = clock.Add(5 * time.Second)
			}
			return 503, errTransport
		})
		wantAttempts := 2
		if e == 0 {
			wantAttempts = 1
		}
		if err != errTransport || attempts != wantAttempts || clock.Sub(start) != e+5*time.Second {
			t.Fatalf("retry boundary: E=%v attempts=%d elapsed=%v err=%v", e, attempts, clock.Sub(start), err)
		}
	}
}

func TestHARequestFenceRedirectAndRetry(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	g := ha.NewGate()
	_ = g.Activate(context.Background(), func(context.Context) error { return nil })
	d := Delivery{Gate: g, HTTPTimeout: time.Second, RetryMaxElapsed: 10 * time.Millisecond}
	hc := &http.Client{}
	d.ConfigureClient(hc)
	req, _ := http.NewRequest(http.MethodPost, source.URL, nil)
	if _, err := d.Send(req.Context(), hc, req, func(resp *http.Response) (int, error) { return resp.StatusCode, nil }); err != nil {
		t.Fatal(err)
	}
	if redirected.Load() != 0 {
		t.Fatal("ungated redirect followed")
	}
	g.Revoke()
	attempts := 0
	err := d.Policy(OTLPPolicy()).Do(context.Background(), func(ctx context.Context) (int, error) {
		attempts++
		return d.Send(ctx, hc, req, func(resp *http.Response) (int, error) { t.Fatal("post-fence request"); return 0, nil })
	})
	if !errors.Is(err, ha.ErrNotLeader) || attempts != 1 {
		t.Fatalf("retry lost terminal fence: attempts=%d err=%v", attempts, err)
	}
}

// errTransport is a synthetic transport-level error (no HTTP status).
var errTransport = errors.New("transport: connection refused")

// ---------------------------------------------------------------------------
// Core retry behaviour
// ---------------------------------------------------------------------------

func TestRetriesOn429(t *testing.T) {
	var calls int32
	p := MetricsPolicy()
	err := p.Do(context.Background(), func(_ context.Context) (int, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return 429, errors.New("rate limited")
		}
		return 200, nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if atomic.LoadInt32(&calls) < 3 {
		t.Fatalf("calls = %d, want ≥3", atomic.LoadInt32(&calls))
	}
}

func TestRetriesOn503(t *testing.T) {
	var calls int32
	p := EmitOncePolicy()
	err := p.Do(context.Background(), func(_ context.Context) (int, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			return 503, errors.New("service unavailable")
		}
		return 200, nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("calls = %d, want ≥2", atomic.LoadInt32(&calls))
	}
}

func TestRetriesOnTransportError(t *testing.T) {
	var calls int32
	p := MetricsPolicy()
	err := p.Do(context.Background(), func(_ context.Context) (int, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			return 0, errTransport
		}
		return 200, nil
	})
	if err != nil {
		t.Fatalf("expected success after transport retry, got: %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("calls = %d, want ≥2", atomic.LoadInt32(&calls))
	}
}

func TestGivesUpAfterMaxElapsed(t *testing.T) {
	p := Policy{
		MaxElapsed:   50 * time.Millisecond,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   1.0,
		Retryable:    func(int, error) bool { return true },
	}
	start := time.Now()
	var calls int32
	err := p.Do(context.Background(), func(_ context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 503, errors.New("still down")
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error after budget exhaustion, got nil")
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("took %v — well over the 50ms budget", elapsed)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("expected multiple calls within budget, got %d", atomic.LoadInt32(&calls))
	}
}

func TestStopsOnNonRetryable4xx(t *testing.T) {
	var calls int32
	p := MetricsPolicy() // MetricsPolicy does NOT retry 400
	err := p.Do(context.Background(), func(_ context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 400, errors.New("bad request")
	})
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 400)", n)
	}
}

func TestRespectsCtxCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	down := errors.New("down")
	var calls int32
	p := Policy{
		MaxElapsed:   5 * time.Second,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   1.0,
		Retryable:    func(int, error) bool { return true },
	}
	// Cancel after the first attempt.
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	err := p.Do(ctx, func(_ context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 503, down
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, down) {
		t.Fatalf("retry sleep lost cancellation or original failure: %v", err)
	}
	// Should not retry much after cancel.
	if n := atomic.LoadInt32(&calls); n > 3 {
		t.Errorf("too many calls after cancel: %d", n)
	}
}

// ---------------------------------------------------------------------------
// OTLP-specific: 500 is NOT retried
// ---------------------------------------------------------------------------

func TestOTLPPolicyExcludes500(t *testing.T) {
	var calls int32
	p := OTLPPolicy()
	err := p.Do(context.Background(), func(_ context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 500, errors.New("internal server error")
	})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("calls = %d, want 1 (500 not retried by OTLP policy)", n)
	}
}

// ---------------------------------------------------------------------------
// Jitter: zero jitter is deterministic
// ---------------------------------------------------------------------------

func TestJitterZeroIsDeterministic(t *testing.T) {
	// With Jitter=0 the sleep sequence is fixed. Run twice and verify both
	// sequences produce the same attempt count within the same tight budget.
	makePolicy := func() Policy {
		return Policy{
			MaxElapsed:   60 * time.Millisecond,
			InitialDelay: 5 * time.Millisecond,
			MaxDelay:     20 * time.Millisecond,
			Multiplier:   2.0,
			Jitter:       0,
			Retryable:    func(int, error) bool { return true },
		}
	}

	countAttempts := func() int32 {
		var calls int32
		_ = makePolicy().Do(context.Background(), func(_ context.Context) (int, error) {
			atomic.AddInt32(&calls, 1)
			return 503, errors.New("still down")
		})
		return atomic.LoadInt32(&calls)
	}

	a, b := countAttempts(), countAttempts()
	// With deterministic sleep the counts must be within 1 of each other
	// (tiny wall-clock variation). They must certainly not diverge wildly.
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	if diff > 2 {
		t.Errorf("jitter=0 attempt counts diverged: %d vs %d", a, b)
	}
}

// ---------------------------------------------------------------------------
// Constructor defaults
// ---------------------------------------------------------------------------

func TestMetricsPolicyConstructorDefaults(t *testing.T) {
	p := MetricsPolicy()
	if p.MaxElapsed != 2500*time.Millisecond {
		t.Errorf("MaxElapsed = %v, want 2.5s", p.MaxElapsed)
	}
	if p.InitialDelay != 30*time.Millisecond {
		t.Errorf("InitialDelay = %v, want 30ms", p.InitialDelay)
	}
	if p.MaxDelay != 500*time.Millisecond {
		t.Errorf("MaxDelay = %v, want 500ms", p.MaxDelay)
	}
	if p.Multiplier != 2.0 {
		t.Errorf("Multiplier = %v, want 2.0", p.Multiplier)
	}
	if p.Jitter != 0 {
		t.Errorf("Jitter = %v, want 0", p.Jitter)
	}
	if p.Retryable == nil {
		t.Fatal("Retryable must not be nil")
	}
	// Should retry transport + 429 + 5xx but not 400.
	if !p.Retryable(0, errors.New("transport")) {
		t.Error("should retry transport (status=0)")
	}
	if !p.Retryable(429, nil) {
		t.Error("should retry 429")
	}
	if !p.Retryable(503, nil) {
		t.Error("should retry 503")
	}
	if p.Retryable(400, nil) {
		t.Error("should NOT retry 400")
	}
}

func TestEmitOncePolicyConstructorDefaults(t *testing.T) {
	p := EmitOncePolicy()
	if p.MaxElapsed != 3*time.Second {
		t.Errorf("MaxElapsed = %v, want 3s", p.MaxElapsed)
	}
	if p.InitialDelay != 500*time.Millisecond {
		t.Errorf("InitialDelay = %v, want 500ms", p.InitialDelay)
	}
	if p.MaxDelay != 3*time.Second {
		t.Errorf("MaxDelay = %v, want 3s", p.MaxDelay)
	}
	if p.Multiplier != 1.5 {
		t.Errorf("Multiplier = %v, want 1.5", p.Multiplier)
	}
	if p.Jitter != 0.5 {
		t.Errorf("Jitter = %v, want 0.5", p.Jitter)
	}
	if p.Retryable == nil {
		t.Fatal("Retryable must not be nil")
	}
	if !p.Retryable(0, errors.New("transport")) {
		t.Error("should retry transport (status=0)")
	}
	if !p.Retryable(429, nil) {
		t.Error("should retry 429")
	}
	if !p.Retryable(500, nil) {
		t.Error("EmitOnce should retry 500 (unlike OTLP)")
	}
	if p.Retryable(400, nil) {
		t.Error("should NOT retry 400")
	}
}

// ---------------------------------------------------------------------------
// OTLPPolicy timing: must match Alloy otlphttp retry_on_failure defaults
// ---------------------------------------------------------------------------

func TestOTLPPolicyMatchesAlloyDefaults(t *testing.T) {
	p := OTLPPolicy()
	if p.InitialDelay != 5*time.Second {
		t.Errorf("InitialDelay = %v, want 5s (Alloy retry_on_failure.initial_interval)", p.InitialDelay)
	}
	if p.MaxDelay != 30*time.Second {
		t.Errorf("MaxDelay = %v, want 30s (Alloy max_interval)", p.MaxDelay)
	}
	if p.MaxElapsed != 5*time.Minute {
		t.Errorf("MaxElapsed = %v, want 5m (Alloy max_elapsed_time)", p.MaxElapsed)
	}
	// 500 stays non-retryable (OTLP spec); 503 is retryable.
	if p.Retryable(500, nil) {
		t.Error("500 must not be retryable (OTLP spec)")
	}
	if !p.Retryable(503, nil) {
		t.Error("503 must be retryable")
	}
}

// ---------------------------------------------------------------------------
// Already-cancelled context
// ---------------------------------------------------------------------------

func TestCancelledCtxReturnedImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done
	p := MetricsPolicy()
	var calls int32
	err := p.Do(ctx, func(_ context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 200, nil
	})
	if err == nil {
		t.Fatal("expected error for already-cancelled ctx")
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Errorf("calls = %d, want 0 (ctx already cancelled before first attempt)", n)
	}
}
