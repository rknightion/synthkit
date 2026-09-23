// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

// mintN mints n requests through a minimally-configured minter and reports how many were
// browser-origin plus the set of distinct session ids among them.
func mintN(t *testing.T, m *minter, n int, start time.Time, step time.Duration) (browser int, sessions map[string]int) {
	t.Helper()
	eng := shape.New("", nil)
	sessions = map[string]int{}
	for i := range n {
		r := m.mintOne(start.Add(time.Duration(i)*step), eng)
		if r.BrowserOrigin {
			browser++
			sessions[r.SessionID]++
		}
	}
	return browser, sessions
}

// A blueprint that omits `rum:` must behave exactly as before: ~60% browser-origin.
func TestRUMSampleRateDefaultsToHistoricFraction(t *testing.T) {
	m := newTestMinter(t, RUMDecl{})
	if m.rumRate != defaultBrowserFraction {
		t.Fatalf("default rumRate = %v, want %v", m.rumRate, defaultBrowserFraction)
	}
	browser, _ := mintN(t, m, 4000, time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), time.Millisecond)
	if frac := float64(browser) / 4000; frac < 0.5 || frac > 0.7 {
		t.Fatalf("browser fraction %.3f outside the expected ~0.6 band", frac)
	}
}

// The knob that makes a demo estate legible: a tiny sample rate must cut browser volume hard
// while leaving the request stream (backend traffic) untouched.
func TestRUMSampleRateThrottlesBrowserLane(t *testing.T) {
	m := newTestMinter(t, RUMDecl{SampleRate: ptr(0.002)})
	const n = 20000
	browser, _ := mintN(t, m, n, time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), time.Millisecond)
	if frac := float64(browser) / n; frac > 0.01 {
		t.Fatalf("browser fraction %.4f — sample_rate 0.002 did not throttle the lane", frac)
	}
	if browser == 0 {
		t.Fatal("sample_rate 0.002 produced zero browser requests over 20k mints (lane is dead, not throttled)")
	}
}

func TestRUMSampleRateZeroDisablesBrowserLane(t *testing.T) {
	m := newTestMinter(t, RUMDecl{SampleRate: ptr(0.0)})
	if browser, _ := mintN(t, m, 2000, time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), time.Millisecond); browser != 0 {
		t.Fatalf("sample_rate 0 still produced %d browser requests", browser)
	}
}

// Without session_duration every browser request gets its own session — the historic behaviour
// that made session count equal page-view count.
func TestRUMWithoutSessionDurationMintsPerRequestSessions(t *testing.T) {
	m := newTestMinter(t, RUMDecl{SampleRate: ptr(1.0)})
	browser, sessions := mintN(t, m, 200, time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), time.Second)
	if len(sessions) != browser {
		t.Fatalf("got %d sessions for %d browser requests, want one session each", len(sessions), browser)
	}
}

// With session_duration set, requests inside one window share a session and the session rotates
// when the window does.
func TestRUMSessionDurationMakesSessionsSticky(t *testing.T) {
	m := newTestMinter(t, RUMDecl{SampleRate: ptr(1.0), SessionDuration: "3m"})
	start := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	// 30 requests spread over a single 3m window → exactly one session.
	_, sessions := mintN(t, m, 30, start, 5*time.Second)
	if len(sessions) != 1 {
		t.Fatalf("within one 3m window got %d sessions, want 1", len(sessions))
	}

	// Spread over ~30m → roughly one session per 3m window, not one per request.
	_, many := mintN(t, m, 60, start, 30*time.Second)
	if len(many) < 8 || len(many) > 13 {
		t.Fatalf("over ~30m got %d sessions, want ~10 (one per 3m window)", len(many))
	}
}

// newTestMinter builds a minter over a one-node entry graph with RUM enabled.
func newTestMinter(t *testing.T, rum RUMDecl) *minter {
	t.Helper()
	g, err := buildGraph([]ServiceNode{{
		Name: "fin-ebanking-web", Type: "frontend", Runtime: "node", Entry: true,
		Routes: []string{"GET /api/portal/overview"},
	}})
	if err != nil {
		t.Fatalf("buildGraph: %v", err)
	}
	m := newMinter("fin-digital-banking", "prod", "fin-prod-euc2", 1.0, false,
		Traffic{OffPeakRPS: 10, PeakRPS: 80}, nil, rum, g)
	m.rumEnabled = true
	return m
}
