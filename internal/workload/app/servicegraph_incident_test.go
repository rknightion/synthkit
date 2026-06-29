// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

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
