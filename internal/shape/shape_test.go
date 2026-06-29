// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"strconv"
	"testing"
	"time"
)

// liveHook builds an Engine.Live function from a static map of mode → failures, so tests
// can drive the control-plane seam deterministically without a global failure registry.
func liveHook(modes map[string][]LiveFailure) func(string) []LiveFailure {
	return func(mode string) []LiveFailure { return modes[mode] }
}

// --- Diurnal plateau --------------------------------------------------------

// TestDiurnalPlateau pins the flat-topped business-hours plateau: overnight floor ≈0.1,
// peak ==1.0 inside the 10:00–16:00 plateau, monotone ramps up (06→10) and down (16→20).
func TestDiurnalPlateau(t *testing.T) {
	e := New("Europe/Zurich", nil)
	loc := e.Loc()
	day := func(h, m int) time.Time { return time.Date(2026, 6, 10, h, m, 0, 0, loc) } // Wednesday

	// Overnight floor ≈0.1 (Factor with weight 1, prod). diurnal is the only time-varying
	// term on a weekday so Factor == diurnal here.
	for _, h := range []int{0, 3, 22} {
		if f := e.Factor(day(h, 0), 1.0, false); f < 0.09 || f > 0.11 {
			t.Fatalf("overnight floor at %02d:00 want ≈0.1, got %.4f", h, f)
		}
	}

	// Plateau peak == 1.0 across the whole 10:00–16:00 flat top.
	for _, h := range []int{10, 12, 15} {
		if f := e.Factor(day(h, 0), 1.0, false); f != 1.0 {
			t.Fatalf("plateau peak at %02d:00 want exactly 1.0, got %.6f", h, f)
		}
	}

	// Monotone ramp UP 06:00 → 10:00.
	prev := e.Factor(day(6, 0), 1.0, false)
	for _, hm := range [][2]int{{7, 0}, {8, 0}, {9, 0}, {10, 0}} {
		cur := e.Factor(day(hm[0], hm[1]), 1.0, false)
		if cur < prev {
			t.Fatalf("ramp-up must be monotone non-decreasing: %02d:%02d dropped %.4f→%.4f", hm[0], hm[1], prev, cur)
		}
		prev = cur
	}

	// Monotone ramp DOWN 16:00 → 20:00.
	prev = e.Factor(day(16, 0), 1.0, false)
	for _, hm := range [][2]int{{17, 0}, {18, 0}, {19, 0}, {20, 0}} {
		cur := e.Factor(day(hm[0], hm[1]), 1.0, false)
		if cur > prev {
			t.Fatalf("ramp-down must be monotone non-increasing: %02d:%02d rose %.4f→%.4f", hm[0], hm[1], prev, cur)
		}
		prev = cur
	}
}

// --- Weekly -----------------------------------------------------------------

// TestWeeklyFactors pins the weekly multiplier folded into Factor: weekdays full (1.0),
// weekends 0.2 (prod) / 0.02 (non-prod). We probe at plateau noon so diurnal == 1.0 and
// Factor == weekly.
func TestWeeklyFactors(t *testing.T) {
	e := New("Europe/Zurich", nil)
	loc := e.Loc()
	wedNoon := time.Date(2026, 6, 10, 13, 0, 0, 0, loc) // Wednesday plateau
	satNoon := time.Date(2026, 6, 13, 13, 0, 0, 0, loc) // Saturday plateau
	sunNoon := time.Date(2026, 6, 14, 13, 0, 0, 0, loc) // Sunday plateau

	if f := e.Factor(wedNoon, 1.0, false); f != 1.0 {
		t.Fatalf("weekday Factor at plateau want 1.0, got %.4f", f)
	}
	if f := e.Factor(satNoon, 1.0, false); f != 0.2 {
		t.Fatalf("prod weekend Factor want 0.2, got %.4f", f)
	}
	if f := e.Factor(sunNoon, 1.0, true); f != 0.02 {
		t.Fatalf("non-prod weekend Factor want 0.02, got %.4f", f)
	}
}

