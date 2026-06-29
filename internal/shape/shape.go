// SPDX-License-Identifier: AGPL-3.0-only

// Package shape produces realistic time-varying multipliers: a business-hours diurnal
// PLATEAU (flat-topped, smoothstep ramps — never a single-cosine spike), a weekly
// pattern (weekends down; non-prod toward zero), goroutine-safe noise, and time-boxed
// schedulable incidents. Lifted from the proven source generator and de-coupled from
// any company canon: callers pass env weight/non-prod-ness from blueprint fixtures.
package shape

import (
	"fmt"
	"hash/fnv"
	"log"
	"math"
	randv2 "math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LiveFailure is one live (control-plane-set) failure mode contribution. Scope is the
// blast-radius value the setting targets ("" = un-scoped/all). The control plane (Phase
// 6) feeds these via Engine.Live; until then the hook is nil and only scheduled
// incident windows apply.
type LiveFailure struct {
	Enabled   bool
	Intensity float64 // clamped to [0,1]
	Scope     string
}

// incidentWindow is a parsed, time-boxed scripted incident. intensity defaults to 1.0
// (an omitted #intensity reproduces binary-magnitude behaviour); scope defaults to ""
// (all). A window is EXACTLY ONE of: absolute (start/end set, fires once); daily-recurring
// (daily=true, todStart/dur set, fires every day at that local time-of-day); or
// interval-recurring (interval=true, period/dur set, fires for dur out of every period,
// anchored on the Unix epoch so it is tz-independent).
type incidentWindow struct {
	kind      string
	start     time.Time     // absolute window start (zero when daily/interval)
	end       time.Time     // absolute window end (zero when daily/interval)
	daily     bool          // true ⇒ recurring daily window, matched by local time-of-day
	todStart  time.Duration // daily: offset from local midnight of the window start
	interval  bool          // true ⇒ interval-recurring window (dur out of every period)
	period    time.Duration // interval: full cycle length (active dur + off time)
	dur       time.Duration // daily/interval: active window duration
	intensity float64
	scope     string
}

// Engine holds the clock location, parsed incident schedule, the optional live
// failure-state hook, and a DETERMINISTICALLY-SEEDED RNG for Noise/Float64/IntN/NormFloat64.
//
// Determinism (I12): the math/rand/v2 GLOBAL functions are seeded with a RANDOM value at process
// start, so using them made every run produce different noise — the -dump inventory drifted and
// statistical tests flaked. The Engine instead owns its own seeded generator (rng), guarded by
// rngMu because *rand.Rand is not goroutine-safe (the historical data race). Each blueprint has
// its own Engine ticked on a single goroutine, so the draw ORDER — and thus the output — is
// reproducible run-to-run in the serial -once/-dump/test paths.
type Engine struct {
	incidents []incidentWindow
	loc       *time.Location

	rngMu sync.Mutex
	rng   *randv2.Rand

	// warnings accumulates human-readable notes about incident-schedule entries that were
	// skipped during New (bad time/dur/intensity, >=24h daily window, etc.). They are also
	// logged; surfacing them lets the control plane show config problems instead of burying
	// them in stderr. Populated only at construction → no concurrency guard needed.
	warnings []string

	// volMult is the live master volume knob (control plane): it scales Factor and
	// BusinessFactor, so request-shaped metric volume AND the ledger's correlated
	// narrative (whose minters draw from Factor) rise and fall coherently — one knob
	// moves ALL synthetic load. Stored as math.Float64bits for lock-free reads on the
	// hot path. Zero (unset) means 1.0.
	volMult atomic.Uint64

	// Live, when non-nil, returns the live failure settings for a mode (control-plane
	// seam). Scheduled windows and live state are UNIONED; intensity is the max of every
	// matching contributor.
	Live func(mode string) []LiveFailure

	// regions, when non-empty, enables the follow-the-sun composite path: diurnal/weekly
	// curves are the weight-normalised sum of per-region curves anchored to their own
	// timezones. Populated only by NewWithRegions; zero value preserves legacy behaviour.
	regions []loadedRegion
}

// warn logs a construction-time warning AND records it for later retrieval via Warnings(),
// so the control plane can surface skipped/invalid incident entries to operators.
func (e *Engine) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print(msg)
	e.warnings = append(e.warnings, msg)
}

