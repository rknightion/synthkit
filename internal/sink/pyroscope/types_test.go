// SPDX-License-Identifier: AGPL-3.0-only

package pyroscope

import "testing"

func TestSeriesSeam(t *testing.T) {
	s := Series{
		Labels: []LabelPair{
			{Name: "service_name", Value: "acme-api"},
			{Name: "pyroscope_spy_name", Value: "gospy"},
		},
		Profile: nil,
	}
	if s.Labels[0].Name != "service_name" {
		t.Fatalf("expected Labels[0].Name == %q, got %q", "service_name", s.Labels[0].Name)
	}
	if len(s.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(s.Labels))
	}
}
