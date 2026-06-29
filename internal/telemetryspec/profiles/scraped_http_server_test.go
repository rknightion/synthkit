// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestScrapedHTTPServerProfile(t *testing.T) {
	p, ok := Lookup("scraped_http_server")
	if !ok {
		t.Fatal("profile \"scraped_http_server\" not found in catalog")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("profile \"scraped_http_server\" Validate() failed: %v", err)
	}
	if len(p.Metrics) < 2 {
		t.Fatalf("expected at least 2 metrics, got %d", len(p.Metrics))
	}
	found := false
	for _, m := range p.Metrics {
		if m.Name == "http_server_request_duration_seconds" {
			found = true
			if m.Instrument != telemetryspec.InstrumentHistogram {
				t.Errorf("http_server_request_duration_seconds: want histogram, got %q", m.Instrument)
			}
			if len(m.Buckets) == 0 {
				t.Error("http_server_request_duration_seconds: missing buckets")
			}
		}
	}
	if !found {
		t.Error("metric http_server_request_duration_seconds not found in profile")
	}
}
