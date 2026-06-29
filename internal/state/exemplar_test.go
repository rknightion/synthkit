// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"testing"
	"time"
)

func TestObserveExemplarAttachesToLandingBucket(t *testing.T) {
	s := NewState()
	bounds := []float64{0.1, 0.5, 1.0}
	// plain observations (no exemplar)
	s.Observe("h", map[string]string{"a": "1"}, bounds, LEBare, 0.05)
	// an observation carrying a real trace_id exemplar; value 0.3 lands in the le=0.5 bucket
	s.ObserveExemplar("h", map[string]string{"a": "1"}, bounds, LEBare, 0.3,
		map[string]string{"trace_id": "abc"}, time.UnixMilli(1000))

	out := s.Collect(time.UnixMilli(2000))

	// find the le=0.5 bucket series; it must carry exactly the exemplar
	var found bool
	for _, ser := range out {
		if ser.Name == "h_bucket" && ser.Labels["le"] == "0.5" {
			found = true
			if len(ser.Exemplars) != 1 {
				t.Fatalf("le=0.5 bucket: want 1 exemplar, got %d", len(ser.Exemplars))
			}
			if ser.Exemplars[0].Labels["trace_id"] != "abc" || ser.Exemplars[0].Value != 0.3 {
				t.Fatalf("exemplar mismatch: %+v", ser.Exemplars[0])
			}
		}
		// no other bucket should carry it
		if ser.Name == "h_bucket" && ser.Labels["le"] != "0.5" && len(ser.Exemplars) > 0 {
			t.Fatalf("exemplar leaked onto le=%s", ser.Labels["le"])
		}
	}
	if !found {
		t.Fatal("le=0.5 bucket series not emitted")
	}
}

func TestObserveExemplarPlusInfBucket(t *testing.T) {
	s := NewState()
	bounds := []float64{0.1, 0.5}
	s.ObserveExemplar("h", nil, bounds, LEBare, 2.0, map[string]string{"trace_id": "big"}, time.UnixMilli(1))
	out := s.Collect(time.UnixMilli(2))
	for _, ser := range out {
		if ser.Name == "h_bucket" && ser.Labels["le"] == "+Inf" {
			if len(ser.Exemplars) != 1 || ser.Exemplars[0].Labels["trace_id"] != "big" {
				t.Fatalf("+Inf bucket should carry the over-range exemplar: %+v", ser.Exemplars)
			}
		}
	}
}

func TestExemplarsDrainedAfterCollect(t *testing.T) {
	s := NewState()
	s.ObserveExemplar("h", nil, []float64{1}, LEBare, 0.5, map[string]string{"trace_id": "t"}, time.UnixMilli(1))
	_ = s.Collect(time.UnixMilli(2))
	out := s.Collect(time.UnixMilli(3)) // second collect: exemplars must be gone (per-emit, not cumulative)
	for _, ser := range out {
		if len(ser.Exemplars) != 0 {
			t.Fatalf("exemplars must drain after Collect; got %d on %s", len(ser.Exemplars), ser.Name)
		}
	}
}

func TestObserveExemplarCapped(t *testing.T) {
	s := NewState()
	for i := 0; i < MaxExemplarsPerSeries+10; i++ {
		s.ObserveExemplar("h", nil, []float64{1}, LEBare, 0.5, map[string]string{"trace_id": "t"}, time.UnixMilli(1))
	}
	total := 0
	for _, ser := range s.Collect(time.UnixMilli(2)) {
		total += len(ser.Exemplars)
	}
	if total > MaxExemplarsPerSeries {
		t.Fatalf("exemplars not capped: got %d, cap %d", total, MaxExemplarsPerSeries)
	}
}

func TestCounterExemplar(t *testing.T) {
	s := NewState()
	s.Add("c_total", map[string]string{"x": "1"}, 100)
	s.CounterExemplar("c_total", map[string]string{"x": "1"}, map[string]string{"trace_id": "ct"}, 1, time.UnixMilli(5))
	for _, ser := range s.Collect(time.UnixMilli(6)) {
		if ser.Name == "c_total" {
			if len(ser.Exemplars) != 1 || ser.Exemplars[0].Labels["trace_id"] != "ct" {
				t.Fatalf("counter exemplar mismatch: %+v", ser.Exemplars)
			}
		}
	}
}
