// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// hopMean returns the mean duration (ns) of hops to a given target node across requests.
func hopMean(reqs []*ledger.Request, target string) float64 {
	var sum, n float64
	for _, r := range reqs {
		for i := range r.Calls {
			if r.Calls[i].Target == target {
				sum += float64(r.Calls[i].Duration)
				n++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// hopFailRate returns the fraction of hops to target that were marked failed.
func hopFailRate(reqs []*ledger.Request, target string) float64 {
	var failed, n float64
	for _, r := range reqs {
		for i := range r.Calls {
			if r.Calls[i].Target == target {
				n++
				if r.Calls[i].Failed {
					failed++
				}
			}
		}
	}
	if n == 0 {
		return 0
	}
	return failed / n
}

// TestApp_PerServiceIncidentLocalizesToNode: a per-service incident on one node shifts THAT node's
// correlated sample (its hop) while leaving sibling nodes unaffected (localized blast radius, §6.5).
func TestApp_PerServiceIncidentLocalizesToNode(t *testing.T) {
	w := buildApp(t, graphCfg()) // web-fe → api → pg
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)
	const N = 400

	calm := shape.New("UTC", nil)
	latStorm := shape.New("UTC", []string{"latency_storm@2026-06-15T13:00:00Z/1h@api"})
	errStorm := shape.New("UTC", []string{"error_spike@2026-06-15T13:00:00Z/1h@api"})

	mint := func(eng *shape.Engine) []*ledger.Request {
		out := make([]*ledger.Request, 0, N)
		for range N {
			out = append(out, w.m.mintOne(now, eng))
		}
		return out
	}
	calmReqs, latReqs, errReqs := mint(calm), mint(latStorm), mint(errStorm)

	// latency_storm@api: the api hop is much slower; the sibling pg hop is ~unchanged.
	apiCalm, apiStorm := hopMean(calmReqs, "api"), hopMean(latReqs, "api")
	pgCalm, pgStorm := hopMean(calmReqs, "pg"), hopMean(latReqs, "pg")
	if apiStorm < apiCalm*2.5 {
		t.Errorf("latency_storm@api: api hop mean=%.0fns not elevated vs calm=%.0fns", apiStorm, apiCalm)
	}
	if pgStorm > pgCalm*1.4 {
		t.Errorf("latency_storm@api: sibling pg hop mean=%.0fns should be ~unchanged vs calm=%.0fns", pgStorm, pgCalm)
	}

	// error_spike@api: the api hop fails far more often; the sibling pg hop barely fails.
	if got := hopFailRate(errReqs, "api"); got < 0.3 {
		t.Errorf("error_spike@api: api hop fail rate=%.2f, want elevated", got)
	}
	if got := hopFailRate(errReqs, "pg"); got > 0.2 {
		t.Errorf("error_spike@api: sibling pg hop fail rate=%.2f, want ~unaffected", got)
	}
}

// TestAppSpanMetricsFollowServiceIncidents keeps the emitted APM RED families aligned with the
// incident-responsive trace lane. The acceptance harness observes these metric families, not the
// in-memory request samples, so a latency_storm/error_spike must move its target's server rows.
func TestAppSpanMetricsFollowServiceIncidents(t *testing.T) {
	now := time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

	tick := func(live map[string][]shape.LiveFailure) *coretest.MetricCapture {
		t.Helper()
		w := buildApp(t, graphCfg())
		mc := &coretest.MetricCapture{}
		world := coretest.World(mc, nil, nil)
		world.Shape.Live = func(mode string) []shape.LiveFailure { return live[mode] }
		if err := w.Tick(context.Background(), now, world); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return mc
	}

	calm := tick(nil)
	storm := tick(map[string][]shape.LiveFailure{
		"latency_storm": {{Enabled: true, Intensity: 0.8, Scope: "api"}},
		"error_spike":   {{Enabled: true, Intensity: 0.8, Scope: "api"}},
	})

	latencyMean := func(mc *coretest.MetricCapture, service string) float64 {
		t.Helper()
		var sum, count float64
		for _, s := range mc.Find("traces_spanmetrics_latency_sum") {
			if s.Labels["service_name"] == service && s.Labels["span_kind"] == spanKindServer {
				sum += s.Value
			}
		}
		for _, s := range mc.Find("traces_spanmetrics_latency_count") {
			if s.Labels["service_name"] == service && s.Labels["span_kind"] == spanKindServer {
				count += s.Value
			}
		}
		if count == 0 {
			t.Fatalf("no spanmetric latency observations for %q", service)
		}
		return sum / count
	}
	errorRate := func(mc *coretest.MetricCapture, service string) float64 {
		t.Helper()
		var errors, calls float64
		for _, s := range mc.Find("traces_spanmetrics_calls_total") {
			if s.Labels["service_name"] != service || s.Labels["span_kind"] != spanKindServer {
				continue
			}
			calls += s.Value
			if s.Labels["status_code"] == statusCodeError {
				errors += s.Value
			}
		}
		if calls == 0 {
			t.Fatalf("no spanmetric calls for %q", service)
		}
		return errors / calls
	}

	if got, want := latencyMean(storm, "api"), latencyMean(calm, "api"); got < want*2 {
		t.Errorf("latency_storm target mean=%0.3f, baseline=%0.3f; want >=2x", got, want)
	}
	if got := errorRate(storm, "api"); got < 0.2 {
		t.Errorf("error_spike target error rate=%0.3f, want >=0.2", got)
	}
}