// TestBusinessFactorWeekend pins the substrate shape: weekday plateau ≈1.0, weekend 0.3
// (a production company keeps some weekend traffic — above the non-prod near-zero).
func TestBusinessFactorWeekend(t *testing.T) {
	e := New("Europe/Zurich", nil)
	loc := e.Loc()
	wedNoon := time.Date(2026, 6, 10, 13, 0, 0, 0, loc)
	satNoon := time.Date(2026, 6, 13, 13, 0, 0, 0, loc)

	if bf := e.BusinessFactor(wedNoon); bf != 1.0 {
		t.Fatalf("weekday BusinessFactor at plateau want 1.0, got %.4f", bf)
	}
	if bf := e.BusinessFactor(satNoon); bf != 0.3 {
		t.Fatalf("weekend BusinessFactor want 0.3, got %.4f", bf)
	}
	if e.BusinessFactor(satNoon) >= e.BusinessFactor(wedNoon) {
		t.Fatal("weekend BusinessFactor must be below weekday")
	}
}

// --- Incident schedule parsing ---------------------------------------------

// TestScheduleGrammar covers the generalized grammar: legacy form (no #/@), #intensity,
// @scope, scope-without-intensity, and bad entries skipped without panic.
func TestScheduleGrammar(t *testing.T) {
	e := New("", []string{
		"tpm_exceeded@2026-06-22T14:00:00Z/20m#0.7@us-east-1", // intensity + scope
		"elb_5xx@2026-06-22T14:00:00Z/20m@eu-west-1",          // scope WITHOUT intensity
		"latency_spike@2026-06-22T14:00:00Z/20m",              // legacy form
		"",                                                    // empty → skipped
		"no_at_sign_here",                                     // no '@' → skipped, no panic
		"missing_slash@2026-06-22T14:00:00Z",                  // no '/' → skipped
		"bad_intensity@2026-06-22T14:00:00Z/20m#notanumber",   // bad #intensity → skipped
		"bad_time@nonsense/20m",                               // unparseable time → skipped
		"bad_dur@2026-06-22T14:00:00Z/notaduration",           // bad duration → skipped
	})
	mid := time.Date(2026, 6, 22, 14, 10, 0, 0, time.UTC)
	out := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)

	// scheduled, in-scope, explicit intensity 0.7
	if ok, inten := e.Eval(mid, "tpm_exceeded", "us-east-1"); !ok || inten != 0.7 {
		t.Fatalf("scheduled in-scope #0.7: ok=%v inten=%v", ok, inten)
	}
	// scheduled, out-of-scope → inactive
	if ok, _ := e.Eval(mid, "tpm_exceeded", "eu-west-1"); ok {
		t.Fatal("scheduled out-of-scope must be inactive")
	}
	// @scope without #intensity defaults intensity 1.0
	if ok, inten := e.Eval(mid, "elb_5xx", "eu-west-1"); !ok || inten != 1.0 {
		t.Fatalf("scope-without-intensity: ok=%v inten=%v", ok, inten)
	}
	// legacy entry defaults intensity 1.0, scope all (matches any caller)
	if ok, inten := e.Eval(mid, "latency_spike", "prod"); !ok || inten != 1.0 {
		t.Fatalf("legacy schedule: ok=%v inten=%v", ok, inten)
	}
	// outside the window → inactive
	if ok, _ := e.Eval(out, "latency_spike", "prod"); ok {
		t.Fatal("outside window must be inactive")
	}
	// Bad entries were skipped (not parsed into windows): only 3 valid windows survived.
	for _, mode := range []string{"no_at_sign_here", "missing_slash", "bad_intensity", "bad_time", "bad_dur"} {
		if ok, _ := e.Eval(mid, mode, ""); ok {
			t.Fatalf("bad entry %q must have been skipped, not parsed", mode)
		}
	}
}

