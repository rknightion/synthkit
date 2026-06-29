// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"testing"
	"time"
)

func TestRegionsCompositeNeverFullyTroughs(t *testing.T) {
	regions := []Region{
		{Name: "eu", Timezone: "Europe/Zurich", Weight: 0.5},
		{Name: "us", Timezone: "America/New_York", Weight: 0.35},
		{Name: "apac", Timezone: "Asia/Singapore", Weight: 0.15},
	}
	e := NewWithRegions(regions, nil)
	single := New("Europe/Zurich", nil)
	zur := single.Loc()
	night := time.Date(2026, 6, 16, 3, 0, 0, 0, zur)
	if got := e.BusinessFactor(night); got <= single.BusinessFactor(night) {
		t.Errorf("composite overnight %v should exceed single-tz %v", got, single.BusinessFactor(night))
	}
}

func TestRegionsBackCompatSingle(t *testing.T) {
	single := New("Europe/Zurich", nil)
	multi := NewWithRegions([]Region{{Name: "eu", Timezone: "Europe/Zurich", Weight: 1.0}}, nil)
	now := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	if a, b := single.BusinessFactor(now), multi.BusinessFactor(now); absf(a-b) > 1e-9 {
		t.Errorf("single-region composite %v != legacy %v", b, a)
	}
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