// Warnings returns the incident-schedule entries that were skipped during New (empty when the
// schedule parsed cleanly).
func (e *Engine) Warnings() []string { return e.warnings }

// SetVolumeMultiplier sets the live master volume knob (clamped to [0, 100]).
func (e *Engine) SetVolumeMultiplier(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	e.volMult.Store(math.Float64bits(v))
}

// VolumeMultiplier returns the live master volume knob (1.0 when unset).
func (e *Engine) VolumeMultiplier() float64 {
	bits := e.volMult.Load()
	if bits == 0 {
		return 1.0
	}
	return math.Float64frombits(bits)
}

// New creates an Engine in the given IANA timezone (the business-hours anchor; "" →
// "Europe/Zurich", matching the proven plateau shape) and parses the incident schedule.
// Each entry has the form  mode@<time>/<dur>[#<intensity>][@<scope>], e.g.
//
//	latency_spike@2026-06-22T14:00:00Z/20m              (intensity 1.0, scope all)
//	error_burst@2026-06-22T15:00/45m#0.7@prod           (intensity 0.7, scope prod)
//
// Entries that cannot be parsed are logged and skipped; New never panics.
func New(tz string, incidents []string) *Engine {
	if tz == "" {
		tz = "Europe/Zurich"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("shape: unknown timezone %q, falling back to UTC: %v", tz, err)
		loc = time.UTC
	}
	e := &Engine{loc: loc, rng: newSeededRNG()}
	for _, raw := range incidents {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Split on the FIRST '@' → kind + rest.
		kindRaw, rest, found := strings.Cut(raw, "@")
		if !found {
			e.warn("shape: skipping incident entry with no '@': %q", raw)
			continue
		}
		kind := strings.TrimSpace(kindRaw)
		rest = strings.TrimSpace(rest)

		// Split on the LAST '/' → timeStr + durStr.
		slashIdx := strings.LastIndex(rest, "/")
		if slashIdx < 0 {
			e.warn("shape: skipping incident entry with no '/': %q", raw)
			continue
		}
		timeStr := strings.TrimSpace(rest[:slashIdx])
		durStr := strings.TrimSpace(rest[slashIdx+1:])

		// Peel optional [#intensity][@scope] off the dur token. Order matters: take
		// @scope first (LAST '@'), then #intensity — so "20m@prod" (scope, no
		// intensity) parses correctly. Times, durations and intensities contain none of
		// '/','#','@', so the splits are unambiguous.
		intensity := 1.0
		scope := ""
		if i := strings.LastIndex(durStr, "@"); i >= 0 {
			scope = strings.TrimSpace(durStr[i+1:])
			durStr = strings.TrimSpace(durStr[:i])
		}
		if i := strings.Index(durStr, "#"); i >= 0 {
			intStr := strings.TrimSpace(durStr[i+1:])
			durStr = strings.TrimSpace(durStr[:i])
			f, ferr := strconv.ParseFloat(intStr, 64)
			if ferr != nil {
				e.warn("shape: skipping incident entry %q — cannot parse intensity %q: %v", raw, intStr, ferr)
				continue
			}
			intensity = clamp01(f)
		}

		// Parse the time token. An "every<dur>" prefix (e.g. "every10m") makes the window
		// INTERVAL-RECURRING — it fires for <dur> out of every <period>, anchored on the Unix
		// epoch (tz-independent). Detect it FIRST, before the absolute/daily layouts (none of which
		// begin with "every"), so the interval form can never be mistaken for a wall-clock time.
		if rest, ok := strings.CutPrefix(timeStr, "every"); ok {
			period, perr := time.ParseDuration(strings.TrimSpace(rest))
			if perr != nil {
				e.warn("shape: skipping incident entry %q — cannot parse interval period %q: %v", raw, rest, perr)
				continue
			}
			dur, durErr := time.ParseDuration(durStr)
			if durErr != nil {
				e.warn("shape: skipping incident entry %q — cannot parse duration %q: %v", raw, durStr, durErr)
				continue
			}
			// Guard: a non-positive period/dur, or an active dur >= the period, would make the
			// incident permanently active (no off period) — never the intent of the interval form.
			if period <= 0 || dur <= 0 || dur >= period {
				e.warn("shape: skipping interval incident entry %q — active dur %q must be > 0 and < period %q", raw, durStr, rest)
				continue
			}
			e.incidents = append(e.incidents, incidentWindow{
				kind: kind, intensity: intensity, scope: scope,
				interval: true, period: period, dur: dur,
			})
			continue
		}

		// Parse the start time. Absolute layouts first (RFC3339, then zone-less datetime in the
		// engine's location so authors think in local business time). Failing those, a bare
		// time-of-day ("15:04"[:05]) makes the window DAILY-RECURRING — it fires every day at that
		// local wall-clock time for <dur>. Use the daily form for ongoing/periodic demo incidents
		// that should recur; use a dated form for a one-shot event.
		var start time.Time
		var parseErr error
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
			if layout == time.RFC3339 {
				start, parseErr = time.Parse(layout, timeStr)
			} else {
				start, parseErr = time.ParseInLocation(layout, timeStr, e.loc)
			}
			if parseErr == nil {
				break
			}
		}
		daily := false
		var tod time.Duration
		if parseErr != nil {
			for _, layout := range []string{"15:04:05", "15:04"} {
				t, tdErr := time.ParseInLocation(layout, timeStr, e.loc)
				if tdErr == nil {
					daily = true
					tod = time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second
					parseErr = nil
					break
				}
			}
		}
		if parseErr != nil {
			e.warn("shape: skipping incident entry %q — cannot parse time %q: %v", raw, timeStr, parseErr)
			continue
		}

		dur, durErr := time.ParseDuration(durStr)
		if durErr != nil {
			e.warn("shape: skipping incident entry %q — cannot parse duration %q: %v", raw, durStr, durErr)
			continue
		}
		// A daily window of >= 24h would match every time-of-day → a permanently-active
		// incident with no off period. That's never the intent of the recurring form (use a
		// dated window for a long one-shot); reject it rather than silently stick "on".
		if daily && dur >= 24*time.Hour {
			e.warn("shape: skipping daily incident entry %q — duration %q >= 24h would be always-active", raw, durStr)
			continue
		}

		win := incidentWindow{kind: kind, intensity: intensity, scope: scope}
		if daily {
			win.daily, win.todStart, win.dur = true, tod, dur
		} else {
			win.start, win.end = start, start.Add(dur)
		}
		e.incidents = append(e.incidents, win)
	}
	return e
}