// TestDailyRecurringIncident proves a bare time-of-day incident ("15:04") fires every day at
// that local wall-clock time for <dur>, independent of date — for ongoing/periodic incidents.
func TestDailyRecurringIncident(t *testing.T) {
	// Daily window 14:00–14:30 Zurich, recurring. Use a scope so we also exercise scope matching.
	e := New("Europe/Zurich", []string{"pod_crashloop@14:00/30m@acme-eks-dev2"})
	loc := e.Loc()

	// Active on two DIFFERENT dates at 14:15 local → proves recurrence (not one-shot).
	for _, day := range []int{20, 21, 22} {
		at := time.Date(2026, 6, day, 14, 15, 0, 0, loc)
		if ok, inten := e.Eval(at, "pod_crashloop", "acme-eks-dev2"); !ok || inten != 1.0 {
			t.Fatalf("daily window must fire on 2026-06-%02d 14:15: ok=%v inten=%v", day, ok, inten)
		}
	}
	// Inactive before/after the window on the same day.
	for _, hm := range [][2]int{{13, 0}, {14, 45}, {18, 0}} {
		at := time.Date(2026, 6, 22, hm[0], hm[1], 0, 0, loc)
		if ok, _ := e.Eval(at, "pod_crashloop", "acme-eks-dev2"); ok {
			t.Fatalf("daily window must be inactive at %02d:%02d", hm[0], hm[1])
		}
	}
	// Out-of-scope caller never matches.
	if ok, _ := e.Eval(time.Date(2026, 6, 22, 14, 15, 0, 0, loc), "pod_crashloop", "acme-eks-prd"); ok {
		t.Fatal("daily window must be scope-fail-closed")
	}
}

// TestDailyRecurringWrapsMidnight proves a daily window crossing local midnight matches on both
// sides of midnight.
func TestDailyRecurringWrapsMidnight(t *testing.T) {
	e := New("Europe/Zurich", []string{"node_not_ready@23:30/1h"}) // 23:30–00:30 local
	loc := e.Loc()
	for _, hm := range [][2]int{{23, 45}, {0, 15}} {
		at := time.Date(2026, 6, 22, hm[0], hm[1], 0, 0, loc)
		if ok, _ := e.Eval(at, "node_not_ready", ""); !ok {
			t.Fatalf("midnight-wrapping daily window must be active at %02d:%02d", hm[0], hm[1])
		}
	}
	if ok, _ := e.Eval(time.Date(2026, 6, 22, 1, 0, 0, 0, loc), "node_not_ready", ""); ok {
		t.Fatal("must be inactive at 01:00 (past the 00:30 end)")
	}
}

// TestWarningsCollectsSkippedEntries proves skipped incident entries are recorded for retrieval
// (so the control plane can surface them), and a clean schedule yields no warnings.
func TestWarningsCollectsSkippedEntries(t *testing.T) {
	e := New("", []string{
		"good@2026-06-22T14:00:00Z/20m", // valid → no warning
		"no_slash@2026-06-22T14:00:00Z", // bad → warned
		"bad_dur@2026-06-22T14:00:00Z/notaduration",
		"always_on@10:00/24h", // daily >=24h → warned
	})
	w := e.Warnings()
	if len(w) != 3 {
		t.Fatalf("expected 3 warnings (no_slash, bad_dur, always_on), got %d: %v", len(w), w)
	}
	if ok, _ := e.Eval(time.Date(2026, 6, 22, 14, 10, 0, 0, time.UTC), "good", ""); !ok {
		t.Error("the valid entry must still be active")
	}
	clean := New("", []string{"good@2026-06-22T14:00:00Z/20m"})
	if len(clean.Warnings()) != 0 {
		t.Errorf("clean schedule should have no warnings, got %v", clean.Warnings())
	}
}

