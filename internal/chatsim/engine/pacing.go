// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"
)

// MaxTypingDelay caps the simulated read-plus-typing delay of one line.
const MaxTypingDelay = 30 * time.Second

// NewRNG returns the run's only source of randomness: a PCG seeded from the scenario seed and
// the run id, so a run is reproducible from (seed, run id) alone.
func NewRNG(seed uint64, runID string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(runID))
	return rand.New(rand.NewPCG(seed, h.Sum64()))
}

// TypingDelay is how long a persona takes to "read and type" text: a read time scaled by
// responsiveness (0..1, higher is quicker), plus len(text) at wpm (five characters a word),
// with ±20% seeded jitter, capped at MaxTypingDelay.
func TypingDelay(rng *rand.Rand, text string, wpm int, responsiveness float64) time.Duration {
	if wpm <= 0 {
		wpm = 60
	}
	read := time.Second + time.Duration((1-responsiveness)*float64(5*time.Second))
	words := float64(len([]rune(text))) / 5
	typing := time.Duration(words / float64(wpm) * float64(time.Minute))
	jitter := 0.8 + 0.4*rng.Float64()
	d := time.Duration(float64(read+typing) * jitter).Round(10 * time.Millisecond)
	return min(d, MaxTypingDelay)
}

// Between returns a uniform duration in [lo, hi].
func Between(rng *rand.Rand, lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(rng.Int64N(int64(hi-lo)+1)).Round(10*time.Millisecond)
}

const tokenEpsilon = 1e-9

// TokenBucket is a classic token bucket. The engine keeps one per channel at 1 token per
// second with a burst of 1, the documented per-channel posting pace.
type TokenBucket struct {
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

// NewTokenBucket starts full.
func NewTokenBucket(rate, burst float64) *TokenBucket {
	return &TokenBucket{rate: rate, burst: burst, tokens: burst}
}

func (b *TokenBucket) refill(now time.Time) {
	if b.last.IsZero() {
		b.last = now
		return
	}
	if now.After(b.last) {
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
	}
}

// Ready reports when the next token is available (now if one is).
func (b *TokenBucket) Ready(now time.Time) time.Time {
	b.refill(now)
	if b.tokens >= 1-tokenEpsilon {
		return now
	}
	need := (1 - b.tokens) / b.rate
	return now.Add(time.Duration(math.Ceil(need*1000)) * time.Millisecond)
}

// Take consumes a token if one is available.
func (b *TokenBucket) Take(now time.Time) bool {
	b.refill(now)
	if b.tokens >= 1-tokenEpsilon {
		b.tokens = max(0, b.tokens-1)
		return true
	}
	return false
}