// Loc returns the engine's clock location so callers can interpret zone-less times
// consistently with the diurnal/weekly/incident logic.
func (e *Engine) Loc() *time.Location { return e.loc }

// Factor combines diurnal × weekly × env volume weight × the live volume multiplier.
// weight and nonProd come from the blueprint's environment declaration. Result is
// unitless (≈0..1.2 at multiplier 1).
func (e *Engine) Factor(now time.Time, weight float64, nonProd bool) float64 {
	return e.diurnal(now) * e.weekly(now, nonProd) * weight * e.VolumeMultiplier()
}

// BusinessFactor is the diurnal×weekly shape for substrate estates with no environment
// weighting (dbo11y/CSP/Cloudflare): weekends scale to 0.3 — a production company keeps
// some weekend traffic, unlike a non-prod env's near-zero. Scaled by the live volume
// multiplier like Factor.
func (e *Engine) BusinessFactor(now time.Time) float64 {
	if len(e.regions) > 0 {
		return e.diurnal(now) * e.businessWeeklyComposite(now) * e.VolumeMultiplier()
	}
	w := 1.0
	switch now.In(e.loc).Weekday() {
	case time.Saturday, time.Sunday:
		w = 0.3
	}
	return e.diurnal(now) * w * e.VolumeMultiplier()
}