// TestDailyRecurringRejectsAllDayDuration proves a daily window of >= 24h is skipped (it would be
// permanently active); a dated (one-shot) long window is still allowed.
func TestDailyRecurringRejectsAllDayDuration(t *testing.T) {
	e := New("Europe/Zurich", []string{
		"always_on@10:00/24h", // daily + 24h → skipped
		"also_on@00:00/48h",   // daily + 48h → skipped
		"ok_daily@10:00/30m",  // daily + short → kept
	})
	loc := e.Loc()
	// The >=24h daily entries must NOT have been parsed into always-active windows.
	for _, hm := range [][2]int{{10, 15}, {3, 0}, {23, 0}} {
		at := time.Date(2026, 6, 22, hm[0], hm[1], 0, 0, loc)
		if ok, _ := e.Eval(at, "always_on", ""); ok {
			t.Errorf("always_on (daily 24h) must be skipped, active at %02d:%02d", hm[0], hm[1])
		}
		if ok, _ := e.Eval(at, "also_on", ""); ok {
			t.Errorf("also_on (daily 48h) must be skipped, active at %02d:%02d", hm[0], hm[1])
		}
	}
	// The short daily window is unaffected.
	if ok, _ := e.Eval(time.Date(2026, 6, 22, 10, 15, 0, 0, loc), "ok_daily", ""); !ok {
		t.Error("ok_daily (daily 30m) must still fire")
	}
	// A DATED long window is still allowed (one-shot, not the recurring form).
	e2 := New("Europe/Zurich", []string{"maint@2026-06-22T00:00/48h"})
	if ok, _ := e2.Eval(time.Date(2026, 6, 23, 12, 0, 0, 0, loc), "maint", ""); !ok {
		t.Error("dated 48h one-shot window must still be honored")
	}
}

// TestIntervalWindow proves the interval-recurring form ("every10m/5m") fires for <dur> out of
// every <period>, anchored on the Unix epoch (tz-independent, deterministic). 10m=600s period,
// 5m=300s active: epoch offset < 300s → active, [300,600) → inactive.
func TestIntervalWindow(t *testing.T) {
	e := New("Europe/Zurich", []string{"oom_kill@every10m/5m#0.2@c1"})

	// time.Unix picks the absolute epoch offset directly. Choose times whose UnixNano % 600s is
	// known: a multiple of 600s + delta gives offset == delta.
	const period = int64(600) // 10m in seconds
	base := int64(1_750_000_800)
	if base%period != 0 {
		base -= base % period // align to a period boundary so base is offset 0
	}

	// Active in the first 5m of the period (offsets 0, 120, 299s).
	for _, off := range []int64{0, 120, 299} {
		at := time.Unix(base+off, 0)
		if ok, inten := e.Eval(at, "oom_kill", "c1"); !ok || inten != 0.2 {
			t.Fatalf("interval window must fire at offset %ds: ok=%v inten=%v", off, ok, inten)
		}
	}
	// Inactive in the second half (offsets 301..540s — i.e. 6-9m in).
	for _, off := range []int64{360, 420, 540} {
		at := time.Unix(base+off, 0)
		if ok, _ := e.Eval(at, "oom_kill", "c1"); ok {
			t.Fatalf("interval window must be inactive at offset %ds", off)
		}
	}
	// Recurs: next period's first half is active again.
	if ok, _ := e.Eval(time.Unix(base+period+60, 0), "oom_kill", "c1"); !ok {
		t.Fatal("interval window must recur into the next period")
	}
	// Out-of-scope caller never matches.
	if ok, _ := e.Eval(time.Unix(base+60, 0), "oom_kill", "other"); ok {
		t.Fatal("interval window must be scope-fail-closed")
	}
}

