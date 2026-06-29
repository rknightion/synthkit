// SPDX-License-Identifier: AGPL-3.0-only

package profiles_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/telemetryspec/profiles"
)

func TestGatewayExportLogRegistered(t *testing.T) {
	p, ok := profiles.Lookup("gateway_export_log")
	if !ok {
		t.Fatal("profile gateway_export_log not registered")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("gateway_export_log Validate: %v", err)
	}
	if len(p.Logs) != 1 {
		t.Fatalf("expected 1 LogSpec, got %d", len(p.Logs))
	}
	if p.Logs[0].Source != "portkey" {
		t.Errorf("expected source portkey, got %q", p.Logs[0].Source)
	}
	// Verify the key realism fields are present.
	body := p.Logs[0].Body
	for _, field := range []string{"ai_model", "ai_org", "cost", "req_units", "res_units",
		"response_status_code", "retry_count", "fallback", "trace_id", "portkey_trace_id"} {
		if _, ok := body[field]; !ok {
			t.Errorf("body field %q missing", field)
		}
	}
	// retry_count must be IntRange (realism fix — was 0).
	if body["retry_count"].IntRange == nil {
		t.Error("retry_count: expected IntRange model (realism fix)")
	}
	// fallback must be Bool (realism fix — was false).
	if body["fallback"].Bool == nil {
		t.Error("fallback: expected Bool model (realism fix)")
	}
}
