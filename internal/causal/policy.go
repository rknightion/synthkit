// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"fmt"
	"math"
)

// CallPolicy is a caller's per-edge policy toward one callee.
type CallPolicy struct {
	// TimeoutMS is the client timeout in ms; 0 means none.
	TimeoutMS float64 `yaml:"timeout_ms"`
	// Retries is the number of resends after the first attempt, 0 to 3.
	Retries int `yaml:"retries"`
	// BackoffMS is the base backoff before resend k, doubled per attempt (backoff x 2^k).
	BackoffMS float64 `yaml:"backoff_ms"`
	// RetryOn lists the failure classes that are resent: 5xx, throttle, timeout, connect.
	RetryOn []string `yaml:"retry_on"`
	// PropagateThrottle returns a callee 429 to the caller's own caller as 429 instead of 503.
	PropagateThrottle bool `yaml:"propagate_throttle"`
}

// Retry-class names accepted in CallPolicy.RetryOn.
const (
	Retry5xx      = "5xx"
	RetryThrottle = "throttle"
	RetryTimeout  = "timeout"
	RetryConnect  = "connect"
)

// MaxRetries bounds CallPolicy.Retries.
const MaxRetries = 3

// Validate checks the policy's ranges and retry classes.
func (p CallPolicy) Validate() error {
	if math.IsNaN(p.TimeoutMS) || math.IsInf(p.TimeoutMS, 0) || p.TimeoutMS < 0 {
		return fmt.Errorf("timeout_ms must be a finite number >= 0")
	}
	if p.Retries < 0 || p.Retries > MaxRetries {
		return fmt.Errorf("retries %d must be in [0, %d]", p.Retries, MaxRetries)
	}
	if math.IsNaN(p.BackoffMS) || math.IsInf(p.BackoffMS, 0) || p.BackoffMS < 0 {
		return fmt.Errorf("backoff_ms must be a finite number >= 0")
	}
	seen := map[string]bool{}
	for _, c := range p.RetryOn {
		switch c {
		case Retry5xx, RetryThrottle, RetryTimeout, RetryConnect:
		default:
			return fmt.Errorf("retry_on %q must be one of 5xx, throttle, timeout, connect", c)
		}
		if seen[c] {
			return fmt.Errorf("retry_on %q is listed twice", c)
		}
		seen[c] = true
	}
	return nil
}

// Retryable reports whether a failed attempt of class c (with HTTP status, 0 when none) is
// resent under the policy. Credential failures and other 4xx are never resent.
func (p CallPolicy) Retryable(c FailureClass, status int) bool {
	for _, r := range p.RetryOn {
		switch r {
		case Retry5xx:
			if c == ClassHTTP && status >= 500 && status <= 599 {
				return true
			}
		case RetryThrottle:
			if c == ClassThrottle {
				return true
			}
		case RetryTimeout:
			if c == ClassTimeout {
				return true
			}
		case RetryConnect:
			if c == ClassConnect {
				return true
			}
		}
	}
	return false
}

// CallerStatus maps an unabsorbed callee failure to the caller's response (§4.4 of the causal
// design): a 4xx other than 429 passes through; 429 becomes 503 (or 429 with
// PropagateThrottle); a 5xx, a connect failure or a credential failure becomes 502; a timeout
// becomes 504. A non-failure (ClassNone, or a status below 400) passes through unchanged.
// callerIsEntry is reserved: the v1 mapping is the same for the entry and an inner caller.
func CallerStatus(p CallPolicy, c FailureClass, status int, callerIsEntry bool) (int, FailureClass) {
	_ = callerIsEntry
	switch c {
	case ClassNone:
		return status, ClassNone
	case ClassTimeout:
		return 504, ClassHTTP
	case ClassConnect, ClassCredential:
		return 502, ClassHTTP
	case ClassThrottle:
		if p.PropagateThrottle {
			return 429, ClassThrottle
		}
		return 503, ClassHTTP
	}
	switch {
	case status == 429:
		if p.PropagateThrottle {
			return 429, ClassThrottle
		}
		return 503, ClassHTTP
	case status >= 500:
		return 502, ClassHTTP
	case status >= 400:
		return status, ClassHTTP
	default:
		return status, ClassNone
	}
}