// TestIntervalWindowGuards proves an interval whose active dur >= period (always-active) is
// skipped, and a malformed "every" token is skipped — never parsed into a live window.
func TestIntervalWindowGuards(t *testing.T) {
	e := New("Europe/Zurich", []string{
		"always@every10m/10m",   // dur == period → always-active → skipped
		"too_long@every5m/10m",  // dur > period → skipped
		"malformed@everyXYZ/5m", // bad period → skipped
		"ok@every10m/5m",        // valid → kept
	})
	// None of the guarded entries may ever be active.
	for _, off := range []int64{0, 60, 180, 300, 420, 599} {
		at := time.Unix(1_750_000_800+off, 0)
		if ok, _ := e.Eval(at, "always", ""); ok {
			t.Errorf("always (dur==period) must be skipped, active at offset %ds", off)
		}
		if ok, _ := e.Eval(at, "too_long", ""); ok {
			t.Errorf("too_long (dur>period) must be skipped, active at offset %ds", off)
		}
		if ok, _ := e.Eval(at, "malformed", ""); ok {
			t.Errorf("malformed every must be skipped, active at offset %ds", off)
		}
	}
	// The valid interval still fires in its first half.
	base := int64(1_750_000_800)
	base -= base % 600
	if ok, _ := e.Eval(time.Unix(base+60, 0), "ok", ""); !ok {
		t.Error("ok (every10m/5m) must still fire in its first half")
	}
}

// TestZoneLessIncidentTime proves a zone-less incident time is parsed in the engine's own
// location (so authors think in local business time), distinct from a UTC reading.
func TestZoneLessIncidentTime(t *testing.T) {
	// Zurich is UTC+2 in June (CEST). A zone-less 14:00 means 14:00 Zurich == 12:00 UTC.
	e := New("Europe/Zurich", []string{"x@2026-06-22T14:00/20m"})
	atZurich14 := time.Date(2026, 6, 22, 14, 10, 0, 0, e.Loc())
	if ok, _ := e.Eval(atZurich14, "x", ""); !ok {
		t.Fatal("zone-less time must be interpreted in the engine location")
	}
	// 14:10 UTC is 16:10 Zurich — well after the 14:00–14:20 Zurich window — must be inactive.
	atUTC14 := time.Date(2026, 6, 22, 14, 10, 0, 0, time.UTC)
	if ok, _ := e.Eval(atUTC14, "x", ""); ok {
		t.Fatal("14:10 UTC is past the 14:00 Zurich window; must be inactive")
	}
}

// --- Eval: union of live + scheduled, max intensity, scope ------------------

// TestEvalLiveOnly proves the Live hook drives activity when no schedule exists, including
// fail-closed scope matching on the live setting.
func TestEvalLiveOnly(t *testing.T) {
	e := New("", nil)
	e.Live = liveHook(map[string][]LiveFailure{
		"oomkill": {{Enabled: true, Intensity: 0.5, Scope: "prod"}},
	})
	now := time.Now()
	if ok, inten := e.Eval(now, "oomkill", "prod"); !ok || inten != 0.5 {
		t.Fatalf("live in-scope: ok=%v inten=%v", ok, inten)
	}
	if ok, _ := e.Eval(now, "oomkill", "dev"); ok {
		t.Fatal("live scoped prod must not fire for dev (fail-closed)")
	}
	// disabled live setting is inert
	e.Live = liveHook(map[string][]LiveFailure{
		"oomkill": {{Enabled: false, Intensity: 1.0}},
	})
	if ok, _ := e.Eval(now, "oomkill", "prod"); ok {
		t.Fatal("disabled live failure must not fire")
	}
}

// TestEvalUnionMaxIntensity proves Eval UNIONS live + scheduled and takes the MAX intensity
// of every matching contributor.
func TestEvalUnionMaxIntensity(t *testing.T) {
	// Schedule a window at intensity 0.3; live at 0.9 — union takes the max (0.9).
	e := New("", []string{"latency_spike@2026-06-22T14:00:00Z/20m#0.3"})
	e.Live = liveHook(map[string][]LiveFailure{
		"latency_spike": {{Enabled: true, Intensity: 0.9}},
	})
	mid := time.Date(2026, 6, 22, 14, 10, 0, 0, time.UTC)
	if ok, inten := e.Eval(mid, "latency_spike", ""); !ok || inten != 0.9 {
		t.Fatalf("max-merge live>sched: ok=%v inten=%v want 0.9", ok, inten)
	}

	// Now flip it: schedule higher than live → schedule wins.
	e2 := New("", []string{"latency_spike@2026-06-22T14:00:00Z/20m#0.8"})
	e2.Live = liveHook(map[string][]LiveFailure{
		"latency_spike": {{Enabled: true, Intensity: 0.2}},
	})
	if ok, inten := e2.Eval(mid, "latency_spike", ""); !ok || inten != 0.8 {
		t.Fatalf("max-merge sched>live: ok=%v inten=%v want 0.8", ok, inten)
	}
}