// smoothstep returns a smooth 0→1 transition over [0,1] using 3t²−2t³.
func smoothstep(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// diurnal returns a business-hours plateau multiplier in ≈[0.1, 1.0]:
//
//	00:00–06:00  floor ≈ 0.1   (overnight)
//	06:00–10:00  smoothstep ramp UP 0.1→1.0
//	10:00–16:00  flat top 1.0  (business-hours plateau)
//	16:00–20:00  smoothstep ramp DOWN 1.0→0.1
//	20:00–24:00  floor ≈ 0.1
func (e *Engine) diurnal(now time.Time) float64 {
	if len(e.regions) > 0 {
		return e.diurnalComposite(now)
	}
	return diurnalAt(now.In(e.loc))
}

// diurnalAt is the pure business-hours plateau in ≈[0.1,1.0] for an already-localised time.
func diurnalAt(t time.Time) float64 {
	const (
		floor    = 0.1
		peak     = 1.0
		rampUpS  = 6.0
		rampUpE  = 10.0
		plateauE = 16.0
		rampDnE  = 20.0
	)
	h := float64(t.Hour()) + float64(t.Minute())/60.0 + float64(t.Second())/3600.0

	var v float64
	switch {
	case h < rampUpS:
		v = floor
	case h < rampUpE:
		frac := (h - rampUpS) / (rampUpE - rampUpS)
		v = floor + (peak-floor)*smoothstep(frac)
	case h < plateauE:
		v = peak
	case h < rampDnE:
		frac := (h - plateauE) / (rampDnE - plateauE)
		v = peak - (peak-floor)*smoothstep(frac)
	default:
		v = floor
	}
	return math.Max(v, floor)
}

// weekly: weekdays full; weekends reduced (prod) or near-zero (non-prod).
func (e *Engine) weekly(now time.Time, nonProd bool) float64 {
	if len(e.regions) > 0 {
		return e.weeklyComposite(now, nonProd)
	}
	return weeklyAt(now.In(e.loc), nonProd)
}

// weeklyAt is the pure weekday/weekend factor for an already-localised time.
func weeklyAt(t time.Time, nonProd bool) float64 {
	switch t.Weekday() {
	case time.Saturday, time.Sunday:
		if nonProd {
			return 0.02
		}
		return 0.2
	default:
		return 1.0
	}
}

// newSeededRNG returns a deterministically-seeded math/rand/v2 generator. The seed is a fixed
// constant so the draw sequence is identical every run (invariant I12) — the per-blueprint Engine
// advances it in deterministic tick/construct order on its single goroutine. (Distinct blueprints
// share the same seed but emit distinct series, so identical streams are invisible — noise is just
// per-series texture.)
func newSeededRNG() *randv2.Rand {
	const seedHi, seedLo = 0x9e3779b97f4a7c15, 0x6a09e667f3bcc909 // golden-ratio / SHA-256 IV constants
	return randv2.New(randv2.NewPCG(seedHi, seedLo))
}

// Noise returns a multiplicative jitter ≈ 1 ± jitter (clamped ≥ 0). Goroutine-safe (rngMu) and
// deterministic per run — draws from the Engine's seeded rng, NOT the randomly-seeded global.
func (e *Engine) Noise(jitter float64) float64 {
	e.rngMu.Lock()
	n := e.rng.NormFloat64()
	e.rngMu.Unlock()
	v := 1 + n*jitter
	if v < 0 {
		v = 0
	}
	return v
}

// Wander returns a deterministic, slow, per-series multiplicative offset ≈ 1 ± amp. Unlike
// Noise (IID jitter), Wander is a low-frequency sinusoid whose PHASE is derived from the
// series key, so two different series drift apart instead of moving rigidly parallel even
// when they share the same diurnal curve. Deterministic in (key, now): no RNG, resume-safe.
// Combine with Noise for texture: value * Wander(key, now, 0.15) * Noise(0.1).
func (e *Engine) Wander(key string, now time.Time, amp float64) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	sum := h.Sum64()
	// Phase in [0, 2π) from the hash; period ~37 min (prime-ish, avoids aligning with the hour).
	phase := 2 * math.Pi * float64(sum%10_000) / 10_000.0
	const periodSec = 37 * 60.0
	tsec := float64(now.Unix())
	return 1 + amp*math.Sin(2*math.Pi*tsec/periodSec+phase)
}

// Spread returns a deterministic, TIME-INVARIANT multiplicative offset ≈ 1 ± amp derived
// solely from the series key. Where Wander makes a series drift over time and Noise adds IID
// texture, Spread gives each series a STABLE distinct baseline so peers that share one formula
// (e.g. a per-project quality ratio) don't emit byte-identical values — Wander alone would
// leave them with the same long-run average. Deterministic in key alone: no RNG, resume-safe.
// Combine for realism: const * Spread(key, 0.04) * Wander(key, now, 0.015) * Noise(0.01).
func (e *Engine) Spread(key string, amp float64) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	// Map the hash to a stable [-1, 1] offset. A second salt byte avoids correlating with
	// Wander's phase (which hashes the same key) so a series' baseline and its drift phase
	// are independent.
	u := float64(h.Sum64()%20_001)/10_000.0 - 1.0
	return 1 + amp*u
}

