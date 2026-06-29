// SPDX-License-Identifier: AGPL-3.0-only

// Package httpretry provides a simple bounded retry policy for HTTP sink pushes.
// It models Alloy's retry approach: bounded by wall-clock budget (MaxElapsed),
// exponential backoff with optional jitter, and a caller-supplied retryability predicate.
//
// Three ready-made policy constructors cover the three sink classes:
//   - MetricsPolicy   — promrw (tight budget, no jitter, transport+429+5xx)
//   - EmitOncePolicy  — loki/faro (looser budget, half-jitter, transport+429+5xx)
//   - OTLPPolicy      — otlp (as EmitOnce but excludes 500 per the OTLP spec)
package httpretry

import (
	"context"
	"math/rand/v2"
	"time"
)

// Policy describes a bounded-retry strategy.
//
// MaxElapsed: total wall-clock budget from the first call to Do.
// InitialDelay: first inter-attempt sleep.
// MaxDelay: upper bound on any single sleep (clamped before jitter).
// Multiplier: each sleep is multiplied by this before the next attempt (≥1.0 or Do degenerates).
// Jitter: fractional jitter — sleep ±= Jitter*sleep (0 = deterministic).
// Retryable: if non-nil, called after every failed attempt; false = give up immediately.
//
// The zero value performs a single attempt with no retry.
type Policy struct {
	MaxElapsed   time.Duration
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	Jitter       float64
	Retryable    func(status int, err error) bool
}

// Do executes attempt repeatedly until it returns nil, the budget expires, or the
// Retryable predicate says to stop. attempt must be safe to call multiple times;
// callers must rebuild per-attempt request bodies from a []byte buffer (not a consumed
// io.Reader). attempt returns the HTTP status (0 for transport errors) and any error.
// Do returns the last non-nil error, or nil on success.
func (p Policy) Do(ctx context.Context, attempt func(context.Context) (status int, err error)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	deadline := time.Now().Add(p.MaxElapsed)
	delay := p.InitialDelay

	var lastErr error
	for {
		status, err := attempt(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		// Check retryability predicate.
		if p.Retryable != nil && !p.Retryable(status, err) {
			return lastErr
		}

		// Check remaining budget.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return lastErr
		}

		// Compute sleep with optional jitter.
		sleep := delay
		if p.Jitter > 0 {
			// ±Jitter fraction: add or subtract a random portion.
			jitterAmt := time.Duration(float64(sleep) * p.Jitter * (rand.Float64()*2 - 1)) //nolint:gosec
			sleep += jitterAmt
			if sleep < 0 {
				sleep = 0
			}
		}

		// Clamp to MaxDelay and remaining budget.
		if p.MaxDelay > 0 && sleep > p.MaxDelay {
			sleep = p.MaxDelay
		}
		if sleep > remaining {
			sleep = remaining
		}

		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(sleep):
		}

		// Advance delay for the next round.
		if p.Multiplier > 1 {
			next := time.Duration(float64(delay) * p.Multiplier)
			if p.MaxDelay > 0 && next > p.MaxDelay {
				next = p.MaxDelay
			}
			delay = next
		}
	}
}

// isTransport returns true for transport-level errors (status <= 0 means no HTTP response).
func isTransport(status int) bool { return status <= 0 }

// isRetryableHTTP returns true for 429 and 5xx responses.
func isRetryableHTTP(status int) bool {
	return status == 429 || (status >= 500 && status <= 599)
}

// MetricsPolicy returns a Policy suited for Prometheus remote_write (promrw).
// Tight budget (2.5s), fast ramp (30ms→500ms, 2×), no jitter (deterministic),
// retries transport errors + 429 + 5xx.
func MetricsPolicy() Policy {
	return Policy{
		MaxElapsed:   2500 * time.Millisecond,
		InitialDelay: 30 * time.Millisecond,
		MaxDelay:     500 * time.Millisecond,
		Multiplier:   2.0,
		Jitter:       0,
		Retryable: func(status int, err error) bool {
			return isTransport(status) || isRetryableHTTP(status)
		},
	}
}

// EmitOncePolicy returns a Policy suited for single-emit sinks (loki, faro).
// Looser budget (3s), slower ramp (500ms→3s, 1.5×), 50% jitter,
// retries transport errors + 429 + 5xx.
func EmitOncePolicy() Policy {
	return Policy{
		MaxElapsed:   3 * time.Second,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     3 * time.Second,
		Multiplier:   1.5,
		Jitter:       0.5,
		Retryable: func(status int, err error) bool {
			return isTransport(status) || isRetryableHTTP(status)
		},
	}
}

// OTLPPolicy returns a Policy modelling Grafana Alloy's otlphttp exporter retry_on_failure
// defaults (collector v0.147.0): initial 5s, max 30s, max-elapsed 5m, randomization 0.5,
// multiplier 1.5. Excludes 500 per the OTLP spec (only transport + 429 + 502/503/504 retry).
// NOTE: a persistent gateway failure can block a queue sender shard up to ~5m here — this is
// faithful to Alloy and combines with synthkit's blocking queue to apply backpressure
// (Alloy would instead drop on queue overflow; we WARN + backpressure on purpose).
func OTLPPolicy() Policy {
	return Policy{
		MaxElapsed:   5 * time.Minute,
		InitialDelay: 5 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   1.5,
		Jitter:       0.5,
		Retryable: func(status int, err error) bool {
			if isTransport(status) {
				return true
			}
			return status == 429 || status == 502 || status == 503 || status == 504
		},
	}
}