// TestScopeMatchFailClosed nails the exact scope-matching truth table directly through
// Eval, since it is the security-shaped invariant (a scoped failure must never leak into an
// un-scoped read):
//
//	set=""  caller=anything → match (un-scoped setting hits everyone)
//	set="x" caller=""       → NO match (scoped setting never hits an un-scoped caller)
//	set="x" caller="x"      → match
//	set="x" caller="y"      → NO match
func TestScopeMatchFailClosed(t *testing.T) {
	now := time.Now()

	// set="" matches any caller, including an un-scoped one.
	eAll := New("", nil)
	eAll.Live = liveHook(map[string][]LiveFailure{"m": {{Enabled: true, Intensity: 1, Scope: ""}}})
	for _, caller := range []string{"", "prod", "anything"} {
		if ok, _ := eAll.Eval(now, "m", caller); !ok {
			t.Fatalf("un-scoped setting must match caller %q", caller)
		}
	}

	// set="x" never matches an un-scoped caller "".
	eX := New("", nil)
	eX.Live = liveHook(map[string][]LiveFailure{"m": {{Enabled: true, Intensity: 1, Scope: "x"}}})
	if ok, _ := eX.Eval(now, "m", ""); ok {
		t.Fatal("scoped setting x must NOT match un-scoped caller (fail-closed)")
	}
	if ok, _ := eX.Eval(now, "m", "x"); !ok {
		t.Fatal("scoped setting x must match caller x")
	}
	if ok, _ := eX.Eval(now, "m", "y"); ok {
		t.Fatal("scoped setting x must NOT match caller y")
	}
}

// TestActiveIgnoresIntensity proves Active reports the boolean from Eval regardless of
// intensity magnitude.
func TestActiveIgnoresIntensity(t *testing.T) {
	e := New("", []string{"m@2026-06-22T14:00:00Z/20m#0.01"})
	mid := time.Date(2026, 6, 22, 14, 10, 0, 0, time.UTC)
	out := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	if !e.Active(mid, "m", "") {
		t.Fatal("Active must be true inside the window even at tiny intensity")
	}
	if e.Active(out, "m", "") {
		t.Fatal("Active must be false outside the window")
	}
}

// --- FailFactor intensity scaling -------------------------------------------

// TestFailFactorScaling proves FailFactor returns 1.0 when inactive and 1+(magAt1-1)*inten
// when active.
func TestFailFactorScaling(t *testing.T) {
	now := time.Now()

	// inactive → 1.0
	e := New("", nil)
	if f := e.FailFactor(now, "latency_spike", "", 3.0); f != 1.0 {
		t.Fatalf("inactive FailFactor must be 1.0, got %v", f)
	}

	// active at intensity 0.5, magAt1=3.0 → 1 + (3-1)*0.5 = 2.0
	e.Live = liveHook(map[string][]LiveFailure{
		"latency_spike": {{Enabled: true, Intensity: 0.5}},
	})
	if f := e.FailFactor(now, "latency_spike", "", 3.0); f != 2.0 {
		t.Fatalf("scaled FailFactor: got %v want 2.0", f)
	}

	// at full intensity 1.0 it reaches the full magnitude.
	e.Live = liveHook(map[string][]LiveFailure{
		"latency_spike": {{Enabled: true, Intensity: 1.0}},
	})
	if f := e.FailFactor(now, "latency_spike", "", 3.0); f != 3.0 {
		t.Fatalf("full-intensity FailFactor: got %v want 3.0", f)
	}
}

// --- Timezone fallback ------------------------------------------------------