// Float64 returns a uniform random float64 in [0,1). Goroutine-safe (rngMu), deterministic per run.
func (e *Engine) Float64() float64 {
	e.rngMu.Lock()
	defer e.rngMu.Unlock()
	return e.rng.Float64()
}

// IntN returns a uniform random int in [0,n); 0 if n <= 0. Goroutine-safe (rngMu), deterministic.
func (e *Engine) IntN(n int) int {
	if n <= 0 {
		return 0
	}
	e.rngMu.Lock()
	defer e.rngMu.Unlock()
	return e.rng.IntN(n)
}

// NormFloat64 returns a standard-normal random float64. Goroutine-safe (rngMu), deterministic.
func (e *Engine) NormFloat64() float64 {
	e.rngMu.Lock()
	defer e.rngMu.Unlock()
	return e.rng.NormFloat64()
}

// Eval resolves a failure mode at now for callerScope by UNIONING the live failure
// state (the Live hook) with the scheduled incident windows. It returns (active,
// intensity) where intensity is the MAX of every matching contributor. Scope matching
// is fail-closed: a value-scoped setting never matches an un-scoped read; an un-scoped
// setting matches every caller.
func (e *Engine) Eval(now time.Time, mode string, callerScope string) (bool, float64) {
	var active bool
	var inten float64
	if e.Live != nil {
		for _, lf := range e.Live(mode) {
			if lf.Enabled && scopeMatch(lf.Scope, callerScope) {
				active = true
				if v := clamp01(lf.Intensity); v > inten {
					inten = v
				}
			}
		}
	}
	for _, w := range e.incidents {
		if w.kind != mode || !scopeMatch(w.scope, callerScope) {
			continue
		}
		hit := false
		switch {
		case w.interval:
			hit = intervalActive(now, w.period, w.dur)
		case w.daily:
			hit = dailyActive(now.In(e.loc), w.todStart, w.dur)
		default:
			hit = !now.Before(w.start) && now.Before(w.end)
		}
		if hit {
			active = true
			if w.intensity > inten {
				inten = w.intensity
			}
		}
	}
	return active, inten
}

// dailyActive reports whether localNow's time-of-day falls in the daily window
// [todStart, todStart+dur), handling windows that wrap past local midnight.
func dailyActive(localNow time.Time, todStart, dur time.Duration) bool {
	off := time.Duration(localNow.Hour())*time.Hour +
		time.Duration(localNow.Minute())*time.Minute +
		time.Duration(localNow.Second())*time.Second
	end := todStart + dur
	if end <= 24*time.Hour {
		return off >= todStart && off < end
	}
	// wraps midnight: active late today OR early tomorrow
	return off >= todStart || off < end-24*time.Hour
}

// intervalActive reports whether now falls in the active part of an interval-recurring window
// that fires for dur out of every period. The phase is anchored on the Unix epoch (offset =
// now mod period) so it is tz-independent and deterministic across runs/resumes: active when
// the epoch-relative offset is below the active duration.
func intervalActive(now time.Time, period, dur time.Duration) bool {
	if period <= 0 {
		return false
	}
	off := time.Duration(now.UnixNano()) % period
	return off < dur
}

// Active reports whether mode is active for callerScope (intensity ignored).
func (e *Engine) Active(now time.Time, mode string, callerScope string) bool {
	a, _ := e.Eval(now, mode, callerScope)
	return a
}

// FailFactor returns an intensity-scaled magnitude: 1 + (magAt1-1)*intensity when
// active, else 1.0.
func (e *Engine) FailFactor(now time.Time, mode string, callerScope string, magAt1 float64) float64 {
	active, inten := e.Eval(now, mode, callerScope)
	if !active {
		return 1.0
	}
	return 1 + (magAt1-1)*inten
}

// scopeMatch is fail-closed: setting scope "" matches any caller; a value-scoped
// setting matches only the identical caller scope (so a scoped failure never leaks
// into an un-scoped read).
func scopeMatch(setScope, callerScope string) bool {
	if setScope == "" {
		return true
	}
	return setScope == callerScope
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
