// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"log"
	"time"
)

// Region is one geographic contributor to a follow-the-sun composite load curve: its own IANA
// timezone (anchors that region's business-hours plateau) and a relative weight. Weights are
// normalised at construction so the composite peak ≈ 1.0 (back-compatible with single-tz).
type Region struct {
	Name     string
	Timezone string
	Weight   float64
}

type loadedRegion struct {
	loc    *time.Location
	weight float64
}

// NewWithRegions builds an Engine whose diurnal/weekly curves are the weight-normalised sum of
// per-region curves, each anchored to its own timezone. Factor/BusinessFactor keep the SAME
// signatures — callers (constructs/workloads) are unaffected. The first region's location is
// used as the incident-parsing/Loc() anchor. Falls back to a single Europe/Zurich region when
// the list is empty.
func NewWithRegions(regions []Region, incidents []string) *Engine {
	if len(regions) == 0 {
		return New("", incidents)
	}
	e := New(regions[0].Timezone, incidents) // reuse incident parsing + primary loc
	var total float64
	for _, r := range regions {
		if r.Weight > 0 {
			total += r.Weight
		}
	}
	if total <= 0 {
		total = 1
	}
	for _, r := range regions {
		loc, err := time.LoadLocation(r.Timezone)
		if err != nil {
			log.Printf("shape: unknown region timezone %q, using UTC", r.Timezone)
			loc = time.UTC
		}
		w := r.Weight
		if w <= 0 {
			continue
		}
		e.regions = append(e.regions, loadedRegion{loc: loc, weight: w / total})
	}
	return e
}

// diurnalComposite sums each region's diurnal curve (computed in that region's local time)
// weighted by its normalised weight.
func (e *Engine) diurnalComposite(now time.Time) float64 {
	var sum float64
	for _, r := range e.regions {
		sum += r.weight * diurnalAt(now.In(r.loc))
	}
	return sum
}

// weeklyComposite sums each region's weekday/weekend factor in that region's local calendar.
func (e *Engine) weeklyComposite(now time.Time, nonProd bool) float64 {
	var sum float64
	for _, r := range e.regions {
		sum += r.weight * weeklyAt(now.In(r.loc), nonProd)
	}
	return sum
}

// businessWeeklyComposite is the substrate (no-env) weekend factor: weekends → 0.3.
func (e *Engine) businessWeeklyComposite(now time.Time) float64 {
	var sum float64
	for _, r := range e.regions {
		w := 1.0
		switch now.In(r.loc).Weekday() {
		case time.Saturday, time.Sunday:
			w = 0.3
		}
		sum += r.weight * w
	}
	return sum
}