// TestTimezoneFallbackToUTC proves a bogus IANA name falls back to UTC without panicking,
// and that the diurnal plateau is then anchored to UTC (10:00–16:00 UTC is the flat top).
func TestTimezoneFallbackToUTC(t *testing.T) {
	e := New("Not/AReal/Zone", nil)
	if e.Loc() != time.UTC {
		t.Fatalf("bogus timezone must fall back to UTC, got %v", e.Loc())
	}
	noonUTC := time.Date(2026, 6, 10, 13, 0, 0, 0, time.UTC) // Wed plateau in UTC
	if f := e.Factor(noonUTC, 1.0, false); f != 1.0 {
		t.Fatalf("after UTC fallback, 13:00 UTC must be on the plateau (1.0), got %.4f", f)
	}
}

// TestEmptyTimezoneDefaultsToZurich proves the documented default anchor: "" → Europe/Zurich.
func TestEmptyTimezoneDefaultsToZurich(t *testing.T) {
	e := New("", nil)
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skipf("Europe/Zurich tz database not available: %v", err)
	}
	if e.Loc().String() != zurich.String() {
		t.Fatalf("empty tz must default to Europe/Zurich, got %v", e.Loc())
	}
}

// --- Wander ------------------------------------------------------------------

func TestWanderIsDeterministicPerKey(t *testing.T) {
	e := New("", nil)
	now := time.Date(2026, 6, 16, 14, 0, 0, 0, time.UTC)
	a1 := e.Wander("model-a", now, 0.15)
	a2 := e.Wander("model-a", now, 0.15)
	if a1 != a2 {
		t.Errorf("Wander must be deterministic for same key+time: %v vs %v", a1, a2)
	}
}

func TestWanderDiffersAcrossKeys(t *testing.T) {
	e := New("", nil)
	now := time.Date(2026, 6, 16, 14, 0, 0, 0, time.UTC)
	diff := false
	for h := 0; h < 24; h++ {
		ts := now.Add(time.Duration(h) * time.Hour)
		if e.Wander("model-a", ts, 0.2) != e.Wander("model-b", ts, 0.2) {
			diff = true
			break
		}
	}
	if !diff {
		t.Error("Wander produced identical curves for different keys")
	}
}

func TestWanderBounded(t *testing.T) {
	e := New("", nil)
	now := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)
	for m := 0; m < 24*60; m++ {
		v := e.Wander("x", now.Add(time.Duration(m)*time.Minute), 0.2)
		if v < 0.7 || v > 1.3 {
			t.Fatalf("Wander out of [1±amp+slack] bounds: %v", v)
		}
	}
}

// --- Spread ------------------------------------------------------------------

func TestSpreadIsTimeInvariantAndDeterministic(t *testing.T) {
	e := New("", nil)
	a1 := e.Spread("project-a|faithfulness", 0.04)
	a2 := e.Spread("project-a|faithfulness", 0.04)
	if a1 != a2 {
		t.Errorf("Spread must be deterministic for same key: %v vs %v", a1, a2)
	}
}

func TestSpreadDiffersAcrossKeys(t *testing.T) {
	e := New("", nil)
	// Distinct peer series must get distinct stable baselines (this is what breaks the
	// "every project shows the identical number" lockstep).
	seen := map[float64]string{}
	keys := []string{
		"contentgen-agents|faithfulness", "datagen-analysis|faithfulness",
		"docintel-extraction|faithfulness", "platform-assistant|faithfulness",
	}
	for _, k := range keys {
		v := e.Spread(k, 0.04)
		if prev, ok := seen[v]; ok {
			t.Errorf("Spread collision: %q and %q both = %v", prev, k, v)
		}
		seen[v] = k
	}
}

func TestSpreadBounded(t *testing.T) {
	e := New("", nil)
	for i := 0; i < 10000; i++ {
		v := e.Spread("key-"+strconv.Itoa(i), 0.05)
		if v < 0.95 || v > 1.05 {
			t.Fatalf("Spread out of [1±amp] bounds: %v", v)
		}
	}
}
